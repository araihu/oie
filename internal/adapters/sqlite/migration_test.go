package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
)

func migrationID() domain.ID {
	value, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return domain.ID(value.String())
}

func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	scripts, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scripts.Close() }()
	version, err := lastMigrationVersion(scripts)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestLastMigrationVersionUsesEmbeddedSourceOrder(t *testing.T) {
	scripts, err := iofs.New(fstest.MapFS{
		"migrations/000001_first.up.sql": {},
		"migrations/000042_last.up.sql":  {},
	}, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scripts.Close() }()
	if version, err := lastMigrationVersion(scripts); err != nil || version != 42 {
		t.Fatalf("last migration: %d, %v", version, err)
	}
}

func migrateFixtureToVersion(t *testing.T, store *Store, version uint) {
	t.Helper()
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
	if err = runner.Migrate(version); err != nil {
		t.Fatal(err)
	}
}

func TestRoomModerationPreviewMigratesIntoSharedCatalog(t *testing.T) {
	ctx := t.Context()
	store, engine, chat := outboxFixture(t)
	engine.SetProtocols(&outboundProtocol{})
	const room = "garden@rooms.localhost"
	const stanzaID = "moderated"
	at := time.Now().UTC()
	for _, id := range []string{stanzaID, "visible"} {
		if err := engine.Receive(ctx, application.Incoming{
			AccountID: chat.AccountID, Kind: "room", Address: room, SenderName: "Guest",
			CreatedAt: at, ProviderID: id, OriginalText: "https://example.test/" + id + ".pdf",
			References: []domain.MessageReference{{Scope: "room:" + room, ID: id}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	migrateFixtureToVersion(t, store, 26)
	if _, err := store.db.ExecContext(ctx, `
		CREATE TABLE room_message_moderation (
			account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			room_address TEXT NOT NULL, stanza_id TEXT NOT NULL, moderated_at TEXT NOT NULL, reason TEXT NOT NULL,
			PRIMARY KEY(account_id,room_address,stanza_id)
		);
		PRAGMA user_version=27;
		UPDATE schema_migrations SET version=27;
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "INSERT INTO room_message_moderation VALUES(?,?,?,?,?)",
		chat.AccountID, room, stanzaID, stamp(at), "Room rules"); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(ctx, store.db, store.dir); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.SharedContentCandidates(ctx, application.SharedContentVersion, 10)
	if err != nil || len(candidates) != 1 || candidates[0].OriginalText != "https://example.test/visible.pdf" {
		t.Fatalf("catalog candidates after preview migration: %+v, %v", candidates, err)
	}
	rooms, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: chat.AccountID, Kind: "room", Limit: 10})
	if err != nil || len(rooms) != 1 {
		t.Fatalf("rooms after preview migration: %+v, %v", rooms, err)
	}
	messages, err := engine.Messages(ctx, rooms[0].ID, "", 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages after preview migration: %+v, %v", messages, err)
	}
	for _, message := range messages {
		if message.Moderation != nil && message.Moderation.Reason == "Room rules" && message.OriginalText == "" {
			return
		}
	}
	t.Fatal("preview moderation was not preserved")
}

func TestSchedulingPreviewMigratesWithoutDiscardingData(t *testing.T) {
	ctx := t.Context()
	store, engine, conversation := outboxFixture(t)
	when := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	const body = "Scheduled before merging main"
	result, err := engine.SendComposition(ctx, migrationID(), conversation.ID, domain.OutgoingComposition{OriginalText: body}, &when)
	if err != nil {
		t.Fatal(err)
	}
	const content = "retained attachment"
	file, _, err := store.StageAttachment(ctx, conversation.ID, domain.StagedAttachment{
		ID: migrationID(), FileName: "note.txt", MediaType: "text/plain",
	}, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"000031_retention_recovery", "000030_retention", "000029_room_ban_nicknames", "000028_room_message_moderation", "000027_shared_catalog_projection", "000026_room_invitation_credentials", "000025_room_creation", "000024_reaction_target_lookups", "000023_dispatch_wire_stage", "000022_scheduled_timeline", "000020_room_reaction_retained_identity", "000019_room_reaction_identity_mode", "000018_room_reaction_reconciliation"} {
		down, err := migrations.ReadFile("migrations/" + name + ".down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.ExecContext(ctx, string(down)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.db.ExecContext(ctx, "UPDATE schema_migrations SET version=18"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, store.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()
	items, err := upgraded.ScheduledMessages(ctx, application.ScheduledQuery{Limit: 10})
	if err != nil || len(items) != 1 || items[0].Message.ID != result.Message.ID || items[0].Message.OriginalText != body ||
		items[0].Message.ScheduledAt == nil || !items[0].Message.ScheduledAt.Equal(when) {
		t.Fatalf("schedule changed during migration: %+v %v", items, err)
	}
	retained, reader, err := upgraded.OpenStagedAttachment(ctx, conversation.ID, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil || retained != file || string(data) != content {
		t.Fatalf("staged attachment changed during migration: %+v %v", retained, err)
	}
	var version int
	var dirty bool
	if err = upgraded.db.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != latestMigrationVersion(t) || dirty {
		t.Fatalf("migrate journal: %d %v %v", version, dirty, err)
	}
}

func TestReactionLookupPreviewMigratesWithoutDiscardingData(t *testing.T) {
	ctx := t.Context()
	store, engine, conversation := outboxFixture(t)
	engine.SetProtocols(&reactionProtocol{max: 8})
	message := ownSubmittedEdit(t, store, engine, conversation, "Retained preview message")
	observeReaction(t, engine, conversation, domain.MessageReference{ID: message.ProviderID}, "bob@localhost", "retained-reaction", time.Now(), "👍")
	before, err := store.Message(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	migrateFixtureToVersion(t, store, 20)
	if _, err := store.db.ExecContext(ctx, `
		CREATE INDEX reaction_command_message ON message_reaction_commands(message_id);
		CREATE INDEX reaction_observation_command ON message_reaction_observations(command_id) WHERE command_id<>'';
		PRAGMA user_version=21;
		UPDATE schema_migrations SET version=21;
	`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, store.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()
	retained, err := upgraded.Message(ctx, message.ID)
	if err != nil || retained.OriginalText != before.OriginalText || retained.RevisionID != before.RevisionID {
		t.Fatalf("message changed during upgrade: %+v %v", retained, err)
	}
	reactions, err := upgraded.ReadMessageReactions(ctx, message.ID)
	if err != nil || len(reactions.Snapshot.Items) != 1 || reactions.Snapshot.Items[0].Emoji != "👍" {
		t.Fatalf("reaction changed during upgrade: %+v %v", reactions.Snapshot, err)
	}
	if scheduled, err := upgraded.ScheduledMessages(ctx, application.ScheduledQuery{Limit: 10}); err != nil || len(scheduled) != 0 {
		t.Fatalf("scheduling schema unavailable after upgrade: %+v %v", scheduled, err)
	}
	var version int
	var dirty bool
	if err = upgraded.db.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != latestMigrationVersion(t) || dirty {
		t.Fatalf("migrate journal: %d %v %v", version, dirty, err)
	}
}

func TestConversationRequestPreviewMigratesWithoutDiscardingCredentials(t *testing.T) {
	ctx := t.Context()
	store, _, conversation := outboxFixture(t)
	migrateFixtureToVersion(t, store, 24)
	migration, err := migrations.ReadFile("migrations/000026_room_invitation_credentials.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, "PRAGMA user_version=24; UPDATE schema_migrations SET version=25"); err != nil {
		t.Fatal(err)
	}
	room := domain.Conversation{
		ID: migrationID(), RevisionID: migrationID(), PeerID: migrationID(),
		AccountID: conversation.AccountID, Kind: "room", Address: "private@conference.localhost",
		Title: "Private room", UpdatedAt: time.Now().UTC(), Request: domain.RoomInvitation,
	}
	const password = "fixture-password"
	if err = store.StoreRoomInvitation(ctx, room, password); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, store.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()
	if got, err := upgraded.RoomInvitationCredential(ctx, room.ID); err != nil || got != password {
		t.Fatal("invitation credential was not retained", err)
	}
	if got, err := upgraded.Conversation(ctx, room.ID); err != nil || got.Request != domain.RoomInvitation {
		t.Fatal("invitation was not retained", err)
	}
	var creationColumn bool
	if err := upgraded.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info('room_intents') WHERE name='creation_name')").Scan(&creationColumn); err != nil || !creationColumn {
		t.Fatal("room creation migration was not applied", err)
	}
	var version int
	var dirty bool
	if err = upgraded.db.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != latestMigrationVersion(t) || dirty {
		t.Fatalf("migrate journal: %d %v %v", version, dirty, err)
	}
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
	if err = store.db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != latestMigrationVersion(t) || dirty {
		t.Fatalf("migrate journal: %d %v %v", version, dirty, err)
	}
}
func TestDirtyAndFutureMigrationsAreNotForced(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		dirty   bool
		want    string
	}{
		{"dirty", 2, true, "dirty"},
		{"dirty before first migration", -1, true, "dirty"},
		{"invalid negative version", -1, false, "invalid"},
		{"unknown version", 0, false, "absent"},
		{"future", latestMigrationVersion(t) + 1, false, "newer"},
	} {
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
			if _, err := os.Stat(filepath.Join(dir, migrationBackupsDir)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected journal triggered a backup: %v", err)
			}
		})
	}
}

func TestMigrationRejectsJournalWithMultipleVersions(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := Open(ctx, dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.db.ExecContext(ctx, "INSERT INTO schema_migrations(version,dirty) VALUES(1, true)"); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(ctx, store.db, dir); err == nil || !strings.Contains(err.Error(), "multiple versions") {
		t.Fatalf("startup should reject a journal with multiple versions: %v", err)
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
