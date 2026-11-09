package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) UpdateConversationPreferences(ctx context.Context, r engineapi.UpdateConversationPreferencesRequestObject) (engineapi.UpdateConversationPreferencesResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	result, err := s.engine.UpdateConversationPreferences(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId), application.ConversationPreferencesInput{
		AccountID:          domain.ID(r.Body.AccountId),
		ExpectedRevisionID: domain.ID(r.Body.ExpectedRevisionId),
		Archived:           r.Body.Archived,
		Favorite:           r.Body.Favorite,
	})
	if err != nil {
		return nil, err
	}
	return engineapi.UpdateConversationPreferences200JSONResponse{
		Body:    conversation(result.Conversation),
		Headers: engineapi.UpdateConversationPreferences200ResponseHeaders{IdempotencyReplayed: &result.Replayed},
	}, nil
}
