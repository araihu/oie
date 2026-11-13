package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestInfoSharedScopeIgnoresInboxFilters(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/api/v1/shared-content" || q.Get("accountId") != testAccount || q.Get("conversationId") != testChat || q.Get("q") != "scoped café" || q.Get("type") != "links" || q.Get("cursor") != "scoped-cursor" || q.Get("includeFirstPageCounts") != "true" {
			t.Errorf("wrong info scope: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(engineapi.SharedContentPage{Items: []engineapi.SharedContentItem{}})
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	v := view{InfoView: "shared", InfoType: "links", InfoCursor: "scoped-cursor", InfoQuery: "scoped café", Query: "main chat query", AccountFilter: testRevision, Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount}, Accounts: []engineapi.Account{{Id: testAccount, Address: "alice@example.test"}}}
	ui.loadChatInfo(context.Background(), &v, true)
	if v.InfoShared == nil || v.Query != "main chat query" || v.AccountFilter != testRevision {
		t.Fatal("scoped read changed main inbox context")
	}
}
func TestInfoStarsScopeAndInvalidCursor(t *testing.T) {
	for _, mode := range []string{"valid", "foreign account", "foreign conversation", "foreign star", "invalid message", "invalid revision", "stale cursor"} {
		t.Run(mode, func(t *testing.T) {
			rev := testRevision
			item := engineapi.StarredMessage{Conversation: engineapi.Conversation{Id: testChat, AccountId: testAccount}, Message: engineapi.Message{Id: testMessage, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat}, Star: engineapi.MessageStar{MessageId: testMessage, RevisionId: &rev, Starred: true, AccountId: testAccount, ConversationId: testChat}}
			switch mode {
			case "foreign account":
				item.Message.AccountId = testRevision
			case "foreign conversation":
				item.Conversation.Id = testRevision
			case "foreign star":
				item.Star.ConversationId = testRevision
			case "invalid message":
				item.Message.Id = "invalid"
			case "invalid revision":
				item.Message.RevisionId = "invalid"
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != "/api/v1/starred-messages" || q.Get("accountId") != testAccount || q.Get("conversationId") != testChat || q.Get("cursor") != testMessage {
					t.Errorf("wrong star scope: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "stale cursor" {
					w.WriteHeader(409)
					_ = json.NewEncoder(w).Encode(map[string]any{"status": 409, "code": "invalid_cursor"})
					return
				}
				_ = json.NewEncoder(w).Encode(engineapi.StarredMessagePage{Items: []engineapi.StarredMessage{item}})
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			v := view{InfoView: "stars", InfoCursor: testMessage, Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount}}
			ui.loadChatInfo(context.Background(), &v, false)
			if (v.InfoStars != nil) != (mode == "valid") || v.InfoInvalid != (mode == "stale cursor") {
				t.Fatal("invalid star projection/cursor accepted")
			}
		})
	}
}
func TestInfoLinksKeepNamespacedContextAndRevealSource(t *testing.T) {
	v := view{InfoType: "documents", InfoQuery: "inside", InfoCursor: "old", ChatID: testChat, ChatOpen: true, Section: "chats", Query: "outside", Filter: "unread", AccountFilter: testAccount, Locale: "pt-BR", Theme: "dark"}
	parsed, err := url.Parse(v.infoHref("shared", "documents", "infoCursor", "next"))
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("q") != "outside" || q.Get("filter") != "unread" || q.Get("infoQuery") != "inside" || q.Get("infoCursor") != "next" || q.Get("chat") != testChat || q.Get("lang") != "pt-BR" {
		t.Fatal("nested navigation lost main/scoped context")
	}
	source, _ := url.Parse(v.infoSourceHref(engineapi.Message{Id: testMessage, AccountId: testAccount, ConversationId: testChat}))
	q = source.Query()
	if q.Get("message") != testMessage || q.Get("account") != testAccount || q.Get("q") != "" || q.Get("filter") != "all" || q.Get("infoView") != "" || q.Get("open") != "" {
		t.Fatal("source jump retained hiding filters/drawer")
	}
}

func TestInfoIdentityVersionTracksNameCountAndPresence(t *testing.T) {
	name := "Server name"
	count := 0
	v := view{Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount, Title: "Fallback", Address: "room@example.test", Kind: "room"}, ConversationInfo: &engineapi.ConversationDetails{Name: &name}}
	before := v.detailsVersion("identity")
	v.ConversationInfo.OccupantCount = &count
	if before == v.detailsVersion("identity") {
		t.Fatal("known zero occupancy was indistinguishable from unknown")
	}
	before = v.detailsVersion("identity")
	name = "Updated server name"
	if before == v.detailsVersion("identity") || v.infoTitle() != name {
		t.Fatal("late name did not update identity")
	}
	v.Selected.Kind = "direct"
	before = v.detailsVersion("identity")
	v.ConversationInfo.PeerPresence = &engineapi.PresenceObservation{State: "away"}
	if before == v.detailsVersion("identity") {
		t.Fatal("observed direct presence did not update identity")
	}
}
