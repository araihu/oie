package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRemembersOwnEchoBeforePendingBackup(t *testing.T) {
	t.Parallel()
	t.Run("retained echo", func(t *testing.T) { checkBackupRestoreOwnEchoOrder(t, true, false) })
	t.Run("expired echo", func(t *testing.T) { checkBackupRestoreOwnEchoOrder(t, false, false) })
	t.Run("expired echo in a rebackup", func(t *testing.T) { checkBackupRestoreOwnEchoOrder(t, false, true) })
}

func checkBackupRestoreOwnEchoOrder(t *testing.T, retainEcho, rebackup bool) {
	t.Helper()
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	dest := copyRestoreFixture(t, source)
	var restored *Store
	if rebackup {
		restored = copyRestoreFixture(t, source)
	}
	witness := copyRestoreFixture(t, source)
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	days := 1
	if _, err := testRestoreEngine(t, dest).SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	composition := domain.OutgoingComposition{OriginalText: "send observed by the other copy"}
	accepted, err := engine.SendComposition(ctx, key, chat.ID, composition, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	old, oldDB, oldScratch := backupRestoreFixture(t, source, false)
	if err := witnessEngine.Receive(ctx, application.Incoming{
		AccountID: chat.AccountID, Kind: "direct", Address: chat.Address, IsOwn: true,
		ProviderID: string(pending.ID), ArchiveID: "server-ack", ArchiveScope: "alice@localhost",
		OriginalText: pending.OriginalText, CreatedAt: pending.CreatedAt, Historical: true,
	}); err != nil {
		t.Fatal(err)
	}
	observed, err := witness.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(observed) != 1 || observed[0].ID == pending.ID ||
		observed[0].ProviderID != string(pending.ID) || observed[0].SendState != "submitted" {
		t.Fatalf("witness did not retain an independent own echo: %+v, %v", observed, err)
	}
	newest, newestDB, newestScratch := backupRestoreFixture(t, witness, false)
	restoreTime := pending.CreatedAt.Add(48 * time.Hour)
	var wantMessages int64
	if retainEcho {
		restoreTime = pending.CreatedAt.Add(time.Hour)
		wantMessages = 1
	}
	if result, err := dest.mergeBackup(ctx, newestDB, newestScratch, newest, restoreTime); err != nil || result.Messages != wantMessages {
		t.Fatalf("restore ignored destination retention: %+v, %v", result, err)
	}
	if rebackup {
		again, againDB, againScratch := backupRestoreFixture(t, dest, false)
		dest = restored
		if _, err := dest.mergeBackup(ctx, againDB, againScratch, again, restoreTime); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dest.mergeBackup(ctx, oldDB, oldScratch, old, restoreTime); err != nil {
		t.Fatal(err)
	}
	if items, err := dest.RecoveredSends(ctx); err != nil || len(items) != 0 {
		t.Fatalf("old backup revived an already-submitted intent: %+v, %v", items, err)
	}
	if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, restoreTime); err == nil {
		t.Fatal("an already-submitted send could be resumed")
	}
	replay, retryErr := testRestoreEngine(t, dest).SendComposition(ctx, key, chat.ID, composition, nil)
	if due, err := dest.PendingMessages(ctx, restoreTime.Add(time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("retrying the original operation key created a new executable send: %+v, retry=%+v, retry error=%v, pending error=%v", due, replay, retryErr, err)
	}
}
