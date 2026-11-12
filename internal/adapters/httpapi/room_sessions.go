package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func roomSession(value domain.RoomSession) engineapi.RoomSession {
	out := engineapi.RoomSession{AccountId: string(value.AccountID), ConversationId: string(value.ConversationID), Active: value.Active, State: engineapi.RoomSessionState(value.State), UpdatedAt: value.UpdatedAt}
	if value.IntentID != "" {
		id := string(value.IntentID)
		out.IntentId = &id
	}
	if value.IntentRevisionID != "" {
		revision := string(value.IntentRevisionID)
		out.IntentRevisionId = &revision
	}
	if value.Nickname != "" {
		out.Nickname = &value.Nickname
	}
	if value.LastErrorCode != "" {
		code := value.LastErrorCode
		switch code {
		case "not_connected", "forbidden", "not_authorized", "nickname_conflict", "registration_required", "room_unavailable", "removed", "timeout", "unsupported", "unknown":
		default:
			code = "unknown"
		}
		typed := engineapi.RoomSessionLastErrorCode(code)
		out.LastErrorCode = &typed
	}
	return out
}
func (s *Server) GetRoomSession(ctx context.Context, r engineapi.GetRoomSessionRequestObject) (engineapi.GetRoomSessionResponseObject, error) {
	value, err := s.engine.RoomSession(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetRoomSession200JSONResponse(roomSession(value)), nil
}
func (s *Server) JoinRoom(ctx context.Context, r engineapi.JoinRoomRequestObject) (engineapi.JoinRoomResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	nickname := ""
	if r.Body.Nickname != nil {
		nickname = *r.Body.Nickname
	}
	revision := domain.ID("")
	if r.Body.ExpectedRevisionId != nil {
		revision = domain.ID(*r.Body.ExpectedRevisionId)
	}
	conversationRevision := domain.ID("")
	if r.Body.ExpectedConversationRevisionId != nil {
		conversationRevision = domain.ID(*r.Body.ExpectedConversationRevisionId)
	}
	value, err := s.engine.JoinRoomInvitation(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId), nickname, revision, conversationRevision)
	if err != nil {
		return nil, err
	}
	return engineapi.JoinRoom200JSONResponse(roomSession(value.Session)), nil
}
func (s *Server) LeaveRoom(ctx context.Context, r engineapi.LeaveRoomRequestObject) (engineapi.LeaveRoomResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	revision := domain.ID("")
	if r.Body.ExpectedRevisionId != nil {
		revision = domain.ID(*r.Body.ExpectedRevisionId)
	}
	value, err := s.engine.LeaveRoom(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId), revision)
	if err != nil {
		return nil, err
	}
	return engineapi.LeaveRoom200JSONResponse(roomSession(value.Session)), nil
}
