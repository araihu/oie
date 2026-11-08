package httpapi_test

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestReceivedCorrectionContractRetainsLogicalMessage(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	const scope, author = "xmpp:sender:bob@localhost", "bob@localhost"
	receive := func(provider, body, target string, created time.Time) {
		t.Helper()
		err := e.Receive(t.Context(), application.Incoming{
			AccountID: c.AccountID, Address: c.Address, ProviderID: provider,
			OriginalText: body, CreatedAt: created, Historical: true,
			Correction: &domain.MessageCorrection{Scope: scope, Author: author, ID: provider, TargetID: target},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	receive("original", "The first retained text", "", at)
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || first.JSON200 == nil || len(first.JSON200.Items) != 1 {
		t.Fatal("original message unavailable", err)
	}
	original := first.JSON200.Items[0]
	if original.Edited != nil && *original.Edited {
		t.Fatal("ordinary source marked edited")
	}
	corrected := "Exact **corrected** text 😀\nKeep this newline."
	receive("correction", corrected, "original", at.Add(22*time.Second))
	page, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 {
		t.Fatal("correction created a second timeline message", err)
	}
	current := page.JSON200.Items[0]
	if current.Id != original.Id || current.RevisionId == original.RevisionId || current.OriginalText != corrected || !current.CreatedAt.Equal(at) || current.Edited == nil || !*current.Edited || current.EditedAt == nil || !current.EditedAt.Equal(at.Add(22*time.Second)) {
		t.Fatal("correction contract lost source identity or edit evidence", current)
	}
	single, err := client.GetMessageWithResponse(t.Context(), original.Id)
	if err != nil || single.JSON200 == nil || single.JSON200.OriginalText != corrected || single.JSON200.RevisionId != current.RevisionId {
		t.Fatal("single-message read disagrees with timeline", err)
	}
	window, err := client.GetMessageContextWithResponse(t.Context(), string(c.ID), original.Id, nil)
	if err != nil || window.JSON200 == nil || len(window.JSON200.Items) != 1 || window.JSON200.Items[0].Id != original.Id || window.JSON200.Items[0].OriginalText != corrected {
		t.Fatal("context resurrected superseded content", err)
	}
	receive("correction", corrected, "original", at.Add(22*time.Second))
	replay, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || replay.JSON200 == nil || len(replay.JSON200.Items) != 1 || replay.JSON200.Items[0].RevisionId != current.RevisionId {
		t.Fatal("replay changed canonical revision", err)
	}
	pending, err := store.PendingMessages(t.Context(), at.AddDate(1, 0, 0))
	if err != nil || len(pending) != 0 {
		t.Fatal("received correction created outbox work", err)
	}
}

func TestArchiveEnrichmentResolvesExistingCorrectionURLs(t *testing.T) {
	e, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	original := application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "legacy-original", OriginalText: "Retained original", CreatedAt: at, Historical: true, ArchiveScope: c.Address, ArchiveID: "archive-original"}
	correction := original
	correction.ProviderID, correction.OriginalText = "legacy-correction", "Retained correction"
	correction.CreatedAt, correction.ArchiveID = at.Add(time.Second), "archive-correction"
	for _, source := range []application.Incoming{original, correction} {
		if err := e.Receive(t.Context(), source); err != nil {
			t.Fatal(err)
		}
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	before, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || before.JSON200 == nil || len(before.JSON200.Items) != 2 {
		t.Fatal("legacy source fixture unavailable", err)
	}
	alias, canonical := before.JSON200.Items[0].Id, before.JSON200.Items[1].Id
	original.Correction = &domain.MessageCorrection{Scope: "xmpp:sender:bob@localhost", Author: "bob@localhost", ID: "client-original"}
	correction.Correction = &domain.MessageCorrection{Scope: original.Correction.Scope, Author: original.Correction.Author, ID: "client-correction", TargetID: "client-original"}
	// Replaying the existing archive aliases enriches retained sources, without
	// allocating replacement logical messages or requiring an empty database.
	for _, source := range []application.Incoming{correction, original} {
		if err := e.Receive(t.Context(), source); err != nil {
			t.Fatal(err)
		}
	}
	page, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Id != canonical || page.JSON200.Items[0].OriginalText != correction.OriginalText {
		t.Fatal("legacy archive enrichment did not repair logical timeline", err)
	}
	single, err := client.GetMessageWithResponse(t.Context(), alias)
	if err != nil || single.JSON200 == nil || single.JSON200.Id != canonical || single.JSON200.OriginalText != correction.OriginalText {
		t.Fatal("old correction URL lost canonical resolution", err)
	}
	window, err := client.GetMessageContextWithResponse(t.Context(), string(c.ID), alias, nil)
	if err != nil || window.JSON200 == nil || window.JSON200.TargetId != canonical || window.JSON200.RequestedTargetId == nil || *window.JSON200.RequestedTargetId != alias || len(window.JSON200.Items) != 1 || window.JSON200.Items[0].Id != canonical {
		t.Fatal("context omitted scoped alias proof", err)
	}
}
