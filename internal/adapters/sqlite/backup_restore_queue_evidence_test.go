package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
)

func TestBackupRestoreStopsActiveSendWithIndependentlyRetainedOwnEcho(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
	}{
		{"inside retention", time.Hour},
		{"outside retention", 48 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest, engine, chat := outboxFixture(t)
			ctx := t.Context()
			now := time.Now().UTC()
			witness := copyRestoreFixture(t, dest)
			witnessEngine := testRestoreEngine(t, witness)
			witnessEngine.SetProtocols(&outboundProtocol{})
			days := 1
			if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
				t.Fatal(err)
			}
			pending := queueBackupFixture(t, engine, chat.ID, "send observed by the other copy", nil)
			if err := witnessEngine.Receive(ctx, application.Incoming{
				AccountID: chat.AccountID, Kind: "direct", Address: chat.Address, IsOwn: true,
				ProviderID: string(pending.ID), ArchiveID: "server-ack", ArchiveScope: "alice@localhost",
				OriginalText: pending.OriginalText, CreatedAt: now.Add(-tc.age), Historical: true,
			}); err != nil {
				t.Fatal(err)
			}
			observed, err := witness.Messages(ctx, chat.ID, "", 10)
			if err != nil || len(observed) != 1 || observed[0].ID == pending.ID ||
				observed[0].ProviderID != string(pending.ID) || observed[0].SendState != "submitted" {
				t.Fatalf("witness fixture did not retain an independent own echo: %+v %v", observed, err)
			}
			manifest, archive, scratch := backupRestoreFixture(t, witness, false)
			if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
				t.Fatal(err)
			}
			due, err := dest.PendingMessages(ctx, now.Add(time.Hour))
			if err != nil || len(due) != 0 {
				t.Fatalf("provider evidence left a send eligible for dispatch: %+v %v", due, err)
			}
			retained, err := dest.Messages(ctx, chat.ID, "", 10)
			if err != nil || len(retained) != 1 || retained[0].ID != pending.ID {
				t.Fatalf("restore did not preserve the original logical message: %+v %v", retained, err)
			}
		})
	}
}

func TestBackupRestoreKeepsCancellationReceiptForExistingQueue(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	pending := queueBackupFixture(t, engine, chat.ID, "cancelled before restore", nil)
	dest := copyRestoreFixture(t, source)
	key := migrationID()
	original, err := engine.CancelMessage(ctx, key, pending.ID, pending.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	replay, err := testRestoreEngine(t, dest).CancelMessage(ctx, key, pending.ID, pending.RevisionID)
	if err != nil || !replay.Replayed || replay.Message.RevisionID != original.Message.RevisionID {
		t.Fatalf("cancel operation lost its receipt: %+v %v", replay, err)
	}
}
