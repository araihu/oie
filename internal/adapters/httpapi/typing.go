package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetConversationTyping(ctx context.Context, r engineapi.GetConversationTypingRequestObject) (engineapi.GetConversationTypingResponseObject, error) {
	active, err := s.engine.Typing(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetConversationTyping200JSONResponse{Active: active}, nil
}

func (s *Server) SetConversationTyping(ctx context.Context, r engineapi.SetConversationTypingRequestObject) (engineapi.SetConversationTypingResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	if err := s.engine.SetTyping(ctx, domain.ID(r.ConversationId), r.Body.Active); err != nil {
		return nil, err
	}
	return engineapi.SetConversationTyping204Response{}, nil
}
