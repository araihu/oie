package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestFailedRevisionWriteRollsBackAccountOperationAndEvent(t *testing.T) {
	newID := func() (domain.ID, error) { u, err := uuid.NewV7(); return domain.ID(u.String()), err }
	engineID, _ := newID()
	store, err := Open(t.Context(), t.TempDir(), engineID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	_, err = store.db.Exec(`CREATE TRIGGER fail_revision BEFORE INSERT ON revisions BEGIN SELECT RAISE(ABORT,'injected write failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	engine, _ := application.New(store, "local", newID, time.Now)
	key, _ := newID()
	if _, err := engine.Register(context.Background(), key, application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"}); err == nil {
		t.Fatal("fault unexpectedly succeeded")
	}
	for _, table := range []string{"accounts", "revisions", "operations", "changes"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial commit in %s: count=%d err=%v", table, count, err)
		}
	}
}

func TestTimelineAndEventCursorReadCommittedStateWhileWriterIsActive(t *testing.T) {
	store, engine, conversation := outboxFixture(t)
	accepted, err := engine.SendMessage(t.Context(), migrationID(), conversation.ID, "committed message")
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tx, q, err := begin(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	pending, err := change(t.Context(), q, "test.pending", struct{}{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, during, err := store.Snapshot(ctx)
	if err != nil || during != before {
		t.Fatalf("event cursor during write = %d, %v; want %d", during, err, before)
	}
	window, err := engine.MessageContext(ctx, conversation.ID, accepted.Message.ID, 16)
	if err != nil || len(window.Items) != 1 || window.Items[0].ID != accepted.Message.ID {
		t.Fatalf("retained context during write = %+v, %v", window, err)
	}
	reactions, err := engine.ConversationReactions(ctx, conversation.ID, []domain.ID{accepted.Message.ID})
	if err != nil || len(reactions) != 1 || reactions[0].MessageID != accepted.Message.ID {
		t.Fatalf("reaction snapshots during write = %+v, %v", reactions, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, after, err := store.Snapshot(ctx)
	if err != nil || after != pending {
		t.Fatalf("event cursor after commit = %d, %v; want %d", after, err, pending)
	}
}
