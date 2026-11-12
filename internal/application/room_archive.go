package application

import (
	"context"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type RoomArchiveHost interface {
	RoomArchive(context.Context, domain.ID) (domain.RoomArchive, error)
	StoreRoomArchiveProgress(context.Context, domain.ID, domain.RoomArchiveProgress) error
	CountRoomArchiveMessages(context.Context, domain.ID, time.Time, time.Time) (int, error)
	ArchivedRoomMessage(context.Context, domain.Conversation, string) (domain.Message, error)
	ListRoomArchiveConversations(context.Context, domain.ID) ([]domain.Conversation, error)
}

type RoomArchiveRepository interface {
	RoomArchiveHost
	SetRoomArchiveMode(context.Context, domain.ID, string) error
}

var _ RoomArchiveHost = (*Engine)(nil)

type RoomArchiveProtocol interface {
	RequestRoomArchive(context.Context, domain.Account, domain.Conversation, domain.RoomArchiveRequest) (domain.RoomArchiveResult, error)
	WakeRoomArchive(domain.ID)
}

func (e *Engine) roomArchiveTarget(ctx context.Context, id domain.ID) (domain.Conversation, RoomArchiveRepository, error) {
	c, err := e.Conversation(ctx, id)
	if err != nil {
		return c, nil, err
	}
	if c.Kind != "room" || c.Request != "" {
		return c, nil, ErrInvalid
	}
	r, ok := e.repo.(RoomArchiveRepository)
	if !ok {
		return c, nil, ErrProtocolUnavailable
	}
	return c, r, nil
}

func (e *Engine) RoomArchive(ctx context.Context, id domain.ID) (domain.RoomArchive, error) {
	_, r, err := e.roomArchiveTarget(ctx, id)
	if err != nil {
		return domain.RoomArchive{}, err
	}
	return r.RoomArchive(ctx, id)
}

func (e *Engine) SetRoomArchiveMode(ctx context.Context, id domain.ID, mode string) (domain.RoomArchive, error) {
	c, r, err := e.roomArchiveTarget(ctx, id)
	if err != nil {
		return domain.RoomArchive{}, err
	}
	if mode != "gradual" && mode != "on_demand" {
		return domain.RoomArchive{}, ErrInvalid
	}
	if err := r.SetRoomArchiveMode(ctx, id, mode); err != nil {
		return domain.RoomArchive{}, err
	}
	if p, ok := e.protocols.(RoomArchiveProtocol); ok {
		p.WakeRoomArchive(c.AccountID)
	}
	return r.RoomArchive(ctx, id)
}

func (e *Engine) RequestRoomArchive(ctx context.Context, id, anchorID domain.ID, action, query, cursor string) (domain.RoomArchiveResult, error) {
	c, _, err := e.roomArchiveTarget(ctx, id)
	if err != nil {
		return domain.RoomArchiveResult{}, err
	}
	query = strings.TrimSpace(query)
	switch action {
	case "focus", "older", "context":
		if query != "" || cursor != "" || (action == "focus" && anchorID != "") || (action == "context" && anchorID == "") {
			return domain.RoomArchiveResult{}, ErrInvalid
		}
	case "search":
		if !validText(query, 200) || anchorID != "" || len(cursor) > 8192 {
			return domain.RoomArchiveResult{}, ErrInvalid
		}
	default:
		return domain.RoomArchiveResult{}, ErrInvalid
	}
	request := domain.RoomArchiveRequest{Action: action, Query: query, Cursor: cursor}
	if anchorID != "" {
		m, err := e.Message(ctx, anchorID)
		if err != nil {
			return domain.RoomArchiveResult{}, err
		}
		if m.ConversationID != c.ID || m.AccountID != c.AccountID {
			return domain.RoomArchiveResult{}, ErrNotFound
		}
		request.Anchor = &m
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return domain.RoomArchiveResult{}, err
	}
	p, ok := e.protocols.(RoomArchiveProtocol)
	if !ok {
		return domain.RoomArchiveResult{}, ErrProtocolUnavailable
	}
	return p.RequestRoomArchive(ctx, a, c, request)
}

func (e *Engine) StoreRoomArchiveProgress(ctx context.Context, id domain.ID, progress domain.RoomArchiveProgress) error {
	_, r, err := e.roomArchiveTarget(ctx, id)
	if err != nil {
		return err
	}
	return r.StoreRoomArchiveProgress(ctx, id, progress)
}

func (e *Engine) CountRoomArchiveMessages(ctx context.Context, id domain.ID, start, end time.Time) (int, error) {
	_, r, err := e.roomArchiveTarget(ctx, id)
	if err != nil {
		return 0, err
	}
	return r.CountRoomArchiveMessages(ctx, id, start, end)
}

func (e *Engine) ArchivedRoomMessage(ctx context.Context, c domain.Conversation, archiveID string) (domain.Message, error) {
	_, r, err := e.roomArchiveTarget(ctx, c.ID)
	if err != nil {
		return domain.Message{}, err
	}
	return r.ArchivedRoomMessage(ctx, c, archiveID)
}

func (e *Engine) ListRoomArchiveConversations(ctx context.Context, id domain.ID) ([]domain.Conversation, error) {
	r, ok := e.repo.(RoomArchiveRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r.ListRoomArchiveConversations(ctx, id)
}
