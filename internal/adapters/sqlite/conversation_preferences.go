package sqlite

import (
	"context"
	"encoding/json"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) UpdateConversationPreferences(ctx context.Context, in application.ConversationPreferencesMutation) (application.ConversationResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.ConversationResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return application.ConversationResult{}, err
	}
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, struct{}{})
	if err != nil {
		return application.ConversationResult{}, err
	}
	if receipt != nil {
		var conversation domain.Conversation
		err = json.Unmarshal([]byte(receipt.Result), &conversation)
		return application.ConversationResult{Conversation: conversation, Replayed: true}, err
	}
	row, err := q.GetConversation(ctx, string(in.ConversationID))
	if err != nil {
		return application.ConversationResult{}, notFound(err)
	}
	if row.AccountID != string(in.AccountID) {
		return application.ConversationResult{}, application.ErrInvalid
	}
	conversation, err := decodeConversation(row)
	if err != nil {
		return application.ConversationResult{}, err
	}
	if conversation.RevisionID != in.ExpectedRevisionID {
		return application.ConversationResult{}, application.ConversationPreferencesConflict{Current: conversation}
	}
	next, changed := applyConversationPreferences(conversation, in)
	if changed {
		parents := jsonText([]domain.ID{conversation.RevisionID})
		conversation = next
		conversation.RevisionID = in.RevisionID
		count, err := q.UpdateConversationPreferences(ctx, dbgen.UpdateConversationPreferencesParams{ID: string(conversation.ID), AccountID: string(in.AccountID), ExpectedRevisionID: string(in.ExpectedRevisionID), RevisionID: string(conversation.RevisionID), Payload: jsonText(conversation), Archived: number(conversation.Archived), Favorite: number(conversation.Favorite)})
		if err != nil {
			return application.ConversationResult{}, err
		}
		if count != 1 {
			return application.ConversationResult{}, conversationPreferencesConflict(ctx, q, in.ConversationID)
		}
		if err = q.InsertConversationRevision(ctx, dbgen.InsertConversationRevisionParams{ID: string(conversation.RevisionID), EntityID: string(conversation.ID), Parents: parents, Payload: jsonText(conversation), CreatedAt: stamp(in.CreatedAt)}); err != nil {
			return application.ConversationResult{}, err
		}
		if _, err = change(ctx, q, "conversation.updated", conversation, in.CreatedAt); err != nil {
			return application.ConversationResult{}, err
		}
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ID: string(in.ID), ResourceID: string(conversation.ID), Result: jsonText(conversation)}); err != nil {
		return application.ConversationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.ConversationResult{}, err
	}
	return application.ConversationResult{Conversation: conversation}, nil
}

func applyConversationPreferences(current domain.Conversation, in application.ConversationPreferencesMutation) (domain.Conversation, bool) {
	next := current
	if in.Archived != nil {
		next.Archived = *in.Archived
	}
	if in.Favorite != nil {
		next.Favorite = *in.Favorite
	}
	return next, current.Archived != next.Archived || current.Favorite != next.Favorite
}

func conversationPreferencesConflict(ctx context.Context, q *dbgen.Queries, id domain.ID) error {
	row, err := q.GetConversation(ctx, string(id))
	if err != nil {
		return notFound(err)
	}
	current, err := decodeConversation(row)
	if err != nil {
		return err
	}
	return application.ConversationPreferencesConflict{Current: current}
}
