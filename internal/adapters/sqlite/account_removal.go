package sqlite

import (
	"context"
	"time"

	"github.com/araihu/oie/internal/domain"
)

func (s *Store) RemoveAccount(ctx context.Context, id domain.ID, at time.Time) (bool, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := q.RemoveAccount(ctx, string(id))
	if err != nil || n == 0 {
		return false, err
	}
	if _, err = change(ctx, q, "account.removed", id, at); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
