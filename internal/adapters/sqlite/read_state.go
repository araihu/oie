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
	var ids []domain.ID
	if in.Latest.MessageID != "" {
		if c.Request != "" || c.RevisionID != in.Latest.ConversationRevisionID {
			return application.ReadResult{}, application.ErrStateConflict
		}
		latest, lookupErr := q.LatestLogicalMessage(ctx, string(c.ID))
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return application.ReadResult{}, lookupErr
		}
		if latest.ID != string(in.Latest.MessageID) {
			return application.ReadResult{}, application.ErrStateConflict
		}
		marked, markErr := q.InsertConversationMessageReads(ctx, dbgen.InsertConversationMessageReadsParams{ConversationID: string(c.ID), RevisionID: string(in.RevisionID)})
		if markErr != nil {
			return application.ReadResult{}, markErr
		}
		ids = make([]domain.ID, 0, len(marked))
		for _, id := range marked {
			ids = append(ids, domain.ID(id))
		}
	} else {
		ids, err = insertObservedReads(ctx, q, c, in)
		if err != nil {
			return application.ReadResult{}, err
		}
	}
	summary, err := q.ConversationUnreadSummary(ctx, string(c.ID))
	if err != nil {
		return application.ReadResult{}, err
	}
	unread, unreadMention := summary.UnreadCount, summary.UnreadMention != 0
	if len(ids) > 0 || int(unread) != c.UnreadCount || unreadMention != c.UnreadMention {
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
		c.UnreadMention = unreadMention
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

func insertObservedReads(ctx context.Context, q *dbgen.Queries, c domain.Conversation, in application.ReadMutation) ([]domain.ID, error) {
	ids := make([]domain.ID, 0, len(in.MessageIDs))
	for _, id := range in.MessageIDs {
		canonical, err := resolveMessageAlias(ctx, q, id)
		if err != nil {
			return nil, err
		}
		row, err := q.GetMessage(ctx, string(canonical))
		if err != nil {
			return nil, notFound(err)
		}
		if row.ConversationID != string(c.ID) || row.AccountID != string(c.AccountID) {
			return nil, application.ErrInvalid
		}
		m, err := decodeMessage(row)
		if err != nil {
			return nil, err
		}
		if m.IsOwn || m.Historical {
			continue
		}
		read, err := q.HasMessageRead(ctx, string(canonical))
		if err != nil {
			return nil, err
		}
		if read {
			continue
		}
		count, err := q.InsertMessageRead(ctx, dbgen.InsertMessageReadParams{MessageID: string(canonical), RevisionID: string(in.RevisionID)})
		if err != nil {
			return nil, err
		}
		if count > 0 {
			ids = append(ids, canonical)
		}
	}
	return ids, nil
}
