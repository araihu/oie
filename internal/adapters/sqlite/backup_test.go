package sqlite

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

func TestManualBackupCapturesWALAndOptionalMediaWithoutCredentials(t *testing.T) {
	store, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	replySeed(t, store, chat, "retained message", now, nil, nil)
	room := domain.Conversation{
		ID: migrationID(), RevisionID: migrationID(), PeerID: migrationID(),
		AccountID: chat.AccountID, Address: "backup@conference.localhost",
		Title: "Backup", Kind: "room", Request: "room_invitation", UpdatedAt: now,
	}
	const password = "private-room-password-fixture"
	if err := store.StoreRoomInvitation(ctx, room, password); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(store.dir, "private", "connections")
	if err := os.MkdirAll(credentials, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(credentials, "fixture"), []byte("xmpp-secret-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.StageAttachment(ctx, chat.ID,
		domain.StagedAttachment{ID: migrationID(), FileName: "fixture.txt", MediaType: "text/plain"},
		bytes.NewBufferString("backup test attachment"),
	)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Hour)
	_, err = engine.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{
		OriginalText: "pending fixture",
	}, &at)
	if err != nil {
		t.Fatal(err)
	}

	for _, includeMedia := range []bool{false, true} {
		name := filepath.Join(t.TempDir(), "backup.zip")
		var data bytes.Buffer
		if err := writeBackup(ctx, store.readDB, store.dir, &data, includeMedia); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		summary, err := VerifyBackup(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		wantFiles := 0
		if includeMedia {
			wantFiles = 1
		}
		if summary.Version != backupFormat || summary.IncludesMedia != includeMedia ||
			summary.Accounts != 1 || summary.Conversations != 2 || summary.Messages != 2 ||
			summary.PendingSends != 1 || summary.MediaFiles != wantFiles {
			t.Fatalf("unexpected backup summary: %+v", summary)
		}

		scratch := t.TempDir()
		if _, err := readBackup(ctx, name, scratch); err != nil {
			t.Fatal(err)
		}
		database, err := os.ReadFile(filepath.Join(scratch, backupDatabaseName))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(database, []byte(password)) {
			t.Fatal("password bytes survived in the archived SQLite file")
		}
		if _, err := os.Stat(filepath.Join(scratch, "private")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("backup contains private directory: %v", err)
		}
	}
	if current, err := store.RoomInvitationCredential(ctx, room.ID); current != password || err != nil {
		t.Fatalf("export changed the live room credentials: %q, %v", current, err)
	}
}

func TestVerifyBackupRejectsCorruptionAndUnsafePaths(t *testing.T) {
	for _, name := range []string{backupDatabaseName, "../outside"} {
		t.Run(name, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "invalid.zip"))
			if err != nil {
				t.Fatal(err)
			}
			archive := zip.NewWriter(file)
			part, err := archive.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte("wrong")); err != nil {
				t.Fatal(err)
			}
			manifest, err := archive.Create(backupManifestName)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte("other"))
			err = json.NewEncoder(manifest).Encode(backupManifest{
				Version: backupFormat, CreatedAt: time.Now().UTC(), SourceEngine: migrationID(),
				IncludesMedia: true,
				Files:         []backupFile{{Name: name, Size: 5, SHA256: hex.EncodeToString(hash[:])}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyBackup(t.Context(), file.Name()); err == nil {
				t.Fatal("verification accepted invalid archive")
			}
		})
	}
}

func TestVerifyBackupAndRestoreRejectInvalidDatabase(t *testing.T) {
	source, _, chat := outboxFixture(t)
	room := domain.Conversation{
		ID: migrationID(), RevisionID: migrationID(), PeerID: migrationID(),
		AccountID: chat.AccountID, Address: "validation@conference.localhost",
		Title: "Validation", Kind: "room", Request: "room_invitation", UpdatedAt: time.Now().UTC(),
	}
	if err := source.StoreRoomInvitation(t.Context(), room, "fixture"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{name: "required table", sql: "ALTER TABLE message_correction_facts ADD COLUMN unexpected TEXT"},
		{name: "optional table", sql: "ALTER TABLE recovered_sends ADD COLUMN unexpected TEXT"},
		{name: "mismatched user version", sql: "PRAGMA user_version=99999"},
		{name: "multiple versions", sql: "INSERT INTO schema_migrations(version, dirty) VALUES (36, 0)"},
		{name: "wrong declared schema", sql: "UPDATE schema_migrations SET version=?", args: []any{firstBackupV1Schema}},
		{name: "missing credentials table", sql: "DROP TABLE room_invitation_credentials"},
		{name: "stored room password", sql: "INSERT INTO room_invitation_credentials(conversation_id,password) VALUES (?,'fixture')", args: []any{room.ID}},
		{name: "missing account deletions", sql: "DROP TABLE deleted_account_ids"},
		{name: "missing retired sends", sql: "DROP TABLE retired_recovered_sends"},
		{name: "missing recovered queue", sql: "DROP TABLE recovered_send_operations; DROP TABLE discarded_recovered_sends; DROP TABLE recovered_sends"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			manifest, database, _ := backupRestoreFixture(t, source, false)
			db, err := openBackupDatabase(database, "rw")
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.ExecContext(ctx, tc.sql, tc.args...)
			if err := errors.Join(err, db.Close()); err != nil {
				t.Fatal(err)
			}
			name := repackBackupFixture(t, manifest, database)
			if _, err := VerifyBackup(ctx, name); err == nil {
				t.Error("verification accepted an invalid database")
			}
			destination := filepath.Join(t.TempDir(), "profile")
			if _, err := RestoreProfileBackup(ctx, destination, name, migrationID()); err == nil {
				t.Error("restore accepted an invalid database")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("restore initialized a destination for an unsupported archive: %v", err)
			}
		})
	}
}

func repackBackupFixture(t *testing.T, manifest backupManifest, database string) string {
	t.Helper()
	var archive bytes.Buffer
	zipFile := zip.NewWriter(&archive)
	entry, err := appendBackupFile(t.Context(), zipFile, backupDatabaseName, database, "", -1)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Files = []backupFile{entry}
	header, err := zipFile.Create(backupManifestName)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(header).Encode(manifest); err != nil {
		t.Fatal(err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "fixture.zip")
	if err := os.WriteFile(name, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestRestoreBackupFromBeforeRoomEchoRepair(t *testing.T) {
	source, _, chat := outboxFixture(t)
	original := replySeed(t, source, chat, "retained in original profile", time.Now().UTC(), nil, nil)
	manifest, database, _ := backupRestoreFixture(t, source, false)
	db, err := openBackupDatabase(database, "rw")
	if err != nil {
		t.Fatal(err)
	}
	migrateFixtureToVersion(t, &Store{db: db}, firstBackupV1Schema)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	archive := repackBackupFixture(t, manifest, database)
	if _, err := VerifyBackup(t.Context(), archive); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := RestoreProfileBackup(t.Context(), dir, archive, migrationID()); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	m, err := restored.Message(t.Context(), original.ID)
	if err != nil || m.OriginalText != original.OriginalText {
		t.Fatalf("lost original from legacy backup: %v, %v", m.ID, err)
	}
}

func TestManualBackupHonorsCancellation(t *testing.T) {
	store, _, _ := outboxFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var dst bytes.Buffer
	if err := writeBackup(ctx, store.readDB, store.dir, &dst, false); !errors.Is(err, context.Canceled) || dst.Len() != 0 {
		t.Fatalf("cancelled backup wrote %d bytes and returned %v", dst.Len(), err)
	}
}

func TestManualBackupUpgradesOnlyThePrivateSnapshot(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	replySeed(t, source, chat, "history from an older profile", time.Now().UTC(), nil, nil)
	migrateFixtureToVersion(t, source, 35)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	var version int
	if err := source.readDB.QueryRowContext(ctx, "SELECT version FROM schema_migrations").Scan(&version); err != nil || version != 35 {
		t.Fatalf("backup changed the live source schema: %d %v", version, err)
	}
	if err := checkRestoreDatabase(ctx, archive); err != nil {
		t.Fatal(err)
	}
	destination, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = destination.Close() }()
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, time.Now().UTC()); err != nil || result.Messages != 1 {
		t.Fatalf("the updated backup could not be restored: %+v %v", result, err)
	}
}
