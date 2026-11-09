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

func TestConversationSearchDetailsLinkHasLocalizedVisibleName(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "pt-BR"} {
		t.Run(locale, func(t *testing.T) {
			v := view{EngineID: testEngine, Locale: locale, Theme: "dark", Text: catalogs[locale], Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount, Title: "Saved conversation", Kind: "direct"}}
			var out strings.Builder
			if err := chatInfoContent(v).Render(t.Context(), &out); err != nil {
				t.Fatal(err)
			}
			document, err := html.Parse(strings.NewReader(out.String()))
			if err != nil {
				t.Fatal(err)
			}
			link := paginationContractNode(document, "data-open-conversation-search")
			if link == nil || link.Data != "a" {
				t.Fatal("missing native conversation search link")
			}
			var visible strings.Builder
			var collect func(*html.Node)
			collect = func(node *html.Node) {
				for _, attr := range node.Attr {
					if attr.Key == "aria-hidden" && attr.Val == "true" {
						return
					}
				}
				if node.Type == html.TextNode {
					visible.WriteString(node.Data)
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					collect(child)
				}
			}
			collect(link)
			if strings.TrimSpace(visible.String()) != v.Text.SearchMessages || v.Text.SearchMessages == "" {
				t.Fatalf("search link lost its visible localized name: %q", visible.String())
			}
			address, err := url.Parse(paginationContractAttrs(link)["href"])
			if err != nil || address.Query().Get("accountId") != testAccount || address.Query().Get("conversationId") != testChat {
				t.Fatal("native search link lost its owning conversation scope", address, err)
			}
		})
	}
}
