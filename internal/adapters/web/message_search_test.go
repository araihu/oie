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
