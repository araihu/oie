package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	sqlitedriver "modernc.org/sqlite"
)

//go:embed migrations/001_foundation.sql
var migration string

type Store struct{ db *sql.DB }

func Open(ctx context.Context, dir string, initialID domain.ID) (_ *Store, err error) {
	if _, err = domain.ParseID(string(initialID)); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(dir, "oie.sqlite"))
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version > 1 {
		return nil, fmt.Errorf("database schema %d is newer than supported version 1", version)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if version == 0 {
		if _, err = tx.ExecContext(ctx, migration); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO engine_identity(singleton,id) VALUES(1,?) ON CONFLICT(singleton) DO NOTHING", initialID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(dir, "media"), 0700); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) Snapshot(ctx context.Context) (domain.ID, int64, error) {
	var id domain.ID
	var seq int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id,(SELECT COALESCE(MAX(sequence),0) FROM changes) FROM engine_identity WHERE singleton=1").Scan(&id, &seq)
	return id, seq, err
}

func (s *Store) Register(ctx context.Context, in application.Registration) (application.RegistrationResult, error) {
	data, err := json.Marshal(in.Account)
	if err != nil {
		return application.RegistrationResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return application.RegistrationResult{}, err
	}
	defer tx.Rollback()
	// Acquire the write lock before any reads: concurrent connections must see
	// the committed receipt, not race a read-to-write transaction upgrade.
	result, err := tx.ExecContext(ctx,
		"INSERT INTO operations(id,fingerprint,resource_id,result,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING",
		in.Operation.ID, in.Fingerprint, in.Account.ID, string(data), stamp(in.Operation.CreatedAt))
	if err != nil {
		return application.RegistrationResult{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return application.RegistrationResult{}, err
	}
	if changed == 0 {
		var fingerprint, original string
		if err = tx.QueryRowContext(ctx, "SELECT fingerprint,result FROM operations WHERE id=?", in.Operation.ID).Scan(&fingerprint, &original); err != nil {
			return application.RegistrationResult{}, err
		}
		if fingerprint != in.Fingerprint {
			return application.RegistrationResult{}, application.ErrIdempotencyConflict
		}
		var account domain.Account
		if err = json.Unmarshal([]byte(original), &account); err != nil {
			return application.RegistrationResult{}, err
		}
		return application.RegistrationResult{Account: account, Replayed: true}, nil
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO accounts(id,revision_id,protocol,address,label,created_at) VALUES(?,?,?,?,?,?)",
		in.Account.ID, in.Account.RevisionID, in.Account.Protocol, in.Account.Address, in.Account.Label, stamp(in.Account.CreatedAt))
	if err != nil {
		var dbErr *sqlitedriver.Error
		if errors.As(err, &dbErr) && dbErr.Code() == 2067 {
			return application.RegistrationResult{}, application.ErrAccountExists
		}
		return application.RegistrationResult{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,'[]',?,?)",
		in.Account.RevisionID, in.Account.ID, string(data), stamp(in.Account.CreatedAt))
	if err != nil {
		return application.RegistrationResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO changes(payload,occurred_at) VALUES(?,?)", string(data), stamp(in.Account.CreatedAt)); err != nil {
		return application.RegistrationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.RegistrationResult{}, err
	}
	return application.RegistrationResult{Account: in.Account}, nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (s *Store) Account(ctx context.Context, id domain.ID) (domain.Account, error) {
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM revisions WHERE id=(SELECT revision_id FROM accounts WHERE id=?)", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Account{}, err
	}
	var account domain.Account
	err = json.Unmarshal([]byte(data), &account)
	return account, err
}

func (s *Store) Accounts(ctx context.Context, after domain.ID, limit int) ([]domain.Account, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT r.payload FROM accounts a JOIN revisions r ON a.revision_id=r.id WHERE a.id>? ORDER BY a.id LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Account, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var account domain.Account
		if err := json.Unmarshal([]byte(data), &account); err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

func (s *Store) Operation(ctx context.Context, id domain.ID) (application.Operation, error) {
	var op application.Operation
	var created string
	err := s.db.QueryRowContext(ctx, "SELECT id,resource_id,created_at FROM operations WHERE id=?", id).
		Scan(&op.ID, &op.ResourceID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return op, application.ErrNotFound
	}
	if err != nil {
		return op, err
	}
	op.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return op, err
}

func (s *Store) Events(ctx context.Context, after int64, limit int) ([]application.Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT sequence,payload,occurred_at FROM changes WHERE sequence>? ORDER BY sequence LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]application.Event, 0)
	for rows.Next() {
		var e application.Event
		var data, created string
		if err := rows.Scan(&e.Sequence, &data, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &e.Account); err != nil {
			return nil, err
		}
		e.OccurredAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
