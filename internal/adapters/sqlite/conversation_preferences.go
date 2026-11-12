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
	if in.AcceptRequest && conversation.Request != domain.MessageRequest {
		return application.ConversationResult{}, application.ConversationPreferencesConflict{Current: conversation}
	}
	next, changed := applyConversationPreferences(conversation, in)
	if changed {
		if err = writeConversationDecision(ctx, q, conversation, next, in.RevisionID, in.CreatedAt); err != nil {
			return application.ConversationResult{}, err
		}
		conversation = next
		conversation.RevisionID = in.RevisionID
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
		if next.Archived {
			next.DirectHistoryCheck = nil
		}
	}
	if in.Favorite != nil {
		next.Favorite = *in.Favorite
	}
	if in.AcceptRequest {
		next.Request, next.DirectHistoryCheck, next.Archived = "", nil, false
	}
	return next, current.Archived != next.Archived || current.Favorite != next.Favorite || current.Request != next.Request || current.DirectHistoryCheck != next.DirectHistoryCheck
}
