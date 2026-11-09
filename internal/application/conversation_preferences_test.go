package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type preferencesSpyRepository struct {
	Repository
	calls []ConversationPreferencesMutation
}

func (s *preferencesSpyRepository) UpdateConversationPreferences(_ context.Context, in ConversationPreferencesMutation) (ConversationResult, error) {
	s.calls = append(s.calls, in)
	return ConversationResult{Conversation: domain.Conversation{ID: in.ConversationID, AccountID: in.AccountID}}, nil
}
func preferenceFlag(value bool) *bool { return &value }

func TestConversationPreferencesValidateAndFingerprintFieldPresence(t *testing.T) {
	ids := []domain.ID{"019a5df0-0000-7000-8000-000000000001", "019a5df0-0000-7000-8000-000000000002", "019a5df0-0000-7000-8000-000000000003", "019a5df0-0000-7000-8000-000000000004", "019a5df0-0000-7000-8000-000000000005"}
	spy := &preferencesSpyRepository{}
	e := &Engine{repo: spy, newID: func() (domain.ID, error) { return ids[4], nil }, now: func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.FixedZone("device", -3*3600)) }}
	input := ConversationPreferencesInput{AccountID: ids[2], ExpectedRevisionID: ids[3], Archived: preferenceFlag(true)}
	result, err := e.UpdateConversationPreferences(t.Context(), ids[0], ids[1], input)
	if err != nil || result.Conversation.ID != ids[1] || len(spy.calls) != 1 {
		t.Fatal(result, err)
	}
	first := spy.calls[0]
	if first.Kind != "update_conversation_preferences" || first.CreatedAt.Location() != time.UTC || first.ResourceID != ids[1] || first.RevisionID != ids[4] {
		t.Fatal(first)
	}
	*input.Archived = false
	if !*first.Archived {
		t.Fatal("accepted flag retained caller-owned pointer")
	}
	variations := []ConversationPreferencesInput{
		{AccountID: ids[2], ExpectedRevisionID: ids[3], Archived: preferenceFlag(true)},
		{AccountID: ids[2], ExpectedRevisionID: ids[3], Archived: preferenceFlag(true), Favorite: preferenceFlag(false)},
		{AccountID: ids[2], ExpectedRevisionID: ids[3], Archived: preferenceFlag(false)},
		{AccountID: ids[1], ExpectedRevisionID: ids[3], Archived: preferenceFlag(true)},
		{AccountID: ids[2], ExpectedRevisionID: ids[4], Archived: preferenceFlag(true)},
	}
	seen := map[string]bool{}
	for i, variation := range variations {
		if _, err := e.UpdateConversationPreferences(t.Context(), ids[0], ids[1], variation); err != nil {
			t.Fatal(err)
		}
		hash := spy.calls[len(spy.calls)-1].Fingerprint
		if i == 0 && hash != first.Fingerprint {
			t.Fatal("equivalent values did not produce stable fingerprint")
		}
		if seen[hash] {
			t.Fatal("field omission, false, scope or precondition shared fingerprint", i)
		}
		seen[hash] = true
	}
	for _, bad := range []struct {
		key, conversation domain.ID
		input             ConversationPreferencesInput
	}{
		{"bad", ids[1], variations[0]}, {ids[0], "bad", variations[0]},
		{ids[0], ids[1], ConversationPreferencesInput{AccountID: "bad", ExpectedRevisionID: ids[3], Archived: preferenceFlag(true)}},
		{ids[0], ids[1], ConversationPreferencesInput{AccountID: ids[2], ExpectedRevisionID: "bad", Archived: preferenceFlag(true)}},
		{ids[0], ids[1], ConversationPreferencesInput{AccountID: ids[2], ExpectedRevisionID: ids[3]}},
	} {
		before := len(spy.calls)
		if _, err := e.UpdateConversationPreferences(t.Context(), bad.key, bad.conversation, bad.input); !errors.Is(err, ErrInvalid) || len(spy.calls) != before {
			t.Fatal("invalid preference called repository", bad, err)
		}
	}
}
