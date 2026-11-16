package web

import (
	"net/http"

	"github.com/araihu/oie/internal/adapters/hostauth"
)

func WithHostedSignIn() Option {
	return func(s *Server) { s.hostedSignIn = true }
}

// SignIn renders the public hosted sign-in page without accessing the engine.
func (s *Server) SignIn(w http.ResponseWriter, r *http.Request, status int, loginURL string) {
	lang := locale(r)
	savePreference(w, r, "lang", lang)
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
	_ = signIn(lang, text.SignInLabel, message, loginURL).Render(r.Context(), w)
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || hostauth.BrowserRequest(r.Context())
}
