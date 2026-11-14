package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlitedriver "modernc.org/sqlite"
)

const migrationBackupsDir = "migration-backups"

func backupBeforeMigration(ctx context.Context, db *sql.DB, dir string, version int) error {
	backupDir := filepath.Join(dir, migrationBackupsDir)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}
	pattern := fmt.Sprintf(".oie-before-v%d-%s-*.partial", version, time.Now().UTC().Format("20060102T150405.000000000Z"))
	file, err := os.CreateTemp(backupDir, pattern)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()

	err = copySQLiteBackup(ctx, db, file.Name())
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file.Name()), "."), ".partial") + ".sqlite"
	if err := os.Rename(file.Name(), filepath.Join(backupDir, name)); err != nil {
		return err
	}
	// Persist the published filename and its containing directory before migrating.
	for _, path := range []string{backupDir, dir} {
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
