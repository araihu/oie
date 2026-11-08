package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestAccountUnreadIncludesOtherAccountsAndArchivedPages(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		if q.Get("accountId") != "" || q.Get("q") != "" || q.Get("unreadOnly") != "true" {
			t.Errorf("totals inherited presentation filters: %v", q)
		}
		page := engineapi.ConversationPage{}
		if q.Get("archived") == "true" {
			page.Items = []engineapi.Conversation{{Id: "archive", AccountId: "bob", UnreadCount: 4}}
		} else if q.Get("cursor") == "second" {
			page.Items = []engineapi.Conversation{{Id: "second", AccountId: "bob", UnreadCount: 3}}
		} else {
			next := "second"
			page.NextCursor = &next
			page.Items = []engineapi.Conversation{{Id: "first", AccountId: "alice", UnreadCount: 2}}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	totals := ui.accountUnread(context.Background())
	if totals["alice"] != 2 || totals["bob"] != 7 {
		t.Fatalf("totals=%v", totals)
	}
}

func TestAccountUnreadHidesIncompleteTotals(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("archived") == "true" {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(engineapi.ConversationPage{Items: []engineapi.Conversation{{Id: "one", AccountId: "alice", UnreadCount: 8}}})
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	if totals := ui.accountUnread(context.Background()); totals != nil {
		t.Fatalf("partial counts must be hidden: %v", totals)
	}
}
