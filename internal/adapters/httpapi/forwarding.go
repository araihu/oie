package httpapi

import (
	"context"
	"errors"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) ForwardMessage(ctx context.Context, r engineapi.ForwardMessageRequestObject) (engineapi.ForwardMessageResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	result, err := s.engine.ForwardMessage(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.MessageId), domain.ID(r.Body.ExpectedRevisionId), domain.ID(r.Body.ConversationId))
	if errors.Is(err, application.ErrRoomNotJoined) {
		if current, readErr := s.engine.RoomSession(ctx, domain.ID(r.Body.ConversationId)); readErr == nil {
			return nil, application.RoomStateConflict{Current: current}
		}
	}
	if err != nil {
		return nil, err
	}
	if result.Replayed {
		return engineapi.ForwardMessage200JSONResponse(message(result.Message)), nil
	}
	return engineapi.ForwardMessage202JSONResponse(message(result.Message)), nil
}
