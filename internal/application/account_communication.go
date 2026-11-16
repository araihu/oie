package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

type AccountCommunicationRepository interface {
	AccountCommunicationPreferences(context.Context, domain.ID) (domain.AccountCommunicationPreferences, error)
	SaveAccountCommunicationPreferences(context.Context, domain.ID, domain.ID, domain.AccountCommunicationPreferences, *bool) (domain.AccountCommunicationPreferences, error)
}

type AccountPresenceHost interface {
	AccountCommunicationPreferences(context.Context, domain.ID) (domain.AccountCommunicationPreferences, error)
}

type AccountPresenceProtocol interface {
	RefreshAccountPresence(context.Context, domain.Account)
}

func (e *Engine) AccountCommunicationPreferences(ctx context.Context, accountID domain.ID) (domain.AccountCommunicationPreferences, error) {
	if _, err := e.Account(ctx, accountID); err != nil {
		return domain.AccountCommunicationPreferences{}, err
	}
	repo, ok := e.repo.(AccountCommunicationRepository)
	if !ok {
		return domain.AccountCommunicationPreferences{}, ErrProtocolUnavailable
	}
	return repo.AccountCommunicationPreferences(ctx, accountID)
}

func (e *Engine) SetAccountCommunicationPreferences(ctx context.Context, accountID, revision domain.ID, presence string, sendTyping *bool) (domain.AccountCommunicationPreferences, error) {
	if revision != "" && !validID(revision) {
		return domain.AccountCommunicationPreferences{}, ErrInvalid
	}
	switch presence {
	case "available", "away", "dnd":
	default:
		return domain.AccountCommunicationPreferences{}, ErrInvalid
	}
	account, err := e.Account(ctx, accountID)
	if err != nil {
		return domain.AccountCommunicationPreferences{}, err
	}
	repo, ok := e.repo.(AccountCommunicationRepository)
	if !ok {
		return domain.AccountCommunicationPreferences{}, ErrProtocolUnavailable
	}
	next, err := e.newID()
	if err != nil {
		return domain.AccountCommunicationPreferences{}, err
	}
	saved, err := repo.SaveAccountCommunicationPreferences(ctx, accountID, revision, domain.AccountCommunicationPreferences{RevisionID: next, Presence: presence}, sendTyping)
	if err == nil {
		if protocol, ok := e.protocols.(AccountPresenceProtocol); ok {
			protocol.RefreshAccountPresence(ctx, account)
		}
	}
	return saved, err
}
