package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
			page.Items = []engineapi.Conversation{
				{Id: "first", AccountId: "alice", UnreadCount: 2},
				{Id: "reminder", AccountId: "alice", UnreadReminder: new(true)},
			}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	totals := ui.accountUnread(context.Background(), false)
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
	if totals := ui.accountUnread(context.Background(), false); totals != nil {
		t.Fatalf("partial counts must be hidden: %v", totals)
	}
}

func TestInboxRefreshReadsUnreadTotalsOnce(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var reads atomic.Int32
			var fixture liveFixture
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/engine":
					_ = json.NewEncoder(w).Encode(engineapi.Engine{Id: testEngine, ApiVersion: "0.41.0", Role: "local", EventCursor: testEngine + ":1"})
				case "/api/v1/accounts/unread-totals":
					reads.Add(1)
					w.WriteHeader(status)
					if status == http.StatusOK {
						_ = json.NewEncoder(w).Encode(engineapi.AccountUnreadTotals{Totals: map[string]int{testAccount: 17}})
					}
				default:
					if r.URL.Query().Get("unreadOnly") == "true" {
						t.Errorf("unexpected unread page %s", r.URL)
					}
					fixture.ServeHTTP(w, r)
				}
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/ui/refresh", nil)
			req.Header.Set("X-Oie-Inbox-Event", testEngine+":1")
			v := ui.baseView(req)
			ui.load(req.Context(), req, &v)
			if reads.Load() != 1 || status == 200 && v.AccountUnread[testAccount] != 17 || status != 200 && v.AccountUnread != nil {
				t.Fatalf("reads=%d totals=%v", reads.Load(), v.AccountUnread)
			}
		})
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
