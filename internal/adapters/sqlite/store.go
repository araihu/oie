package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/gofrs/flock"
	sqlitedriver "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	queries   *dbgen.Queries
	lock      *flock.Flock
	closeOnce sync.Once
	closeErr  error
}

func Open(ctx context.Context, dir string, initialID domain.ID) (_ *Store, err error) {
	if _, err = domain.ParseID(string(initialID)); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	owner := flock.New(filepath.Join(dir, "engine.lock"))
	locked, err := owner.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("engine data directory already in use")
	}
	defer func() {
		if err != nil {
			_ = owner.Close()
		}
	}()
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
	if err = migrateDatabase(ctx, db); err != nil {
		return nil, err
	}
	queries := dbgen.New(db)
	if err = queries.InsertEngineIdentity(ctx, string(initialID)); err != nil {
		return nil, err
	}
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(dir, "media"), 0700); err != nil {
		return nil, err
	}
	return &Store{db: db, queries: queries, lock: owner}, nil
}
func (s *Store) Close() error {
	s.closeOnce.Do(func() { s.closeErr = errors.Join(s.db.Close(), s.lock.Close()) })
	return s.closeErr
}
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Snapshot(ctx context.Context) (domain.ID, int64, error) {
	row, err := s.queries.Snapshot(ctx)
	return domain.ID(row.ID), row.Sequence, err
}
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func jsonText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
} // Only concrete domain structs reach this helper.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return application.ErrNotFound
	}
	return err
}
func begin(ctx context.Context, s *Store) (*sql.Tx, *dbgen.Queries, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	return tx, s.queries.WithTx(tx), nil
}
func operation(ctx context.Context, q *dbgen.Queries, op application.Operation, hash string, value any) (*dbgen.Operation, error) {
	changed, err := q.InsertOperation(ctx, dbgen.InsertOperationParams{ID: string(op.ID), Fingerprint: hash, ResourceID: string(op.ResourceID), Result: jsonText(value), CreatedAt: stamp(op.CreatedAt), Kind: op.Kind})
	if err != nil {
		return nil, err
	}
	if changed != 0 {
		return nil, nil
	}
	existing, err := q.GetOperation(ctx, string(op.ID))
	if err != nil {
		return nil, err
	}
	if existing.Fingerprint != hash || existing.Kind != op.Kind {
		return nil, application.ErrIdempotencyConflict
	}
	return &existing, nil
}
func change(ctx context.Context, q *dbgen.Queries, kind string, value any, at time.Time) (int64, error) {
	return q.InsertChange(ctx, dbgen.InsertChangeParams{Payload: jsonText(value), OccurredAt: stamp(at), Kind: kind})
}
func (s *Store) Register(ctx context.Context, in application.Registration) (application.RegistrationResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.RegistrationResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	in.Operation.Kind = "register_account"
	existing, err := operation(ctx, q, in.Operation, in.Fingerprint, in.Account)
	if err != nil {
		return application.RegistrationResult{}, err
	}
	if existing != nil {
		var a domain.Account
		err = json.Unmarshal([]byte(existing.Result), &a)
		return application.RegistrationResult{Account: a, Replayed: true}, err
	}
	a := in.Account
	err = q.InsertAccount(ctx, dbgen.InsertAccountParams{ID: string(a.ID), RevisionID: string(a.RevisionID), Protocol: a.Protocol, Address: a.Address, Label: a.Label, CreatedAt: stamp(a.CreatedAt)})
	if err != nil {
		var dbErr *sqlitedriver.Error
		if errors.As(err, &dbErr) && dbErr.Code() == 2067 {
			return application.RegistrationResult{}, application.ErrAccountExists
		}
		return application.RegistrationResult{}, err
	}
	if err = q.InsertAccountRevision(ctx, dbgen.InsertAccountRevisionParams{ID: string(a.RevisionID), EntityID: string(a.ID), Payload: jsonText(a), CreatedAt: stamp(a.CreatedAt)}); err != nil {
		return application.RegistrationResult{}, err
	}
	if _, err = change(ctx, q, "account.registered", a, a.CreatedAt); err != nil {
		return application.RegistrationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.RegistrationResult{}, err
	}
	return application.RegistrationResult{Account: a}, nil
}
func (s *Store) Account(ctx context.Context, id domain.ID) (domain.Account, error) {
	data, err := s.queries.GetAccount(ctx, string(id))
	if err != nil {
		return domain.Account{}, notFound(err)
	}
	var a domain.Account
	err = json.Unmarshal([]byte(data), &a)
	return a, err
}
func (s *Store) Accounts(ctx context.Context, after domain.ID, limit int) ([]domain.Account, error) {
	rows, err := s.queries.ListAccounts(ctx, dbgen.ListAccountsParams{ID: string(after), Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Account, 0, len(rows))
	for _, data := range rows {
		var a domain.Account
		if err = json.Unmarshal([]byte(data), &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
func (s *Store) Operation(ctx context.Context, id domain.ID) (application.Operation, error) {
	row, err := s.queries.GetOperation(ctx, string(id))
	if err != nil {
		return application.Operation{}, notFound(err)
	}
	at, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	return application.Operation{ID: domain.ID(row.ID), ResourceID: domain.ID(row.ResourceID), CreatedAt: at, Kind: row.Kind}, err
}
func (s *Store) Events(ctx context.Context, after int64, limit int) ([]application.Event, error) {
	rows, err := s.queries.ListChanges(ctx, dbgen.ListChangesParams{Sequence: after, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]application.Event, 0, len(rows))
	for _, row := range rows {
		e := application.Event{Sequence: row.Sequence, Type: row.Kind}
		e.OccurredAt, err = time.Parse(time.RFC3339Nano, row.OccurredAt)
		if err != nil {
			return nil, err
		}
		var target any
		switch row.Kind {
		case "account.registered":
			e.Account = &domain.Account{}
			target = e.Account
		case "message.preview_changed":
			e.Preview = &domain.MessagePreview{}
			target = e.Preview
		case "profile.avatar_changed":
			e.Avatar = &domain.Avatar{}
			target = e.Avatar
		case "account.history_changed":
			e.History = &domain.HistoryStatus{}
			target = e.History
		case "account.connection_changed":
			e.Connection = &domain.Connection{}
			target = e.Connection
		case "conversation.created", "conversation.updated":
			e.Conversation = &domain.Conversation{}
			target = e.Conversation
		case "message.created", "message.updated":
			e.Message = &domain.Message{}
			target = e.Message
		default:
			return nil, errors.New("unsupported durable event type")
		}
		if err = json.Unmarshal([]byte(row.Payload), target); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}
