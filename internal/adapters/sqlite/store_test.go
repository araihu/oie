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
	defer store.Close()
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
