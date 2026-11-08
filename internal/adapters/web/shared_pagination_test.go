package web

import (
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestSharedPaginationScopeSeparatesCollectionsFromPages(t *testing.T) {
	v := view{EngineID: testEngine, AccountFilter: testAccount, SharedType: "media", Query: "café", Locale: "pt-BR", Theme: "dark"}
	initial := v.sharedPageScope()
	v.SharedCursor = "opaque-next-page"
	if v.sharedPageScope() != initial {
		t.Fatal("page cursor changed collection identity")
	}
	for name, mutate := range map[string]func(*view){
		"engine":       func(v *view) { v.EngineID = testRevision },
		"account":      func(v *view) { v.AccountFilter = "" },
		"conversation": func(v *view) { v.SharedConversation = testChat },
		"category":     func(v *view) { v.SharedType = "links" },
		"query":        func(v *view) { v.Query = "another search" },
		"locale":       func(v *view) { v.Locale = "en" },
		"theme":        func(v *view) { v.Theme = "light" },
	} {
		t.Run(name, func(t *testing.T) {
			next := v
			mutate(&next)
			if next.sharedPageScope() == initial {
				t.Fatal("different collections share a pagination identity")
			}
		})
	}
}

func TestDrawerPaginationUsesConversationScopeWithoutChangingInbox(t *testing.T) {
	v := view{EngineID: testEngine, SharedInDetails: true, SharedType: "media", Query: "inbox query", InfoQuery: "room query", Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount}}
	initial := v.sharedPageScope()
	v.AccountFilter = testRevision
	v.Query = "another inbox query"
	if v.sharedPageScope() != initial {
		t.Fatal("surrounding inbox filters changed the room media collection")
	}
	v.InfoQuery = "another room query"
	if v.sharedPageScope() == initial {
		t.Fatal("drawer search did not change its media collection")
	}
}
