package hostauth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/httpapi"
)

// Allow a 200-character search whose UTF-8 characters are percent-encoded,
// while leaving room for the OIDC state in a browser cookie.
const maxWorkspaceDestinationBytes = 2600

type SignInPage func(w http.ResponseWriter, r *http.Request, status int, loginURL, destination string)

func signInPage(w http.ResponseWriter, r *http.Request, page SignInPage, status int, destination string) {
	page(w, r, status, authURL("/auth/login", destination), destination)
}

func authURL(path, destination string) string {
	if destination == "/" {
		return path
	}
	return path + "?" + url.Values{"next": {destination}}.Encode()
}

func workspaceDestination(raw string) string {
	if len(raw) > maxWorkspaceDestinationBytes || !utf8.ValidString(raw) || strings.ContainsAny(raw, `\"#`+"\u2028\u2029") || strings.ContainsFunc(raw, unicode.IsControl) {
		return "/"
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.EscapedPath() != u.Path || (u.Path != "/" && u.Path != "/search") {
		return "/"
	}
	return raw
}

func (a *Auth) API(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, secret, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if len(r.Header.Values("Authorization")) != 1 || !strings.EqualFold(scheme, "Bearer") {
			httpapi.WriteAuthenticationRequired(w, r)
			return
		}
		ok, err := a.store.validCredential(secret, true)
		if err != nil {
			httpapi.WriteUnavailable(w, r)
			return
		}
		if !ok {
			httpapi.WriteAuthenticationRequired(w, r)
			return
		}
		a.serveAuthenticated(w, r, secret, true, next)
	})
}

type browserRequestKey struct{}

func BrowserRequest(ctx context.Context) bool {
	hosted, _ := ctx.Value(browserRequestKey{}).(bool)
	return hosted
}

func (a *Auth) Browser(next http.Handler, page SignInPage) http.Handler {
	protect := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookie)
			if err != nil {
				a.browserUnauthorized(w, r)
				return
			}
			ok, err := a.store.validCredential(cookie.Value, false)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
			if !ok {
				a.browserUnauthorized(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.sameOrigin(r) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			a.serveAuthenticated(w, r, cookie.Value, false, next)
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/sign-in", func(w http.ResponseWriter, r *http.Request) {
		signInPage(w, r, page, http.StatusOK, workspaceDestination(r.URL.Query().Get("next")))
	})
	mux.Handle("GET /auth/session", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) { a.beginLogin(w, r, page) })
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) { a.finishLogin(w, r, page) })
	mux.Handle("POST /auth/logout", a.logout())
	mux.Handle("/auth/", http.NotFoundHandler())
	mux.Handle("/", protect(next))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), browserRequestKey{}, true)
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Auth) logout() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.sameOrigin(r) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			ok, err := a.store.validCredential(cookie.Value, false)
			if err == nil && ok {
				err = a.store.revokeSession(r.Context(), cookie.Value)
			}
			if err != nil {
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				return
			}
		}
		setCookie(w, sessionCookie, "", -time.Second)
		setCookie(w, loginCookie, "", -time.Second)
		if r.Header.Get("Accept") == "application/json" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/auth/sign-in", http.StatusSeeOther)
	})
}

func (a *Auth) sameOrigin(r *http.Request) bool {
	return len(r.Header.Values("Origin")) == 1 && r.Header.Get("Origin") == a.origin
}

func (a *Auth) browserUnauthorized(w http.ResponseWriter, r *http.Request) {
	mode := r.Header.Get("Sec-Fetch-Mode")
	if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/search") &&
		r.Header.Get("HX-Request") == "" && (mode == "navigate" ||
		mode == "" && (r.URL.Path == "/" || strings.Contains(r.Header.Get("Accept"), "text/html"))) {
		destination := workspaceDestination(r.URL.RequestURI())
		http.Redirect(w, r, authURL("/auth/sign-in", destination), http.StatusSeeOther)
		return
	}
	w.Header().Set("X-Oie-Sign-In-Required", "1")
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

func (a *Auth) serveAuthenticated(w http.ResponseWriter, r *http.Request, secret string, key bool, next http.Handler) {
	// A long-lived media request or event stream must observe expiry, logout,
	// local revocation and recovery even if its client never reconnects.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ok, err := a.store.validCredential(secret, key)
				if err != nil || !ok {
					cancel()
					return
				}
			}
		}
	}()
	next.ServeHTTP(w, r.WithContext(ctx))
}

// CreateBrowserKey creates a key while the owner's browser session is valid.
func (a *Auth) CreateBrowserKey(r *http.Request, name string) (string, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" || !a.sameOrigin(r) {
		return "", errUnauthorized
	}
	_, secret, err := a.store.createKey(r.Context(), name, DefaultKeyLifetime, cookie.Value)
	return secret, err
}
