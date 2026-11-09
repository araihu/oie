package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func readState(s domain.ReadState) engineapi.ConversationReadState {
	out := engineapi.ConversationReadState{AccountId: string(s.AccountID), ConversationId: string(s.ConversationID), UnreadCount: s.UnreadCount}
	if s.RevisionID != "" {
		id, at := string(s.RevisionID), s.UpdatedAt
		out.RevisionId = &id
		out.UpdatedAt = &at
	}
	return out
}
func (s *Server) GetConversationReadState(ctx context.Context, r engineapi.GetConversationReadStateRequestObject) (engineapi.GetConversationReadStateResponseObject, error) {
	state, err := s.engine.ConversationReadState(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetConversationReadState200JSONResponse(readState(state)), nil
}
func (s *Server) MarkConversationMessagesRead(ctx context.Context, r engineapi.MarkConversationMessagesReadRequestObject) (engineapi.MarkConversationMessagesReadResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	ids := make([]domain.ID, 0, len(r.Body.MessageIds))
	for _, id := range r.Body.MessageIds {
		ids = append(ids, domain.ID(id))
	}
	result, err := s.engine.MarkMessagesRead(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId), ids)
	if err != nil {
		return nil, err
	}
	return engineapi.MarkConversationMessagesRead200JSONResponse(readState(result.State)), nil
}
