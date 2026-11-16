package hostauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	publicOrigin = "https://oie.example.test"
	clientID     = "fixture-client"
	clientSecret = "fixture-client-secret"
)

func TestOIDCOwnerAndSessionRecovery(t *testing.T) {
	fixture := newIssuerFixture(t)
	store := NewStore(t.TempDir())
	owner := Owner{Issuer: fixture.URL, Subject: "allowed-owner"}
	if err := store.Initialize(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	auth, err := New(store, publicOrigin, clientID, clientSecret)
	if err != nil {
		t.Fatal(err)
	}
	auth.httpClient = fixture.Client()
	target := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := auth.Browser(target, func(w http.ResponseWriter, _ *http.Request, status int, loginURL string) {
		w.Header().Set("X-Fixture-Login", loginURL)
		w.WriteHeader(status)
	})
	destination := "/search?q=" + url.QueryEscape("a+b "+strings.Repeat("😀", 195)) + "&lang=pt-BR"
	loginURL := "/auth/login?" + url.Values{"next": {destination}}.Encode()

	for _, attempt := range []string{"wrong-state", "wrong-nonce", "wrong-owner", "wrong-audience", "wrong-key", "expired"} {
		t.Run(attempt, func(t *testing.T) {
			w := finishFixtureLogin(t, handler, startFixtureLogin(t, handler, fixture.Client(), loginURL), attempt)
			if w.Code != http.StatusUnauthorized || responseCookie(w, sessionCookie) != nil {
				t.Fatalf("%s issued a session: %d", attempt, w.Code)
			}
			want := loginURL
			if attempt == "wrong-state" {
				want = "/auth/login"
			}
			if got := w.Header().Get("X-Fixture-Login"); got != want {
				t.Fatalf("retry link %q, want %q", got, want)
			}
		})
	}

	pending := startFixtureLogin(t, handler, fixture.Client(), loginURL)
	w := finishFixtureLogin(t, handler, startFixtureLogin(t, handler, fixture.Client(), loginURL), "valid")
	cookie := responseCookie(w, sessionCookie)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != destination || cookie == nil || !cookie.Secure || !cookie.HttpOnly ||
		cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("did not issue a protected session: %d %#v", w.Code, cookie)
	}
	check := func(method, path, origin string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, publicOrigin+path, nil)
		r.AddCookie(cookie)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, r)
		if resp.Code != want {
			t.Fatalf("%s %s returned %d, want %d", method, path, resp.Code, want)
		}
	}
	check("GET", "/auth/session", "", http.StatusNoContent)
	check("GET", "/ui/events", "", http.StatusNoContent)
	check("POST", "/ui/messages/send", "https://cross-origin.example", http.StatusForbidden)
	check("POST", "/ui/messages/send", publicOrigin, http.StatusNoContent)
	check("POST", "/auth/logout", "https://cross-origin.example", http.StatusForbidden)

	createKey := httptest.NewRequest(http.MethodPost, publicOrigin+"/ui/access-keys", nil)
	createKey.AddCookie(cookie)
	createKey.Header.Set("Origin", "https://cross-origin.example")
	if _, err := auth.CreateBrowserKey(createKey, "fixture"); err == nil {
		t.Fatal("created a browser key from another origin")
	}
	createKey.Header.Set("Origin", publicOrigin)
	key, err := auth.CreateBrowserKey(createKey, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := store.validCredential(key, true); err != nil || !valid {
		t.Fatalf("browser key did not authenticate: %v %v", valid, err)
	}
	if err := store.Recover(t.Context(), Owner{Issuer: owner.Issuer, Subject: "replacement-owner"}); err != nil {
		t.Fatal(err)
	}
	// A deployment restart must not overwrite a recovered owner.
	if err := store.Initialize(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	check("GET", "/ui/events", "", http.StatusUnauthorized)
	if _, err := auth.CreateBrowserKey(createKey, "stale-fixture"); err == nil {
		t.Fatal("a recovered session created another key")
	}
	if valid, err := store.validCredential(key, true); err != nil || valid {
		t.Fatalf("recovered instance accepted the old key: %v %v", valid, err)
	}
	if w := finishFixtureLogin(t, handler, pending, "replacement"); w.Code != http.StatusUnauthorized {
		t.Fatalf("login started before recovery was accepted: %d", w.Code)
	}

	w = finishFixtureLogin(t, handler, startFixtureLogin(t, handler, fixture.Client(), loginURL), "replacement")
	cookie = responseCookie(w, sessionCookie)
	if w.Code != http.StatusSeeOther || cookie == nil {
		t.Fatalf("replacement owner could not sign in: %d", w.Code)
	}
	check("GET", "/ui/events", "", http.StatusNoContent)
	check("POST", "/auth/logout", publicOrigin, http.StatusSeeOther)
	check("GET", "/auth/session", "", http.StatusUnauthorized)
	check("GET", "/ui/events", "", http.StatusUnauthorized)
}

func TestOIDCWorkspaceDestination(t *testing.T) {
	for _, path := range []string{"/", "/?chat=123&message=456", "/search?q=a%2Bb", "/search?q=https%3A%2F%2Fexample.net"} {
		if got := workspaceDestination(path); got != path {
			t.Errorf("workspaceDestination(%q) = %q", path, got)
		}
	}
	for _, path := range []string{
		"https://other.example/search", "//other.example", "/\\other.example", "/%2Fother.example",
		"/se%61rch", "/search/../", "/search#fragment", "/ui/events", "/auth/logout",
		"/?q=\n/me", "/?q=" + strings.Repeat("x", maxWorkspaceDestinationBytes),
	} {
		if got := workspaceDestination(path); got != "/" {
			t.Errorf("workspaceDestination(%q) = %q", path, got)
		}
	}
}

func TestStoreRejectsExpiredKeysAndPersistsOnlyVerifier(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Initialize(t.Context(), Owner{Issuer: "https://id.example.test", Subject: "owner"}); err != nil {
		t.Fatal(err)
	}
	_, key, err := store.CreateKey(t.Context(), "fixture", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeKey(t.Context(), key); err == nil || strings.Contains(err.Error(), key) {
		t.Fatal("revocation accepted a secret as its ID or echoed it in an error")
	}
	if err := store.update(t.Context(), false, func(s *state) error {
		if stored := s.Keys[0].Verifier; stored == key || stored != verifier(key) {
			t.Error("key was not stored as a verifier")
		}
		s.Keys[0].ExpiresAt = time.Now().Add(-time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if valid, err := store.validCredential(key, true); err != nil || valid {
		t.Fatalf("expired key was accepted: %v %v", valid, err)
	}
}

type fixtureLogin struct {
	cookie *http.Cookie
	state  string
}

func startFixtureLogin(t *testing.T, browser http.Handler, client *http.Client, loginURL string) fixtureLogin {
	t.Helper()
	w := httptest.NewRecorder()
	browser.ServeHTTP(w, httptest.NewRequest("GET", publicOrigin+loginURL, nil))
	next, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != http.StatusSeeOther {
		t.Fatalf("login failed to start: %d %v", w.Code, err)
	}
	q := next.Query()
	cookie := responseCookie(w, loginCookie)
	if cookie == nil || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" ||
		q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != publicOrigin+"/auth/callback" {
		t.Fatalf("missing login parameters: %s", next.Path)
	}
	req, err := http.NewRequestWithContext(t.Context(), "GET", next.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("fixture could not authorize: %d", resp.StatusCode)
	}
	return fixtureLogin{cookie, q.Get("state")}
}

func finishFixtureLogin(t *testing.T, browser http.Handler, login fixtureLogin, code string) *httptest.ResponseRecorder {
	t.Helper()
	state := login.state
	if code == "wrong-state" {
		state = "invalid"
	}
	r := httptest.NewRequest("GET", publicOrigin+"/auth/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), nil)
	r.AddCookie(login.cookie)
	w := httptest.NewRecorder()
	browser.ServeHTTP(w, r)
	return w
}

func responseCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	for _, item := range res.Cookies() {
		if item.Name == name {
			return item
		}
	}
	return nil
}

func newIssuerFixture(t *testing.T) *httptest.Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	type authorization struct{ nonce, challenge string }
	var mu sync.Mutex
	var current authorization
	respond := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize?fixture=login",
			"token_endpoint": issuer + "/token?fixture=exchange", "jwks_uri": issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, map[string]any{"keys": []any{map[string]string{
			"kty": "RSA", "kid": "fixture", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
		}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("fixture") != "login" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		current = authorization{q.Get("nonce"), q.Get("code_challenge")}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if id, secret, _ := r.BasicAuth(); id != clientID || secret != clientSecret || r.URL.Query().Get("fixture") != "exchange" || r.ParseForm() != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		attempt := current
		current = authorization{}
		mu.Unlock()
		pkce := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if attempt.challenge != base64.RawURLEncoding.EncodeToString(pkce[:]) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		claims := map[string]any{
			"iss": issuer, "sub": "allowed-owner", "aud": clientID,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": attempt.nonce,
		}
		switch r.Form.Get("code") {
		case "wrong-nonce":
			claims["nonce"] = "wrong"
		case "wrong-owner":
			claims["sub"] = "wrong"
		case "wrong-audience":
			claims["aud"] = "wrong"
		case "expired":
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		case "replacement":
			claims["sub"] = "replacement-owner"
		}
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture"}`))
		body, _ := json.Marshal(claims)
		data := header + "." + base64.RawURLEncoding.EncodeToString(body)
		hash := sha256.Sum256([]byte(data))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Form.Get("code") == "wrong-key" {
			signature[0] ^= 0xff
		}
		respond(w, map[string]any{
			"access_token": "fixture", "token_type": "Bearer",
			"id_token": strings.Join([]string{data, base64.RawURLEncoding.EncodeToString(signature)}, "."),
		})
	})
	server := httptest.NewUnstartedServer(mux)
	issuer = "https://" + server.Listener.Addr().String()
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
