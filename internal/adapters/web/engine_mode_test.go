package web

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestDesktopEngineKeyRequiresMatchingAddress(t *testing.T) {
	const address = "https://engine.example.test"
	const key = "fixture-key"
	csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct {
		name, key, boundAddress string
		status                  int
	}{
		{"same address", key, address, http.StatusNoContent},
		{"different address", key, "https://other.example.test", http.StatusBadRequest},
		{"missing binding", key, "", http.StatusBadRequest},
		{"selection without a key", "", "", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			server, err := New("http://127.0.0.1", WithEngineMode("current", "", false, func(_ context.Context, gotAddress, gotKey string) error {
				called = true
				if gotAddress != address || gotKey != tc.key {
					t.Error("unexpected connection settings")
				}
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			form := url.Values{
				"selection": {"current"}, "csrf": {csrf}, "mode": {"remote"},
				"url": {address}, "apiKey": {tc.key}, "apiKeyAddress": {tc.boundAddress},
			}
			request := httptest.NewRequest(http.MethodPost, "/ui/engine-mode", strings.NewReader(form.Encode()))
			request.AddCookie(&http.Cookie{Name: csrfCookie, Value: csrf})
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Accept", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != tc.status || called != (tc.status == http.StatusNoContent) {
				t.Fatalf("status = %d, checked connection = %v", response.Code, called)
			}
		})
	}
}

func TestDesktopOffersBrowserSignInAfterHostedUpgrade(t *testing.T) {
	var hosted, dataOnly atomic.Bool
	var signInProbes atomic.Int32
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/engine":
			if hosted.Load() && r.Header.Get("Authorization") != "Bearer fixture-key" {
				httpapi.WriteAuthenticationRequired(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"019a5df0-0000-7000-8000-000000000001","role":"local"}`)
		case "/api/v1/accounts", "/api/v1/conversations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"items":[]}`)
		case "/auth/sign-in":
			signInProbes.Add(1)
			if r.Method != http.MethodHead || r.Header.Get("Authorization") != "" {
				t.Error("sign-in discovery should use HEAD without the engine key")
			}
			if dataOnly.Load() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	key := ""
	server, err := New(remote.URL,
		WithEngineMode("current", remote.URL, false, nil),
		WithEngineRequestEditor(func(_ context.Context, r *http.Request) error {
			if key != "" {
				r.Header.Set("Authorization", "Bearer "+key)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	server.client.ClientInterface.(*engineapi.Client).Client = remote.Client()
	server.streamClient.Client = remote.Client()
	workspace := func() string {
		t.Helper()
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?lang=pt-BR", nil))
		return response.Body.String()
	}
	offersSignIn := func(page string) bool {
		return strings.Contains(page, "Entrar pelo navegador") &&
			strings.Contains(page, remote.URL+"/?open=settings") &&
			strings.Contains(page, "Inserir chave de acesso")
	}
	if offersSignIn(workspace()) || signInProbes.Load() != 0 {
		t.Fatal("an unauthenticated remote engine should work without requesting sign-in")
	}
	hosted.Store(true)
	if !offersSignIn(workspace()) || signInProbes.Load() != 1 {
		t.Fatal("the upgraded hosted workspace did not offer browser sign-in")
	}
	dataOnly.Store(true)
	if offersSignIn(workspace()) || signInProbes.Load() != 2 {
		t.Fatal("an API-only engine should not offer browser sign-in")
	}
	dataOnly.Store(false)
	key = "fixture-key"
	if offersSignIn(workspace()) || signInProbes.Load() != 2 {
		t.Fatal("a working access key should bypass browser sign-in")
	}
}
