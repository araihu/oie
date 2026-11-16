package web

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
