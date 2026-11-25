package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetRoomOmemo(ctx context.Context, r engineapi.GetRoomOmemoRequestObject) (engineapi.GetRoomOmemoResponseObject, error) {
	state, err := s.engine.InspectRoomOMEMO(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetRoomOmemo200JSONResponse(engineapi.RoomOmemo{
		Address: state.Address, Enabled: state.Enabled, Eligible: state.Eligible, Members: state.Members,
	}), nil
}

func (s *Server) SetRoomOmemo(ctx context.Context, r engineapi.SetRoomOmemoRequestObject) (engineapi.SetRoomOmemoResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	if err := s.engine.SetRoomOMEMO(ctx, domain.ID(r.ConversationId), r.Body.Enabled); err != nil {
		return nil, err
	}
	return engineapi.SetRoomOmemo204Response{}, nil
}
