package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreMergesRoomEchoWithExistingLogicalSend(t *testing.T) {
	source, _, placeholder := outboxFixture(t)
	engine, room := promoteBackupRoomFixture(t, source, placeholder)
	witness := copyRestoreFixture(t, source)
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	ctx := t.Context()

	pending := queueBackupFixture(t, engine, room.ID, "already transmitted to the room", nil)
	dest := copyRestoreFixture(t, source)
	now := pending.CreatedAt.Add(time.Hour)
	if err := witnessEngine.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		IsOwn: true, SenderAddress: "alice@localhost", SenderName: "Tester",
		ProviderID:  "room:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CanonicalID: pending.ID, ArchiveID: "known-room-archive-echo", ArchiveScope: room.Address,
		OriginalText: pending.OriginalText, CreatedAt: pending.CreatedAt, Historical: true,
	}); err != nil {
		t.Fatal(err)
	}
	proof, archive, scratch := backupRestoreFixture(t, witness, false)
	for i := 0; i < 2; i++ {
		if _, err := dest.mergeBackup(ctx, archive, scratch, proof, now); err != nil {
			t.Fatal(err)
		}
		items, err := dest.Messages(ctx, room.ID, "", 100)
		if err != nil || len(items) != 1 || items[0].ID != pending.ID || items[0].OriginalText != pending.OriginalText {
			t.Fatalf("restore %d duplicated or replaced the original logical room message: %+v, %v", i+1, items, err)
		}
	}
	if due, err := dest.PendingMessages(ctx, now); err != nil || len(due) != 0 {
		t.Fatalf("an authenticated room echo left an executable send: %+v, %v", due, err)
	}
}

func TestBackupRestoreResumedSendCannotDispatchAfterAuthenticatedEcho(t *testing.T) {
	checkResumedSendBeforeWire(t, func(t *testing.T, store *Store, chat domain.Conversation, message domain.Message, _ time.Time) {
		receiveBackupOwnDirectEcho(t, store, chat, message)
	})
}

func TestBackupRestoreResumedSendCannotDispatchAfterAuthenticatedRetraction(t *testing.T) {
	checkResumedSendBeforeWire(t, receiveBackupOwnDirectRetraction)
}

func receiveBackupOwnDirectRetraction(t *testing.T, store *Store, chat domain.Conversation, message domain.Message, now time.Time) {
	t.Helper()
	ctx := t.Context()
	engine := testRestoreEngine(t, store)
	engine.SetProtocols(&outboundProtocol{})
	account, err := engine.Account(ctx, chat.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Message(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	retract := func(sender string) {
		t.Helper()
		if err := engine.Receive(ctx, application.Incoming{
			AccountID: chat.AccountID, Kind: "direct", Address: chat.Address,
			Retraction: &application.IncomingRetraction{Scope: "xmpp:correct:sender:" + sender, RemoteID: string(message.ID), At: now},
		}); err != nil {
			t.Fatal(err)
		}
	}
	retract(chat.Address)
	if current, err := store.Message(ctx, message.ID); err != nil || current.RevisionID != before.RevisionID || current.SendState != before.SendState {
		t.Fatalf("peer retraction affected our pending message: %+v, %v", current, err)
	}
	retract(account.Address)
}

func checkResumedSendBeforeWire(t *testing.T, observe func(*testing.T, *Store, domain.Conversation, domain.Message, time.Time)) {
	t.Helper()
	t.Run("before claim", func(t *testing.T) { checkResumedSendObservation(t, observe, false) })
	t.Run("after guarded claim", func(t *testing.T) { checkResumedSendObservation(t, observe, true) })
}

func checkResumedSendObservation(t *testing.T, observe func(*testing.T, *Store, domain.Conversation, domain.Message, time.Time), claimedBeforeObservation bool) {
	t.Helper()
	dest, chat, pending, now := resumedRestoreFixture(t)
	ctx := t.Context()
	observed, _, err := dest.PendingMessagesByAttachments(ctx, now, false, application.PendingMessageCursor{})
	if err != nil || len(observed) != 1 || observed[0].ID != pending.ID {
		t.Fatalf("worker did not observe the resumed send: %+v, %v", observed, err)
	}
	claimedRevision := observed[0].RevisionID
	if claimedBeforeObservation {
		claimed, err := dest.ClaimDispatchMessage(ctx, pending.ID, claimedRevision, "", migrationID(), now, true)
		if err != nil {
			t.Fatal(err)
		}
		claimedRevision = claimed.RevisionID
	}

	observe(t, dest, chat, pending, now)
	if due, err := dest.PendingMessages(ctx, now); err != nil || len(due) != 0 {
		t.Errorf("resumed original still appears ready after authenticated evidence: %+v, %v", due, err)
	}
	if claimedBeforeObservation {
		if err := dest.RecordOriginalWireAttempt(ctx, pending.ID, claimedRevision, ""); !errors.Is(err, application.ErrStateConflict) {
			t.Errorf("worker advanced an already-observed send to the wire: %v", err)
		}
	} else if claimed, err := dest.ClaimDispatchMessage(ctx, pending.ID, claimedRevision, "", migrationID(), now, true); !errors.Is(err, application.ErrStateConflict) {
		t.Errorf("worker claim after authenticated evidence: %s, %s, %v", claimed.ID, claimed.SendState, err)
	}
}

func TestBackupRestoreManagedAttemptCannotRetryAfterAuthenticatedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		observe func(*testing.T, *Store, domain.Conversation, domain.Message, time.Time)
	}{
		{"echo", func(t *testing.T, s *Store, c domain.Conversation, m domain.Message, _ time.Time) {
			receiveBackupOwnDirectEcho(t, s, c, m)
		}},
		{"retraction", receiveBackupOwnDirectRetraction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest, chat, pending, now := resumedRestoreFixture(t)
			ctx := t.Context()
			claimed, err := dest.ClaimDispatchMessage(ctx, pending.ID, pending.RevisionID, "", migrationID(), now, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := dest.RecordOriginalWireAttempt(ctx, claimed.ID, claimed.RevisionID, ""); err != nil {
				t.Fatal(err)
			}
			tc.observe(t, dest, chat, pending, now)
			if current, err := dest.Message(ctx, pending.ID); err != nil || current.SendState != "dispatching" || current.RevisionID != claimed.RevisionID {
				t.Fatalf("evidence changed the wire attempt before it completed: %+v, %v", current, err)
			}
			retryAt := now.Add(time.Second)
			current, err := dest.TransitionMessage(ctx, claimed.ID, "dispatching", "queued", migrationID(), now, retryAt)
			if err != nil || current.SendState != "unknown" {
				t.Fatalf("completed attempt with conflicting evidence: %+v, %v", current, err)
			}
			if due, err := dest.PendingMessages(ctx, retryAt); err != nil || len(due) != 0 {
				t.Fatalf("the already-observed intent became eligible for retry: %+v, %v", due, err)
			}
		})
	}
}

func resumedRestoreFixture(t *testing.T) (*Store, domain.Conversation, domain.Message, time.Time) {
	t.Helper()
	source, engine, chat := outboxFixture(t)
	dest := copyRestoreFixture(t, source)
	ctx := t.Context()
	pending := queueBackupFixture(t, engine, chat.ID, "server evidence arrives after review", nil)
	now := pending.CreatedAt.Add(time.Hour)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, now); err != nil {
		t.Fatal(err)
	}
	return dest, chat, pending, now
}

func TestBackupRestoreAuthenticatesPlainHistoricalEchoAndKeepsTimestamp(t *testing.T) {
	for _, tc := range []struct{ state, wireText string }{
		{"submitted", "scheduled text"},
		{"unknown", "earlier composition"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			dest := copyRestoreFixture(t, source)
			ctx := t.Context()
			due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
			pending := queueBackupFixture(t, engine, chat.ID, "scheduled text", &due)
			sentAt, observedAt := due.Add(time.Minute), due.Add(48*time.Hour)
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, observedAt); err != nil {
				t.Fatal(err)
			}
			if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, observedAt); err != nil {
				t.Fatal(err)
			}
			receiver, err := application.New(dest, "local", func() (domain.ID, error) { return migrationID(), nil }, func() time.Time { return observedAt })
			if err != nil {
				t.Fatal(err)
			}
			receiver.SetProtocols(&outboundProtocol{})
			if err := receiver.Receive(ctx, application.Incoming{
				Kind: "direct", Address: chat.Address, AccountID: chat.AccountID, IsOwn: true,
				ProviderID: string(pending.ID), ArchiveID: "scheduled-own-echo", ArchiveScope: "alice@localhost",
				OriginalText: tc.wireText, CreatedAt: sentAt, Historical: true,
			}); err != nil {
				t.Fatal(err)
			}
			current, err := dest.Message(ctx, pending.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.SendState != tc.state || current.OriginalText != pending.OriginalText || !current.TimelineTime().Equal(sentAt) {
				t.Fatalf("reconciled scheduled message: state=%s text=%q timeline=%s", current.SendState, current.OriginalText, current.TimelineTime())
			}
		})
	}
}
