// Package web is the Goshtoso BFF. Its only engine dependency is the generated HTTP client.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/araihu/goshtoso/expressions"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"golang.org/x/text/language"
)

//go:embed app.css
var stylesheet []byte

//go:embed theme.js
var themeScript []byte

//go:embed locales/*.json
var localeFiles embed.FS

type Server struct {
	client   *engineapi.ClientWithResponses
	catalogs map[string]copy
}

func New(engineURL string) (*Server, error) {
	u, err := url.Parse(engineURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, &url.Error{Op: "configure engine", URL: "", Err: errInvalidURL{}}
	}
	catalogs, err := loadCatalogs()
	if err != nil {
		return nil, err
	}
	client, err := engineapi.NewClientWithResponses(engineURL, engineapi.WithHTTPClient(&http.Client{Timeout: 3 * time.Second}))
	return &Server{client: client, catalogs: catalogs}, err
}

type errInvalidURL struct{}

func (errInvalidURL) Error() string {
	return "expected an HTTP(S) engine base URL without credentials, query, or fragment"
}

type view struct {
	Locale      string
	Theme       string
	Text        copy
	Role        string
	Accounts    []engineapi.Account
	Next        string
	Unavailable bool
	NotFound    bool
}

type copy struct {
	Accounts, EmptyTitle, EmptyBody, NotConnected, Conversations, ConversationBody  string
	Unavailable, UnavailableBody, Retry, Local, Hosted, Skip, Languages, Appearance string
	System, Light, Dark, Next, English, Portuguese                                  string
	NotFound, NotFoundBody                                                          string
}

func loadCatalogs() (map[string]copy, error) {
	out := make(map[string]copy)
	for _, locale := range []string{"en", "pt-BR"} {
		data, err := localeFiles.ReadFile("locales/" + locale + ".json")
		if err != nil {
			return nil, err
		}
		var text copy
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&text); err != nil {
			return nil, err
		}
		out[locale] = text
	}
	return out, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/ui/app.css":
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(stylesheet)
		return
	case "/ui/theme.js":
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write(themeScript)
		return
	}
	v := view{Locale: locale(r), Theme: preference(r, "theme", []string{"system", "light", "dark"}, "system")}
	v.Text = s.catalogs[v.Locale]
	if r.URL.Path != "/" {
		v.NotFound = true
		s.render(w, r, v)
		return
	}
	savePreference(w, r, "lang", v.Locale)
	savePreference(w, r, "theme", v.Theme)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil {
		v.Unavailable = true
	} else {
		v.Role = v.Text.Local
		if engine.JSON200.Role == "hosted" {
			v.Role = v.Text.Hosted
		}
		params := &engineapi.ListAccountsParams{}
		if after := r.URL.Query().Get("after"); after != "" {
			params.After = &after
		}
		accounts, err := s.client.ListAccountsWithResponse(ctx, params)
		if err != nil || accounts.JSON200 == nil {
			v.Unavailable = true
		} else {
			v.Accounts = accounts.JSON200.Items
			if accounts.JSON200.NextCursor != nil {
				v.Next = "/?after=" + url.QueryEscape(*accounts.JSON200.NextCursor)
			}
		}
	}
	s.render(w, r, v)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, v view) {
	var buf bytes.Buffer
	ctx := expressions.With(r.Context(), expressions.Set{AppShell: expressions.AppShell{SkipLinkLabel: v.Text.Skip}})
	component := page(v)
	if r.Header.Get("HX-Request") == "true" {
		component = workspace(v)
	}
	if err := component.Render(ctx, &buf); err != nil {
		http.Error(w, v.Text.Unavailable, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if v.NotFound {
		w.WriteHeader(http.StatusNotFound)
	} else if v.Unavailable {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_, _ = buf.WriteTo(w)
}

func preference(r *http.Request, key string, choices []string, fallback string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		if c, err := r.Cookie("oie_" + key); err == nil {
			value = c.Value
		}
	}
	for _, choice := range choices {
		if choice == value {
			return value
		}
	}
	return fallback
}

func locale(r *http.Request) string {
	choice := preference(r, "lang", []string{"en", "pt-BR"}, "")
	if choice != "" {
		return choice
	}
	tags, _, err := language.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
	if err != nil {
		return "en"
	}
	_, index, _ := language.NewMatcher([]language.Tag{language.English, language.BrazilianPortuguese}).Match(tags...)
	return []string{"en", "pt-BR"}[index]
}

func savePreference(w http.ResponseWriter, r *http.Request, key, value string) {
	if r.URL.Query().Has(key) {
		http.SetCookie(w, &http.Cookie{
			Name: "oie_" + key, Value: value, Path: "/", MaxAge: 365 * 24 * 60 * 60,
			HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
		})
	}
}
