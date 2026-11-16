package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) AccountCommunicationPreferences(ctx context.Context, account domain.ID) (domain.AccountCommunicationPreferences, error) {
	return accountCommunicationPreferences(ctx, s.read, account)
}

func accountCommunicationPreferences(ctx context.Context, q *dbgen.Queries, account domain.ID) (domain.AccountCommunicationPreferences, error) {
	row, err := q.GetAccountCommunicationPreferences(ctx, string(account))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountCommunicationPreferences{Presence: "available", SendTyping: true}, nil
	}
	return domain.AccountCommunicationPreferences{RevisionID: domain.ID(row.RevisionID), Presence: row.Presence, SendTyping: row.SendTyping != 0}, err
}

func (s *Store) SaveAccountCommunicationPreferences(ctx context.Context, account, expected domain.ID, next domain.AccountCommunicationPreferences, typing *bool) (domain.AccountCommunicationPreferences, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.AccountCommunicationPreferences{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return domain.AccountCommunicationPreferences{}, err
	}
	current, err := accountCommunicationPreferences(ctx, q, account)
	if err != nil {
		return current, err
	}
	next.SendTyping = current.SendTyping
	if typing != nil {
		next.SendTyping = *typing
	}
	if current.Presence == next.Presence && current.SendTyping == next.SendTyping {
		return current, nil
	}
	if current.RevisionID != expected {
		return current, application.ErrStateConflict
	}
	if err := q.PutAccountCommunicationPreferences(ctx, dbgen.PutAccountCommunicationPreferencesParams{
		AccountID: string(account), RevisionID: string(next.RevisionID), Presence: next.Presence, SendTyping: number(next.SendTyping),
	}); err != nil {
		return current, err
	}
	return next, tx.Commit()
}
