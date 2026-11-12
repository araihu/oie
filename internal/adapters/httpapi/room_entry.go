package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) PreviewRoom(ctx context.Context, r engineapi.PreviewRoomRequestObject) (engineapi.PreviewRoomResponseObject, error) {
	preview, err := s.engine.PreviewRoom(ctx, domain.ID(r.AccountId), r.Params.Address, r.Params.DiscoveredServiceOnly != nil && *r.Params.DiscoveredServiceOnly)
	if err != nil {
		return nil, err
	}
	return engineapi.PreviewRoom200JSONResponse{
		Address: preview.Address, State: engineapi.RoomPreviewState(preview.State),
		Name: optionalRoomText(preview.Name), Description: optionalRoomText(preview.Description),
		Topic: optionalRoomText(preview.Topic), OccupantCount: preview.OccupantCount, AvatarHash: optionalRoomText(preview.AvatarHash),
	}, nil
}

func optionalRoomText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (s *Server) OpenRoom(ctx context.Context, r engineapi.OpenRoomRequestObject) (engineapi.OpenRoomResponseObject, error) {
	if r.Body == nil || !r.Body.Action.Valid() {
		return nil, application.ErrInvalid
	}
	value, err := s.engine.OpenRoom(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.AccountId),
		r.Body.Address, roomText(r.Body.Nickname), roomText(r.Body.Name), r.Body.Action == "create")
	if err != nil {
		return nil, err
	}
	return engineapi.OpenRoom202JSONResponse(conversation(value.Conversation)), nil
}

func roomText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
