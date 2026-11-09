package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/icon/heroicons"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// A command receipt can describe an older state. Only the following canonical
// read is projected, and transport uncertainty never becomes inferred success.
func (s *Server) changeConversationPreferences(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id, account, key, expected := r.Form.Get("conversationId"), r.Form.Get("accountId"), r.Form.Get("operationKey"), r.Form.Get("expectedRevisionId")
	for _, field := range []string{"engineId", "conversationId", "accountId", "operationKey", "expectedRevisionId"} {
		if len(r.Form[field]) != 1 || !validOperationKey(r.Form.Get(field)) {
			return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
		}
	}
	input := engineapi.UpdateConversationPreferences{AccountId: account, ExpectedRevisionId: expected}
	count := 0
	for _, field := range []string{"archived", "favorite"} {
		if !r.Form.Has(field) {
			continue
		}
		count++
		value := r.Form.Get(field)
		if len(r.Form[field]) != 1 || (value != "true" && value != "false") {
			return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
		}
		desired := value == "true"
		if field == "archived" {
			input.Archived = &desired
		} else {
			input.Favorite = &desired
		}
	}
	if count != 1 {
		return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	owner, err := s.client.GetConversationWithResponse(ctx, id)
	if err != nil || owner.JSON200 == nil {
		if err == nil && owner.StatusCode() == 404 {
			return 404, mutationResult{Message: text.ConversationPreferenceUnavailable, Rejected: true}
		}
		return 503, mutationResult{Message: text.ConversationPreferenceUncertain, PreferencesPending: true}
	}
	if owner.JSON200.AccountId != account {
		return 400, mutationResult{Message: text.OperationConflict, Rejected: true}
	}
	if !validPreferenceConversation(*owner.JSON200, id, account) {
		return 503, mutationResult{Message: text.ConversationPreferenceUncertain, PreferencesPending: true}
	}
	response, writeErr := s.client.UpdateConversationPreferencesWithResponse(ctx, id, &engineapi.UpdateConversationPreferencesParams{IdempotencyKey: key}, input)
	// Always reread, including a successful idempotent replay and a lost reply.
	current, readErr := s.client.GetConversationWithResponse(ctx, id)
	var conversation *engineapi.Conversation
	if readErr == nil && current.JSON200 != nil && validPreferenceConversation(*current.JSON200, id, account) {
		conversation = current.JSON200
	}
	if writeErr != nil {
		return 503, mutationResult{Message: text.ConversationPreferenceUncertain, Code: "preference_unknown", Conversation: conversation, PreferencesPending: true}
	}
	if response.StatusCode() == 409 {
		message, code := text.ConversationPreferenceConflict, "state_conflict"
		var problem engineapi.Problem
		if json.Unmarshal(response.Body, &problem) == nil && problem.Code == "idempotency_conflict" {
			message, code = text.OperationConflict, "idempotency_conflict"
		}
		return 409, mutationResult{Message: message, Code: code, Conversation: conversation, Rejected: true, NextKey: newOperationKey()}
	}
	if response.JSON200 == nil {
		if response.StatusCode() == 400 || response.StatusCode() == 403 || response.StatusCode() == 404 || response.StatusCode() == 405 || response.StatusCode() == 422 {
			return response.StatusCode(), mutationResult{Message: text.ConversationPreferenceUnavailable, Conversation: conversation, Rejected: true, NextKey: newOperationKey()}
		}
		return 503, mutationResult{Message: text.ConversationPreferenceUncertain, Conversation: conversation, PreferencesPending: true}
	}
	if !validPreferenceConversation(*response.JSON200, id, account) || conversation == nil {
		return 503, mutationResult{Message: text.ConversationPreferenceUncertain, Code: "preference_unknown", PreferencesPending: true}
	}
	return 200, mutationResult{OK: true, Message: text.ConversationPreferenceUpdated, Conversation: conversation, NextKey: newOperationKey()}
}
func validPreferenceConversation(c engineapi.Conversation, id, account string) bool {
	return c.Id == id && c.AccountId == account && validOperationKey(c.RevisionId)
}
func (v view) conversationPreferenceLabel(field string) string {
	if field == "archived" {
		if v.Selected.Archived {
			return v.Text.UnarchiveConversation
		}
		return v.Text.ArchiveConversation
	}
	if v.Selected.Favorite {
		return v.Text.UnfavoriteConversation
	}
	return v.Text.FavoriteConversation
}
func (v view) conversationPreferenceSection() dropdown.Section {
	id := v.Selected.Id
	return dropdown.Section{Items: []dropdown.Item{
		{ID: "archive-conversation-" + id, Label: v.Text.ArchiveConversation, Icon: glyph(heroicons.Icon16SolidFolder), OnClick: "$dispatch('oie:conversation-preference', {field: 'archived'})", Disabled: v.Preview},
		{ID: "unarchive-conversation-" + id, Label: v.Text.UnarchiveConversation, Icon: glyph(heroicons.Icon16SolidFolder), OnClick: "$dispatch('oie:conversation-preference', {field: 'archived'})", Disabled: v.Preview},
		{ID: "favorite-conversation-" + id, Label: v.Text.FavoriteConversation, Icon: glyph(heroicons.Icon16SolidStar), OnClick: "$dispatch('oie:conversation-preference', {field: 'favorite'})", Disabled: v.Preview},
		{ID: "unfavorite-conversation-" + id, Label: v.Text.UnfavoriteConversation, Icon: glyph(heroicons.Icon16SolidStar), OnClick: "$dispatch('oie:conversation-preference', {field: 'favorite'})", Disabled: v.Preview},
		{ID: "retry-archive-conversation-" + id, Label: v.Text.RetryConversationPreference, Icon: glyph(heroicons.Icon16SolidArrowPath), OnClick: "$dispatch('oie:conversation-preference', {field: 'archived'})", Disabled: v.Preview},
		{ID: "retry-favorite-conversation-" + id, Label: v.Text.RetryConversationPreference, Icon: glyph(heroicons.Icon16SolidArrowPath), OnClick: "$dispatch('oie:conversation-preference', {field: 'favorite'})", Disabled: v.Preview},
	}}
}
func preferenceFormID(id, field string) string {
	suffix := "favorite"
	if field == "archived" {
		suffix = "archive"
	}
	return "conversation-preferences-" + id + "-" + suffix
}
func (v view) preferenceDesired(field string) string {
	if field == "archived" {
		return strconv.FormatBool(!v.Selected.Archived)
	}
	return strconv.FormatBool(!v.Selected.Favorite)
}

// Implicit desktop selection is still the action's selected conversation after
// archiving removes its inbox row. Keep native PRG and recovery on that UUID.
func preferenceDestination(r *http.Request) string {
	target, _ := url.Parse(safeReturn(r.Form.Get("returnTo")))
	query := target.Query()
	query.Set("chat", r.Form.Get("conversationId"))
	target.RawQuery = query.Encode()
	return target.String()
}
