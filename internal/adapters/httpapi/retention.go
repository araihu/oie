package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetGlobalRetention(ctx context.Context, _ engineapi.GetGlobalRetentionRequestObject) (engineapi.GetGlobalRetentionResponseObject, error) {
	value, err := s.engine.GlobalRetention(ctx)
	return engineapi.GetGlobalRetention200JSONResponse(globalRetention(value)), err
}

func (s *Server) SetGlobalRetention(ctx context.Context, r engineapi.SetGlobalRetentionRequestObject) (engineapi.SetGlobalRetentionResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	value, err := s.engine.SetGlobalRetention(ctx, domain.GlobalRetention{Revision: r.Body.ExpectedRevision, RoomDays: r.Body.RoomDays, DirectDays: r.Body.DirectDays})
	return engineapi.SetGlobalRetention200JSONResponse(globalRetention(value)), err
}

func (s *Server) GetConversationRetention(ctx context.Context, r engineapi.GetConversationRetentionRequestObject) (engineapi.GetConversationRetentionResponseObject, error) {
	value, err := s.engine.ConversationRetention(ctx, domain.ID(r.ConversationId))
	return engineapi.GetConversationRetention200JSONResponse(conversationRetention(value)), err
}

func (s *Server) SetConversationRetention(ctx context.Context, r engineapi.SetConversationRetentionRequestObject) (engineapi.SetConversationRetentionResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	value, err := s.engine.SetConversationRetention(ctx, domain.ID(r.ConversationId), r.Body.ExpectedRevision, r.Body.Days)
	return engineapi.SetConversationRetention200JSONResponse(conversationRetention(value)), err
}

func globalRetention(value domain.GlobalRetention) engineapi.GlobalRetention {
	return engineapi.GlobalRetention{Revision: value.Revision, RoomDays: value.RoomDays, DirectDays: value.DirectDays}
}

func conversationRetention(value domain.ConversationRetention) engineapi.ConversationRetention {
	return engineapi.ConversationRetention{Revision: value.Revision, Days: value.Days, EffectiveDays: value.EffectiveDays, GlobalDays: value.GlobalDays}
}
