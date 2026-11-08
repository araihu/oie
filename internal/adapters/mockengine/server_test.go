package mockengine

import (
	"context"
	"github.com/araihu/oie/internal/adapters/previewapi"
	"net/http"
	"net/http/httptest"
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
