package sqlite

import (
	"context"
	"encoding/json"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func decodeStar(id, account, conversation string, payload *string) (domain.MessageStar, error) {
	state := domain.MessageStar{MessageID: domain.ID(id), AccountID: domain.ID(account), ConversationID: domain.ID(conversation)}
	if payload == nil {
		return state, nil
	}
	err := json.Unmarshal([]byte(*payload), &state)
	return state, err
}

func readStar(ctx context.Context, q *dbgen.Queries, id domain.ID) (domain.MessageStar, error) {
	row, err := q.GetMessageStar(ctx, string(id))
	if err != nil {
		return domain.MessageStar{}, notFound(err)
	}
	return decodeStar(row.ID, row.AccountID, row.ConversationID, row.Payload)
}

func (s *Store) MessageStar(ctx context.Context, id domain.ID) (domain.MessageStar, error) {
	return readStar(ctx, s.queries, id)
}

func (s *Store) MessageStars(ctx context.Context, conversation domain.ID, ids []domain.ID) ([]domain.MessageStar, error) {
	rows, err := s.queries.ListConversationMessageStars(ctx, dbgen.ListConversationMessageStarsParams{ConversationID: string(conversation), MessageIds: jsonText(ids)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.MessageStar, 0, len(rows))
	for _, row := range rows {
		state, err := decodeStar(row.ID, row.AccountID, row.ConversationID, row.Payload)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

func (s *Store) SetMessageStar(ctx context.Context, in application.StarMutation) (application.StarResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.StarResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return application.StarResult{}, err
	}
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, struct{}{})
	if err != nil {
		return application.StarResult{}, err
	}
	if receipt != nil {
		var state domain.MessageStar
		err = json.Unmarshal([]byte(receipt.Result), &state)
		return application.StarResult{Star: state, Replayed: true}, err
	}
	state, err := readStar(ctx, q, in.MessageID)
	if err != nil {
		return application.StarResult{}, err
	}
	if state.RevisionID != in.ExpectedRevisionID {
		return application.StarResult{}, application.StarConflict{Current: state}
	}
	if state.Starred != in.Starred {
		parents := []domain.ID{}
		if state.RevisionID != "" {
			parents = append(parents, state.RevisionID)
		}
		state.RevisionID = in.RevisionID
		state.Starred = in.Starred
		state.UpdatedAt = in.Operation.CreatedAt
		if err = q.InsertMessageStarRevision(ctx, dbgen.InsertMessageStarRevisionParams{
			ID: string(state.RevisionID), MessageID: string(state.MessageID), Parents: jsonText(parents), Payload: jsonText(state), CreatedAt: stamp(state.UpdatedAt),
		}); err != nil {
			return application.StarResult{}, err
		}
		if err = q.PutMessageStar(ctx, dbgen.PutMessageStarParams{
			MessageID: string(state.MessageID), RevisionID: string(state.RevisionID), Starred: number(state.Starred), Payload: jsonText(state), UpdatedAt: stamp(state.UpdatedAt),
		}); err != nil {
			return application.StarResult{}, err
		}
		if _, err = change(ctx, q, "message.star_changed", state, state.UpdatedAt); err != nil {
			return application.StarResult{}, err
		}
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ID: string(in.Operation.ID), ResourceID: string(state.MessageID), Result: jsonText(state)}); err != nil {
		return application.StarResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.StarResult{}, err
	}
	return application.StarResult{Star: state}, nil
}

func (s *Store) StarredMessages(ctx context.Context, in application.StarQuery) ([]domain.StarredMessage, error) {
	rows, err := s.queries.ListStarredMessages(ctx, dbgen.ListStarredMessagesParams{
		AccountID: string(in.AccountID), ConversationID: string(in.ConversationID), PageCursor: string(in.Cursor), PageLimit: int64(in.Limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.StarredMessage, 0, len(rows))
	for _, row := range rows {
		var item domain.StarredMessage
		for _, part := range []struct {
			payload string
			target  any
		}{{row.MessagePayload, &item.Message}, {row.ConversationPayload, &item.Conversation}, {row.StarPayload, &item.Star}} {
			if err = json.Unmarshal([]byte(part.payload), part.target); err != nil {
				return nil, err
			}
		}
		if err = hydrateReply(ctx, s.queries, &item.Message); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
