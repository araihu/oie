package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) ListContacts(ctx context.Context, r engineapi.ListContactsRequestObject) (engineapi.ListContactsResponseObject, error) {
	query := domain.ContactQuery{Limit: 50, AccountIDs: searchAccountIDs(r.Params.AccountIds)}
	query.IncludePresence = r.Params.IncludePresence != nil && *r.Params.IncludePresence
	if r.Params.Q != nil {
		query.Search = *r.Params.Q
	}
	if r.Params.AccountId != nil {
		query.AccountID = domain.ID(*r.Params.AccountId)
	}
	if r.Params.Cursor != nil {
		query.Cursor = *r.Params.Cursor
	}
	if r.Params.Limit != nil {
		query.Limit = *r.Params.Limit
	}
	page, err := s.engine.Contacts(ctx, query)
	if err != nil {
		return nil, err
	}
	out := engineapi.ContactPage{Items: []engineapi.Contact{}, Sources: []engineapi.ContactSource{}}
	for _, c := range page.Items {
		item := engineapi.Contact{Id: string(c.ID), RevisionId: string(c.RevisionID), AccountId: string(c.AccountID), AccountLabel: c.AccountLabel, Address: c.Address, DisplayName: c.DisplayName, Groups: c.Groups, Subscription: engineapi.ContactSubscription(c.Subscription)}
		if c.ConversationID != "" {
			id := string(c.ConversationID)
			item.ConversationId = &id
		}
		if c.Presence != nil {
			observation := presenceObservation(*c.Presence)
			item.Presence = &observation
		}
		out.Items = append(out.Items, item)
	}
	for _, source := range page.Sources {
		item := engineapi.ContactSource{AccountId: string(source.AccountID), State: engineapi.ContactSourceState(source.State)}
		if !source.CheckedAt.IsZero() {
			at := source.CheckedAt
			item.CheckedAt = &at
		}
		out.Sources = append(out.Sources, item)
	}
	if page.NextCursor != "" {
		cursor := page.NextCursor
		out.NextCursor = &cursor
	}
	return engineapi.ListContacts200JSONResponse(out), nil
}

func (s *Server) ListContactRequests(ctx context.Context, r engineapi.ListContactRequestsRequestObject) (engineapi.ListContactRequestsResponseObject, error) {
	items, err := s.engine.ContactRequests(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	return engineapi.ListContactRequests200JSONResponse{Items: items}, nil
}

func (s *Server) ChangeProtocolContact(ctx context.Context, r engineapi.ChangeProtocolContactRequestObject) (engineapi.ChangeProtocolContactResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	err := s.engine.ChangeContact(ctx, domain.ID(r.AccountId), r.Body.Address, string(r.Body.Action))
	if err != nil {
		return nil, err
	}
	return engineapi.ChangeProtocolContact204Response{}, nil
}
