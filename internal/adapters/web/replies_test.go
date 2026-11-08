package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestProtocolReplyCardPreservesOriginalAndUsesDisplayBody(t *testing.T) {
	target := testMessage
	name := "Original <sender>"
	source := "> Original quote 😀\n\n**Own body**"
	m := messageView{Message: engineapi.Message{OriginalText: source, ConversationId: testChat, Reply: &engineapi.MessageReply{Available: true, MessageId: &target, SenderName: &name, Excerpt: "<script>bad()</script> **literal**", BodyText: "**Own body**"}}}
	v := view{Locale: "pt-BR", Theme: "dark", ChatID: testChat, ChatOpen: true, AccountFilter: testAccount, FocusMessage: testMessage, Query: "Unicode 😀", Filter: "unread", Section: "chats", Text: copy{UnknownOriginal: "Unknown", OriginalUnavailable: "Unavailable"}}
	var out bytes.Buffer
	if err := replyCard(v, m).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "**literal**") {
		t.Fatal("excerpt was interpreted as markup")
	}
	link, err := url.Parse(v.replyHref(m))
	if err != nil {
		t.Fatal(err)
	}
	q := link.Query()
	for key, want := range map[string]string{"message": target, "chat": testChat, "account": testAccount, "lang": "pt-BR", "theme": "dark", "q": "Unicode 😀", "filter": "unread"} {
		if q.Get(key) != want {
			t.Errorf("reply link lost %s", key)
		}
	}
	if m.displayBody() != "**Own body**" || m.OriginalText != source {
		t.Fatal("reply changed original or retained fallback quote in body")
	}
	before := view{ChatID: testChat, Messages: []messageView{m}}
	version := before.timelineVersion()
	before.Messages[0].Reply = &engineapi.MessageReply{Available: false, Excerpt: "fallback", BodyText: "**Own body**"}
	if before.timelineVersion() == version {
		t.Fatal("reply resolution did not invalidate timeline")
	}
	if strings.Contains(v.href("chat", testRevision), "message=") {
		t.Fatal("another chat retained foreign message context")
	}
	m.Reply.Available = false
	out.Reset()
	if err := replyCard(v, m).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "data-reply-target") || !strings.Contains(out.String(), "Unavailable") {
		t.Fatal("unavailable original remained navigable")
	}
}

func TestContextReadIsBoundedAndConversationScoped(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		status                              int
		foreign, wrongTarget, omittedTarget bool
		missing, unavailable                bool
	}{
		{name: "retained context", status: 200},
		{name: "foreign projection", status: 200, foreign: true, unavailable: true},
		{name: "wrong target projection", status: 200, wrongTarget: true, unavailable: true},
		{name: "target missing from projection", status: 200, omittedTarget: true, unavailable: true},
		{name: "missing source", status: 404, missing: true},
		{name: "unavailable engine", status: 503, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/conversations/"+testChat+"/messages/"+testMessage+"/context" || r.URL.Query().Get("limit") != "50" {
					t.Errorf("unbounded/wrong context %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				account := testAccount
				if tc.foreign {
					account = testRevision
				}
				target, item := testMessage, testMessage
				if tc.wrongTarget {
					target = testRevision
				}
				if tc.omittedTarget {
					item = testRevision
				}
				_ = json.NewEncoder(w).Encode(engineapi.MessageContext{TargetId: target, HasOlder: true, HasNewer: true, Items: []engineapi.Message{{Id: item, ConversationId: testChat, AccountId: account}}})
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			v := view{ChatID: testChat, FocusMessage: testMessage}
			items, used := ui.contextMessages(context.Background(), &v, testAccount)
			if v.ContextMissing != tc.missing || v.Unavailable != tc.unavailable {
				t.Fatalf("unsafe context result %#v", v)
			}
			if !tc.missing && !tc.unavailable && (!used || len(items) != 1 || !v.ContextHasNewer || !v.ContextHasOlder) {
				t.Fatal("context flags/target lost")
			}
		})
	}
}

func TestReplyImagePresentationUsesOwnBodyOnly(t *testing.T) {
	source := "> https://images.example.test/quoted.png\n\nCaption\nhttps://images.example.test/own.png\n"
	m := messageView{Message: engineapi.Message{OriginalText: source, Reply: &engineapi.MessageReply{BodyText: "Caption\nhttps://images.example.test/own.png\n"}}}
	p := []engineapi.MessagePreview{{Kind: "image", Url: "https://images.example.test/own.png"}}
	if got := imagePresentation(m.displayBody(), p); got != "Caption\n" {
		t.Fatalf("reply own-body projection = %q", got)
	}
	if m.OriginalText != source {
		t.Fatal("reply presentation modified original")
	}
	m.Reply.BodyText = ""
	if m.presentationText() != "" {
		t.Fatal("empty reply body reintroduced fallback quote")
	}
}
