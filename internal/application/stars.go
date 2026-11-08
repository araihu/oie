package application

import (
	"context"
	"errors"

	"github.com/araihu/oie/internal/domain"
)

type StarMutation struct {
	Operation          Operation
	Fingerprint        string
	MessageID          domain.ID
	ExpectedRevisionID domain.ID
	RevisionID         domain.ID
	Starred            bool
}

type StarResult struct {
	Star     domain.MessageStar
	Replayed bool
}

type StarQuery struct {
	AccountID, ConversationID, Cursor domain.ID
	Limit                             int
}

type StarRepository interface {
	MessageStar(context.Context, domain.ID) (domain.MessageStar, error)
	MessageStars(context.Context, domain.ID, []domain.ID) ([]domain.MessageStar, error)
	SetMessageStar(context.Context, StarMutation) (StarResult, error)
	StarredMessages(context.Context, StarQuery) ([]domain.StarredMessage, error)
}

type StarConflict struct{ Current domain.MessageStar }

func (StarConflict) Error() string { return ErrStateConflict.Error() }
func (StarConflict) Unwrap() error { return ErrStateConflict }

func (e *Engine) starRepo() (StarRepository, error) {
	r, ok := e.repo.(StarRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r, nil
}

func (e *Engine) MessageStar(ctx context.Context, id domain.ID) (domain.MessageStar, error) {
	if !validID(id) {
		return domain.MessageStar{}, ErrInvalid
	}
	r, err := e.starRepo()
	if err != nil {
		return domain.MessageStar{}, err
	}
	return r.MessageStar(ctx, id)
}

func (e *Engine) MessageStars(ctx context.Context, conversation domain.ID, ids []domain.ID) ([]domain.MessageStar, error) {
	if len(ids) < 1 || len(ids) > 100 {
		return nil, ErrInvalid
	}
	if _, err := e.Conversation(ctx, conversation); err != nil {
		return nil, err
	}
	unique := make([]domain.ID, 0, len(ids))
	seen := make(map[domain.ID]bool, len(ids))
	for _, id := range ids {
		if !validID(id) {
			return nil, ErrInvalid
		}
		m, err := e.Message(ctx, id)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
			return nil, ErrInvalid
		}
		if err != nil {
			return nil, err
		}
		if m.ConversationID != conversation {
			return nil, ErrInvalid
		}
		id = m.ID
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	r, err := e.starRepo()
	if err != nil {
		return nil, err
	}
	states, err := r.MessageStars(ctx, conversation, unique)
	if err != nil {
		return nil, err
	}
	if len(states) != len(unique) {
		return nil, ErrInvalid
	}
	return states, nil
}

func (e *Engine) SetMessageStar(ctx context.Context, key, id, expected domain.ID, starred bool) (StarResult, error) {
	if !validID(key) || !validID(id) || (expected != "" && !validID(expected)) {
		return StarResult{}, ErrInvalid
	}
	r, err := e.starRepo()
	if err != nil {
		return StarResult{}, err
	}
	revision, err := e.newID()
	if err != nil {
		return StarResult{}, err
	}
	return r.SetMessageStar(ctx, StarMutation{
		Operation: Operation{ID: key, ResourceID: id, Kind: "set_message_star", CreatedAt: e.now().UTC()},
		Fingerprint: fingerprint("set_message_star", struct {
			MessageID, ExpectedRevisionID domain.ID
			Starred                       bool
		}{id, expected, starred}),
		MessageID: id, ExpectedRevisionID: expected, RevisionID: revision, Starred: starred,
	})
}

func (e *Engine) StarredMessages(ctx context.Context, in StarQuery) ([]domain.StarredMessage, error) {
	if in.Limit < 1 || in.Limit > 100 {
		return nil, ErrInvalid
	}
	if in.AccountID != "" {
		if _, err := e.Account(ctx, in.AccountID); err != nil {
			return nil, err
		}
	}
	if in.ConversationID != "" {
		c, err := e.Conversation(ctx, in.ConversationID)
		if err != nil {
			return nil, err
		}
		if in.AccountID != "" && c.AccountID != in.AccountID {
			return nil, ErrInvalid
		}
	}
	if in.Cursor != "" {
		m, err := e.Message(ctx, in.Cursor)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
			return nil, ErrInvalidCursor
		}
		if err != nil {
			return nil, err
		}
		if (in.AccountID != "" && m.AccountID != in.AccountID) || (in.ConversationID != "" && m.ConversationID != in.ConversationID) {
			return nil, ErrInvalidCursor
		}
	}
	r, err := e.starRepo()
	if err != nil {
		return nil, err
	}
	in.Limit++
	return r.StarredMessages(ctx, in)
}
