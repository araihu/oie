package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
)

func TestBackupRestoreRetiresExactQueuedEditOnArchivedOutcome(t *testing.T) {
	for _, state := range []string{"failed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			ctx := t.Context()
			source, editor, chat := outboxFixture(t)
			editor.SetProtocols(&editProtocol{})
			original := ownSubmittedEdit(t, source, editor, chat, "the locally retained original")
			mismatched := copyRestoreFixture(t, source)
			accepted := queuedEdit(t, editor, original.ID, "the same accepted correction")
			destination := copyRestoreFixture(t, source)
			terminal := copyRestoreFixture(t, source)
			finishEdit(t, terminal, accepted.Command.ID, "failed")
			localTerminal, err := terminal.queries.GetMessageEditCommand(ctx, string(accepted.Command.ID))
			if err != nil {
				t.Fatal(err)
			}
			mismatchEngine := testRestoreEngine(t, mismatched)
			mismatchEngine.SetProtocols(&editProtocol{})
			other, err := mismatchEngine.EditMessage(ctx, accepted.Command.ID, original.ID, accepted.Command.ExpectedRevisionID,
				"the destination's different accepted text")
			if err != nil {
				t.Fatal(err)
			}
			local, err := destination.MessageEdit(ctx, accepted.Command.ID)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := destination.queries.GetOperation(ctx, string(accepted.Command.ID))
			if err != nil {
				t.Fatal(err)
			}
			final := finishEdit(t, source, accepted.Command.ID, state)
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			now := time.Now().UTC()
			assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, now)
			got, err := destination.MessageEdit(ctx, final.ID)
			if err != nil || got.Command.State != state || got.Command.RevisionID != final.RevisionID ||
				got.Plan != local.Plan || got.ContentRevisionID != local.ContentRevisionID ||
				got.Command.OriginalText != local.Command.OriginalText || got.WireBodyHash() != local.WireBodyHash() {
				t.Fatalf("restoring the exact outcome did not preserve the accepted edit: %+v, %v", got, err)
			}
			if keep, err := destination.queries.GetOperation(ctx, string(final.ID)); err != nil || keep != receipt {
				t.Fatalf("restoring the outcome changed its original acceptance receipt: %+v, %v", keep, err)
			}
			engine := testRestoreEngine(t, destination)
			if replayed, err := engine.EditMessage(ctx, final.ID, original.ID, final.ExpectedRevisionID, final.OriginalText); err != nil ||
				!replayed.Replayed || replayed.Command.State != state {
				t.Fatalf("the original key did not replay the restored outcome: %+v, %v", replayed, err)
			}
			if due, err := destination.PendingMessageEdits(ctx, now.Add(time.Hour)); err != nil || len(due) != 0 {
				t.Fatalf("restoring the outcome left the edit executable: %+v, %v", due, err)
			}
			if current, err := destination.Message(ctx, original.ID); err != nil || current.Edited || current.OriginalText != original.OriginalText {
				t.Fatalf("an unmaterialized outcome changed the original body: %+v, %v", current, err)
			}

			assertLocalEditRestoreRepeats(t, terminal, manifest, archive, scratch, now)
			if keep, err := terminal.queries.GetMessageEditCommand(ctx, string(final.ID)); err != nil || keep != localTerminal {
				t.Fatalf("restoring the outcome replaced a local terminal decision: %+v, %v", keep, err)
			}
			assertLocalEditRestoreRepeats(t, mismatched, manifest, archive, scratch, now)
			if due, err := mismatched.PendingMessageEdits(ctx, now.Add(time.Hour)); err != nil ||
				len(due) != 1 || due[0].Command.ID != other.Command.ID || due[0].Command.RevisionID != other.Command.RevisionID ||
				due[0].Command.OriginalText != other.Command.OriginalText {
				t.Fatalf("an unrelated archived outcome changed an executable edit: %+v, %v", due, err)
			}
			if state == "unknown" {
				relayManifest, relayArchive, relayScratch := backupRestoreFixture(t, destination, false)
				fresh := openLegacyDownloadDestination(t)
				assertLocalEditRestoreRepeats(t, fresh, relayManifest, relayArchive, relayScratch, now)
				if _, err := fresh.MessageEdit(ctx, final.ID); !errors.Is(err, application.ErrNotFound) {
					t.Fatalf("a fresh restore imported the private unresolved command: %v", err)
				}
				assertLocalEditKeyOnly(t, ctx, fresh, final.ID)
			}
		})
	}
}

func TestBackupRestoreSettlesUnknownEditWithVerifiedSubmittedHistory(t *testing.T) {
	ctx := t.Context()
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	original := ownSubmittedEdit(t, source, editor, chat, "original before the confirmed edit")
	mismatched := copyRestoreFixture(t, source)
	accepted := queuedEdit(t, editor, original.ID, "the submitted correction")
	destination := copyRestoreFixture(t, source)
	failed := copyRestoreFixture(t, source)
	finishEdit(t, destination, accepted.Command.ID, "unknown")
	finishEdit(t, failed, accepted.Command.ID, "failed")
	mismatchEngine := testRestoreEngine(t, mismatched)
	mismatchEngine.SetProtocols(&editProtocol{})
	different, err := mismatchEngine.EditMessage(ctx, accepted.Command.ID, original.ID, accepted.Command.ExpectedRevisionID,
		"a different accepted correction using the same key")
	if err != nil {
		t.Fatal(err)
	}
	finishEdit(t, mismatched, different.Command.ID, "unknown")
	local, err := destination.MessageEdit(ctx, accepted.Command.ID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := destination.queries.GetOperation(ctx, string(accepted.Command.ID))
	if err != nil {
		t.Fatal(err)
	}
	final := finishEdit(t, source, accepted.Command.ID, "submitted")
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	now := time.Now().UTC()
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, now)
	got, err := destination.MessageEdit(ctx, final.ID)
	if err != nil || got.Command.State != "submitted" || got.Command.RevisionID != final.RevisionID ||
		got.Command.OriginalText != local.Command.OriginalText || got.Plan != local.Plan ||
		got.ContentRevisionID != local.ContentRevisionID || got.WireBodyHash() != local.WireBodyHash() {
		t.Fatalf("verified submission did not settle the exact local command: %+v, %v", got, err)
	}
	if after, err := destination.queries.GetOperation(ctx, string(final.ID)); err != nil || after != receipt {
		t.Fatalf("verified submission changed the original acceptance: %+v, %v", after, err)
	}
	engine := testRestoreEngine(t, destination)
	engine.SetProtocols(&editProtocol{})
	if replay, err := engine.EditMessage(ctx, final.ID, original.ID, final.ExpectedRevisionID, final.OriginalText); err != nil ||
		!replay.Replayed || replay.Command.State != "submitted" {
		t.Fatalf("the original key did not replay the verified submission: %+v, %v", replay, err)
	}
	if target, err := engine.MessageEditTarget(ctx, original.ID); err != nil || !target.Eligible || target.OriginalText != final.OriginalText {
		t.Fatalf("the verified correction left further editing unavailable: %+v, %v", target, err)
	}
	for _, store := range []*Store{failed, mismatched} {
		before, err := store.queries.GetMessageEditCommand(ctx, string(accepted.Command.ID))
		if err != nil {
			t.Fatal(err)
		}
		assertLocalEditRestoreRepeats(t, store, manifest, archive, scratch, now)
		if after, err := store.queries.GetMessageEditCommand(ctx, string(accepted.Command.ID)); err != nil || after != before {
			t.Fatalf("restore changed a failed or mismatched local command: %+v, %v", after, err)
		}
	}
}
