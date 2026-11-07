package web

import (
	"github.com/araihu/oie/internal/adapters/mockengine"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceReadsMockThroughHTTPAndPreservesPresentationState(t *testing.T) {
	handler, err := mockengine.New(false)
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(handler)
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, want string
		rows       int
	}{
		{"/?lang=en", "Sample conversations", 10},
		{"/?lang=pt-BR&chat=019a5df0-0000-7000-8000-000000000102", "Tudo certo para sábado?", 10},
		{"/?q=Nina+Martins&lang=en", "Nina Martins", 1},
		{"/?filter=unread&lang=en", "Unread", 3},
		{"/?q=no-match&lang=en", "No conversations found", 0},
		{"/?section=archived&lang=en", "Coffee club", 1},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s status=%d missing %q", tc.path, w.Code, tc.want)
		}
		if got := strings.Count(w.Body.String(), "data-conversation-id="); got != tc.rows {
			t.Errorf("%s rows=%d want=%d", tc.path, got, tc.rows)
		}
	}
	// An unknown remote ID returns a useful 404 shell instead of silently showing another chat.
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest("GET", "/?chat=019a5df0-0000-7000-8000-000000009999&lang=en", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "Page not found") {
		t.Fatal("unknown conversation did not keep truthful state")
	}
}
