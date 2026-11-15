package application

import (
	"context"
	"errors"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type accountRemovalRepository interface {
	RemoveAccount(context.Context, domain.ID, time.Time) (bool, error)
}

type accountRemovalProtocol interface {
	ForgetConnection(context.Context, domain.Account) error
}

func (e *Engine) RemoveAccount(ctx context.Context, id domain.ID) error {
	e.accountConnectionMu.Lock()
	defer e.accountConnectionMu.Unlock()
	a, err := e.Account(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	repo, ok := e.repo.(accountRemovalRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	if e.protocols != nil {
		p, ok := e.protocols.(accountRemovalProtocol)
		if !ok {
			return ErrProtocolUnavailable
		}
		// Forget the connection before deleting the account. A failed deletion
		// can be retried; a restart cannot revive the old connection.
		if err := p.ForgetConnection(ctx, a); err != nil {
			return err
		}
	}
	removed, err := repo.RemoveAccount(ctx, id, e.now().UTC())
	if err == nil && removed {
		e.notifyEvents()
	}
	return err
}
