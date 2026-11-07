// Package web is the Goshtoso BFF. Its only engine dependency is the generated HTTP client.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/araihu/goshtoso/expressions"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/previewapi"
	"golang.org/x/text/language"
)

//go:embed app.css
var stylesheet []byte

//go:embed chat-pattern.svg
var chatPattern []byte

//go:embed theme.js
var themeScript []byte

//go:embed locales/*.json
var localeFiles embed.FS

type Server struct {
	client     *engineapi.ClientWithResponses
	chatClient *previewapi.ClientWithResponses
	catalogs   map[string]copy
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
	if err != nil {
		return nil, err
	}
	chatClient, err := previewapi.NewClientWithResponses(engineURL, previewapi.WithHTTPClient(&http.Client{Timeout: 3 * time.Second}))
	return &Server{client: client, chatClient: chatClient, catalogs: catalogs}, err
}

type errInvalidURL struct{}

func (errInvalidURL) Error() string {
	return "expected an HTTP(S) engine base URL without credentials, query, or fragment"
}

type view struct {
	Locale                         string
	Theme                          string
	Text                           copy
	Role                           string
	Accounts                       []engineapi.Account
	Next                           string
	Unavailable                    bool
	NotFound                       bool
	Preview                        bool
	ChatOpen                       bool
	Section, Filter, Query, ChatID string
	Chats                          []previewapi.Conversation
	Selected                       *previewapi.Conversation
	Messages                       []previewapi.Message
}

type copy struct {
	Accounts, EmptyTitle, EmptyBody, NotConnected, Conversations    string
	Unavailable, UnavailableBody, Retry, Local, Hosted              string
	Skip, Languages, Appearance, System, Light                      string
	Dark, Next, English, Portuguese, NotFound                       string
	NotFoundBody, Chats, Favorites, Archived, SharedMedia           string
	Settings, You, All, Unread, Groups                              string
	SearchChats, NoResults, NoResultsBody, NewChat, More            string
	Preview, PreviewNote, Message, Send, Attach                     string
	SendingUnavailable, Back, Today, Navigation, Active             string
	OpenNavigation, Close, Details, Participants, MediaEmpty        string
	MediaBody, SelectChat, SelectChatBody, EngineState, AccountHelp string
	PreviewOnly, ThemeHelp, LanguageHelp, NoChats, NoChatsBody      string
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
	case "/ui/chat-pattern.svg":
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(chatPattern)
		return
	case "/ui/preview-landscape.svg":
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(previewLandscape)
		return
	case "/ui/theme.js":
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write(themeScript)
		return
	}
	v := view{Locale: locale(r), Theme: preference(r, "theme", []string{"system", "light", "dark"}, "system")}
	v.Text = s.catalogs[v.Locale]
	v.configureWorkspace(r)
	if r.URL.Path != "/" {
		v.NotFound = true
		v.ChatOpen = true
		s.render(w, r, v)
		return
	}
	savePreference(w, r, "lang", v.Locale)
	savePreference(w, r, "theme", v.Theme)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	editor := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Accept-Language", v.Locale)
		return nil
	}
	engine, err := s.client.GetEngineWithResponse(ctx, editor)
	if err != nil || engine.JSON200 == nil {
		v.Unavailable = true
	} else {
		v.Preview = engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true"
		v.Role = v.Text.Local
		if engine.JSON200.Role == "hosted" {
			v.Role = v.Text.Hosted
		}
		params := &engineapi.ListAccountsParams{}
		if after := r.URL.Query().Get("after"); after != "" {
			params.After = &after
		}
		accounts, err := s.client.ListAccountsWithResponse(ctx, params, editor)
		if err != nil || accounts.JSON200 == nil {
			v.Unavailable = true
		} else {
			v.Accounts = accounts.JSON200.Items
			if accounts.JSON200.NextCursor != nil {
				v.Next = "/?after=" + url.QueryEscape(*accounts.JSON200.NextCursor)
			}
		}
	}
	if !v.Unavailable {
		params := &previewapi.ListConversationsParams{}
		archived := v.Section == "archived"
		params.Archived = &archived
		if v.Query != "" {
			params.Q = &v.Query
		}
		if v.Filter == "unread" {
			value := true
			params.UnreadOnly = &value
		}
		if v.Filter == "favorites" || v.Section == "favorites" {
			value := true
			params.FavoritesOnly = &value
		}
		if v.Filter == "groups" {
			kind := previewapi.ListConversationsParamsKindRoom
			params.Kind = &kind
		}
		chatEditor := func(_ context.Context, req *http.Request) error {
			req.Header.Set("Accept-Language", v.Locale)
			return nil
		}
		chats, err := s.chatClient.ListConversationsWithResponse(ctx, params, chatEditor)
		if err != nil {
			v.Unavailable = true
		} else if chats.JSON200 != nil {
			v.Chats = chats.JSON200.Items
			selected := v.ChatID
			if selected == "" && len(v.Chats) > 0 {
				selected = v.Chats[0].Id
			}
			if selected != "" && v.Section != "media" {
				conversation, err := s.chatClient.GetConversationWithResponse(ctx, selected, chatEditor)
				if err != nil {
					v.Unavailable = true
				} else if conversation.JSON200 == nil {
					if conversation.StatusCode() == 404 || conversation.StatusCode() == 400 {
						v.NotFound = true
					} else {
						v.Unavailable = true
					}
				} else {
					messages, err := s.chatClient.ListMessagesWithResponse(ctx, selected, &previewapi.ListMessagesParams{}, chatEditor)
					if err != nil || messages.JSON200 == nil {
						v.Unavailable = true
					} else {
						v.Selected = conversation.JSON200
						v.Messages = messages.JSON200.Items
						slices.Reverse(v.Messages)
						v.ChatID = selected
					}
				}
			}
		} else if chats.StatusCode() != 404 {
			v.Unavailable = true
		}
	}
	s.render(w, r, v)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, v view) {
	var buf bytes.Buffer
	ctx := expressions.With(r.Context(), expressions.Set{AppShell: expressions.AppShell{SkipLinkLabel: v.Text.Skip},
		Sidebar:   expressions.Sidebar{AriaLabel: v.Text.Navigation, ActiveLabel: v.Text.Active, TriggerLabel: v.Text.OpenNavigation, SkipLabel: v.Text.Skip},
		Drawer:    expressions.Drawer{CloseAriaLabel: v.Text.Close},
		TextInput: expressions.TextInput{SearchAriaLabel: v.Text.SearchChats},
	})
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
