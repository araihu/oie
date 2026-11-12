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

const schedulingMigration = 21
const latestMigration = 23

func migrateDatabase(ctx context.Context, db *sql.DB) error {
	// Catalog introspection is bootstrap housekeeping, not application queries.
	var legacy, tables int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&legacy); err != nil {
		return err
	}
	if legacy > latestMigration {
		return fmt.Errorf("database schema %d is newer than supported version %d", legacy, latestMigration)
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
	if err == nil && version > latestMigration {
		return fmt.Errorf("database migration %d is newer than supported version %d", version, latestMigration)
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
	if version >= 18 && version < schedulingMigration {
		if err := adoptSchedulingPreview(ctx, db, runner, version, legacy); err != nil {
			return err
		}
	}
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// The scheduling preview used version 18 before main assigned it to reactions.
// Apply the missing reaction migration and keep the already installed schedule schema.
func adoptSchedulingPreview(ctx context.Context, db *sql.DB, runner *migrate.Migrate, version uint, legacy int) error {
	var scheduled, roomPrivacy bool
	if err := db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM pragma_table_info('outbox') WHERE name = 'scheduled_at')
		AND EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'staged_attachments')
		AND EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'index' AND name = 'outbox_schedule'),
		EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'room_reaction_privacy')`).Scan(&scheduled, &roomPrivacy); err != nil {
		return err
	}
	if !scheduled {
		return nil
	}
	if !roomPrivacy {
		if version != 18 || legacy != 17 {
			return errors.New("unrecognized scheduling preview schema")
		}
		migration, err := migrations.ReadFile("migrations/000018_room_reaction_reconciliation.up.sql")
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, string(migration)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if err := runner.Migrate(20); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	// All reaction migrations have run and the preview had already applied the
	// exact scheduling schema now registered as version 21.
	return runner.Force(schedulingMigration)
}
