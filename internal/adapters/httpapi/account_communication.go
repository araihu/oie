package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func accountCommunicationPreferences(p domain.AccountCommunicationPreferences) engineapi.AccountCommunicationPreferences {
	out := engineapi.AccountCommunicationPreferences{Presence: engineapi.AccountCommunicationPreferencesPresence(p.Presence), SendTyping: &p.SendTyping, SendReadReceipts: &p.SendReadReceipts}
	if p.RevisionID != "" {
		out.RevisionId = new(string(p.RevisionID))
	}
	return out
}

func (s *Server) GetAccountCommunicationPreferences(ctx context.Context, r engineapi.GetAccountCommunicationPreferencesRequestObject) (engineapi.GetAccountCommunicationPreferencesResponseObject, error) {
	preferences, err := s.engine.AccountCommunicationPreferences(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetAccountCommunicationPreferences200JSONResponse(accountCommunicationPreferences(preferences)), nil
}

func (s *Server) SetAccountCommunicationPreferences(ctx context.Context, r engineapi.SetAccountCommunicationPreferencesRequestObject) (engineapi.SetAccountCommunicationPreferencesResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	var revision domain.ID
	if r.Body.RevisionId != nil {
		revision = domain.ID(*r.Body.RevisionId)
	}
	preferences, err := s.engine.SetAccountCommunicationPreferences(ctx, domain.ID(r.AccountId), revision, string(r.Body.Presence), application.AccountCommunicationToggles{SendTyping: r.Body.SendTyping, SendReadReceipts: r.Body.SendReadReceipts})
	if err != nil {
		return nil, err
	}
	return engineapi.SetAccountCommunicationPreferences200JSONResponse(accountCommunicationPreferences(preferences)), nil
}
