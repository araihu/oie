package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func searchTestPage() engineapi.MessageSearchPage {
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	return engineapi.MessageSearchPage{Query: "needle", Items: []engineapi.MessageSearchHit{{Message: engineapi.Message{Id: testMessage, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat, CreatedAt: at}, MatchedRevisionId: testRevision, MatchedAt: at, MatchKind: "text", Excerpt: "<img src=x onerror=alert(1)>"}}, MetadataIndexing: engineapi.SharedContentIndexing{State: "complete", IndexedMessages: 1, TotalMessages: 1}}
}
func TestMessageSearchPageContentIdentityAndMillisecondOrdering(t *testing.T) {
	request := messageSearchView{Query: "needle", Limit: 30}
	page := searchTestPage()
	page.Items[0].Message.SendState = "cancelled"
	page.Items[0].MatchedRevisionId = testChat // retained content, different transport head
	if !validMessageSearchPage(page, request) {
		t.Fatal("current content rejected after delivery revision")
	}
	newer := page.Items[0]
	newer.Message.Id = "019a5df0-0000-7000-8000-999999999999"
	newer.Message.CreatedAt = newer.Message.CreatedAt.Add(100 * time.Nanosecond)
	page.Items[0].Message.CreatedAt = page.Items[0].Message.CreatedAt.Add(900 * time.Nanosecond)
	page.Items = append([]engineapi.MessageSearchHit{newer}, page.Items...)
	if !validMessageSearchPage(page, request) {
		t.Fatal("engine millisecond/UUID order rejected for opposite sub-ms timestamps")
	}
	page.Items[1].Historical = true
	if validMessageSearchPage(page, request) {
		t.Fatal("history accepted without explicit flag")
	}
	request.IncludeHistory = true
	page.IncludeEditHistory = true
	if !validMessageSearchPage(page, request) {
		t.Fatal("explicit retained history rejected")
	}
	page.Items[1].Message.AccountId = testChat
	request.AccountID = testAccount
	page.AccountId = &request.AccountID
	if validMessageSearchPage(page, request) {
		t.Fatal("foreign result admitted")
	}
}
func TestMessageSearchReadOnlyScopedEscapedRecovery(t *testing.T) {
	reads := 0
	fail := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("effectful search: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.21.0", "eventCursor": testEngine + ":0"})
		case "/api/v1/contacts":
			w.WriteHeader(404)
		case "/api/v1/conversations":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/v1/accounts":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/v1/conversations/" + testChat:
			_ = json.NewEncoder(w).Encode(engineapi.Conversation{Id: testChat, AccountId: testAccount, Title: "Saved room"})
		case "/api/v1/messages/search":
			reads++
			if fail {
				w.WriteHeader(503)
				return
			}
			page := searchTestPage()
			if r.URL.Query().Get("accountId") != "" {
				a := r.URL.Query().Get("accountId")
				page.AccountId = &a
			}
			if r.URL.Query().Get("conversationId") != "" {
				c := r.URL.Query().Get("conversationId")
				page.ConversationId = &c
			}
			page.IncludeEditHistory = r.URL.Query().Get("includeEditHistory") == "true"
			_ = json.NewEncoder(w).Encode(page)
		default:
			t.Errorf("search attempted unrelated/enrichment read: %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	prefix := "/ui/message-search?engineId=" + testEngine
	if w := get(prefix + "&q=%20%20"); w.Code != 200 || reads != 0 {
		t.Fatalf("blank query called search: %d %d", w.Code, reads)
	}
	if w := get(prefix + "&q=needle&accountId=" + testAccount + "&conversationId=" + testChat); w.Code != 200 || reads != 1 || strings.Contains(w.Body.String(), "<img src=x") || !strings.Contains(w.Body.String(), "&lt;img") || !strings.Contains(w.Body.String(), "message="+testMessage) {
		t.Fatalf("scoped escaped current-source projection failed: %d %s", w.Code, w.Body.String())
	}
	if w := get(prefix + "&q=needle&accountId=" + testAccount + "&nativeAccountId="); w.Code != 200 || !strings.Contains(w.Body.String(), `data-search-account="`+testAccount+`"`) {
		t.Fatal("fragment native fallback field overrode explicit account scope")
	}

	for _, path := range []string{prefix + "&q=" + url.QueryEscape(strings.Repeat("a", 201)), prefix + "&q=needle&accountId=foreign", prefix + "&q=needle&includeEditHistory=yes", "/ui/message-search?q=needle", "/ui/message-search?engineId=" + testChat + "&q=needle"} {
		if w := get(path); w.Code < 400 {
			t.Fatalf("invalid scope accepted: %s", path)
		}
	}
	fail = true
	if w := get(prefix + "&q=needle"); w.Code != 503 || !strings.Contains(w.Body.String(), "data-message-search-retry") {
		t.Fatal("fragment transport error lost explicit retry")
	}
	if w := get("/search?q=needle&lang=en"); w.Code != 503 || !strings.Contains(w.Body.String(), "<main") || !strings.Contains(w.Body.String(), "message-search-form") {
		t.Fatal("native search error lost shell")
	}
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest("POST", prefix, nil))
	if w.Code != 405 {
		t.Fatal("fragment accepted mutation method")
	}
}

func TestMessageSearchRejectsForeignOrMalformedPages(t *testing.T) {
	request := messageSearchView{Query: "needle", AccountID: testAccount, ConversationID: testChat, Limit: 30, Cursor: "older"}
	valid := func() engineapi.MessageSearchPage {
		p := searchTestPage()
		p.AccountId = &request.AccountID
		p.ConversationId = &request.ConversationID
		return p
	}
	for _, tc := range []struct {
		name   string
		mutate func(*engineapi.MessageSearchPage)
	}{
		{"wrong query", func(p *engineapi.MessageSearchPage) { p.Query = "another" }},
		{"wrong account echo", func(p *engineapi.MessageSearchPage) { p.AccountId = nil }},
		{"wrong conversation echo", func(p *engineapi.MessageSearchPage) { p.ConversationId = nil }},
		{"history echo", func(p *engineapi.MessageSearchPage) { p.IncludeEditHistory = true }},
		{"repeated cursor", func(p *engineapi.MessageSearchPage) { p.NextCursor = &request.Cursor }},
		{"duplicate content", func(p *engineapi.MessageSearchPage) { p.Items = append(p.Items, p.Items[0]) }},
		{"foreign hit", func(p *engineapi.MessageSearchPage) { p.Items[0].Message.ConversationId = testAccount }},
		{"oversized excerpt", func(p *engineapi.MessageSearchPage) { p.Items[0].Excerpt = strings.Repeat("😀", 513) }},
		{"invalid indexing", func(p *engineapi.MessageSearchPage) { p.MetadataIndexing.IndexedMessages = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := valid()
			tc.mutate(&page)
			if validMessageSearchPage(page, request) {
				t.Fatal("foreign/malformed projection accepted")
			}
		})
	}
}

func TestSavedContactsRequireScopedDirectoryEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, state                      string
		foreign, empty, unavailable      bool
		wantKnown, wantPartial, wantRows bool
	}{
		{name: "known roster", state: "known", wantKnown: true, wantRows: true},
		{name: "known empty", state: "known", empty: true, wantKnown: true},
		{name: "unobserved", state: "unknown", empty: true, wantPartial: true},
		{name: "cached unavailable", state: "unavailable", wantPartial: true, wantRows: true},
		{name: "foreign source", state: "known", foreign: true},
		{name: "old API unavailable", unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/contacts" || r.URL.Query().Get("q") != "needle" || r.URL.Query().Get("accountId") != testAccount || r.URL.Query().Get("limit") != "20" {
					t.Errorf("unexpected directory request %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.unavailable {
					w.WriteHeader(404)
					return
				}
				owner := testAccount
				if tc.foreign {
					owner = testChat
				}
				page := engineapi.ContactPage{Items: []engineapi.Contact{}, Sources: []engineapi.ContactSource{{AccountId: owner, State: engineapi.ContactSourceState(tc.state)}}}
				if !tc.empty {
					page.Items = append(page.Items, engineapi.Contact{Id: testMessage, RevisionId: testRevision, AccountId: owner, AccountLabel: "Account", Address: "peer@example.test", DisplayName: "<img> needle", Subscription: "none"})
				}
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			v := view{MessageSearch: messageSearchView{Query: "needle", AccountID: testAccount}}
			ui.loadSearchContacts(t.Context(), &v)
			if v.MessageSearch.ContactsKnown != tc.wantKnown || v.MessageSearch.ContactsPartial != tc.wantPartial || (len(v.MessageSearch.Contacts) > 0) != tc.wantRows {
				t.Fatalf("directory coverage inferred or foreign entries exposed: %#v", v.MessageSearch)
			}
		})
	}
}

func TestSearchNavigationSeparatesGlobalAndConversationScopes(t *testing.T) {
	v := view{GlobalMessageSearch: true, ConversationSearchOpen: true, ConversationSearchQuery: "inner", ConversationSearchHistory: true, Section: "chats", Filter: "unread", Query: "old title", ChatID: testChat, ChatOpen: true, Locale: "en", Theme: "dark", EngineID: testEngine, MessageSearch: messageSearchView{Query: "outer", AccountID: testAccount, IncludeHistory: true}}
	same, _ := url.Parse(v.href("chat", testChat))
	if same.Query().Get("chatQuery") != "inner" || same.Query().Get("searchQuery") != "outer" {
		t.Fatal("same-chat navigation lost an independent query")
	}
	changed, _ := url.Parse(v.href("chat", testMessage))
	if changed.Query().Has("chatQuery") || changed.Query().Has("chatSearch") || changed.Query().Get("searchQuery") != "outer" {
		t.Fatal("different conversation inherited the old conversation query")
	}
	contact := engineapi.Contact{AccountId: testAccount, Address: "friend@example.test"}
	target, _ := url.Parse(v.messageSearchContactHref(contact))
	if target.Path != "/" || target.Query().Get("open") != "newchat" || target.Query().Get("newChatAddress") != contact.Address || target.Query().Get("account") != testAccount {
		t.Fatal("new contact did not lead to an explicit prefilled conversation form")
	}
	contact.ConversationId = &v.ChatID
	target, _ = url.Parse(v.messageSearchContactHref(contact))
	if target.Query().Get("chat") != testChat || target.Query().Has("newChatAddress") {
		t.Fatal("existing contact conversation was not reused")
	}
	category, _ := url.Parse(v.messageSearchCategoryHref("documents"))
	if category.Query().Get("section") != "media" || category.Query().Get("q") != "outer" || category.Query().Has("searchMode") || category.Query().Has("chatQuery") {
		t.Fatal("category did not use the existing scoped catalog")
	}
}

func TestEmptyGlobalSearchQualifiesUnavailableContactCoverage(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, known := range []bool{false, true} {
		v := view{Locale: "en", Text: catalogs["en"], EngineID: testEngine, MessageSearch: messageSearchView{Query: "needle", State: "ready", ContactsKnown: known, Page: &engineapi.MessageSearchPage{Items: []engineapi.MessageSearchHit{}, MetadataIndexing: engineapi.SharedContentIndexing{State: "complete"}}}}
		var out strings.Builder
		if err := messageSearchResponse(v).Render(presentationContext(t.Context(), v), &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "data-message-search-contact-coverage") == known {
			t.Fatalf("unknown contacts became a complete empty result: known=%v %s", known, out.String())
		}
		if strings.Contains(out.String(), "data-message-search-contacts") {
			t.Fatal("fabricated contacts group for an empty directory")
		}
	}
}

func TestLiveRefreshDoesNotRequeryGlobalSearch(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.22.0", "eventCursor": testEngine + ":0"})
		case "/api/v1/accounts", "/api/v1/conversations":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			t.Errorf("live refresh reissued a search/group read: %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest("GET", "/ui/refresh?searchMode=messages&searchQuery=needle", nil))
	if w.Code != 200 {
		t.Fatalf("live refresh failed: %d", w.Code)
	}
}
