package apphost

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/api"
	"github.com/araihu/oie/internal/adapters/hostauth"
	"github.com/getkin/kin-openapi/openapi3"
)

const testPublicURL = "https://oie.example.test"

var testOwner = hostauth.Owner{Issuer: "https://id.example.test", Subject: "fixture-owner"}

func hostedConfig(t *testing.T, dir string, dataOnly bool) Config {
	t.Helper()
	if err := hostauth.NewStore(dir).Initialize(t.Context(), testOwner); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(secretFile, []byte("fixture-client-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Config{
		Role: "hosted", Listen: "127.0.0.1:0", DataDir: dir, DataOnly: dataOnly,
		Hosted: HostedConfig{
			PublicURL: testPublicURL, TrustedTLSProxies: "127.0.0.1/32",
			OIDCClientID: "fixture-client", OIDCClientSecretFile: secretFile,
		},
	}
}

func hostedRequest(method, path, key string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, testPublicURL+path, body)
	r.RemoteAddr = "127.0.0.1:23456"
	r.Header.Set("X-Forwarded-Proto", "https")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func hostedResponse(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, r)
	return w
}

func TestHostedGuardsAllEngineOperations(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, hostedConfig(t, dir, false))
	document, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range document.Paths.Map() {
		path := strings.ReplaceAll(strings.ReplaceAll(path, "{", "fixture-"), "}", "")
		for method := range item.Operations() {
			w := hostedResponse(s, hostedRequest(method, path, "", nil))
			if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" ||
				w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "authentication_required") {
				t.Errorf("%s %s returned %d: %s", method, path, w.Code, w.Body)
			}
		}
	}
	for _, path := range []string{"/api/openapi.yaml", "/api/v1/%65vents", "/%61pi/v1/health"} {
		r := hostedRequest(http.MethodGet, path, "", nil)
		// Neither a client-supplied private capability nor a cookie is an API key.
		r.Header.Set(capabilityHeader, s.capability)
		r.AddCookie(&http.Cookie{Name: "__Host-oie-session", Value: "fixture"})
		if w := hostedResponse(s, r); w.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d", path, w.Code)
		}
	}
	for _, path := range []string{
		"/ui/events", "/ui/api/v1/previews/fixture", "/ui/scheduling/file", "/ui/notification-worker.js",
		"/ui/access-keys", "/ui/engine/reset", "/ui/refresh", "/ui/media/fixture", "/ui/avatars/fixture",
	} {
		if w := hostedResponse(s, hostedRequest(http.MethodGet, path, "", nil)); w.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d", path, w.Code)
		}
	}
	if w := hostedResponse(s, hostedRequest(http.MethodGet, "/", "", nil)); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/auth/sign-in" {
		t.Fatalf("signed-out page did not redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
	deepLink := "/?chat=fixture&message=target"
	r := hostedRequest(http.MethodGet, deepLink, "", nil)
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w := hostedResponse(s, r)
	signIn, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || err != nil || signIn.Path != "/auth/sign-in" || signIn.Query().Get("next") != deepLink {
		t.Fatalf("signed-out deep link lost its destination: %d %v", w.Code, signIn)
	}
	for _, path := range []string{"/auth/session", "/search", "/ui/diagnostics/log"} {
		r := hostedRequest(http.MethodGet, path, "", nil)
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set("Accept", "text/html")
		w := hostedResponse(s, r)
		if w.Code != http.StatusUnauthorized || w.Header().Get("X-Oie-Sign-In-Required") != "1" || w.Header().Get("Location") != "" {
			t.Errorf("%s did not signal session loss: %d", path, w.Code)
		}
	}
	r = hostedRequest(http.MethodGet, "/auth/sign-in", "", nil)
	r.Header.Set("Accept-Language", "pt-BR")
	if w := hostedResponse(s, r); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Entrar") {
		t.Fatalf("public sign-in missing: %d %s", w.Code, w.Body)
	}

	store := hostauth.NewStore(dir)
	info, key, err := store.CreateKey(t.Context(), "fixture", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w := hostedResponse(s, hostedRequest(http.MethodGet, "/ui/events", key, nil)); w.Code != http.StatusUnauthorized {
		t.Fatalf("API key authenticated the browser surface: %d", w.Code)
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" },
		func(r *http.Request) { r.Header.Del("X-Forwarded-Proto") },
		func(r *http.Request) { r.Header.Add("X-Forwarded-Proto", "http") },
		func(r *http.Request) { r.Host = "different.example.test" },
	} {
		r := hostedRequest(http.MethodGet, "/api/v1/health", key, nil)
		change(r)
		if w := hostedResponse(s, r); w.Code != http.StatusBadRequest {
			t.Fatalf("accepted an untrusted transport or origin: %d", w.Code)
		}
	}

	w = hostedResponse(s, hostedRequest(http.MethodGet, "/api/v1/engine", key, nil))
	var engine struct{ ID, Role string }
	if err := json.Unmarshal(w.Body.Bytes(), &engine); err != nil || engine.ID == "" || engine.Role != "hosted" ||
		w.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("hosted engine access failed: %v %d %s", err, w.Code, w.Body)
	}
	reset := strings.NewReader(`{"engineId":"` + engine.ID + `","confirmation":"delete"}`)
	if w := hostedResponse(s, hostedRequest(http.MethodPost, "/api/v1/engine/reset", key, reset)); w.Code != http.StatusNoContent {
		t.Fatalf("key could not reset engine: %d %s", w.Code, w.Body)
	}
	if w := hostedResponse(s, hostedRequest(http.MethodGet, "/api/v1/health", key, nil)); w.Code != http.StatusOK {
		t.Fatalf("engine reset erased auth state: %d", w.Code)
	}
	if err := store.RevokeKey(t.Context(), info.ID); err != nil {
		t.Fatal(err)
	}
	if w := hostedResponse(s, hostedRequest(http.MethodGet, "/api/v1/health", key, nil)); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key was accepted: %d", w.Code)
	}
}

func TestHostedDataOnlyAndEventRevocation(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, hostedConfig(t, dir, true))
	store := hostauth.NewStore(dir)
	info, key, err := store.CreateKey(t.Context(), "event fixture", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/assets/", "/auth/sign-in", "/ui/events"} {
		if w := hostedResponse(s, hostedRequest(http.MethodGet, path, key, nil)); w.Code != http.StatusNotFound {
			t.Fatalf("data-only %s returned %d", path, w.Code)
		}
	}
	if w := hostedResponse(s, hostedRequest(http.MethodGet, "/api/openapi.yaml", key, nil)); w.Code != http.StatusOK {
		t.Fatalf("key could not read the hosted contract: %d", w.Code)
	}
	front := httptest.NewServer(s.server.Handler)
	defer front.Close()
	client := front.Client()
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, front.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "oie.example.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	input := bufio.NewReader(res.Body)
	if line, err := input.ReadString('\n'); err != nil || line != ": connected\n" ||
		res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("expected authenticated SSE and no-store, got %q %v", line, err)
	}
	if err := store.RevokeKey(t.Context(), info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, input); err != nil {
		t.Fatalf("revocation did not close the active SSE connection: %v", err)
	}
}

func TestHostedRequiresConfiguration(t *testing.T) {
	cfg := hostedConfig(t, t.TempDir(), false)
	for _, change := range []func(*Config){
		func(c *Config) { c.Hosted.PublicURL = "http://oie.example.test" },
		func(c *Config) { c.Hosted.TrustedTLSProxies = "" },
		func(c *Config) { c.Hosted.TrustedTLSProxies = "0.0.0.0/0" },
		func(c *Config) { c.Hosted.TrustedTLSProxies = "192.0.2.0/24" },
		func(c *Config) { c.Hosted.OIDCClientID = "" },
	} {
		c := cfg
		change(&c)
		if _, err := configureHosted(c); err == nil {
			t.Fatalf("accepted unsafe hosted configuration: %#v", c)
		}
	}
	cfg.DataDir = t.TempDir()
	if _, err := Start(t.Context(), cfg); !errors.Is(err, hostauth.ErrUnconfigured) {
		t.Fatalf("started without a configured owner: %v", err)
	}
}
