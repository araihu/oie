package httpapi_test

import (
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"testing"
	"time"
)

func TestMessageEditHistoryContractIsScopedReadOnlyAndSnapshotBound(t *testing.T) {
	protocol := &protocolStub{}
	e, store, server := newMessagingEngine(t, t.TempDir(), protocol)
	c := openTestConversation(t, e, "alice@localhost")
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	receive := func(remote, body, target string, created time.Time) {
		t.Helper()
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: remote, OriginalText: body, CreatedAt: created, Historical: true, Correction: &domain.MessageCorrection{Scope: "xmpp:sender:alice@localhost", Author: "alice@localhost", ID: remote, TargetID: target}}); err != nil {
			t.Fatal(err)
		}
	}
	receive("history-original", "Exact **original** 😀", "", at)
	messages, err := e.Messages(t.Context(), c.ID, "", 1)
	if err != nil || len(messages) != 1 {
		t.Fatal(messages, err)
	}
	canonical := string(messages[0].ID)
	receive("history-edit-one", "First correction", "history-original", at.Add(30*time.Second))
	receive("history-edit-two", "Second correction, earlier clock", "history-edit-one", at.Add(10*time.Second))
	client, _ := engineapi.NewClientWithResponses(server.URL)
	one := 1
	_, before, _ := store.Snapshot(t.Context())
	first, err := client.GetMessageEditHistoryWithResponse(t.Context(), canonical, &engineapi.GetMessageEditHistoryParams{Limit: &one})
	if err != nil || first.JSON200 == nil || len(first.JSON200.Items) != 1 || first.JSON200.NextCursor == nil {
		t.Fatal(first, err)
	}
	page := first.JSON200
	if page.MessageId != canonical || page.RequestedMessageId != canonical || page.AccountId != string(c.AccountID) || page.ConversationId != string(c.ID) || len(page.Snapshot) != 64 {
		t.Fatal("missing history ownership", page)
	}
	if first.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private edit history became cacheable")
	}
	all := append([]engineapi.MessageEditHistoryItem{}, page.Items...)
	cursor := page.NextCursor
	for cursor != nil {
		response, err := client.GetMessageEditHistoryWithResponse(t.Context(), canonical, &engineapi.GetMessageEditHistoryParams{Limit: &one, Cursor: cursor})
		if err != nil || response.JSON200 == nil {
			t.Fatal(response, err)
		}
		next := response.JSON200
		if next.Snapshot != page.Snapshot || next.CurrentRevisionId != page.CurrentRevisionId {
			t.Fatal("mixed snapshots")
		}
		all = append(all, next.Items...)
		cursor = next.NextCursor
		if len(all) > 4 {
			t.Fatal("history cursor loop")
		}
	}
	if len(all) != 3 {
		t.Fatal("transport or duplicate revisions leaked", len(all))
	}
	current, original := 0, 0
	for _, item := range all {
		if item.IsCurrent {
			current++
			if item.RevisionId != page.CurrentRevisionId || item.OriginalText != "Second correction, earlier clock" {
				t.Fatal("date order overrode content ancestry")
			}
		}
		if item.IsOriginal {
			original++
			if item.OriginalText != "Exact **original** 😀" || len(item.ParentRevisionIds) != 0 {
				t.Fatal("original source lost")
			}
		}
	}
	if current != 1 || original != 1 || all[0].IsCurrent {
		t.Fatal("history inferred current/original from position")
	}
	_, after, _ := store.Snapshot(t.Context())
	if before != after || protocol.count != 0 {
		t.Fatal("history read mutated state or dispatched")
	}
	invalid := "bad-cursor"
	bad, err := client.GetMessageEditHistoryWithResponse(t.Context(), canonical, &engineapi.GetMessageEditHistoryParams{Cursor: &invalid})
	if err != nil || bad.StatusCode() != 400 {
		t.Fatal("malformed cursor", bad, err)
	}
	receive("history-separate", "Separate retained source", "", at.Add(2*time.Minute))
	other, err := e.Messages(t.Context(), c.ID, "", 1)
	if err != nil || len(other) == 0 {
		t.Fatal(other, err)
	}
	foreign, err := client.GetMessageEditHistoryWithResponse(t.Context(), string(other[0].ID), &engineapi.GetMessageEditHistoryParams{Cursor: page.NextCursor})
	if err != nil || foreign.StatusCode() != 400 {
		t.Fatal("foreign history cursor", foreign, err)
	}
	receive("history-edit-three", "Newest corrected text", "history-original", at.Add(time.Minute))
	changed, err := client.GetMessageEditHistoryWithResponse(t.Context(), canonical, &engineapi.GetMessageEditHistoryParams{Cursor: page.NextCursor})
	if err != nil || changed.StatusCode() != 409 {
		t.Fatal("stale history accepted", changed, err)
	}
	fresh, err := client.GetMessageEditHistoryWithResponse(t.Context(), canonical, nil)
	if err != nil || fresh.JSON200 == nil || fresh.JSON200.Snapshot == page.Snapshot {
		t.Fatal("history restart not refreshed", fresh, err)
	}
	missing, err := client.GetMessageEditHistoryWithResponse(t.Context(), string(id()), nil)
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal("unknown source", missing, err)
	}
}
