package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func readState(ctx context.Context, q *dbgen.Queries, c domain.Conversation) (domain.ReadState, error) {
	state := domain.ReadState{ConversationID: c.ID, AccountID: c.AccountID, UnreadCount: c.UnreadCount}
	head, err := q.GetConversationReadHead(ctx, string(c.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.RevisionID = domain.ID(head.RevisionID)
	state.UpdatedAt, err = time.Parse(time.RFC3339Nano, head.UpdatedAt)
	return state, err
}
func (s *Store) ConversationReadState(ctx context.Context, id domain.ID) (domain.ReadState, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.ReadState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := q.GetConversation(ctx, string(id))
	if err != nil {
		return domain.ReadState{}, notFound(err)
	}
	c, err := decodeConversation(row)
	if err != nil {
		return domain.ReadState{}, err
	}
	return readState(ctx, q, c)
}
func (s *Store) MarkMessagesRead(ctx context.Context, in application.ReadMutation) (application.ReadResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.ReadResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return application.ReadResult{}, err
	}
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, struct{}{})
	if err != nil {
		return application.ReadResult{}, err
	}
	if receipt != nil {
		var state domain.ReadState
		err = json.Unmarshal([]byte(receipt.Result), &state)
		return application.ReadResult{State: state, Replayed: true}, err
	}
	row, err := q.GetConversation(ctx, string(in.ConversationID))
	if err != nil {
		return application.ReadResult{}, notFound(err)
	}
	c, err := decodeConversation(row)
	if err != nil {
		return application.ReadResult{}, err
	}
	state, err := readState(ctx, q, c)
	if err != nil {
		return application.ReadResult{}, err
	}
	ids := make([]domain.ID, 0, len(in.MessageIDs))
	for _, id := range in.MessageIDs {
		canonical, resolveErr := resolveMessageAlias(ctx, q, id)
		if resolveErr != nil {
			return application.ReadResult{}, resolveErr
		}
		mrow, getErr := q.GetMessage(ctx, string(canonical))
		if getErr != nil {
			return application.ReadResult{}, notFound(getErr)
		}
		if mrow.ConversationID != string(c.ID) || mrow.AccountID != string(c.AccountID) {
			return application.ReadResult{}, application.ErrInvalid
		}
		m, decodeErr := decodeMessage(mrow)
		if decodeErr != nil {
			return application.ReadResult{}, decodeErr
		}
		if m.IsOwn || m.Historical {
			continue
		}
		read, readErr := q.HasMessageRead(ctx, string(canonical))
		if readErr != nil {
			return application.ReadResult{}, readErr
		}
		if read {
			continue
		}
		count, insertErr := q.InsertMessageRead(ctx, dbgen.InsertMessageReadParams{MessageID: string(canonical), RevisionID: string(in.RevisionID)})
		if insertErr != nil {
			return application.ReadResult{}, insertErr
		}
		if count > 0 {
			ids = append(ids, canonical)
		}
	}
	unread, err := q.CountConversationUnread(ctx, string(c.ID))
	if err != nil {
		return application.ReadResult{}, err
	}
	if len(ids) > 0 || int(unread) != c.UnreadCount {
		parents := []domain.ID{}
		if state.RevisionID != "" {
			parents = append(parents, state.RevisionID)
		}
		state.RevisionID = in.RevisionID
		state.UpdatedAt = in.Operation.CreatedAt
		state.UnreadCount = int(unread)
		if err = q.InsertReadRevision(ctx, dbgen.InsertReadRevisionParams{ID: string(in.RevisionID), ConversationID: string(c.ID), Parents: jsonText(parents), Payload: jsonText(state), MessageIds: jsonText(ids), CreatedAt: stamp(state.UpdatedAt)}); err != nil {
			return application.ReadResult{}, err
		}
		if err = q.PutConversationReadHead(ctx, dbgen.PutConversationReadHeadParams{ConversationID: string(c.ID), RevisionID: string(in.RevisionID), UpdatedAt: stamp(state.UpdatedAt)}); err != nil {
			return application.ReadResult{}, err
		}
		conversationParents := jsonText([]domain.ID{c.RevisionID})
		c.RevisionID = in.ConversationRevisionID
		c.UnreadCount = int(unread)
		if err = q.UpdateConversation(ctx, dbgen.UpdateConversationParams{RevisionID: string(c.RevisionID), Payload: jsonText(c), ActivitySequence: c.ActivitySequence, UnreadCount: unread, ID: string(c.ID)}); err != nil {
			return application.ReadResult{}, err
		}
		if err = q.InsertConversationRevision(ctx, dbgen.InsertConversationRevisionParams{ID: string(c.RevisionID), EntityID: string(c.ID), Parents: conversationParents, Payload: jsonText(c), CreatedAt: stamp(state.UpdatedAt)}); err != nil {
			return application.ReadResult{}, err
		}
		if _, err = change(ctx, q, "conversation.updated", c, state.UpdatedAt); err != nil {
			return application.ReadResult{}, err
		}
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ID: string(in.Operation.ID), ResourceID: string(c.ID), Result: jsonText(state)}); err != nil {
		return application.ReadResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.ReadResult{}, err
	}
	return application.ReadResult{State: state}, nil
}
