package application

import (
	"context"
	"slices"

	"github.com/araihu/oie/internal/domain"
)

type ReadMutation struct {
	Operation                                          Operation
	Fingerprint                                        string
	ConversationID, RevisionID, ConversationRevisionID domain.ID
	MessageIDs                                         []domain.ID
	Latest                                             ReadLatest
}

type ReadLatest struct {
	MessageID, ConversationRevisionID domain.ID
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
	intent := struct {
		ConversationID domain.ID
		MessageIDs     []domain.ID
	}{conversation, ids}
	return e.markRead(ctx, key, "mark_messages_read", intent, ReadMutation{ConversationID: conversation, MessageIDs: ids})
}

func (e *Engine) MarkLatestRead(ctx context.Context, key, conversation domain.ID, latest ReadLatest) (ReadResult, error) {
	if !validID(key) || !validID(conversation) || !validID(latest.MessageID) || !validID(latest.ConversationRevisionID) {
		return ReadResult{}, ErrInvalid
	}
	intent := struct {
		ConversationID domain.ID
		ReadLatest
	}{conversation, latest}
	return e.markRead(ctx, key, "mark_latest_read", intent, ReadMutation{ConversationID: conversation, Latest: latest})
}

func (e *Engine) markRead(ctx context.Context, key domain.ID, kind string, intent any, in ReadMutation) (ReadResult, error) {
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
	in.Operation = Operation{ID: key, ResourceID: in.ConversationID, Kind: kind, CreatedAt: e.now().UTC()}
	in.Fingerprint = fingerprint(kind, intent)
	in.RevisionID = revision
	in.ConversationRevisionID = conversationRevision
	return r.MarkMessagesRead(ctx, in)
}
