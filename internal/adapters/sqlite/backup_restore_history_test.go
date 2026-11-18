package sqlite

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreConversationPagesUseLocalActivityCursor(t *testing.T) {
	t.Parallel()
	source, engine, first := outboxFixture(t)
	engine.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	second, err := engine.OpenConversation(ctx, migrationID(), first.AccountID, "backup-second@localhost")
	if err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	dest, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dest.Close() })
	restoreTime := time.Now().UTC()
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, restoreTime); err != nil {
		t.Fatal(err)
	}

	destEngine := testRestoreEngine(t, dest)
	engineID, sequence, err := dest.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page, err := destEngine.Conversations(ctx, "", application.ConversationQuery{Limit: 1})
	if err != nil || len(page) != 2 || page[0].ID != second.Conversation.ID {
		t.Fatalf("first page %+v: %v", page, err)
	}
	cursor := fmt.Sprintf("%s:%d", engineID, page[0].ActivitySequence)
	page, err = destEngine.Conversations(ctx, cursor, application.ConversationQuery{Limit: 1})
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("second page %+v with cursor %q: %v", page, cursor, err)
	}
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, restoreTime); err != nil {
		t.Fatal(err)
	}
	_, after, err := dest.Snapshot(ctx)
	if err != nil || after != sequence {
		t.Fatalf("reapplying unchanged backup changed event sequence from %d to %d: %v", sequence, after, err)
	}
}

func TestBackupRestoreInvalidatesHistoryWithoutChangingDestinationActivity(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	replySeed(t, source, chat, "latest shared between profiles", now, nil, nil)
	dest := copyRestoreFixture(t, source)
	older := replySeed(t, source, chat, "older retained message from backup", now.Add(-time.Hour), nil, nil)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	before := newerConversation(t, dest, chat.ID)
	_, sequence, err := dest.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	after := newerConversation(t, dest, chat.ID)
	if after.ActivitySequence != before.ActivitySequence || after.LastMessage != before.LastMessage || after.UnreadCount != before.UnreadCount {
		t.Fatalf("import changed existing conversation activity: %+v, before %+v", after, before)
	}
	if _, err := dest.Message(ctx, older.ID); err != nil {
		t.Fatal(err)
	}
	events, err := dest.Events(ctx, sequence, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].HistoryPruned == nil || events[0].HistoryPruned.ConversationID != chat.ID || len(events[0].HistoryPruned.MessageIDs) != 0 {
		t.Fatalf("old history import must refresh the conversation without retiring messages: %+v", events)
	}
}

func TestBackupRestoreCanRecoverPreviouslyOutsideRetentionAfterWidening(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	dest := copyRestoreFixture(t, source)
	now := time.Now().UTC()
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "retention-window-review"}
	old := replySeed(t, source, chat, "outside initial restore window", now.AddDate(0, 0, -9), []domain.MessageReference{ref}, nil)
	engine := testRestoreEngine(t, dest)
	days := 7
	policy, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days)
	if err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Messages != 0 {
		t.Fatalf("message outside seven days was imported: %+v, %v", result, err)
	}
	days = 14
	if _, err := engine.SetConversationRetention(ctx, chat.ID, policy.Revision, &days); err != nil {
		t.Fatal(err)
	}
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Messages != 1 {
		t.Fatalf("message now inside fourteen days stayed excluded: %+v, %v", result, err)
	}
	if restored, err := dest.Message(ctx, old.ID); err != nil || restored.OriginalText != old.OriginalText {
		t.Fatalf("retention widening did not recover message: %+v, %v", restored, err)
	}
}

func TestBackupRestoreDoesNotResurrectAccountDeletedAfterImport(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	replySeed(t, source, chat, "account history", now, nil, nil)
	// This models an archive made from a profile imported before registration
	// receipts were restored. Its account still needs a deletion marker.
	if _, err := source.db.ExecContext(ctx, "DELETE FROM operations WHERE kind='register_account'"); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	dest, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dest.Close() })

	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Accounts != 1 {
		t.Fatalf("initial restore: %+v, %v", result, err)
	}
	if removed, err := dest.RemoveAccount(ctx, chat.AccountID, now); err != nil || !removed {
		t.Fatalf("remove imported account: %v, %v", removed, err)
	}
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("backup resurrected account: %+v, %v", result, err)
	}
	if _, err := dest.Account(ctx, chat.AccountID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("imported-then-deleted account is still available: %v", err)
	}

	deletion, deletionArchive, deletionScratch := backupRestoreFixture(t, dest, false)
	reopened, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.mergeBackup(ctx, deletionArchive, deletionScratch, deletion, now); err != nil {
		t.Fatal(err)
	}
	if result, err := reopened.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("account tombstone did not survive rebackup: %+v, %v", result, err)
	}
}
