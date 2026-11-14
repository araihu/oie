package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrations embed.FS

const schedulingMigration = 21

func migrateDatabase(ctx context.Context, db *sql.DB, dir string) error {
	scripts, err := iofs.New(migrations, "migrations")
	if err != nil {
		return err
	}
	defer func() { _ = scripts.Close() }()
	latestMigration, err := lastMigrationVersion(scripts)
	if err != nil {
		return err
	}

	var legacy int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&legacy); err != nil {
		return err
	}
	if legacy > latestMigration {
		return fmt.Errorf("database schema %d is newer than supported version %d", legacy, latestMigration)
	}
	// WithInstance creates the migration journal. Inspect the database before
	// opening the migration driver so the backup includes the original schema.
	var hasJournal, hasSchema bool
	if err := db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='schema_migrations'),
		EXISTS(SELECT 1 FROM sqlite_schema WHERE tbl_name<>'schema_migrations' AND name NOT GLOB 'sqlite_*')`).Scan(&hasJournal, &hasSchema); err != nil {
		return err
	}
	var version int
	var tracked, dirty bool
	if hasJournal {
		var entries int
		err := db.QueryRowContext(ctx, `SELECT version, dirty,
			(SELECT COUNT(*) FROM schema_migrations) FROM schema_migrations LIMIT 1`).Scan(&version, &dirty, &entries)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if entries > 1 {
			return errors.New("database migration journal has multiple versions")
		}
		tracked = err == nil
	}
	if dirty {
		return fmt.Errorf("database migration %d is dirty; repair explicitly before starting", version)
	}
	if version < 0 {
		return fmt.Errorf("invalid database migration version %d", version)
	}
	if version > latestMigration {
		return fmt.Errorf("database migration %d is newer than supported version %d", version, latestMigration)
	}
	if tracked && version == latestMigration {
		return nil
	}
	if tracked {
		if _, err := scripts.Next(uint(version)); errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("database migration version %d is absent from the embedded migrations", version)
		} else if err != nil {
			return err
		}
	}
	if !tracked && legacy == 1 {
		var tables int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('accounts','revisions','operations','changes','engine_identity')").Scan(&tables); err != nil {
			return err
		}
		if tables != 5 {
			return errors.New("unrecognized legacy database; refusing migration baseline")
		}
	} else if !tracked && legacy != 0 {
		return errors.New("untracked non-legacy database; refusing migration baseline")
	}
	if tracked || hasSchema {
		backupVersion := version
		if !tracked {
			backupVersion = legacy
		}
		if err := backupBeforeMigration(ctx, db, dir, backupVersion); err != nil {
			return fmt.Errorf("back up database before migration: %w", err)
		}
	}

	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		return err
	}
	runner, err := migrate.NewWithInstance("iofs", scripts, "sqlite", driver)
	if err != nil {
		return err
	}
	if !tracked && legacy == 1 {
		// The only automatic baseline is the shipped, pre-migrate foundation v1.
		if err := runner.Force(1); err != nil {
			return err
		}
	}
	if version >= 18 && version < schedulingMigration {
		if err := adoptSchedulingPreview(ctx, db, runner, version, legacy); err != nil {
			return err
		}
	}
	if version == schedulingMigration && legacy == schedulingMigration {
		if err := adoptReactionLookupPreview(ctx, db); err != nil {
			return err
		}
	}
	if version == 25 {
		if err := adoptConversationRequestPreview(ctx, db); err != nil {
			return err
		}
	}
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func lastMigrationVersion(scripts source.Driver) (int, error) {
	version, err := scripts.First()
	if err != nil {
		return 0, err
	}
	for {
		next, err := scripts.Next(version)
		if errors.Is(err, fs.ErrNotExist) {
			return int(version), nil
		}
		if err != nil {
			return 0, err
		}
		version = next
	}
}

// The request preview used version 25 for invitation credentials. Install the
// room-creation schema now assigned to that version before continuing to 26.
func adoptConversationRequestPreview(ctx context.Context, db *sql.DB) error {
	var preview bool
	if err := db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'room_invitation_credentials')
		AND NOT EXISTS(SELECT 1 FROM pragma_table_info('room_intents') WHERE name = 'creation_name')`).Scan(&preview); err != nil {
		return err
	}
	if !preview {
		return nil
	}
	migration, err := migrations.ReadFile("migrations/000025_room_creation.up.sql")
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
	return tx.Commit()
}

// The reaction lookup preview registered version 21 with only the two indexes.
// Complete the scheduling schema transactionally before running version 22.
func adoptReactionLookupPreview(ctx context.Context, db *sql.DB) error {
	var preview bool
	if err := db.QueryRowContext(ctx, `SELECT
		NOT EXISTS(SELECT 1 FROM pragma_table_info('outbox') WHERE name = 'scheduled_at')
		AND NOT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'staged_attachments')
		AND EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'index' AND name = 'reaction_command_message' AND tbl_name = 'message_reaction_commands')
		AND EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'index' AND name = 'reaction_observation_command' AND tbl_name = 'message_reaction_observations')`).Scan(&preview); err != nil {
		return err
	}
	if !preview {
		return nil
	}
	migration, err := migrations.ReadFile("migrations/000021_scheduled_sends.up.sql")
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
	return tx.Commit()
}

// The scheduling preview used version 18 before main assigned it to reactions.
// Apply the missing reaction migration and keep the already installed schedule schema.
func adoptSchedulingPreview(ctx context.Context, db *sql.DB, runner *migrate.Migrate, version, legacy int) error {
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
