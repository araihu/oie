package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetMessageSenderProfile(ctx context.Context, r engineapi.GetMessageSenderProfileRequestObject) (engineapi.GetMessageSenderProfileResponseObject, error) {
	p, err := s.engine.MessageSenderProfile(ctx, domain.ID(r.MessageId))
	if err != nil {
		return nil, err
	}
	out := engineapi.MessageSenderProfile{AccountId: string(p.AccountID), ConversationId: string(p.ConversationID), MessageId: string(p.MessageID), State: engineapi.MessageSenderProfileState(p.State)}
	if p.FullName != "" {
		out.FullName = &p.FullName
	}
	if p.PersonalAddress != "" {
		out.PersonalAddress = &p.PersonalAddress
	}
	return engineapi.GetMessageSenderProfile200JSONResponse(out), nil
}
