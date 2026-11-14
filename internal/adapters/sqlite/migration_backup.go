package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	sqlitedriver "modernc.org/sqlite"
)

const (
	migrationBackupsDir = "private/migration-backups"
	migrationStagingDir = ".pending"
)

func backupBeforeMigration(ctx context.Context, db *sql.DB, dir string, version int) error {
	backupDir := filepath.Join(dir, migrationBackupsDir)
	privateDir := filepath.Dir(backupDir)
	for _, path := range []string{privateDir, backupDir} {
		if err := ensureBackupDirectory(path); err != nil {
			return err
		}
	}
	// The engine lock excludes other backups. Discard files from an interrupted copy.
	staging := filepath.Join(backupDir, migrationStagingDir)
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.Mkdir(staging, 0700); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	pattern := fmt.Sprintf("oie-before-v%d-%s-*.sqlite", version, time.Now().UTC().Format("20060102T150405.000000000Z"))
	file, err := os.CreateTemp(staging, pattern)
	if err != nil {
		return err
	}

	err = copySQLiteBackup(ctx, db, file.Name())
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(backupDir, filepath.Base(file.Name()))); err != nil {
		return err
	}
	// Persist the published filename and its containing directory before migrating.
	for _, path := range []string{backupDir, privateDir, dir} {
		directory, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
			return err
		}
	}
	return nil
}

func ensureBackupDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("backup path %s must be a directory", path)
	}
	return nil
}

func copySQLiteBackup(ctx context.Context, db *sql.DB, path string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	destination := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw"}
	return conn.Raw(func(driverConn any) (err error) {
		source, ok := driverConn.(interface {
			NewBackup(string) (*sqlitedriver.Backup, error)
		})
		if !ok {
			return errors.New("SQLite driver does not support backups")
		}
		backup, err := source.NewBackup(destination.String())
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, backup.Finish()) }()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(256)
			if err != nil || !more {
				return err
			}
		}
	})
}
