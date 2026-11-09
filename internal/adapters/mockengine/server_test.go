package mockengine

import (
	"context"
	"encoding/json"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/previewapi"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestMockUsesGeneratedReadContractAndStableFixtureIdentities(t *testing.T) {
	handler, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := previewapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	all, err := client.ListConversationsWithResponse(ctx, &previewapi.ListConversationsParams{})
	if err != nil || all.JSON200 == nil || len(all.JSON200.Items) != 10 || all.HTTPResponse.Header.Get("X-Oie-Mock") != "true" {
		t.Fatalf("mock read failed: %v %+v", err, all)
	}
	yes := true
	unread, err := client.ListConversationsWithResponse(ctx, &previewapi.ListConversationsParams{UnreadOnly: &yes})
	if err != nil || unread.JSON200 == nil || len(unread.JSON200.Items) != 3 {
		t.Fatal("unread filter did not affect API results")
	}
	one := 1
	first, _ := client.ListConversationsWithResponse(ctx, &previewapi.ListConversationsParams{Limit: &one})
	second, _ := client.ListConversationsWithResponse(ctx, &previewapi.ListConversationsParams{Limit: &one, Cursor: first.JSON200.NextCursor})
	if first.JSON200.NextCursor == nil || second.JSON200.Items[0].Id == first.JSON200.Items[0].Id {
		t.Fatal("cursor did not advance")
	}
	seen := map[string]bool{}
	for _, c := range all.JSON200.Items {
		messages, err := client.ListMessagesWithResponse(ctx, c.Id, &previewapi.ListMessagesParams{})
		if err != nil || messages.JSON200 == nil {
			t.Fatalf("timeline failed: %v", err)
		}
		for _, m := range messages.JSON200.Items {
			if seen[m.Id] {
				t.Fatalf("message identity reused across conversations: %s", m.Id)
			}
			seen[m.Id] = true
			if m.ConversationId != c.Id || m.AccountId != c.AccountId || m.CreatedAt.Location().String() != "UTC" {
				t.Fatal("timeline identity or UTC mismatch")
			}
		}
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/conversations", nil)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 400 {
		t.Fatal("read-only mock accepted a mutation")
	}
}

func TestMockValidatesItsReadContract(t *testing.T) {
	handler, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/v1/conversations?limit=0", 400}, {"/api/v1/conversations/not-a-uuid", 400}, {"/api/v1/conversations?cursor=foreign:1", 409}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || w.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s status=%d content-type=%s", tc.path, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

func TestMockUnifiedSearchUsesRetainedSamplesAndNoInventedContacts(t *testing.T) {
	handler, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	account, conversation := id(2), id(102)
	query := "the"
	limit := 1
	params := engineapi.SearchMessagesParams{Q: query, AccountId: &account, ConversationId: &conversation, Limit: &limit}
	seen := map[string]bool{}
	var firstCursor string
	var previous *engineapi.MessageSearchHit
	for range 20 {
		response, err := client.SearchMessagesWithResponse(t.Context(), &params)
		if err != nil || response.JSON200 == nil {
			t.Fatalf("mock search failed: %v %+v", err, response)
		}
		page := response.JSON200
		if response.HTTPResponse.Header.Get("X-Oie-Mock") != "true" || page.Query != query || page.AccountId == nil || *page.AccountId != account || page.ConversationId == nil || *page.ConversationId != conversation || page.IncludeEditHistory || page.MetadataIndexing.State != "complete" || page.MetadataIndexing.TotalMessages != 7 || page.MetadataIndexing.IndexedMessages != 7 || len(page.Items) > 1 {
			t.Fatal("mock lost query scope or sample coverage", page)
		}
		for _, hit := range page.Items {
			if hit.Message.AccountId != account || hit.Message.ConversationId != conversation || hit.Historical || hit.MatchedRevisionId != hit.Message.RevisionId || hit.MatchKind != "text" || !strings.Contains(strings.ToLower(hit.Excerpt), query) || seen[hit.Message.Id] {
				t.Fatal("invented or repeated sample result", hit)
			}
			if previous != nil && (previous.Message.CreatedAt.Before(hit.Message.CreatedAt) || previous.Message.CreatedAt.Equal(hit.Message.CreatedAt) && previous.Message.Id < hit.Message.Id) {
				t.Fatal("sample search not newest first")
			}
			copy := hit
			previous = &copy
			seen[hit.Message.Id] = true
		}
		if page.NextCursor == nil {
			break
		}
		if firstCursor == "" {
			firstCursor = *page.NextCursor
		}
		params.Cursor = page.NextCursor
	}
	if len(seen) != 3 || firstCursor == "" {
		t.Fatal("sample text not filtered before pagination", seen)
	}
	params.Cursor = nil
	params.Q = "Nina Martins"
	response, err := client.SearchMessagesWithResponse(t.Context(), &params)
	if err != nil || response.JSON200 == nil || len(response.JSON200.Items) != 0 {
		t.Fatal("person name invented matching message", err)
	}
	params.Q = "zz-no-match"
	response, err = client.SearchMessagesWithResponse(t.Context(), &params)
	if err != nil || response.JSON200 == nil || response.JSON200.Items == nil || len(response.JSON200.Items) != 0 {
		t.Fatal("no-match query not an explicit empty result", err)
	}
	params.Q = "fresh air"
	response, err = client.SearchMessagesWithResponse(t.Context(), &params)
	if err != nil || response.JSON200 == nil || len(response.JSON200.Items) != 1 || response.JSON200.Items[0].MatchKind != "metadata" || !strings.Contains(response.JSON200.Items[0].Excerpt, "fresh air") {
		t.Fatal("available sample attachment metadata not searched", err)
	}
	for _, scope := range []*string{nil, &account} {
		contacts, err := client.ListContactsWithResponse(t.Context(), &engineapi.ListContactsParams{AccountId: scope})
		if err != nil || contacts.JSON200 == nil || contacts.JSON200.Items == nil || len(contacts.JSON200.Items) != 0 || len(contacts.JSON200.Sources) != 1 || contacts.JSON200.Sources[0].AccountId != account || contacts.JSON200.Sources[0].State != "known" {
			t.Fatal("mock inferred contacts from conversation names", contacts, err)
		}
	}
	history := true
	params = engineapi.SearchMessagesParams{Q: query, ConversationId: &conversation, IncludeEditHistory: &history}
	response, err = client.SearchMessagesWithResponse(t.Context(), &params)
	if err != nil || response.JSON200 == nil || !response.JSON200.IncludeEditHistory || len(response.JSON200.Items) != 3 {
		t.Fatal("explicit history invented fixture revisions", err)
	}
	for _, hit := range response.JSON200.Items {
		if hit.Historical {
			t.Fatal("mock fabricated edit history")
		}
	}
	// All shared data remains a read-only sample. Queries neither change the
	// ordinary list nor create receipts or provider operations.
	preview, _ := previewapi.NewClientWithResponses(server.URL)
	before, _ := preview.ListConversationsWithResponse(t.Context(), &previewapi.ListConversationsParams{})
	_, _ = client.SearchMessagesWithResponse(t.Context(), &params)
	after, _ := preview.ListConversationsWithResponse(t.Context(), &previewapi.ListConversationsParams{})
	if !reflect.DeepEqual(before.JSON200, after.JSON200) {
		t.Fatal("search changed sample conversation state")
	}
	for _, path := range []string{"/api/v1/messages/search", "/api/v1/contacts"} {
		request, _ := http.NewRequest(http.MethodPost, server.URL+path, nil)
		reply, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = reply.Body.Close()
		if reply.StatusCode < 400 {
			t.Fatal("mock accepted a search mutation")
		}
	}
	for name, changed := range map[string]engineapi.SearchMessagesParams{"query": {Q: "other", Cursor: &firstCursor}, "conversation": {Q: query, Cursor: &firstCursor}, "history": {Q: query, AccountId: &account, ConversationId: &conversation, IncludeEditHistory: &history, Cursor: &firstCursor}} {
		reply, err := client.SearchMessagesWithResponse(t.Context(), &changed)
		if err != nil || reply.StatusCode() != 400 {
			t.Fatalf("%s reused another mock query cursor: %v %+v", name, err, reply)
		}
	}
}
func TestMockUnifiedSearchRejectsInvalidInputAndKeepsEmptyMode(t *testing.T) {
	handler, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/api/v1/messages/search", 400}, {"/api/v1/messages/search?q=%20%20", 400}, {"/api/v1/messages/search?q=%00", 400}, {"/api/v1/messages/search?q=%ff", 400}, {"/api/v1/messages/search?q=the&limit=0", 400}, {"/api/v1/messages/search?q=the&limit=101", 400}, {"/api/v1/messages/search?q=the&includeEditHistory=maybe", 400}, {"/api/v1/messages/search?q=the&accountId=invalid", 400}, {"/api/v1/messages/search?q=the&conversationId=" + id(999), 404}, {"/api/v1/messages/search?q=the&accountId=" + id(999), 404}, {"/api/v1/messages/search?q=the&q=other", 400}, {"/api/v1/messages/search?q=the&cursor=foreign", 400}, {"/api/v1/contacts?accountId=" + id(999), 404}, {"/api/v1/contacts?cursor=foreign", 400}, {"/api/v1/contacts?limit=0", 400}, {"/api/v1/contacts?q=%00", 400}, {"/api/v1/contacts?q=" + strings.Repeat("a", 201), 400},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.status || recorder.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("%s returned%d %s", test.path, recorder.Code, recorder.Body)
		}
	}
	empty, err := New(true)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	empty.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/messages/search?q=the", nil))
	var page engineapi.MessageSearchPage
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &page) != nil || page.Items == nil || len(page.Items) != 0 || page.MetadataIndexing.TotalMessages != 0 || page.MetadataIndexing.State != "complete" {
		t.Fatal("empty sample generated search hits", recorder.Body)
	}
}
