package hostauth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
)

type SignInPage func(http.ResponseWriter, *http.Request, int)

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
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/sign-in", func(w http.ResponseWriter, r *http.Request) { page(w, r, http.StatusOK) })
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) { a.beginLogin(w, r, page) })
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) { a.finishLogin(w, r, page) })
	mux.Handle("POST /auth/logout", a.logout())
	mux.Handle("/auth/", http.NotFoundHandler())
	mux.Handle("/", protected)
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
		http.Redirect(w, r, "/auth/sign-in", http.StatusSeeOther)
	})
}

func (a *Auth) sameOrigin(r *http.Request) bool {
	return len(r.Header.Values("Origin")) == 1 && r.Header.Get("Origin") == a.origin
}

func (a *Auth) browserUnauthorized(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/ui/") &&
		r.Header.Get("HX-Request") == "" && (r.URL.Path == "/" || strings.Contains(r.Header.Get("Accept"), "text/html")) {
		http.Redirect(w, r, "/auth/sign-in", http.StatusSeeOther)
		return
	}
	w.Header().Set("HX-Redirect", "/auth/sign-in")
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
