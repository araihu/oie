package web

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"golang.org/x/net/html"
)

// Read-only search ledger: closed has no query effects; open retains exact query
// and independent global state; native submission is scoped to this engine/chat;
// missing selected conversation renders no toolbar. Browser acceptance owns
// debounce/IME, stale responses, missing targets, and bounded timeline focus.
func TestConversationSearchToolbarScopeNativeFallbackAndEscaping(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "pt-BR"} {
		v := view{EngineID: testEngine, Locale: locale, Theme: "dark", Section: "chats", Filter: "all", ChatOpen: true, ChatID: testChat, Text: catalogs[locale], Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount}, ConversationSearchOpen: true, ConversationSearchQuery: `<script>" 😀`, ConversationSearchHistory: true}
		var out strings.Builder
		if err := conversationSearchBar(v).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		document, err := html.Parse(strings.NewReader(out.String()))
		if err != nil {
			t.Fatal(err)
		}
		nodes := map[string]*html.Node{}
		var form *html.Node
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode {
				for _, a := range n.Attr {
					if a.Key == "id" {
						if nodes[a.Val] != nil {
							t.Fatalf("duplicate toolbar ID %s", a.Val)
						}
						nodes[a.Val] = n
					}
				}
				if n.Data == "form" {
					form = n
				}
				if n.Data == "script" {
					t.Fatal("query became script markup")
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(document)
		attr := func(n *html.Node, key string) string {
			if n == nil {
				t.Fatalf("missing node for %s", key)
			}
			for _, a := range n.Attr {
				if a.Key == key {
					return a.Val
				}
			}
			return ""
		}
		if attr(nodes["conversation-message-search"], "data-search-engine") != testEngine || attr(nodes["conversation-message-search"], "data-search-account") != testAccount || attr(nodes["conversation-message-search"], "data-search-conversation") != testChat || attr(nodes["conversation-search-query"], "value") != v.ConversationSearchQuery {
			t.Fatal("toolbar lost source scope or Unicode input")
		}
		if attr(form, "action") != "/search" || attr(form, "method") != "get" {
			t.Fatal("native scoped search unavailable")
		}
		fields := map[string]string{}
		var gather func(*html.Node)
		gather = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "input" {
				fields[attr(n, "name")] = attr(n, "value")
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				gather(c)
			}
		}
		gather(form)
		if fields["engineId"] != testEngine || fields["accountId"] != testAccount || fields["conversationId"] != testChat || fields["q"] != v.ConversationSearchQuery {
			t.Fatal("native form lost source scope", fields)
		}
		if attr(nodes["conversation-search-query"], "aria-label") == "" || attr(nodes["conversation-search-status"], "role") != "status" {
			t.Fatal("unlabelled search state")
		}
		v.ConversationSearchOpen = false
		out.Reset()
		if err := conversationSearchBar(v).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `data-search-open="false"`) || !strings.Contains(out.String(), " hidden") {
			t.Fatal("closed search not hidden")
		}
		closeURL, err := url.Parse(v.conversationSearchCloseHref())
		if err != nil || closeURL.Query().Get("chat") != testChat || closeURL.Query().Has("chatSearch") {
			t.Fatal("close loses selected conversation")
		}
	}
}
func TestConversationSearchDoesNotRenderWithoutSelectedConversation(t *testing.T) {
	var out strings.Builder
	if err := conversationSearchBar(view{}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("toolbar exposes an unscoped search")
	}
}
