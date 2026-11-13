package sqlite

import (
	"context"
	"encoding/json"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) ClaimRoomManagement(ctx context.Context, op application.Operation, fingerprint string) (string, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := operation(ctx, q, op, fingerprint, "unknown")
	if err != nil {
		return "", err
	}
	if old != nil {
		var state string
		err = json.Unmarshal([]byte(old.Result), &state)
		return state, err
	}
	return "", tx.Commit()
}

func (s *Store) FinishRoomManagement(ctx context.Context, op application.Operation, state, name string, revision domain.ID) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.SetOperationResult(ctx, dbgen.SetOperationResultParams{
		ID: string(op.ID), ResourceID: string(op.ResourceID), Result: jsonText(state),
	}); err != nil {
		return err
	}
	if name != "" {
		row, err := q.GetConversation(ctx, string(op.ResourceID))
		if err != nil {
			return err
		}
		current, err := decodeConversation(row)
		if err != nil {
			return err
		}
		if current.Title != name {
			next := current
			next.Title = name
			if err := writeConversationDecision(ctx, q, current, next, revision, op.CreatedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
