package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetGlobalReadReceipts(ctx context.Context, _ engineapi.GetGlobalReadReceiptsRequestObject) (engineapi.GetGlobalReadReceiptsResponseObject, error) {
	policy, err := s.engine.GlobalReadReceipts(ctx)
	return engineapi.GetGlobalReadReceipts200JSONResponse(engineapi.GlobalReadReceipts{Revision: policy.Revision, Enabled: policy.Enabled}), err
}

func (s *Server) SetGlobalReadReceipts(ctx context.Context, r engineapi.SetGlobalReadReceiptsRequestObject) (engineapi.SetGlobalReadReceiptsResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	policy, err := s.engine.SetGlobalReadReceipts(ctx, domain.GlobalReadReceipts{Revision: r.Body.ExpectedRevision, Enabled: r.Body.Enabled})
	return engineapi.SetGlobalReadReceipts200JSONResponse(engineapi.GlobalReadReceipts{Revision: policy.Revision, Enabled: policy.Enabled}), err
}

func (s *Server) GetConversationReadReceipts(ctx context.Context, r engineapi.GetConversationReadReceiptsRequestObject) (engineapi.GetConversationReadReceiptsResponseObject, error) {
	policy, err := s.engine.ConversationReadReceipts(ctx, domain.ID(r.ConversationId))
	return engineapi.GetConversationReadReceipts200JSONResponse(conversationReadReceipts(policy)), err
}

func (s *Server) SetConversationReadReceipts(ctx context.Context, r engineapi.SetConversationReadReceiptsRequestObject) (engineapi.SetConversationReadReceiptsResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	policy, err := s.engine.SetConversationReadReceipts(ctx, domain.ID(r.ConversationId), r.Body.ExpectedRevision, r.Body.Override)
	return engineapi.SetConversationReadReceipts200JSONResponse(conversationReadReceipts(policy)), err
}

func conversationReadReceipts(policy domain.ConversationReadReceipts) engineapi.ConversationReadReceipts {
	return engineapi.ConversationReadReceipts{
		Revision: policy.Revision, Override: policy.Override, GlobalEnabled: policy.GlobalEnabled, EffectiveEnabled: policy.EffectiveEnabled,
	}
}
