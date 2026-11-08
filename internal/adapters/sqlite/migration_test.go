package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func migrationID() domain.ID {
	value, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return domain.ID(value.String())
}
func TestLegacyFoundationMigratesWithoutDiscardingData(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := migrations.ReadFile("migrations/000001_foundation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	engineID, accountID, revisionID, key := migrationID(), migrationID(), migrationID(), migrationID()
	a := domain.Account{ID: accountID, RevisionID: revisionID, Protocol: "xmpp", Address: "alice@localhost", Label: "Retained", CreatedAt: time.Now().UTC()}
	data := jsonText(a)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO engine_identity VALUES(1,?)", []any{engineID}},
		{"INSERT INTO accounts VALUES(?,?,'xmpp','alice@localhost','Retained',?)", []any{accountID, revisionID, stamp(a.CreatedAt)}},
		{"INSERT INTO revisions VALUES(?,?,'[]',?,?)", []any{revisionID, accountID, data, stamp(a.CreatedAt)}},
		{"INSERT INTO operations VALUES(?,'legacy-fingerprint',?,?,?)", []any{key, accountID, data, stamp(a.CreatedAt)}},
		{"INSERT INTO changes(payload,occurred_at) VALUES(?,?)", []any{data, stamp(a.CreatedAt)}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := Open(context.Background(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	gotID, seq, err := store.Snapshot(t.Context())
	if err != nil || gotID != engineID || seq != 1 {
		t.Fatalf("engine identity/history changed: %s %d %v", gotID, seq, err)
	}
	got, err := store.Account(t.Context(), accountID)
	if err != nil || got != a {
		t.Fatalf("account/revision changed: %+v %v", got, err)
	}
	op, err := store.Operation(t.Context(), key)
	if err != nil || op.Kind != "register_account" || op.ResourceID != accountID {
		t.Fatal("legacy receipt not adopted")
	}
	events, err := store.Events(t.Context(), 0, 100)
	if err != nil || len(events) != 1 || events[0].Type != "account.registered" || events[0].Account.ID != accountID {
		t.Fatal("legacy stream not adopted")
	}
	var version int
	var dirty bool
	if err = store.db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 7 || dirty {
		t.Fatalf("migrate journal: %d %v %v", version, dirty, err)
	}
}
func TestDirtyAndFutureMigrationsAreNotForced(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		dirty   bool
		want    string
	}{{"dirty", 2, true, "dirty"}, {"future", 8, false, "newer"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := Open(t.Context(), dir, migrationID())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec("UPDATE schema_migrations SET version=?,dirty=?", tc.version, tc.dirty); err != nil {
				t.Fatal(err)
			}
			_ = store.Close()
			next, err := Open(t.Context(), dir, migrationID())
			if next != nil {
				_ = next.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe migration repair: %v", err)
			}
		})
	}
}

func TestDataDirectoryHasOneEngineOwner(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), dir, migrationID())
	if second != nil {
		_ = second.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatal("two dispatchers can open one engine directory")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	// Repeated cleanup of the former owner must not release the new owner's lock.
	_ = first.Close()
	third, err := Open(t.Context(), dir, migrationID())
	if third != nil {
		_ = third.Close()
	}
	if err == nil {
		t.Fatal("former owner cleanup released another engine's lock")
	}
}
