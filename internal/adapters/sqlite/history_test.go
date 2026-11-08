package sqlite

import (
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"testing"
	"time"
)

func TestHistoryReplayChronologyUnreadAndOutboxIsolation(t *testing.T) {
	store, e, c := outboxFixture(t)
	now := time.Now().UTC()
	live := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "latest live", CreatedAt: now, ProviderID: "live-id", SendState: "received"}
	if err := store.ReceiveMessage(t.Context(), c, live, migrationID()); err != nil {
		t.Fatal(err)
	}
	first, err := store.Conversation(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		old := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "old archive", CreatedAt: now.Add(-time.Duration(i+1) * time.Hour), ProviderID: "historical-" + string(rune('a'+i)), SendState: "received", Historical: true, ArchiveScope: "alice@localhost", ArchiveID: string(rune('a' + i))}
		for attempt := 0; attempt < 2; attempt++ {
			if err = store.ReceiveMessage(t.Context(), c, old, migrationID()); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, _ := store.Conversation(t.Context(), c.ID)
	if got.LastMessage != "latest live" || got.UnreadCount != 1 || got.RevisionID != first.RevisionID {
		t.Fatalf("history changed latest preview/unread: %+v", got)
	}
	page, err := store.Messages(t.Context(), c.ID, "", 2)
	if err != nil || len(page) != 2 || page[0].OriginalText != "latest live" {
		t.Fatalf("chronology: %+v %v", page, err)
	}
	next, err := store.Messages(t.Context(), c.ID, page[1].ID, 2)
	if err != nil || len(next) != 2 || !next[0].CreatedAt.After(next[1].CreatedAt) {
		t.Fatalf("chronological paging: %+v %v", next, err)
	}
	alias := live
	alias.ID = migrationID()
	alias.RevisionID = migrationID()
	alias.Historical = true
	alias.ArchiveScope = "alice@localhost"
	alias.ArchiveID = "live-archive"
	if err = store.ReceiveMessage(t.Context(), c, alias, migrationID()); err != nil {
		t.Fatal(err)
	}
	all, _ := store.Messages(t.Context(), c.ID, "", 100)
	if len(all) != 4 {
		t.Fatalf("live/archive duplicate: %d", len(all))
	}
	pending, _ := store.PendingMessages(t.Context(), now.Add(time.Hour))
	if len(pending) != 0 {
		t.Fatal("archive created send work")
	}
	if err = e.CommitHistoryCheckpoint(t.Context(), c.AccountID, "alice@localhost", "opaque-last"); err != nil {
		t.Fatal(err)
	}
	if pos, _ := e.HistoryCheckpoint(t.Context(), c.AccountID, "alice@localhost"); pos != "opaque-last" {
		t.Fatal("checkpoint lost")
	}
	if err = e.HistoryChanged(t.Context(), domain.HistoryStatus{AccountID: c.AccountID, State: "complete"}); err != nil {
		t.Fatal(err)
	}
	h, err := e.History(t.Context(), c.AccountID)
	if err != nil || h.RetainedMessages != 4 || h.Conversations != 1 || h.State != "complete" {
		t.Fatalf("history progress: %+v %v", h, err)
	}
	// Importing an observed own message must never revive a cancelled intent.
	own, err := e.SendMessage(t.Context(), migrationID(), c.ID, "cancelled own")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := e.CancelMessage(t.Context(), migrationID(), own.Message.ID, own.Message.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	copy := cancelled.Message
	copy.ID = migrationID()
	copy.RevisionID = migrationID()
	copy.Historical = true
	copy.SendState = "submitted"
	copy.ArchiveScope = "alice@localhost"
	copy.ArchiveID = "cancelled-archive"
	if err = store.ReceiveMessage(t.Context(), c, copy, migrationID()); err != nil {
		t.Fatal(err)
	}
	original, _ := e.Message(t.Context(), own.Message.ID)
	if original.SendState != "cancelled" {
		t.Fatal("archive revived cancelled send")
	}
}
func TestRestoredRoomIsIdempotentAndRejectsSend(t *testing.T) {
	store, e, c := outboxFixture(t)
	room := c
	room.ID = migrationID()
	room.RevisionID = migrationID()
	room.PeerID = migrationID()
	room.Address = "room@conference.localhost"
	room.Title = "Saved room"
	room.Kind = "room"
	for i := 0; i < 2; i++ {
		if err := store.RestoreConversation(t.Context(), room); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.Conversations(t.Context(), application.ConversationQuery{Cursor: 1 << 62, Limit: 100})
	if err != nil || len(rows) != 2 {
		t.Fatalf("room duplicated: %v %+v", err, rows)
	}
	if _, err = e.SendMessage(t.Context(), migrationID(), room.ID, "must not send"); err != application.ErrProtocolUnavailable {
		t.Fatalf("read-only room accepted send: %v", err)
	}
}

func TestHistoryMigrationDownUpPreservesExistingMessagesAndOutbox(t *testing.T) {
	store, e, c := outboxFixture(t)
	accepted, err := e.SendMessage(t.Context(), migrationID(), c.ID, "retained through migration")
	if err != nil {
		t.Fatal(err)
	}
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	driver, err := migratesqlite.WithInstance(store.db, &migratesqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if err = runner.Steps(1); err != nil {
		t.Fatal(err)
	}
	messages, err := store.Messages(t.Context(), c.ID, "", 100)
	if err != nil || len(messages) != 1 || messages[0].ID != accepted.Message.ID || messages[0].OriginalText != accepted.Message.OriginalText || !messages[0].CreatedAt.Equal(accepted.Message.CreatedAt) {
		t.Fatalf("migration lost retained data: %+v %v", messages, err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 1 || pending[0].SendState != "queued" {
		t.Fatal("migration lost pending intent")
	}
}
