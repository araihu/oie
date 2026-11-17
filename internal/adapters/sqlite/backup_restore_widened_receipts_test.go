package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRecoversTerminalReceiptsAfterRetentionWidens(t *testing.T) {
	for _, tc := range []struct {
		name            string
		explicitDiscard bool
		relay           bool
		oldSource       bool
		retainedKey     bool
	}{
		{name: "retired terminal send"},
		{name: "legacy terminal archive", oldSource: true},
		{name: "relayed retirement", relay: true},
		{name: "explicit review discard", explicitDiscard: true},
		{name: "existing destination operation key", retainedKey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, sender, chat := outboxFixture(t)
			ctx := t.Context()
			destination := copyRestoreFixture(t, source)
			target := testRestoreEngine(t, destination)
			key := migrationID()
			composition := domain.OutgoingComposition{OriginalText: "the submitted message before the cutoff"}
			accepted, err := sender.SendComposition(ctx, key, chat.ID, composition, nil)
			if err != nil {
				t.Fatal(err)
			}
			pending := accepted.Message
			var local domain.Message
			localComposition := domain.OutgoingComposition{OriginalText: "the different local operation"}
			if tc.retainedKey {
				accepted, err := target.SendComposition(ctx, key, chat.ID, localComposition, nil)
				if err != nil {
					t.Fatal(err)
				}
				local = accepted.Message
			}
			stale, staleDB, staleScratch := backupRestoreFixture(t, source, false)
			now := pending.CreatedAt.Add(48 * time.Hour)
			days := 1
			if _, err := target.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
				t.Fatal(err)
			}
			if tc.explicitDiscard {
				if result, err := destination.mergeBackup(ctx, staleDB, staleScratch, stale, now); err != nil || result.PausedSends != 1 {
					t.Fatalf("restore did not pause the operation for review: %+v, %v", result, err)
				}
				if err := destination.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, false, now); err != nil {
					t.Fatal(err)
				}
			}
			var live, receiver *Store
			if tc.relay {
				live = copyRestoreFixture(t, source)
				receiver = copyRestoreFixture(t, destination)
			}
			sentAt := pending.CreatedAt.Add(time.Minute)
			if _, err := source.TransitionMessage(ctx, pending.ID, "queued", "dispatching", migrationID(), sentAt, sentAt); err != nil {
				t.Fatal(err)
			}
			if _, err := source.TransitionMessage(ctx, pending.ID, "dispatching", "submitted", migrationID(), sentAt, sentAt); err != nil {
				t.Fatal(err)
			}
			if tc.oldSource {
				migrateFixtureToVersion(t, source, 51)
			}
			terminal, terminalDB, terminalScratch := backupRestoreFixture(t, source, false)
			if result, err := destination.mergeBackup(ctx, terminalDB, terminalScratch, terminal, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("terminal history ignored the destination cutoff: %+v, %v", result, err)
			}
			receiptError := application.ErrNotFound
			if tc.retainedKey {
				receiptError = application.ErrIdempotencyConflict
			}
			if _, err := target.SendComposition(ctx, key, chat.ID, composition, nil); !errors.Is(err, receiptError) {
				t.Fatalf("out-of-retention acceptance receipt was shown: %v", err)
			}
			if tc.relay {
				relay, relayDB, relayScratch := backupRestoreFixture(t, destination, false)
				if _, err := live.mergeBackup(ctx, relayDB, relayScratch, relay, now); err != nil {
					t.Fatal(err)
				}
				if due, err := live.PendingMessages(ctx, now); err != nil || len(due) != 0 {
					t.Fatalf("relay left an obsolete local send eligible for dispatch: %+v, %v", due, err)
				}
				destination = receiver
				target = testRestoreEngine(t, destination)
				if _, err := destination.mergeBackup(ctx, relayDB, relayScratch, relay, now); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := destination.mergeBackup(ctx, staleDB, staleScratch, stale, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("stale queued archive revived the retired send: %+v, %v", result, err)
			}
			rule, err := destination.ConversationRetention(ctx, chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			wider := 7
			if _, err := target.SetConversationRetention(ctx, chat.ID, rule.Revision, &wider); err != nil {
				t.Fatal(err)
			}
			if result, err := destination.mergeBackup(ctx, terminalDB, terminalScratch, terminal, now); err != nil || result.Messages != 1 || result.PausedSends != 0 {
				t.Fatalf("widened policy did not restore only terminal history: %+v, %v", result, err)
			}
			if got, err := destination.Message(ctx, pending.ID); err != nil || got.SendState != "submitted" {
				t.Fatalf("submitted history unavailable after widening: %+v, %v", got, err)
			}
			assertReceipt := func() {
				t.Helper()
				got, err := target.SendComposition(ctx, key, chat.ID, composition, nil)
				switch {
				case tc.retainedKey:
					if !errors.Is(err, application.ErrIdempotencyConflict) {
						t.Fatalf("restore took over a destination operation key: %+v, %v", got, err)
					}
					replay, err := target.SendComposition(ctx, key, chat.ID, localComposition, nil)
					if err != nil || !replay.Replayed || replay.Message.ID != local.ID {
						t.Fatalf("the original destination operation no longer replays: %+v, %v", replay, err)
					}
				case tc.explicitDiscard:
					if !errors.Is(err, application.ErrNotFound) {
						t.Fatalf("an explicit discard's receipt was reopened: %+v, %v", got, err)
					}
				default:
					if err != nil || !got.Replayed || got.Message.ID != pending.ID || got.Message.RevisionID != pending.RevisionID {
						t.Fatalf("the recovered terminal receipt did not replay its original acceptance: %+v, %v", got, err)
					}
				}
			}
			assertReceipt()
			if result, err := destination.mergeBackup(ctx, terminalDB, terminalScratch, terminal, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("reapplying terminal history changed the destination: %+v, %v", result, err)
			}
			assertReceipt()
			if paused, err := destination.RecoveredSends(ctx); err != nil || len(paused) != 0 {
				t.Fatalf("widening recreated a review item for terminal history: %+v, %v", paused, err)
			}
		})
	}
}
