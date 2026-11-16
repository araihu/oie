package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) RemoveRoomFromChats(ctx context.Context, r engineapi.RemoveRoomFromChatsRequestObject) (engineapi.RemoveRoomFromChatsResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	if err := s.engine.RemoveRoomFromChats(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId),
		domain.ID(r.Body.AccountId), domain.ID(r.Body.ExpectedRevisionId)); err != nil {
		return nil, err
	}
	return engineapi.RemoveRoomFromChats204Response{}, nil
}

func (s *Server) RemoveDirectFromChats(ctx context.Context, r engineapi.RemoveDirectFromChatsRequestObject) (engineapi.RemoveDirectFromChatsResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	if err := s.engine.RemoveDirectFromChats(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId),
		domain.ID(r.Body.AccountId), domain.ID(r.Body.ExpectedRevisionId)); err != nil {
		return nil, err
	}
	return engineapi.RemoveDirectFromChats204Response{}, nil
}
