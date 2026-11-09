package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrations embed.FS

func migrateDatabase(ctx context.Context, db *sql.DB) error {
	// Catalog introspection is bootstrap housekeeping, not application queries.
	var legacy, tables int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&legacy); err != nil {
		return err
	}
	if legacy > 13 {
		return fmt.Errorf("database schema %d is newer than supported version 13", legacy)
	}
	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		return err
	}
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return err
	}
	version, dirty, err := runner.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return err
	}
	if dirty {
		return fmt.Errorf("database migration %d is dirty; repair explicitly before starting", version)
	}
	if err == nil && version > 13 {
		return fmt.Errorf("database migration %d is newer than supported version 13", version)
	}
	if errors.Is(err, migrate.ErrNilVersion) && legacy == 1 {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('accounts','revisions','operations','changes','engine_identity')").Scan(&tables); err != nil {
			return err
		}
		if tables != 5 {
			return errors.New("unrecognized legacy database; refusing migration baseline")
		}
		// The only automatic baseline is the shipped, pre-migrate foundation v1.
		if err := runner.Force(1); err != nil {
			return err
		}
	} else if errors.Is(err, migrate.ErrNilVersion) && legacy != 0 {
		return errors.New("untracked non-legacy database; refusing migration baseline")
	}
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
