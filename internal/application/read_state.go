package application

import (
	"context"
	"github.com/araihu/oie/internal/domain"
	"slices"
)

type ReadMutation struct {
	Operation                                          Operation
	Fingerprint                                        string
	ConversationID, RevisionID, ConversationRevisionID domain.ID
	MessageIDs                                         []domain.ID
}
type ReadResult struct {
	State    domain.ReadState
	Replayed bool
}
type ReadRepository interface {
	ConversationReadState(context.Context, domain.ID) (domain.ReadState, error)
	MarkMessagesRead(context.Context, ReadMutation) (ReadResult, error)
}

func (e *Engine) ConversationReadState(ctx context.Context, id domain.ID) (domain.ReadState, error) {
	if !validID(id) {
		return domain.ReadState{}, ErrInvalid
	}
	r, ok := e.repo.(ReadRepository)
	if !ok {
		return domain.ReadState{}, ErrProtocolUnavailable
	}
	return r.ConversationReadState(ctx, id)
}
func (e *Engine) MarkMessagesRead(ctx context.Context, key, conversation domain.ID, ids []domain.ID) (ReadResult, error) {
	if !validID(key) || !validID(conversation) || len(ids) < 1 || len(ids) > 100 {
		return ReadResult{}, ErrInvalid
	}
	ids = slices.Clone(ids)
	for _, id := range ids {
		if !validID(id) {
			return ReadResult{}, ErrInvalid
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	r, ok := e.repo.(ReadRepository)
	if !ok {
		return ReadResult{}, ErrProtocolUnavailable
	}
	revision, err := e.newID()
	if err != nil {
		return ReadResult{}, err
	}
	conversationRevision, err := e.newID()
	if err != nil {
		return ReadResult{}, err
	}
	return r.MarkMessagesRead(ctx, ReadMutation{Operation: Operation{ID: key, ResourceID: conversation, Kind: "mark_messages_read", CreatedAt: e.now().UTC()}, Fingerprint: fingerprint("mark_messages_read", struct {
		ConversationID domain.ID
		MessageIDs     []domain.ID
	}{conversation, ids}), ConversationID: conversation, RevisionID: revision, ConversationRevisionID: conversationRevision, MessageIDs: ids})
}
