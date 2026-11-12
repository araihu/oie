package scheduling_test

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/web"
	"github.com/google/uuid"
)

const testEngine = "019a5df0-0000-7000-8000-000000000001"

func newOperationKey() string { return uuid.Must(uuid.NewV7()).String() }

func liveUI(t *testing.T, handler http.Handler) (*httptest.Server, *http.Client, string) {
	t.Helper()
	engine := httptest.NewServer(handler)
	t.Cleanup(engine.Close)
	ui, err := web.New(engine.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(ui)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("BFF setup: %s", response.Status)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "oie_csrf" {
			return server, client, cookie.Value
		}
	}
	t.Fatal("missing browser CSRF cookie")
	return nil, nil, ""
}

type mutationResult struct {
	Accepted  bool   `json:"accepted"`
	Rejected  bool   `json:"rejected"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	MessageID string `json:"messageId"`
}

func livePost(t *testing.T, client *http.Client, base, path, csrf string, values url.Values) (int, mutationResult) {
	t.Helper()
	values.Set("engineId", testEngine)
	values.Set("csrf", csrf)
	request, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var result mutationResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, result
}
