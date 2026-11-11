package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func roomArchive(a domain.RoomArchive) engineapi.RoomArchive {
	v := engineapi.RoomArchive{
		ConversationId: string(a.ConversationID), Mode: engineapi.RoomArchiveMode(a.Mode),
		InitialComplete: a.InitialComplete, OlderComplete: a.OlderComplete,
		Availability: engineapi.RoomArchiveAvailability(a.Availability), TextSearch: engineapi.RoomArchiveTextSearch(a.TextSearch),
	}
	if !a.WindowStart.IsZero() {
		v.WindowStart, v.WindowEnd = &a.WindowStart, &a.WindowEnd
	}
	if !a.Boundary.IsZero() {
		v.OldestCoveredAt = &a.Boundary
	}
	return v
}

func (s *Server) GetRoomArchive(ctx context.Context, r engineapi.GetRoomArchiveRequestObject) (engineapi.GetRoomArchiveResponseObject, error) {
	a, err := s.engine.RoomArchive(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetRoomArchive200JSONResponse(roomArchive(a)), nil
}

func (s *Server) SetRoomArchiveMode(ctx context.Context, r engineapi.SetRoomArchiveModeRequestObject) (engineapi.SetRoomArchiveModeResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	a, err := s.engine.SetRoomArchiveMode(ctx, domain.ID(r.ConversationId), string(r.Body.Mode))
	if err != nil {
		return nil, err
	}
	return engineapi.SetRoomArchiveMode200JSONResponse(roomArchive(a)), nil
}

func (s *Server) RequestRoomArchive(ctx context.Context, r engineapi.RequestRoomArchiveRequestObject) (engineapi.RequestRoomArchiveResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	var anchor domain.ID
	var query, cursor string
	if r.Body.MessageId != nil {
		anchor = domain.ID(*r.Body.MessageId)
	}
	if r.Body.Query != nil {
		query = *r.Body.Query
	}
	if r.Body.Cursor != nil {
		cursor = *r.Body.Cursor
	}
	result, err := s.engine.RequestRoomArchive(ctx, domain.ID(r.ConversationId), anchor, string(r.Body.Action), query, cursor)
	if err != nil {
		return nil, err
	}
	out := engineapi.RoomArchiveResult{Archive: roomArchive(result.Archive), Outcome: engineapi.RoomArchiveResultOutcome(result.Outcome), Items: []engineapi.Message{}}
	for _, item := range result.Items {
		out.Items = append(out.Items, message(item))
	}
	if result.NextCursor != "" {
		out.NextCursor = &result.NextCursor
	}
	return engineapi.RequestRoomArchive200JSONResponse(out), nil
}
