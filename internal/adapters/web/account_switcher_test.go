package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAccountManagerRendersOneScreenAtATime(t *testing.T) {
	fixture := &liveFixture{}
	backend := httptest.NewServer(fixture)
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query               string
		connects, registers int
	}{
		{"?open=accounts", 0, 0},
		{"?open=accounts&accountPage=add", 0, 1},
		{"?open=accounts&managedAccount=" + testAccount, 1, 0},
		{"?open=accounts&managedAccount=missing", 0, 0},
	} {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", "/"+tc.query, nil))
		html := w.Body.String()
		if got := strings.Count(html, "data-connect-account"); got != tc.connects {
			t.Errorf("%s: connection forms=%d", tc.query, got)
		}
		if got := strings.Count(html, `id="register-account-form"`); got != tc.registers {
			t.Errorf("%s: register forms=%d", tc.query, got)
		}
	}
}
