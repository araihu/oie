package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestRecoveredSendResumesOnlySanitizedReplyToMissingExpiredTarget(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	neverRetained := copyRestoreFixture(t, source)
	const quote = "original message outside retention"
	const answer = "the authored reply survives"
	ref := domain.MessageReference{Scope: "sender:bob@localhost", ID: "old-reply-target", Author: "bob@localhost"}
	target := replySeed(t, source, chat, quote, now.AddDate(0, 0, -20), []domain.MessageReference{ref}, nil)
	expiredFirst, restoredFirst, unsanitized := copyRestoreFixture(t, source), copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	engine.SetProtocols(&outboundProtocol{wireReply: true})
	accepted, err := engine.SendMessageWithReply(ctx, migrationID(), chat.ID, answer, target.ID)
	if err != nil || accepted.Message.OutboundReply == nil || accepted.Message.OutboundReply.Reference == nil {
		t.Fatalf("fixture did not retain the outgoing quote: %+v, %v", accepted, err)
	}
	message := accepted.Message
	manifest, archive, scratch := backupRestoreFixture(t, source, false)

	for _, tc := range []struct {
		name            string
		destination     *Store
		purgeBefore     bool
		hasExpiryMarker bool
		unsafePayload   bool
	}{
		{name: "target expired before restore", destination: expiredFirst, purgeBefore: true, hasExpiryMarker: true},
		{name: "target expired after restore", destination: restoredFirst, hasExpiryMarker: true},
		{name: "absent target without an expiry marker", destination: neverRetained},
		{name: "expired target with an unsanitized payload", destination: unsanitized, hasExpiryMarker: true, unsafePayload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := tc.destination
			days := 1
			if _, err := testRestoreEngine(t, dest).SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
				t.Fatal(err)
			}
			purge := func() {
				t.Helper()
				n, err := dest.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
				if err != nil || n != 1 {
					t.Fatalf("target purge fixture: %d, %v", n, err)
				}
			}
			if tc.purgeBefore {
				purge()
			}
			if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
				t.Fatal(err)
			}
			if tc.hasExpiryMarker && !tc.purgeBefore {
				purge()
			}
			if _, err := dest.Message(ctx, target.ID); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("target still exists: %v", err)
			}
			if expired, err := dest.queries.IsExpiredMessage(ctx, string(target.ID)); err != nil || expired != tc.hasExpiryMarker {
				t.Fatalf("unexpected target expiry marker: %t, %v", expired, err)
			}
			if tc.unsafePayload {
				if _, err := dest.db.ExecContext(ctx, "UPDATE recovered_sends SET payload=? WHERE message_id=?", jsonText(message), message.ID); err != nil {
					t.Fatal(err)
				}
			}
			err := dest.ReviewRecoveredSend(ctx, message.ID, message.RevisionID, true, now)
			if !tc.hasExpiryMarker || tc.unsafePayload {
				if err == nil {
					t.Fatal("an unverified reply became executable")
				}
				if pending, err := dest.PendingMessages(ctx, now); err != nil || len(pending) != 0 {
					t.Fatalf("a refused reply entered the active outbox: %+v, %v", pending, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the explicitly reviewed sanitized reply did not resume: %v", err)
			}
			current, err := dest.Message(ctx, message.ID)
			if err != nil || current.SendState != "queued" || current.OriginalText != answer || current.Reply == nil ||
				!current.Reply.OriginalExpired || current.Reply.Excerpt != "" {
				t.Fatalf("resumed reply contained more than the approved answer: %+v, %v", current, err)
			}
			claimed, err := dest.ClaimDispatchMessage(ctx, message.ID, message.RevisionID, "", migrationID(), now, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := dest.RecordOriginalWireAttempt(ctx, claimed.ID, claimed.RevisionID, application.Outgoing{OriginalText: answer}, ""); err != nil {
				t.Fatalf("wire gate did not accept the answer without the expired quote or reference: %v", err)
			}
		})
	}
}
