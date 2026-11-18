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

func TestVerifyBackupAndRestoreRejectUnsupportedSchema(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"required table", "ALTER TABLE message_correction_facts ADD COLUMN unexpected TEXT"},
		{"optional table", "ALTER TABLE recovered_sends ADD COLUMN unexpected TEXT"},
		{"multiple versions", "INSERT INTO schema_migrations(version, dirty) VALUES (36, 0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _, _ := outboxFixture(t)
			ctx := t.Context()
			if _, err := source.db.ExecContext(ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if err := writeBackup(ctx, source.readDB, source.dir, &archive, false); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(t.TempDir(), "unexpected-schema.zip")
			if err := os.WriteFile(name, archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyBackup(ctx, name); err == nil {
				t.Error("verification accepted an unsupported schema")
			}
			destination := filepath.Join(t.TempDir(), "profile")
			if _, err := RestoreProfileBackup(ctx, destination, name, migrationID()); err == nil {
				t.Error("restore accepted an unsupported schema")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("restore initialized a destination for an unsupported archive: %v", err)
			}
		})
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
