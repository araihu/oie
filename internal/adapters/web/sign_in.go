package web

import (
	_ "embed"
	"net/http"
	"net/url"
	"strings"

	"github.com/araihu/oie/internal/adapters/hostauth"
)

//go:embed sign-in.css
var signInStylesheet []byte

// HostedSignInAsset exposes only the static assets used before sign-in.
func HostedSignInAsset(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/assets/sign-in.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(signInStylesheet)
	case "/auth/assets/logo-light.svg", "/auth/assets/logo-dark.svg", "/auth/assets/favicon.svg":
		name := strings.TrimPrefix(r.URL.Path, "/auth/assets/")
		data, err := brandFiles.ReadFile("brand/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func WithHostedSignIn(auth *hostauth.Auth) Option {
	return func(s *Server) { s.createHostedKey = auth.CreateBrowserKey }
}

// SignIn renders the public hosted sign-in page without accessing the engine.
func (s *Server) SignIn(w http.ResponseWriter, r *http.Request, status int, loginURL, destination string) {
	// Apply presentation preferences from the original navigation before OIDC
	// completes. Explicit preferences on the sign-in URL take precedence.
	if target, err := url.ParseRequestURI(destination); err == nil && target.RawQuery != "" {
		query := r.URL.Query()
		targetQuery := target.Query()
		for _, key := range []string{"lang", "theme"} {
			if !query.Has(key) && targetQuery.Has(key) {
				query.Set(key, targetQuery.Get(key))
			}
		}
		presentation := r.WithContext(r.Context())
		presentationURL := *r.URL
		presentationURL.RawQuery = query.Encode()
		presentation.URL = &presentationURL
		r = presentation
	}
	lang, mode := locale(r), appearanceMode(r)
	savePreference(w, r, "lang", lang)
	savePreference(w, r, "theme", mode)
	text := s.catalogs[lang]
	message := ""
	switch status {
	case http.StatusUnauthorized:
		message = text.SignInRejected
	case http.StatusServiceUnavailable:
		message = text.SignInUnavailable
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_ = signIn(lang, mode, text.SignInLabel, message, loginURL).Render(r.Context(), w)
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || hostauth.BrowserRequest(r.Context())
}
