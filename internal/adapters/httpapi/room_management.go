package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetRoomManagement(ctx context.Context, r engineapi.GetRoomManagementRequestObject) (engineapi.GetRoomManagementResponseObject, error) {
	var affiliation string
	if r.Params.Affiliation != nil {
		affiliation = string(*r.Params.Affiliation)
	}
	data, err := s.engine.RoomManagementForMessage(ctx, domain.ID(r.ConversationId), affiliation, domain.ID(roomText(r.Params.MessageId)))
	if err != nil {
		return nil, err
	}
	out := engineapi.GetRoomManagement200JSONResponse{
		State: engineapi.RoomManagementState(data.State), Revision: optionalRoomText(data.Revision),
		Actions:      roomValues[engineapi.RoomManagementAction](data.Actions),
		Occupants:    make([]engineapi.RoomManagedOccupant, 0, len(data.Occupants)),
		Affiliations: data.Affiliations,
	}
	for _, item := range data.Occupants {
		out.Occupants = append(out.Occupants, engineapi.RoomManagedOccupant{
			Id: item.ID, Nickname: item.Nickname, Role: engineapi.RoomRole(item.Role), Affiliation: engineapi.RoomAffiliation(item.Affiliation),
			Roles: roomValues[engineapi.RoomRole](item.Roles), Affiliations: roomValues[engineapi.RoomAffiliation](item.Affiliations),
		})
	}
	return out, nil
}

func roomValues[T ~string](values []string) []T {
	out := make([]T, 0, len(values))
	for _, value := range values {
		out = append(out, T(value))
	}
	return out
}

func (s *Server) ManageRoom(ctx context.Context, r engineapi.ManageRoomRequestObject) (engineapi.ManageRoomResponseObject, error) {
	if r.Body == nil || !r.Body.Action.Valid() {
		return nil, application.ErrInvalid
	}
	cmd := domain.RoomManagementCommand{
		Action: string(r.Body.Action), ExpectedRevision: r.Body.ExpectedRevision,
		Target: roomText(r.Body.Target), Value: roomText(r.Body.Value), Reason: roomText(r.Body.Reason),
		MessageID: domain.ID(roomText(r.Body.MessageId)),
	}
	state, err := s.engine.ManageRoom(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId), cmd)
	if err != nil {
		return nil, err
	}
	return engineapi.ManageRoom200JSONResponse{State: engineapi.RoomManagementResultState(state)}, nil
}
