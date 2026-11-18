package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreReservesEditsWithoutMaterializedSource(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"queued", "dispatching", "unknown", "failed"} {
		t.Run(state, func(t *testing.T) {
			ctx := t.Context()
			source, editor, chat := outboxFixture(t)
			editor.SetProtocols(&editProtocol{})
			original := ownSubmittedEdit(t, source, editor, chat, "retained native original")
			destination := copyRestoreFixture(t, source)
			accepted := queuedEdit(t, editor, original.ID, "accepted but not materialized")
			final := accepted.Command
			if state != "queued" {
				claim, err := source.ClaimMessageEdit(ctx, final.ID, migrationID(), time.Now())
				if err != nil {
					t.Fatal(err)
				}
				final = claim.Command
			}
			if state == "unknown" || state == "failed" {
				var err error
				final, err = source.FinishMessageEdit(ctx, final.ID, state, migrationID(), time.Now(), time.Now(), "fixture_transport_result")
				if err != nil {
					t.Fatal(err)
				}
			}
			wantReceipt, err := source.queries.GetOperation(ctx, string(final.ID))
			if err != nil {
				t.Fatal(err)
			}
			if state != "failed" {
				wantReceipt.Result = "null"
			}
			check := func(store *Store) {
				t.Helper()
				if receipt, err := store.queries.GetOperation(ctx, string(final.ID)); err != nil || receipt != wantReceipt {
					t.Fatalf("restore lost the exact reserved or terminal receipt: %+v, %v", receipt, err)
				}
				engine := testRestoreEngine(t, store)
				engine.SetProtocols(&editProtocol{})
				retry, err := engine.EditMessage(ctx, final.ID, original.ID, final.ExpectedRevisionID, final.OriginalText)
				if state == "failed" {
					if err != nil || !retry.Replayed || retry.Command.State != state || retry.Command.RevisionID != final.RevisionID ||
						retry.Command.OriginalText != final.OriginalText || retry.Command.LastErrorCode != final.LastErrorCode {
						t.Fatalf("retry lost the terminal failure: %+v, %v", retry, err)
					}
				} else if !errors.Is(err, application.ErrNotFound) {
					t.Fatalf("unresolved edit became executable on retry: %+v, %v", retry, err)
				}
				if due, err := store.PendingMessageEdits(ctx, time.Now().Add(time.Hour)); err != nil || len(due) != 0 {
					t.Fatalf("restore left an executable edit: %+v, %v", due, err)
				}
				if current, err := store.Message(ctx, original.ID); err != nil || current.Edited || current.OriginalText != original.OriginalText {
					t.Fatalf("unmaterialized edit changed the original: %+v, %v", current, err)
				}
			}

			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			now := time.Now().UTC()
			assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, now)
			check(destination)

			relayedManifest, relayedDB, relayedScratch := backupRestoreFixture(t, destination, false)
			relay := openLegacyDownloadDestination(t)
			assertLocalEditRestoreRepeats(t, relay, relayedManifest, relayedDB, relayedScratch, now)
			check(relay)
		})
	}
}

func TestBackupRestoreDoesNotReviveFailedEditOfExpiredOriginal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	destination := copyRestoreFixture(t, source)
	original := ownSubmittedEdit(t, source, editor, chat, "original subject to retention")
	accepted := queuedEdit(t, editor, original.ID, "failed edit subject to retention")
	final := finishEdit(t, source, accepted.Command.ID, "failed")
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	engine := testRestoreEngine(t, destination)
	when := original.CreatedAt.Add(48 * time.Hour)
	days := 1
	policy, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, when)
	assertLocalEditKeyOnly(t, ctx, destination, final.ID)

	// Age-only exclusion can be widened. An explicit purge of the same
	// original after that permanently excludes the command and its text.
	days = 7
	policy, err = engine.SetConversationRetention(ctx, chat.ID, policy.Revision, &days)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, when)
	if got, err := engine.MessageEdit(ctx, final.ID); err != nil || got.State != "failed" || got.RevisionID != final.RevisionID {
		t.Fatalf("widening failed to rehydrate the exact terminal edit: %+v, %v", got, err)
	}
	days = 1
	policy, err = engine.SetConversationRetention(ctx, chat.ID, policy.Revision, &days)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := destination.PurgeExpiredHistory(ctx, when, func() (domain.ID, error) { return migrationID(), nil }); err != nil || count != 1 {
		t.Fatalf("expected the original to expire, got %d, %v", count, err)
	}
	days = 7
	if _, err := engine.SetConversationRetention(ctx, chat.ID, policy.Revision, &days); err != nil {
		t.Fatal(err)
	}
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, when)
	assertLocalEditKeyOnly(t, ctx, destination, final.ID)

	relayedManifest, relayedDB, relayedScratch := backupRestoreFixture(t, destination, false)
	relay := openLegacyDownloadDestination(t)
	assertLocalEditRestoreRepeats(t, relay, relayedManifest, relayedDB, relayedScratch, when)
	assertLocalEditRestoreRepeats(t, relay, manifest, archive, scratch, when)
	assertLocalEditKeyOnly(t, ctx, relay, final.ID)
}

func TestBackupRestoreReconcilesSubmittedEditWithExactQueuedCommand(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	original := ownSubmittedEdit(t, source, editor, chat, "original before submitted correction")
	accepted := queuedEdit(t, editor, original.ID, "same accepted correction on both profiles")
	destination := copyRestoreFixture(t, source)
	before, err := destination.MessageEdit(ctx, accepted.Command.ID)
	if err != nil {
		t.Fatal(err)
	}
	final := finishEdit(t, source, accepted.Command.ID, "submitted")
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, time.Now().UTC())

	after, err := destination.MessageEdit(ctx, final.ID)
	if err != nil || after.Command.State != final.State || after.Command.RevisionID != final.RevisionID ||
		after.Plan != before.Plan || after.Command.ExpectedRevisionID != before.Command.ExpectedRevisionID ||
		after.Command.OriginalText != before.Command.OriginalText {
		t.Fatalf("restore did not reconcile the same accepted command: %+v, %v", after, err)
	}
	engine := testRestoreEngine(t, destination)
	if replay, err := engine.EditMessage(ctx, final.ID, original.ID, final.ExpectedRevisionID, final.OriginalText); err != nil || !replay.Replayed || replay.Command.State != "submitted" {
		t.Fatalf("exact retry did not find the submitted correction: %+v, %v", replay, err)
	}
	if due, err := destination.PendingMessageEdits(ctx, time.Now().Add(time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("terminal proof left an executable edit: %+v, %v", due, err)
	}
}
