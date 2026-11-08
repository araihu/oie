package application

import (
	"context"
	"github.com/araihu/oie/internal/domain"
	"strings"
	"unicode/utf8"
)

// Optional history ports keep archive cursors and provider XML out of the core.
type HistoryHost interface {
	History(context.Context, domain.ID) (domain.HistoryStatus, error)
	HistoryChanged(context.Context, domain.HistoryStatus) error
	HistoryCheckpoint(context.Context, domain.ID, string) (string, error)
	CommitHistoryCheckpoint(context.Context, domain.ID, string, string) error
	RestoreConversation(context.Context, domain.ID, string, string, string) error
}
type HistoryRepository interface {
	History(context.Context, domain.ID) (domain.HistoryStatus, error)
	RecordHistory(context.Context, domain.HistoryStatus) error
	HistoryCheckpoint(context.Context, domain.ID, string) (string, error)
	CommitHistoryCheckpoint(context.Context, domain.ID, string, string) error
	RestoreConversation(context.Context, domain.Conversation) error
}

func (e *Engine) History(ctx context.Context, id domain.ID) (domain.HistoryStatus, error) {
	a, err := e.Account(ctx, id)
	if err != nil {
		return domain.HistoryStatus{}, err
	}
	h := domain.HistoryStatus{AccountID: id, State: "not_started", UpdatedAt: a.CreatedAt}
	r, ok := e.repo.(HistoryRepository)
	if !ok {
		return h, nil
	}
	h, err = r.History(ctx, id)
	if h.UpdatedAt.IsZero() {
		h.UpdatedAt = a.CreatedAt
	}
	return h, err
}
func (e *Engine) HistoryChanged(ctx context.Context, h domain.HistoryStatus) error {
	if _, err := e.Account(ctx, h.AccountID); err != nil {
		return err
	}
	switch h.State {
	case "syncing", "complete", "partial", "unsupported", "error":
	default:
		return ErrInvalid
	}
	r, ok := e.repo.(HistoryRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	h.UpdatedAt = e.now().UTC()
	return r.RecordHistory(ctx, h)
}
func (e *Engine) HistoryCheckpoint(ctx context.Context, id domain.ID, scope string) (string, error) {
	r, ok := e.repo.(HistoryRepository)
	if !ok {
		return "", ErrProtocolUnavailable
	}
	return r.HistoryCheckpoint(ctx, id, scope)
}
func (e *Engine) CommitHistoryCheckpoint(ctx context.Context, id domain.ID, scope, after string) error {
	r, ok := e.repo.(HistoryRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	return r.CommitHistoryCheckpoint(ctx, id, scope, after)
}
func (e *Engine) RestoreConversation(ctx context.Context, id domain.ID, address, kind, title string) error {
	a, err := e.Account(ctx, id)
	if err != nil {
		return err
	}
	address, err = e.canonical(a.Protocol, address)
	if err != nil {
		return err
	}
	if kind != "room" && kind != "direct" {
		return ErrInvalid
	}
	if !utf8.ValidString(title) {
		return ErrInvalid
	}
	title = strings.TrimSpace(title)
	if len([]rune(title)) > 120 {
		title = string([]rune(title)[:120])
	}
	if title == "" {
		title = address
	}
	ids := make([]domain.ID, 3)
	for i := range ids {
		ids[i], err = e.newID()
		if err != nil {
			return err
		}
	}
	r, ok := e.repo.(HistoryRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	return r.RestoreConversation(ctx, domain.Conversation{ID: ids[0], RevisionID: ids[1], PeerID: ids[2], AccountID: id, Address: address, Title: title, Kind: kind, UpdatedAt: e.now().UTC()})
}
