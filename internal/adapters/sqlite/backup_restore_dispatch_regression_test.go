package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
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
	t.Run("before claim", func(t *testing.T) { checkResumedSendBeforeWireEcho(t, false) })
	t.Run("after guarded claim", func(t *testing.T) { checkResumedSendBeforeWireEcho(t, true) })
}

func checkResumedSendBeforeWireEcho(t *testing.T, claimBeforeEcho bool) {
	t.Helper()
	source, engine, chat := outboxFixture(t)
	dest := copyRestoreFixture(t, source)
	ctx := t.Context()
	pending := queueBackupFixture(t, engine, chat.ID, "echo arrives after review", nil)
	now := pending.CreatedAt.Add(time.Hour)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, now); err != nil {
		t.Fatal(err)
	}
	observed, _, err := dest.PendingMessagesByAttachments(ctx, now, false, application.PendingMessageCursor{})
	if err != nil || len(observed) != 1 || observed[0].ID != pending.ID {
		t.Fatalf("worker did not observe the resumed send: %+v, %v", observed, err)
	}
	claimedRevision := observed[0].RevisionID
	if claimBeforeEcho {
		claimed, err := dest.ClaimDispatchMessage(ctx, pending.ID, claimedRevision, "", migrationID(), now, true)
		if err != nil {
			t.Fatal(err)
		}
		claimedRevision = claimed.RevisionID
	}

	receiveBackupOwnDirectEcho(t, dest, chat, pending)
	if due, err := dest.PendingMessages(ctx, now); err != nil || len(due) != 0 {
		t.Errorf("resumed original still appears ready after its authenticated echo: %+v, %v", due, err)
	}
	if claimBeforeEcho {
		if err := dest.RecordOriginalWireAttempt(ctx, pending.ID, claimedRevision, ""); !errors.Is(err, application.ErrStateConflict) {
			t.Errorf("worker advanced an already-observed send to the wire: %v", err)
		}
	} else if claimed, err := dest.ClaimDispatchMessage(ctx, pending.ID, claimedRevision, "", migrationID(), now, true); !errors.Is(err, application.ErrStateConflict) {
		t.Errorf("worker claim after the echo: %s, %s, %v", claimed.ID, claimed.SendState, err)
	}
}
