package sqlite

import (
	"context"
	"encoding/json"
	"errors"

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

func (s *Store) FinishRoomManagement(ctx context.Context, op application.Operation, state, name string, revision domain.ID, affiliation string, target domain.RoomAffiliationTarget, destruction *application.RoomDestruction) error {
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
	if state == "confirmed" && destruction != nil {
		if err := finishRoomDestruction(ctx, q, op, revision, *destruction); err != nil {
			return err
		}
	}
	if state == "confirmed" && target.Address != "" {
		if affiliation == "outcast" && target.Nickname != "" {
			err = q.PutRoomBannedNickname(ctx, dbgen.PutRoomBannedNicknameParams{
				ConversationID: string(op.ResourceID), Address: target.Address, Nickname: target.Nickname,
			})
		} else {
			err = q.RemoveRoomBannedNickname(ctx, dbgen.RemoveRoomBannedNicknameParams{
				ConversationID: string(op.ResourceID), Address: target.Address,
			})
		}
		if err != nil {
			return err
		}
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
		if current.Title != name || current.TitleFromRoomProfile {
			if err := writeRoomTitle(ctx, q, current, name, false, revision, op.CreatedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func finishRoomDestruction(ctx context.Context, q *dbgen.Queries, op application.Operation, revision domain.ID, destruction application.RoomDestruction) error {
	intent, err := revokeRoomIntent(ctx, q, op.ResourceID, destruction.ExpectedIntentRevision, destruction.IntentRevision, "room_destroyed", op.CreatedAt)
	switch {
	case errors.Is(err, application.ErrNotFound):
		return nil
	case errors.Is(err, application.ErrStateConflict):
		// The destruction notice can revoke the intent before the IQ reply.
		// Preserve a later entry or an independent leave.
		if intent.Active || intent.LastErrorCode != "room_destroyed" {
			return nil
		}
	case err != nil:
		return err
	}
	current, err := readConversation(ctx, q, op.ResourceID)
	if err != nil {
		return err
	}
	if current.RemovedFromChats {
		return nil
	}
	next := current
	next.RemovedFromChats = true
	return writeConversationDecision(ctx, q, current, next, revision, op.CreatedAt)
}

func (s *Store) RoomBannedNicknames(ctx context.Context, id domain.ID) (map[string]string, error) {
	rows, err := s.read.ListRoomBannedNicknames(ctx, string(id))
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.Address] = row.Nickname
	}
	return names, nil
}
