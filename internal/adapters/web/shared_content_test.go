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

func TestSharedContentReadsRemainScopedAndPreviewBound(t *testing.T) {
	for _, variant := range []string{"valid", "foreign account", "foreign conversation", "foreign preview", "wrong category", "unsafe link"} {
		t.Run(variant, func(t *testing.T) {
			p := engineapi.MessagePreview{Id: testRevision, AccountId: testAccount, ConversationId: testChat, MessageId: testMessage, Kind: "image", Url: "https://images.example.test/photo.png"}
			item := engineapi.SharedContentItem{Id: testRevision, Kind: "image", Url: p.Url, Message: engineapi.Message{Id: testMessage, AccountId: testAccount, ConversationId: testChat}, Preview: &p}
			switch variant {
			case "foreign account":
				item.Message.AccountId = testRevision
			case "foreign conversation":
				item.Message.ConversationId = testRevision
			case "foreign preview":
				p.MessageId = testRevision
			case "wrong category":
				item.Kind = "document"
			case "unsafe link":
				item.Url = "javascript:alert(1)"
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != "/api/v1/shared-content" || q.Get("type") != "media" || q.Get("limit") != "50" || q.Get("accountId") != testAccount || q.Get("conversationId") != testChat || q.Get("q") != "café 😀" || q.Get("cursor") != "opaque:cursor" {
					t.Errorf("lost scope: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(engineapi.SharedContentPage{Items: []engineapi.SharedContentItem{item}})
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			v := view{SharedType: "media", AccountFilter: testAccount, SharedConversation: testChat, Query: "café 😀", SharedCursor: "opaque:cursor", Accounts: []engineapi.Account{{Id: testAccount, Address: "alice@example.test"}}}
			ui.loadSharedContent(context.Background(), &v)
			if (v.SharedContent != nil) != (variant == "valid") {
				t.Fatal("unscoped or unsafe catalog projection accepted")
			}
		})
	}
}
func TestSharedSourceNavigationAndPreviewVersions(t *testing.T) {
	v := view{Section: "media", SharedType: "links", SharedCursor: "cursor", SharedConversation: testChat, AccountFilter: testAccount, Query: "café", Locale: "pt-BR", Theme: "dark"}
	item := engineapi.SharedContentItem{Id: testRevision, Message: engineapi.Message{Id: testMessage, ConversationId: testChat, AccountId: testAccount}, Preview: &engineapi.MessagePreview{RevisionId: testRevision}}
	source, err := url.Parse(v.sharedSourceHref(item))
	if err != nil {
		t.Fatal(err)
	}
	q := source.Query()
	if q.Get("chat") != testChat || q.Get("message") != testMessage || q.Get("section") == "media" || q.Get("contentCursor") != "" || q.Get("q") != "" || q.Get("lang") != "pt-BR" || q.Get("theme") != "dark" {
		t.Fatalf("source navigation lost its origin: %v", q)
	}
	v.SharedContent = &engineapi.SharedContentPage{Items: []engineapi.SharedContentItem{item}}
	version := v.sharedVersion()
	v.SharedContent.Items[0].Preview.RevisionId = testMessage
	if v.sharedVersion() != version {
		t.Fatal("preview enrichment forces gallery rebuild")
	}
	v.SharedContent.Items[0].ConversationTitle = "Changed chat label"
	if v.sharedVersion() == version {
		t.Fatal("source context update does not refresh gallery")
	}
}
