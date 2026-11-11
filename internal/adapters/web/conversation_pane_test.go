package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestConversationFragmentOwnsItsStateAndDetailsWithoutTheInbox(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	chat := engineapi.Conversation{Id: testChat, AccountId: testAccount, Title: "Selected conversation", Address: "peer@fixture", Kind: "direct"}
	v := view{EngineID: testEngine, ChatID: chat.Id, ChatOpen: true, Selected: &chat, Accounts: []engineapi.Account{{Id: testAccount, Address: "alice@fixture", Label: "Personal"}}, Locale: "en", Theme: "dark", Section: "chats", Filter: "all", Text: catalogs["en"]}
	server := &Server{}
	for _, fragment := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/?chat="+testChat, nil)
		if fragment {
			r.Header.Set("HX-Request", "true")
			r.Header.Set("X-Oie-Fragment", "conversation")
		}
		w := httptest.NewRecorder()
		server.render(w, r, v)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `id="conversation-pane"`) || !strings.Contains(body, `data-conversation-id="`+testChat+`"`) || !strings.Contains(body, `data-details-conversation="`+testChat+`"`) {
			t.Fatalf("conversation fragment lost scoped identity or details: %d", w.Code)
		}
		for _, id := range []string{"oie-avatar-data", "oie-star-data", "chat-info-content"} {
			if strings.Count(body, `id="`+id+`"`) != 1 {
				t.Error("duplicated or missing conversation-owned state", id)
			}
		}
		if fragment {
			for _, forbidden := range []string{"<!DOCTYPE", `id="oie-app"`, `id="conversation-list"`, `class="oie-list-pane"`} {
				if strings.Contains(body, forbidden) {
					t.Error("conversation response would replace the surrounding inbox", forbidden)
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Vary"), "X-Oie-Fragment") {
				t.Fatal("fragment response omitted cache negotiation")
			}
		} else if !strings.Contains(strings.ToLower(body), "<!doctype") || !strings.Contains(body, `id="conversation-list"`) {
			t.Fatal("native navigation fallback lost the full shell")
		}
	}
}

func TestConversationFragmentScopeExcludesConversationAndInboxFilter(t *testing.T) {
	v := view{EngineID: testEngine, AccountFilter: testAccount, Locale: "en", Theme: "dark", Section: "chats", Filter: "all"}
	scope := v.conversationScope()
	next := v
	next.ChatID, next.FocusMessage, next.InfoQuery, next.InfoView, next.Filter = testChat, testMessage, "room search", "shared", "unread"
	if next.conversationScope() != scope {
		t.Fatal("conversation selection unnecessarily invalidates the inbox")
	}
	for _, change := range []func(*view){
		func(v *view) { v.EngineID = testRevision },
		func(v *view) { v.AccountFilter = "" },
		func(v *view) { v.Section = "archived" },
		func(v *view) { v.Query = "search" },
		func(v *view) { v.Locale = "pt-BR" },
		func(v *view) { v.Theme = "light" },
	} {
		changed := v
		change(&changed)
		if changed.conversationScope() == scope {
			t.Fatal("changed inbox or presentation scope accepted an old conversation fragment")
		}
	}
}

func TestConversationListVersionTracksContentWithoutForcingSelectionRepaints(t *testing.T) {
	v := view{Chats: []engineapi.Conversation{{Id: testChat, UnreadCount: 1}}, Locale: "en", Section: "chats", Filter: "all"}
	version := v.conversationListVersion()
	next := v
	next.ChatID, next.ChatOpen = testChat, true
	next.Selected = &engineapi.Conversation{Id: testChat}
	next.CSRF = "new browser token"
	if next.conversationListVersion() != version {
		t.Fatal("selection or browser metadata would unnecessarily replace inbox rows")
	}
	changed := v
	changed.Chats = append([]engineapi.Conversation(nil), v.Chats...)
	changed.Chats[0].UnreadCount++
	if changed.conversationListVersion() == version {
		t.Fatal("new unread data would not reach the inbox")
	}
}
