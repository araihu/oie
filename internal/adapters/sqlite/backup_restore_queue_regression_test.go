package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestRecoveredSendUsesLatestCompositionAcrossRestoreOrder(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	oldFirst, newFirst := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	due := now.Add(time.Hour)
	original := queueBackupFixture(t, engine, chat.ID, "original body", &due)
	oldManifest, oldDB, oldScratch := backupRestoreFixture(t, source, false)
	nextDue := due.Add(time.Hour)
	edited, err := engine.UpdatePendingMessage(ctx, migrationID(), original.ID, original.RevisionID,
		&domain.OutgoingComposition{OriginalText: "edited body"}, &nextDue, false)
	if err != nil {
		t.Fatal(err)
	}
	newManifest, newDB, newScratch := backupRestoreFixture(t, source, false)
	if _, err := oldFirst.mergeBackup(ctx, oldDB, oldScratch, oldManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := oldFirst.mergeBackup(ctx, newDB, newScratch, newManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := newFirst.mergeBackup(ctx, newDB, newScratch, newManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := newFirst.mergeBackup(ctx, oldDB, oldScratch, oldManifest, now); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []*Store{oldFirst, newFirst} {
		items, err := dest.RecoveredSends(ctx)
		if err != nil || len(items) != 1 {
			t.Fatalf("expected one recovered send: %+v %v", items, err)
		}
		current := items[0]
		if current.Text != "edited body" || current.RevisionID != edited.Message.RevisionID ||
			current.ScheduledAt == nil || !current.ScheduledAt.Equal(nextDue) {
			t.Fatalf("recovered composition did not retain the latest revision: %+v", current)
		}
		if err := dest.ReviewRecoveredSend(ctx, original.ID, original.RevisionID, true, now); err == nil {
			t.Fatal("stale review resumed the send")
		}
	}
}

func TestRecoveredSendRetainsUncertainAttemptAcrossRestoreOrder(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	oldFirst, newFirst := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	message := queueBackupFixture(t, engine, chat.ID, "possibly transmitted", nil)
	oldManifest, oldDB, oldScratch := backupRestoreFixture(t, source, false)
	if _, err := source.TransitionMessage(ctx, message.ID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, message.ID, "dispatching", "unknown", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	newManifest, newDB, newScratch := backupRestoreFixture(t, source, false)
	if _, err := oldFirst.mergeBackup(ctx, oldDB, oldScratch, oldManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := oldFirst.mergeBackup(ctx, newDB, newScratch, newManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := newFirst.mergeBackup(ctx, newDB, newScratch, newManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := newFirst.mergeBackup(ctx, oldDB, oldScratch, oldManifest, now); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []*Store{oldFirst, newFirst} {
		items, err := dest.RecoveredSends(ctx)
		if err != nil || len(items) != 1 {
			t.Fatalf("expected one recovered send: %+v %v", items, err)
		}
		if items[0].OriginalState == "queued" && items[0].AttemptCount == 0 {
			t.Fatalf("recovered send lost transmission evidence: %+v", items[0])
		}
		if err := dest.ReviewRecoveredSend(ctx, message.ID, items[0].RevisionID, true, now); err == nil {
			t.Fatal("possibly transmitted send was resumed")
		}
	}
}

func TestBackupRestoreUsesTerminalSendEvidenceOutsideRetention(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	dest := copyRestoreFixture(t, source)
	days := 1
	if _, err := testRestoreEngine(t, dest).SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	message := queueBackupFixture(t, engine, chat.ID, "sent before cutoff", nil)
	oldManifest, oldDB, oldScratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, oldDB, oldScratch, oldManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, message.ID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, message.ID, "dispatching", "submitted", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	newManifest, newDB, newScratch := backupRestoreFixture(t, source, false)
	later := now.AddDate(0, 0, 2)
	if _, err := dest.mergeBackup(ctx, newDB, newScratch, newManifest, later); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(ctx, oldDB, oldScratch, oldManifest, later); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.Message(ctx, message.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("out-of-retention message content was restored: %v", err)
	}
	if err := dest.ReviewRecoveredSend(ctx, message.ID, message.RevisionID, true, later); err == nil {
		t.Fatal("send was resumed despite confirmed transmission in the newer archive")
	}
}

func TestBackupRestoreStopsOldDestinationQueueOnTerminalEvidence(t *testing.T) {
	for _, terminal := range []string{"cancelled", "submitted"} {
		t.Run(terminal, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			ctx := t.Context()
			now := time.Now().UTC()
			message := queueBackupFixture(t, engine, chat.ID, "original submitted body", nil)
			dest := copyRestoreFixture(t, source)
			destEngine := testRestoreEngine(t, dest)
			unrelated := queueBackupFixture(t, destEngine, chat.ID, "unrelated live send", nil)
			alternate, err := destEngine.UpdatePendingMessage(ctx, migrationID(), message.ID, message.RevisionID,
				&domain.OutgoingComposition{OriginalText: "edited only on destination"}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if terminal == "cancelled" {
				if _, err := engine.CancelMessage(ctx, migrationID(), message.ID, message.RevisionID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := source.TransitionMessage(ctx, message.ID, "queued", "dispatching", migrationID(), now, now); err != nil {
					t.Fatal(err)
				}
				if _, err := source.TransitionMessage(ctx, message.ID, "dispatching", terminal, migrationID(), now, now); err != nil {
					t.Fatal(err)
				}
			}
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
				t.Fatal(err)
			}
			pending, err := dest.PendingMessages(ctx, now.Add(time.Hour))
			if err != nil || len(pending) != 1 || pending[0].ID != unrelated.ID {
				t.Fatalf("terminal evidence did not stop only the corresponding send: %+v %v", pending, err)
			}
			if terminal == "submitted" {
				current, err := dest.Message(ctx, message.ID)
				if err != nil || (current.SendState != "unknown" &&
					(current.SendState != "submitted" || current.OriginalText != message.OriginalText)) {
					t.Fatalf("restore presented an unsent edit as the submitted body: %+v %v", current, err)
				}
			}
			var revisions int
			if err := dest.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM message_revisions WHERE entity_id=? AND id=?",
				message.ID, alternate.Message.RevisionID).Scan(&revisions); err != nil || revisions != 1 {
				t.Fatalf("terminal reconciliation lost the destination revision: %d %v", revisions, err)
			}
		})
	}
}

func TestRecoveredSendReceiptDoesNotVerifyAsExpiredWhilePaused(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	dest := copyRestoreFixture(t, source)
	key := migrationID()
	input := domain.OutgoingComposition{OriginalText: "paused browser send"}
	if _, err := engine.SendComposition(ctx, key, chat.ID, input, nil); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if err := testRestoreEngine(t, dest).VerifyExpiredSend(ctx, key, chat.ID, input, nil, application.ReplyReview{}); err == nil {
		t.Fatal("expired-send verification reported success for a send still awaiting review")
	}
}

func TestRecoveredSendResumePreservesOriginalRevisionLineage(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	dest := copyRestoreFixture(t, source)
	original := queueBackupFixture(t, engine, chat.ID, "before editing", nil)
	edited, err := engine.UpdatePendingMessage(ctx, migrationID(), original.ID, original.RevisionID,
		&domain.OutgoingComposition{OriginalText: "after editing"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, original.ID, edited.Message.RevisionID, true, now); err != nil {
		t.Fatal(err)
	}
	const getRevision = "SELECT parents, payload, created_at FROM message_revisions WHERE entity_id=? AND id=?"
	for _, revision := range []domain.ID{original.RevisionID, edited.Message.RevisionID} {
		var want, got [3]string
		if err := source.readDB.QueryRowContext(ctx, getRevision, original.ID, revision).Scan(&want[0], &want[1], &want[2]); err != nil {
			t.Fatal(err)
		}
		if err := dest.readDB.QueryRowContext(ctx, getRevision, original.ID, revision).Scan(&got[0], &got[1], &got[2]); err != nil {
			t.Fatalf("recovered revision %s was lost: %v", revision, err)
		}
		if got != want {
			t.Fatalf("recovered revision %s changed: got %q want %q", revision, got, want)
		}
	}
}

func TestRecoveredSendResumeRejectsDirectOwnRetractionEvidence(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	dest := copyRestoreFixture(t, source)
	message := queueBackupFixture(t, engine, chat.ID, "already retracted", nil)
	account, err := source.Account(ctx, chat.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if err := dest.RecordMessageRetraction(ctx, chat.AccountID, "direct", chat.Address, application.IncomingRetraction{
		Scope: "xmpp:correct:sender:" + account.Address, RemoteID: string(message.ID), At: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, message.ID, message.RevisionID, true, now); err == nil {
		t.Fatal("send was resumed despite its authenticated retraction")
	}
}

func TestRecoveredSendResumePreservesSendNowReceipt(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	dest := copyRestoreFixture(t, source)
	due := now.Add(time.Hour)
	message := queueBackupFixture(t, engine, chat.ID, "send now", &due)
	key := migrationID()
	sendNow, err := engine.UpdatePendingMessage(ctx, key, message.ID, message.RevisionID, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, message.ID, sendNow.Message.RevisionID, true, now); err != nil {
		t.Fatal(err)
	}
	current, err := dest.Message(ctx, message.ID)
	if err != nil || current.TimelineAt == nil || sendNow.Message.TimelineAt == nil ||
		!current.TimelineAt.Equal(*sendNow.Message.TimelineAt) {
		t.Fatalf("resuming lost the send-now timeline position: %+v %v", current, err)
	}
	replay, err := testRestoreEngine(t, dest).UpdatePendingMessage(ctx, key, message.ID, message.RevisionID, nil, nil, true)
	if err != nil || !replay.Replayed || replay.Message.RevisionID != sendNow.Message.RevisionID {
		t.Fatalf("send-now receipt did not survive recovery: %+v %v", replay, err)
	}
}

func TestRecoveredSendNeverRetainsExpiredAutomaticQuote(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	const quote = "original content removed by retention"
	const reply = "new reply body"
	target := replySeed(t, source, chat, quote, now.AddDate(0, 0, -20), nil, nil)
	expiredFirst, restoredFirst := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	key := migrationID()
	sent, err := engine.SendMessageWithReply(ctx, key, chat.ID, reply, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Message.OutboundReply == nil || sent.Message.OutboundReply.Excerpt != quote {
		t.Fatal("reply fixture has no frozen quote")
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	expire := func(dest *Store) {
		t.Helper()
		days := 1
		if _, err := testRestoreEngine(t, dest).SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
			t.Fatal(err)
		}
		purged, err := dest.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
		if err != nil || purged != 1 {
			t.Fatalf("expiry fixture: count=%d error=%v", purged, err)
		}
	}
	expire(expiredFirst)
	if _, err := expiredFirst.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := restoredFirst.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	expire(restoredFirst)
	for _, dest := range []*Store{expiredFirst, restoredFirst} {
		items, err := dest.RecoveredSends(ctx)
		if err != nil || len(items) != 1 || items[0].Text != reply {
			t.Fatalf("restored authored text was lost: %+v %v", items, err)
		}
		recovered, err := dest.queries.GetRecoveredSend(ctx, string(sent.Message.ID))
		if err != nil {
			t.Fatal(err)
		}
		var receipt string
		if err := dest.readDB.QueryRowContext(ctx, "SELECT result FROM recovered_send_operations WHERE operation_id=?", key).Scan(&receipt); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(recovered.Payload, quote) || strings.Contains(receipt, quote) {
			t.Fatal("expired target's quote remains in recoverable message or receipt")
		}
	}
}
