package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsOwnSubmittedEditsAndReceipts(t *testing.T) {
	t.Parallel()
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	ctx := t.Context()
	original := ownSubmittedEdit(t, source, editor, chat, "the actual original body")
	base, err := source.MessageSource(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameNative := copyRestoreFixture(t, source)
	withOccupiedKey := copyRestoreFixture(t, source)
	first := queuedEdit(t, editor, original.ID, "the first accepted edit")
	finishEdit(t, source, first.Command.ID, "submitted")
	withFirstEdit := copyRestoreFixture(t, source)
	second := queuedEdit(t, editor, original.ID, "the second accepted edit")
	finishEdit(t, source, second.Command.ID, "submitted")
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	for _, tc := range []struct {
		name  string
		store *Store
	}{
		{"fresh profile", openLegacyDownloadDestination(t)},
		{"native original already present", sameNative},
		{"local acceptance already present", withFirstEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			restoredEditor := testRestoreEngine(t, tc.store)
			if _, err := tc.store.mergeBackup(ctx, archive, scratch, manifest, time.Now().UTC()); err != nil {
				t.Fatalf("restore could not merge actual submitted edits: %v", err)
			}
			for _, command := range []domain.MessageEditCommand{first.Command, second.Command} {
				want, err := source.queries.GetOperation(ctx, string(command.ID))
				if err != nil {
					t.Fatal(err)
				}
				if got, err := tc.store.queries.GetOperation(ctx, string(command.ID)); err != nil || got != want || got.Result == "null" {
					t.Fatalf("restore lost or changed the immutable edit receipt: %+v, %v", got, err)
				}
				if got, err := restoredEditor.EditMessage(ctx, command.ID, original.ID, command.ExpectedRevisionID, command.OriginalText); err != nil || !got.Replayed || got.Command.State != "submitted" {
					t.Fatalf("retry did not find the already submitted command: %+v, %v", got, err)
				}
			}
			if got, err := tc.store.MessageSource(ctx, original.ID); err != nil || got.RevisionID != base.RevisionID || got.OriginalText != base.OriginalText {
				t.Fatalf("restore changed the native original: %+v, %v", got, err)
			}
			history := editHistory(t, restoredEditor, original.ID, "", 20)
			if len(history.Items) != 3 || !history.Items[0].IsCurrent ||
				history.Items[0].OriginalText != second.Command.OriginalText || len(history.Items[0].ParentRevisionIDs) != 1 ||
				history.Items[0].ParentRevisionIDs[0] != history.Items[1].RevisionID || history.Items[1].OriginalText != first.Command.OriginalText {
				t.Fatalf("restore lost the actual accepted parent chain: %+v", history)
			}
			assertLocalEditRestoreRepeats(t, tc.store, manifest, archive, scratch, time.Now().UTC())
		})
	}
	t.Run("unrelated local operation key", func(t *testing.T) {
		ctx := t.Context()
		local := testRestoreEngine(t, withOccupiedKey)
		local.SetProtocols(&outboundProtocol{})
		if _, err := local.SendMessage(ctx, first.Command.ID, chat.ID, "the independent local operation"); err != nil {
			t.Fatal(err)
		}
		want, err := withOccupiedKey.queries.GetOperation(ctx, string(first.Command.ID))
		if err != nil {
			t.Fatal(err)
		}
		assertLocalEditRestoreRepeats(t, withOccupiedKey, manifest, archive, scratch, time.Now().UTC())
		if got, err := withOccupiedKey.queries.GetOperation(ctx, string(first.Command.ID)); err != nil || got != want {
			t.Fatalf("restore overwrote an existing operation key: %+v, %v", got, err)
		}
		assertLocalEditKeyOnly(t, ctx, withOccupiedKey, second.Command.ID)
		if m, err := withOccupiedKey.Message(ctx, original.ID); err != nil || m.Edited || m.OriginalText != base.OriginalText {
			t.Fatalf("the conflicting idempotency key admitted an edit or its dependent source: %+v, %v", m, err)
		}
	})
}

func TestBackupRestoreKeepsExpiredOwnEditCommandAndDescendantBodyOut(t *testing.T) {
	t.Parallel()
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	ctx := t.Context()
	original := ownSubmittedEdit(t, source, editor, chat, "the actual original body")
	destination := copyRestoreFixture(t, source)
	receiver := testRestoreEngine(t, destination)
	receiver.SetProtocols(&outboundProtocol{})
	first := queuedEdit(t, editor, original.ID, "the previously expired edit body")
	finishEdit(t, source, first.Command.ID, "submitted")
	second := queuedEdit(t, editor, original.ID, "later own edit based on the expired revision")
	finishEdit(t, source, second.Command.ID, "submitted")
	now := time.Now().UTC()
	// The destination saw the first edit independently in MAM. It uses the
	// same wire ID with a different local source row and no acceptance receipt.
	if err := receiver.Receive(ctx, application.Incoming{
		AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", Historical: true, IsOwn: true, SenderAddress: "alice@localhost",
		ProviderID: string(first.Command.ID), OriginalText: first.Command.OriginalText, CreatedAt: now.Add(-48 * time.Hour),
		Correction: &domain.MessageCorrection{
			Scope: "xmpp:correct:sender:alice@localhost", Author: "alice@localhost", ID: string(first.Command.ID), TargetID: string(original.ID),
		},
	}); err != nil {
		t.Fatal(err)
	}
	days := 1
	if _, err := receiver.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if count, err := destination.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || count != 1 {
		t.Fatalf("did not expire the independently witnessed edit: %d, %v", count, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	assertExcluded := func(store *Store) {
		t.Helper()
		if m, err := store.Message(ctx, original.ID); err != nil || m.Edited || m.OriginalText != original.OriginalText {
			t.Fatalf("an excluded edit changed the retained original: %+v, %v", m, err)
		}
		for _, command := range []domain.MessageEditCommand{first.Command, second.Command} {
			assertLocalEditKeyOnly(t, ctx, store, command.ID)
			if _, err := testRestoreEngine(t, store).EditMessage(ctx, command.ID, original.ID, command.ExpectedRevisionID, command.OriginalText); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("retry recreated the expired edit: %v", err)
			}
		}
	}
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, now)
	assertExcluded(destination)

	relayed, relayedDB, relayedScratch := backupRestoreFixture(t, destination, false)
	relay := openLegacyDownloadDestination(t)
	if _, err := relay.mergeBackup(ctx, relayedDB, relayedScratch, relayed, now); err != nil {
		t.Fatal(err)
	}
	assertExcluded(relay)
	assertLocalEditRestoreRepeats(t, relay, manifest, archive, scratch, now)
	assertExcluded(relay)
}

func TestBackupRestoreRehydratesOwnEditReceiptAfterWideningRetention(t *testing.T) {
	t.Parallel()
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	ctx := t.Context()
	destination := copyRestoreFixture(t, source)
	settings := testRestoreEngine(t, destination)
	original := ownSubmittedEdit(t, source, editor, chat, "an older original")
	edit := queuedEdit(t, editor, original.ID, "an eligible edit after widening")
	finishEdit(t, source, edit.Command.ID, "submitted")
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	mergeAt := original.CreatedAt.Add(48 * time.Hour)
	days := 1
	policy, err := settings.SetConversationRetention(ctx, chat.ID, "0", &days)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, mergeAt)
	assertLocalEditKeyOnly(t, ctx, destination, edit.Command.ID)
	days = 7
	if _, err := settings.SetConversationRetention(ctx, chat.ID, policy.Revision, &days); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, mergeAt); err != nil {
		t.Fatal(err)
	}
	want, err := source.queries.GetOperation(ctx, string(edit.Command.ID))
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := destination.queries.GetOperation(ctx, string(edit.Command.ID)); err != nil || receipt != want || receipt.Result == "null" {
		t.Fatalf("widening could not rehydrate the matching edit receipt: %+v, %v", receipt, err)
	}
	if m, err := destination.Message(ctx, original.ID); err != nil || !m.Edited || m.OriginalText != edit.Command.OriginalText {
		t.Fatalf("widening did not restore the legitimate edit: %+v, %v", m, err)
	}
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, mergeAt)
}

func assertLocalEditKeyOnly(t *testing.T, ctx context.Context, store *Store, id domain.ID) {
	t.Helper()
	if receipt, err := store.queries.GetOperation(ctx, string(id)); err != nil || receipt.Kind != "edit_message" || receipt.Result != "null" {
		t.Fatalf("excluded edit lost its reserved key or retained its body: %+v, %v", receipt, err)
	}
	if _, err := testRestoreEngine(t, store).MessageEdit(ctx, id); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("excluded edit command is still available: %v", err)
	}
	if _, err := store.MessageSource(ctx, id); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("excluded edit source is still available: %v", err)
	}
}

func assertLocalEditRestoreRepeats(t *testing.T, store *Store, manifest backupManifest, archive, scratch string, now time.Time) {
	t.Helper()
	ctx := t.Context()
	if _, err := store.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	_, before, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("repeated restore changed the edit history: %+v, %v", result, err)
	}
	if _, after, err := store.Snapshot(ctx); err != nil || after != before {
		t.Fatalf("repeated restore emitted events from %d to %d: %v", before, after, err)
	}
}
