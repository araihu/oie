package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) RetractMessage(ctx context.Context, r engineapi.RetractMessageRequestObject) (engineapi.RetractMessageResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	m, err := s.engine.RetractMessage(ctx, domain.ID(r.MessageId), domain.ID(r.Body.ExpectedRevisionId))
	if err != nil {
		return nil, err
	}
	return engineapi.RetractMessage200JSONResponse(message(m)), nil
}

func (s *Server) GetRetractedOriginal(ctx context.Context, r engineapi.GetRetractedOriginalRequestObject) (engineapi.GetRetractedOriginalResponseObject, error) {
	m, err := s.engine.RetractedOriginal(ctx, domain.ID(r.MessageId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetRetractedOriginal200JSONResponse(message(m)), nil
}

func (s *Server) messagePageItem(ctx context.Context, a domain.Account, c domain.Conversation, m domain.Message) engineapi.Message {
	out := message(m)
	if s.engine.CanRetractMessage(ctx, a, c, m) {
		allowed := true
		out.CanRetract = &allowed
	}
	return out
}
