package sqlite

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func starMessage(t *testing.T, store *Store, c domain.Conversation, at time.Time) domain.Message {
	t.Helper()
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob"}, OriginalText: "retained source", CreatedAt: at, SendState: "received", ProviderID: string(migrationID()), Historical: true}
	if err := store.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err := store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStarChangesAreAtomicAndIndependentOfSource(t *testing.T) {
	for _, failure := range []string{"BEFORE INSERT ON message_star_revisions", "BEFORE INSERT ON message_stars", "BEFORE INSERT ON changes", "BEFORE UPDATE ON operations"} {
		t.Run(failure, func(t *testing.T) {
			store, e, c := outboxFixture(t)
			m := starMessage(t, store, c, time.Now().UTC())
			before, err := store.Conversation(t.Context(), c.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, sequence, err := store.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec("CREATE TRIGGER fail_star " + failure + " BEGIN SELECT RAISE(ABORT,'injected star failure'); END;"); err != nil {
				t.Fatal(err)
			}
			key := migrationID()
			if _, err = e.SetMessageStar(t.Context(), key, m.ID, "", true); err == nil {
				t.Fatal("injected failure was accepted")
			}
			if _, err = e.Operation(t.Context(), key); !errors.Is(err, application.ErrNotFound) {
				t.Fatal("failed star retained an operation receipt", err)
			}
			for _, table := range []string{"message_stars", "message_star_revisions", "outbox"} {
				var count int
				if err = store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial %s rows: %d %v", table, count, err)
				}
			}
			_, after, err := store.Snapshot(t.Context())
			if err != nil || after != sequence {
				t.Fatal("failed star emitted an event", err)
			}
			got, err := store.Message(t.Context(), m.ID)
			if err != nil || !reflect.DeepEqual(got, m) {
				t.Fatal("star changed message content or revision", err)
			}
			chat, err := store.Conversation(t.Context(), c.ID)
			if err != nil || !reflect.DeepEqual(before, chat) {
				t.Fatal("star changed unread or chat activity", err)
			}
		})
	}
}

func TestStarPreconditionsSerializeConcurrentTabsAndKeepReceipts(t *testing.T) {
	store, e, c := outboxFixture(t)
	m := starMessage(t, store, c, time.Now().UTC())
	initial, err := e.MessageStar(t.Context(), m.ID)
	if err != nil || initial.Starred || initial.RevisionID != "" || !initial.UpdatedAt.IsZero() {
		t.Fatal("read must expose untouched state", initial, err)
	}
	key := migrationID()
	first, err := e.SetMessageStar(t.Context(), key, m.ID, "", true)
	if err != nil || !first.Star.Starred || first.Star.RevisionID == "" {
		t.Fatal(first, err)
	}
	var successes, conflicts atomic.Int32
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			_, err := e.SetMessageStar(t.Context(), migrationID(), m.ID, first.Star.RevisionID, false)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, application.ErrStateConflict):
				conflicts.Add(1)
			default:
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if successes.Load() != 1 || conflicts.Load() != 15 {
		t.Fatalf("stale mutation bypassed precondition: %d successes, %d conflicts", successes.Load(), conflicts.Load())
	}
	current, err := e.MessageStar(t.Context(), m.ID)
	if err != nil || current.Starred || current.RevisionID == first.Star.RevisionID {
		t.Fatal(current, err)
	}
	_, before, _ := store.Snapshot(t.Context())
	replayed, err := e.SetMessageStar(t.Context(), key, m.ID, "", true)
	if err != nil || !replayed.Replayed || replayed.Star != first.Star {
		t.Fatal("lost-response retry did not return original receipt", replayed, err)
	}
	if _, err = e.SetMessageStar(t.Context(), key, m.ID, current.RevisionID, true); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("operation UUID accepted different input", err)
	}
	noChange, err := e.SetMessageStar(t.Context(), migrationID(), m.ID, current.RevisionID, false)
	if err != nil || noChange.Star != current {
		t.Fatal("same value changed its revision", err)
	}
	_, after, _ := store.Snapshot(t.Context())
	if after != before {
		t.Fatal("replay or no-op emitted another event")
	}
	var revisions int
	if err = store.db.QueryRow("SELECT COUNT(*) FROM message_star_revisions").Scan(&revisions); err != nil || revisions != 2 {
		t.Fatal("star revision history was overwritten or duplicated", revisions, err)
	}
	events, err := e.Events(t.Context(), before-2)
	if err != nil || len(events) != 2 || events[0].Star == nil || events[1].Star == nil || events[0].Star.RevisionID != first.Star.RevisionID || events[1].Star.Starred {
		t.Fatal("durable source-linked star events missing", events, err)
	}
}

func TestStarDoesNotInvalidateQueuedMessageCancellation(t *testing.T) {
	_, e, c := outboxFixture(t)
	sent, err := e.SendMessage(t.Context(), migrationID(), c.ID, "queued source")
	if err != nil {
		t.Fatal(err)
	}
	starred, err := e.SetMessageStar(t.Context(), migrationID(), sent.Message.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := e.CancelMessage(t.Context(), migrationID(), sent.Message.ID, sent.Message.RevisionID)
	if err != nil || cancelled.Message.SendState != "cancelled" {
		t.Fatal("starring changed the send cutoff/revision", err)
	}
	page, err := e.StarredMessages(t.Context(), application.StarQuery{Limit: 50})
	if err != nil || len(page) != 1 || page[0].Message.SendState != "cancelled" || page[0].Star != starred.Star {
		t.Fatal("starred list did not return current source projection", page, err)
	}
}

func TestStarMigrationDownUpPreservesMessagesAndOutbox(t *testing.T) {
	store, e, c := outboxFixture(t)
	sent, err := e.SendMessage(t.Context(), migrationID(), c.ID, "pending send must survive")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.SetMessageStar(t.Context(), migrationID(), sent.Message.ID, "", true); err != nil {
		t.Fatal(err)
	}
	driver, err := migratesqlite.WithInstance(store.db, &migratesqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if err = runner.Up(); err != nil {
		t.Fatal(err)
	}
	m, err := e.Message(t.Context(), sent.Message.ID)
	if err != nil || !reflect.DeepEqual(m, sent.Message) {
		t.Fatal("migration changed source message", err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 1 || pending[0].ID != m.ID {
		t.Fatal("migration discarded pending intent", err)
	}
	if _, err = e.Events(t.Context(), 0); err != nil {
		t.Fatal("downgrade left an undecodable star event", err)
	}
}
