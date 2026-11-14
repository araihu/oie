package sqlite

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationBackupIncludesUncheckpointedWAL(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Keep this connection open so startup must read the committed WAL frames.
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	foundation, err := migrations.ReadFile("migrations/000001_foundation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(foundation)); err != nil {
		t.Fatal(err)
	}
	id := migrationID()
	if _, err = db.ExecContext(ctx, "INSERT INTO engine_identity(singleton,id) VALUES(1,?)", id); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "oie.sqlite-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("fixture has no WAL frames: %v", err)
	}
	store, err := Open(ctx, dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	backupDir := filepath.Join(dir, migrationBackupsDir)
	files, err := os.ReadDir(backupDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one backup, got %v: %v", files, err)
	}
	if !strings.HasPrefix(files[0].Name(), "oie-before-v1-") || filepath.Ext(files[0].Name()) != ".sqlite" {
		t.Fatalf("unexpected backup name: %s", files[0].Name())
	}
	info, err := files[0].Info()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup is not private: %v, %v", info, err)
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(backupDir, files[0].Name()), RawQuery: "mode=ro&immutable=1"}
	backup, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backup.Close() }()
	var backupID string
	if err := backup.QueryRowContext(ctx, "SELECT id FROM engine_identity").Scan(&backupID); err != nil || backupID != string(id) {
		t.Fatalf("backup did not retain engine identity: %q, %v", backupID, err)
	}
	var version int
	var hasJournal bool
	if err := backup.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("backup has unexpected schema version: %d, %v", version, err)
	}
	if err := backup.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='schema_migrations')").Scan(&hasJournal); err != nil || hasJournal {
		t.Fatalf("backup should precede creation of migration journal: %v, %v", hasJournal, err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT version FROM schema_migrations").Scan(&version); err != nil || version != latestMigrationVersion(t) {
		t.Fatalf("live database did not migrate: %d, %v", version, err)
	}
}

func TestMigrationBackupOnlyCreatedForUpgrade(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	id := migrationID()
	backupDir := filepath.Join(dir, migrationBackupsDir)
	store, err := Open(ctx, dir, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := os.Stat(backupDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh profile created a backup directory: %v", err)
	}
	migrateFixtureToVersion(t, store, 26)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(backupDir, migrationStagingDir)
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"interrupted.sqlite", "interrupted.sqlite-journal"} {
		if err := os.WriteFile(filepath.Join(staging, name), []byte("partial copy"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		reopened, err := Open(ctx, dir, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
		files, err := os.ReadDir(backupDir)
		if err != nil || len(files) != 1 {
			t.Fatalf("expected one backup across both startups, got %v: %v", files, err)
		}
	}
	reopened, err := Open(ctx, dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err := reopened.Reset(ctx, migrationID()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile reset retained migration backups: %v", err)
	}
}

func TestMigrationBackupFailureStopsUpgrade(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := Open(ctx, dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	migrateFixtureToVersion(t, store, 26)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// A regular file blocks creation of the backup directory on any host.
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, migrationBackupsDir)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, migrationBackupsDir), nil, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir, migrationID())
	if reopened != nil {
		_ = reopened.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "back up database before migration") {
		t.Fatalf("startup should fail at backup: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 26 || dirty {
		t.Fatalf("startup modified the migration journal: %d, %v, %v", version, dirty, err)
	}
	var catalogExists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='shared_catalog_sources')").Scan(&catalogExists); err != nil || catalogExists {
		t.Fatalf("startup modified the schema: %v, %v", catalogExists, err)
	}
}

func TestMigrationBackupRejectsSymlinkDestination(t *testing.T) {
	for _, name := range []string{"private", migrationBackupsDir} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			store, err := Open(ctx, dir, migrationID())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			migrateFixtureToVersion(t, store, 26)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			link := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, dir, migrationID())
			if reopened != nil {
				_ = reopened.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "must be a directory") {
				t.Fatalf("startup should reject symlink destination: %v", err)
			}
			files, err := os.ReadDir(target)
			if err != nil || len(files) != 0 {
				t.Fatalf("startup modified the external directory: %v, %v", files, err)
			}
		})
	}
}

func TestMigrationFailureRetainsBackup(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := Open(ctx, dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	migrateFixtureToVersion(t, store, 26)
	// The conflicting table makes migration 27 fail at its first statement.
	if _, err := store.db.ExecContext(ctx, "CREATE TABLE shared_catalog_sources (sentinel TEXT); INSERT INTO shared_catalog_sources VALUES ('before upgrade')"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir, migrationID())
	if reopened != nil {
		_ = reopened.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected migration failure: %v", err)
	}
	backupDir := filepath.Join(dir, migrationBackupsDir)
	files, err := os.ReadDir(backupDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("expected a retained backup, got %v: %v", files, err)
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(backupDir, files[0].Name()), RawQuery: "mode=ro&immutable=1"}
	backup, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backup.Close() }()
	var version int
	var dirty bool
	if err := backup.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 26 || dirty {
		t.Fatalf("backup contains an attempted migration: %d, %v, %v", version, dirty, err)
	}
	var sentinel string
	if err := backup.QueryRowContext(ctx, "SELECT sentinel FROM shared_catalog_sources").Scan(&sentinel); err != nil || sentinel != "before upgrade" {
		t.Fatalf("backup did not preserve fixture data: %q, %v", sentinel, err)
	}
}
