package web

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"golang.org/x/net/html"
)

func paginationContractHTML(t *testing.T, component templ.Component) *html.Node {
	t.Helper()
	var out bytes.Buffer
	if err := component.Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func paginationContractNode(root *html.Node, attribute string) *html.Node {
	for _, attr := range root.Attr {
		if attr.Key == attribute {
			return root
		}
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := paginationContractNode(child, attribute); found != nil {
			return found
		}
	}
	return nil
}
func paginationContractAttrs(node *html.Node) map[string]string {
	attrs := map[string]string{}
	if node != nil {
		for _, attr := range node.Attr {
			attrs[attr.Key] = attr.Val
		}
	}
	return attrs
}
func TestSharedPageEnvelopeDistinguishesFailuresFromEndOfCollection(t *testing.T) {
	next := "opaque:next<&\""
	for _, variant := range []string{"ready-more", "ready-end", "unavailable", "invalid", "missing"} {
		t.Run(variant, func(t *testing.T) {
			v := view{EngineID: testEngine, Section: "media", SharedType: "media", SharedCursor: "opaque:requested<&\"", Locale: "en", Theme: "light", SharedContent: &engineapi.SharedContentPage{NextCursor: &next}}
			v.Text.SharedNext = "Load older"
			v.Text.Retry = "Retry"
			v.Text.SharedLoadingOlder = "Loading older"
			v.Text.SharedLoadFailed = "Could not load older"
			wantState := "ready"
			wantNext := next
			switch variant {
			case "ready-end":
				v.SharedContent.NextCursor = nil
				wantNext = ""
			case "unavailable":
				v.Unavailable = true
				v.SharedContent = nil
				wantState = "unavailable"
				wantNext = ""
			case "invalid":
				v.SharedInvalid = true
				v.SharedContent = nil
				wantState = "invalid"
				wantNext = ""
			case "missing":
				v.SharedContent = nil
				wantState = "unavailable"
				wantNext = ""
			}
			doc := paginationContractHTML(t, sharedPagination(v))
			page := paginationContractNode(doc, "data-shared-page")
			if page == nil {
				t.Fatal("page metadata absent at error or end")
			}
			attrs := paginationContractAttrs(page)
			required := map[string]string{"data-shared-page-engine": testEngine, "data-shared-page-scope": v.sharedPageScope(), "data-shared-page-cursor": v.SharedCursor, "data-shared-page-state": wantState, "data-shared-page-next": wantNext}
			for key, want := range required {
				got, exists := attrs[key]
				if !exists || got != want {
					t.Fatalf("%s: got %q exists=%t want %q", key, got, exists, want)
				}
			}
			status := paginationContractNode(page, "data-shared-page-status")
			if status == nil || paginationContractAttrs(status)["role"] != "status" || paginationContractAttrs(status)["aria-live"] != "polite" {
				t.Fatal("loading/retry status is not accessible")
			}
			link := paginationContractNode(page, "data-shared-next")
			if wantNext == "" {
				if link == nil || link.Data != "a" {
					t.Fatal("terminal/error page lacks its retry control")
				}
				if _, hidden := paginationContractAttrs(link)["hidden"]; !hidden {
					t.Fatal("terminal/error page offers visible unproven pagination")
				}
				return
			}
			if link == nil || link.Data != "a" {
				t.Fatal("native fallback pagination link missing")
			}
			if _, hidden := paginationContractAttrs(link)["hidden"]; hidden {
				t.Fatal("proven next page is hidden from native fallback")
			}
			href, err := url.Parse(paginationContractAttrs(link)["href"])
			if err != nil || href.Query().Get("contentCursor") != next {
				t.Fatal("opaque next cursor changed by rendering", href, err)
			}
		})
	}
}
func TestSharedDetailsScopeBindsEffectiveQueryAndIdentity(t *testing.T) {
	details := view{EngineID: testEngine, Section: "chats", SharedInDetails: true, SharedType: "media", AccountFilter: testRevision, SharedConversation: testRevision, Query: "global search", InfoQuery: "room search", Locale: "en", Theme: "dark", Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount}}
	expected := details.sharedPageScope()
	unrelated := details
	unrelated.Query = "global search changed"
	unrelated.AccountFilter = testMessage
	unrelated.SharedConversation = testMessage
	unrelated.SharedCursor = "different-room-page"
	unrelated.InfoCursor = "also-different"
	if unrelated.sharedPageScope() != expected {
		t.Fatal("details scope inherited unrelated inbox filters or cursor")
	}
	global := details
	global.SharedInDetails = false
	global.AccountFilter = testAccount
	global.SharedConversation = testChat
	global.Query = details.InfoQuery
	if global.sharedPageScope() == expected {
		t.Fatal("global and drawer collections share an identity")
	}
	changedQuery := details
	changedQuery.InfoQuery = "different room search"
	if changedQuery.sharedPageScope() == expected {
		t.Fatal("details search is absent from scope")
	}
	changedAccount := details
	selected := *details.Selected
	selected.AccountId = testMessage
	changedAccount.Selected = &selected
	if changedAccount.sharedPageScope() == expected {
		t.Fatal("details owning account is absent from scope")
	}
	changedChat := details
	selected = *details.Selected
	selected.Id = testMessage
	changedChat.Selected = &selected
	if changedChat.sharedPageScope() == expected {
		t.Fatal("details conversation is absent from scope")
	}
}

func TestSharedItemOrderingMetadataPreservesExactMilliseconds(t *testing.T) {
	second := time.Date(2026, 11, 8, 23, 59, 59, 0, time.UTC)
	v := view{EngineID: testEngine, Section: "media", SharedType: "documents", Locale: "en", Theme: "light"}
	values := []time.Time{second.Add(123456789 * time.Nanosecond), second.Add(987654321 * time.Nanosecond), time.Unix(-1, 123456789)}
	seen := map[string]bool{}
	for _, at := range values {
		item := engineapi.SharedContentItem{Id: testRevision, Kind: "document", Url: "https://example.test/report.pdf", FileName: "report.pdf", Message: engineapi.Message{Id: testMessage, AccountId: testAccount, ConversationId: testChat, CreatedAt: at}}
		doc := paginationContractHTML(t, sharedItem(v, item))
		node := paginationContractNode(doc, "data-shared-item")
		if node == nil {
			t.Fatal("shared item missing")
		}
		attrs := paginationContractAttrs(node)
		want := strconv.FormatInt(at.UnixMilli(), 10)
		if attrs["data-shared-created-ms"] != want || attrs["data-shared-item"] != item.Id {
			t.Fatal("ordering tuple loses precision or identity", attrs, want)
		}
		if seen[want] {
			t.Fatal("distinct milliseconds collapsed")
		}
		seen[want] = true
	}
}
func TestDetailsPaginationKeepsItsOwnOpaqueCursor(t *testing.T) {
	next := "room:opaque/next"
	v := view{EngineID: testEngine, Section: "chats", SharedInDetails: true, SharedType: "media", SharedCursor: "room:requested", InfoCursor: "room:requested", InfoView: "shared", InfoType: "media", ChatID: testChat, ChatOpen: true, Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount}, SharedContent: &engineapi.SharedContentPage{NextCursor: &next}, Locale: "en", Theme: "system"}
	doc := paginationContractHTML(t, sharedPagination(v))
	link := paginationContractNode(doc, "data-shared-next")
	if link == nil {
		t.Fatal("details fallback link absent")
	}
	href, err := url.Parse(paginationContractAttrs(link)["href"])
	if err != nil {
		t.Fatal(err)
	}
	q := href.Query()
	if q.Get("infoCursor") != next || q.Get("chat") != testChat || q.Get("open") != "details" || q.Get("infoView") != "shared" {
		t.Fatal("details pagination escaped current drawer", q)
	}
}
