package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestPickerFindsExactArchivedDirectConversation(t *testing.T) {
	const address = "person@example.org"
	calls := 0
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Get("accountId") != testAccount || q.Get("q") != address || q.Get("kind") != "direct" {
			t.Errorf("unexpected lookup %s %s", r.Method, r.URL)
		}
		page := engineapi.ConversationPage{}
		if q.Get("archived") == "true" {
			if q.Get("cursor") == "" {
				page.Items = []engineapi.Conversation{{AccountId: testAccount, Kind: "direct", Address: "another@example.org", Title: address}}
				page.NextCursor = new("next")
			} else {
				page.Items = []engineapi.Conversation{{AccountId: testAccount, Kind: "direct", Address: address, Id: testChat}}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer engine.Close()
	s, err := New(engine.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/new-conversation/direct?account="+testAccount+"&address=Person%40Example.org%2Fmobile&lang=en&theme=dark", nil)
	res := httptest.NewRecorder()
	s.ServeHTTP(res, req)
	u, err := url.Parse(res.Body.String())
	if err != nil || res.Code != http.StatusOK || u.Query().Get("chat") != testChat || u.Query().Get("account") != testAccount || u.Query().Get("theme") != "dark" || calls != 3 {
		t.Fatalf("expected exact archived chat after three pages, got %d %s (%d calls)", res.Code, res.Body.String(), calls)
	}
}
