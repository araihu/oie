package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

// ConversationPreferencesInput is a field-isolated app-owned change. Nil flags
// are omitted, rather than false; at least one flag must be provided.
type ConversationPreferencesInput struct {
	AccountID, ExpectedRevisionID domain.ID
	Archived, Favorite, Read      *bool
	AcceptRequest                 bool
}

type ConversationPreferencesMutation struct {
	Operation
	Fingerprint                                               string
	ConversationID, AccountID, ExpectedRevisionID, RevisionID domain.ID
	Archived, Favorite, Read                                  *bool
	ReadRevisionID                                            domain.ID
	AcceptRequest                                             bool
}

type ConversationPreferencesRepository interface {
	UpdateConversationPreferences(context.Context, ConversationPreferencesMutation) (ConversationResult, error)
}

type ConversationPreferencesConflict struct{ Current domain.Conversation }

func (ConversationPreferencesConflict) Error() string { return ErrStateConflict.Error() }
func (ConversationPreferencesConflict) Unwrap() error { return ErrStateConflict }

func copyPreference(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// UpdateConversationPreferences changes only supplied flags. The repository
// serializes its receipt, precondition and projection updates in one transaction.
// A committed Mark as read may queue a best-effort displayed marker.
func (e *Engine) UpdateConversationPreferences(ctx context.Context, key, conversationID domain.ID, in ConversationPreferencesInput) (ConversationResult, error) {
	if !validID(key) || !validID(conversationID) || !validID(in.AccountID) || !validID(in.ExpectedRevisionID) || (in.Archived == nil && in.Favorite == nil && in.Read == nil && !in.AcceptRequest) || ((in.AcceptRequest || in.Read != nil) && (in.Archived != nil || in.Favorite != nil)) || (in.AcceptRequest && in.Read != nil) {
		return ConversationResult{}, ErrInvalid
	}
	repo, ok := e.repo.(ConversationPreferencesRepository)
	if !ok {
		return ConversationResult{}, ErrProtocolUnavailable
	}
	archived, favorite := copyPreference(in.Archived), copyPreference(in.Favorite)
	revision, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	var readRevision domain.ID
	if in.Read != nil && *in.Read {
		readRevision, err = e.newID()
		if err != nil {
			return ConversationResult{}, err
		}
	}
	const kind = "update_conversation_preferences"
	requestFingerprint := fingerprint(kind, struct {
		AccountID, ConversationID, ExpectedRevisionID domain.ID
		Archived, Favorite                            *bool
	}{in.AccountID, conversationID, in.ExpectedRevisionID, archived, favorite})
	if in.AcceptRequest {
		requestFingerprint = fingerprint(kind, []string{string(in.AccountID), string(conversationID), string(in.ExpectedRevisionID), "accept"})
	}
	read := copyPreference(in.Read)
	if read != nil {
		requestFingerprint = fingerprint(kind, struct {
			AccountID, ConversationID, ExpectedRevisionID domain.ID
			Read                                          bool
		}{in.AccountID, conversationID, in.ExpectedRevisionID, *read})
	}
	result, err := repo.UpdateConversationPreferences(ctx, ConversationPreferencesMutation{
		Operation:      Operation{ID: key, ResourceID: conversationID, Kind: kind, CreatedAt: e.now().UTC()},
		Fingerprint:    requestFingerprint,
		ConversationID: conversationID, AccountID: in.AccountID, ExpectedRevisionID: in.ExpectedRevisionID, RevisionID: revision, Archived: archived, Favorite: favorite, AcceptRequest: in.AcceptRequest, Read: read, ReadRevisionID: readRevision,
	})
	if err == nil && (in.AcceptRequest || read != nil) && !result.Replayed {
		e.notifyEvents()
		e.queueDisplayedMarker(result.DisplayedMarker)
	}
	return result, err
}
