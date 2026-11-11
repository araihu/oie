package application

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

// MentionProvider supplies only observed, room-scoped participants. Protocol
// address interpretation and disclosed-target selection belong to the adapter.
type MentionProvider interface {
	MentionCandidates(context.Context, domain.Account, domain.Conversation) (domain.MentionCandidates, error)
}

func (e *Engine) MentionCandidates(ctx context.Context, conversationID domain.ID) (domain.MentionCandidates, error) {
	c, err := e.Conversation(ctx, conversationID)
	if err != nil {
		return domain.MentionCandidates{}, err
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return domain.MentionCandidates{}, err
	}
	return e.mentionCandidates(ctx, a, c)
}
func (e *Engine) mentionCandidates(ctx context.Context, a domain.Account, c domain.Conversation) (domain.MentionCandidates, error) {
	out := domain.MentionCandidates{ConversationID: c.ID, AccountID: a.ID, State: "unavailable", Items: []domain.MentionCandidate{}}
	if c.Kind != "room" {
		return out, nil
	}
	if provider, ok := e.protocols.(MentionProvider); ok {
		result, err := provider.MentionCandidates(ctx, a, c)
		if err != nil {
			return out, err
		}
		result.ConversationID, result.AccountID = c.ID, a.ID
		return result, nil
	}
	return out, nil
}
func validateMentionSelections(text string, selections []domain.MentionSelection) error {
	if len(selections) > 32 {
		return ErrInvalid
	}
	body := []rune(text)
	last := 0
	for _, selection := range selections {
		if selection.ParticipantID == "" || len(selection.ParticipantID) > 1024 || !utf8.ValidString(selection.ParticipantID) || strings.ContainsRune(selection.ParticipantID, 0) || selection.Begin < last || selection.End <= selection.Begin || selection.End > len(body) {
			return ErrInvalid
		}
		last = selection.End
	}
	return nil
}
func mentionFingerprint(parts []string, selections []domain.MentionSelection) []string {
	if len(selections) == 0 {
		return parts
	}
	data, _ := json.Marshal(selections)
	return append(parts, "mentions", string(data))
}
func (e *Engine) freezeMentions(ctx context.Context, a domain.Account, c domain.Conversation, text string, selections []domain.MentionSelection) ([]domain.MessageMention, error) {
	if len(selections) == 0 {
		return nil, nil
	}
	if c.Kind != "room" {
		return nil, ErrInvalid
	}
	candidates, err := e.mentionCandidates(ctx, a, c)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.MentionCandidate{}
	for _, candidate := range candidates.Items {
		byID[candidate.ID] = candidate
	}
	body := []rune(text)
	mentions := make([]domain.MessageMention, 0, len(selections))
	for _, selection := range selections {
		candidate, ok := byID[selection.ParticipantID]
		selected := string(body[selection.Begin:selection.End])
		if !ok || (selected != strings.TrimPrefix(candidate.InsertText, "@") && selected != candidate.InsertText) {
			return nil, ErrInvalid
		}
		mode, uri := "text", ""
		if candidate.ReferenceMode == "structured" && candidate.URI != "" {
			mode, uri = "structured", candidate.URI
		}
		mentions = append(mentions, domain.MessageMention{MentionSelection: selection, ReferenceMode: mode, URI: uri})
	}
	return mentions, nil
}
