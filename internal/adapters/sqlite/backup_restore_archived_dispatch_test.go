package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreAuthenticatesIndependentlyArchivedPlainSchedule(t *testing.T) {
	const body = "scheduled text"
	for _, tc := range []struct {
		name, echoBody, wantState string
		reply                     *domain.ReplyReference
	}{
		{name: "exact wire body", echoBody: body, wantState: "submitted"},
		{name: "earlier composition", echoBody: "different text", wantState: "unknown"},
		{name: "different reply metadata", echoBody: body, wantState: "unknown", reply: &domain.ReplyReference{
			Target: domain.MessageReference{Scope: "sender:bob@localhost", ID: "earlier-message", Author: "bob@localhost"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			destination, witness := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
			ctx := t.Context()
			due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
			pending := queueBackupFixture(t, engine, chat.ID, body, &due)
			sentAt, observedAt := due.Add(time.Minute), due.Add(48*time.Hour)
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, observedAt); err != nil {
				t.Fatal(err)
			}
			if err := destination.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, observedAt); err != nil {
				t.Fatal(err)
			}
			receiver := testRestoreEngine(t, witness)
			receiver.SetProtocols(&outboundProtocol{})
			if err := receiver.Receive(ctx, application.Incoming{
				AccountID: chat.AccountID, Kind: "direct", Address: chat.Address, IsOwn: true,
				ProviderID: string(pending.ID), ArchiveID: "scheduled-own-echo", ArchiveScope: "alice@localhost",
				OriginalText: tc.echoBody, Reply: tc.reply, CreatedAt: sentAt, Historical: true,
			}); err != nil {
				t.Fatal(err)
			}
			echoes, err := witness.Messages(ctx, chat.ID, "", 10)
			if err != nil || len(echoes) != 1 || echoes[0].ID == pending.ID {
				t.Fatalf("the witness did not retain its independent archive identity: %+v, %v", echoes, err)
			}
			observed, observedArchive, observedScratch := backupRestoreFixture(t, witness, false)
			for range 2 {
				if _, err := destination.mergeBackup(ctx, observedArchive, observedScratch, observed, observedAt); err != nil {
					t.Fatal(err)
				}
				current, err := destination.Message(ctx, pending.ID)
				if err != nil || current.SendState != tc.wantState || current.OriginalText != body || !current.TimelineTime().Equal(sentAt) {
					t.Fatalf("reconciled send: state=%s body=%q timeline=%s, %v", current.SendState, current.OriginalText, current.TimelineTime(), err)
				}
				queue, err := destination.PendingMessages(ctx, observedAt)
				if err != nil || len(queue) != 0 {
					t.Fatalf("the archived proof left a dispatchable send: %+v, %v", queue, err)
				}
			}
		})
	}
}

func TestBackupRestoreReconcilesUncertainNativeSubmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcome  string
		mismatch bool
	}{
		{name: "same native composition", outcome: "submitted"},
		{name: "different submitted composition", outcome: "submitted", mismatch: true},
		{name: "different failed composition", outcome: "failed", mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			ctx := t.Context()
			due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
			sentAt, uncertainAt := due.Add(2*time.Minute), due.Add(48*time.Hour)
			const text = "the scheduled original"
			pending := queueBackupFixture(t, engine, chat.ID, text, &due)
			var destination *Store
			if tc.mismatch {
				destination = copyRestoreFixture(t, source)
			}
			if _, err := source.TransitionMessage(ctx, pending.ID, "queued", "dispatching", migrationID(), sentAt, sentAt); err != nil {
				t.Fatal(err)
			}
			if tc.mismatch {
				uncertainAt = sentAt.Add(-time.Minute)
				local := domain.OutgoingComposition{OriginalText: "the destination's different draft"}
				if _, err := testRestoreEngine(t, destination).UpdatePendingMessage(ctx, migrationID(), pending.ID, pending.RevisionID, &local, nil, false); err != nil {
					t.Fatal(err)
				}
				if _, err := destination.TransitionMessage(ctx, pending.ID, "queued", "dispatching", migrationID(), uncertainAt, uncertainAt); err != nil {
					t.Fatal(err)
				}
			} else {
				destination = copyRestoreFixture(t, source)
			}
			uncertain, err := destination.TransitionMessage(ctx, pending.ID, "dispatching", "unknown", migrationID(), uncertainAt, uncertainAt)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.TransitionMessage(ctx, pending.ID, "dispatching", tc.outcome, migrationID(), sentAt, sentAt); err != nil {
				t.Fatal(err)
			}
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			restoredAt := sentAt.Add(72 * time.Hour)
			if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, restoredAt); err != nil {
				t.Fatal(err)
			}
			got, err := destination.Message(ctx, pending.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.mismatch {
				if got.SendState != "submitted" || got.OriginalText != text || !got.TimelineTime().Equal(sentAt) {
					t.Fatalf("native submission was not reconciled at the original send time: %+v", got)
				}
				row, err := destination.read.GetMessage(ctx, string(pending.ID))
				if err != nil || row.SubmittedAtMs == nil || *row.SubmittedAtMs != sentAt.UnixMilli() {
					t.Fatalf("authoritative submission timestamp was lost: %+v, %v", row.SubmittedAtMs, err)
				}
			} else if got.RevisionID != uncertain.RevisionID || got.SendState != "unknown" || got.OriginalText != uncertain.OriginalText ||
				!got.TimelineTime().Equal(uncertain.TimelineTime()) {
				t.Fatalf("the competing native body changed the uncertain send: %+v", got)
			}
			_, before, err := destination.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, restoredAt); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("reapplying native proof changed restored content: %+v, %v", result, err)
			}
			if again, err := destination.Message(ctx, pending.ID); err != nil || again.RevisionID != got.RevisionID || again.SendState != got.SendState {
				t.Fatalf("reapplying native proof changed the send: %+v, %v", again, err)
			}
			if _, after, err := destination.Snapshot(ctx); err != nil || before != after {
				t.Fatalf("reapplying native proof emitted events from %d to %d: %v", before, after, err)
			}
		})
	}
}
