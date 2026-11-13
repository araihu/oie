package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
)

func TestSharedContentHTTPAggregatesRetainedSourcesWithoutSending(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	for i, body := range []string{"Imagem ÁRVORE https://example.test/one.png", "https://example.test/two.png", "Documento https://example.test/report.pdf", "Link https://example.test/article"} {
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: body, OriginalText: body, Historical: true, CreatedAt: time.Date(2020, 1, 1, 1, i, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	initial, err := client.ListSharedContentWithResponse(t.Context(), nil)
	if err != nil || initial.JSON200 == nil || initial.JSON200.Indexing.State != "pending" {
		t.Fatal("index progress", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	var page *engineapi.SharedContentPage
	for time.Now().Before(deadline) {
		res, err := client.ListSharedContentWithResponse(t.Context(), nil)
		if err != nil || res.JSON200 == nil {
			t.Fatal(res, err)
		}
		page = res.JSON200
		if page.Indexing.State == "complete" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if page == nil || len(page.Items) != 2 || page.Indexing.IndexedMessages != 4 || page.FirstPageCounts != nil {
		t.Fatal("global image catalog", page)
	}
	item := page.Items[0]
	if item.Message.AccountId != string(c.AccountID) || item.Message.ConversationId != string(c.ID) || item.Preview == nil || item.Preview.MessageId != item.Message.Id || item.Message.CreatedAt.Year() != 2020 || item.Preview.DownloadState != "not_downloaded" {
		t.Fatal("source context/original time", item)
	}
	n := 1
	first, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{Limit: &n})
	if err != nil || first.JSON200 == nil || first.JSON200.NextCursor == nil {
		t.Fatal("keyset cursor", err)
	}
	second, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{Limit: &n, Cursor: first.JSON200.NextCursor})
	if err != nil || second.JSON200 == nil || len(second.JSON200.Items) != 1 || second.JSON200.Items[0].Id == first.JSON200.Items[0].Id {
		t.Fatal("cursor duplicate", err)
	}
	// A cursor changes the requested items and leaves the three head counts intact.
	includeCounts := true
	withCounts, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{
		Limit: &n, Cursor: first.JSON200.NextCursor, IncludeFirstPageCounts: &includeCounts,
	})
	if err != nil || withCounts.JSON200 == nil || withCounts.JSON200.FirstPageCounts == nil ||
		*withCounts.JSON200.FirstPageCounts != (engineapi.SharedContentFirstPageCounts{Media: 1, Links: 1, Documents: 1}) {
		t.Fatalf("bounded first-page counts for a cursor request: %+v, %v", withCounts, err)
	}
	search := "arvore"
	account, conversation := string(c.AccountID), string(c.ID)
	found, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{
		Q: &search, AccountId: &account, ConversationId: &conversation, IncludeFirstPageCounts: &includeCounts,
	})
	if err != nil || found.JSON200 == nil || len(found.JSON200.Items) != 1 || found.JSON200.FirstPageCounts == nil ||
		*found.JSON200.FirstPageCounts != (engineapi.SharedContentFirstPageCounts{Media: 1}) {
		t.Fatal("Unicode source metadata search", err)
	}
	kind := engineapi.ListSharedContentParamsType("documents")
	docs, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{Type: &kind})
	if err != nil || docs.JSON200 == nil || len(docs.JSON200.Items) != 1 || docs.JSON200.Items[0].Preview != nil || docs.JSON200.Items[0].FileName != "report.pdf" {
		t.Fatal("document reference fallback", err)
	}
	invalid := "bad cursor"
	bad, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{Cursor: &invalid})
	if err != nil || bad.StatusCode() != 400 {
		t.Fatal("invalid cursor accepted", err)
	}
	missing := string(id())
	absent, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{AccountId: &missing})
	if err != nil || absent.StatusCode() != 404 {
		t.Fatal("unknown owner accepted", err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("catalog queued sends", err)
	}
}
