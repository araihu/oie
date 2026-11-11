package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/goshtoso/components/popover"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed reactions.js
var reactionsScript []byte

//go:embed reactions.css
var reactionsStylesheet []byte

type reactionPreparation struct {
	EngineID     string                     `json:"engineId"`
	Snapshot     engineapi.MessageReactions `json:"snapshot"`
	OperationKey string                     `json:"operationKey"`
}
type reactionResult struct {
	OK       bool                              `json:"ok"`
	Accepted bool                              `json:"accepted"`
	Rejected bool                              `json:"rejected"`
	Message  string                            `json:"message"`
	Code     string                            `json:"code,omitempty"`
	Command  *engineapi.MessageReactionCommand `json:"command,omitempty"`
}

func validReactionSet(items []string) bool {
	if len(items) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, emoji := range items {
		if emoji == "" || !utf8.ValidString(emoji) || len(emoji) > 128 || strings.ContainsRune(emoji, 0) || seen[emoji] {
			return false
		}
		seen[emoji] = true
	}
	return true
}
func reactionRevision(id *engineapi.NullableReactionRevision) string {
	if id != nil {
		return string(*id)
	}
	return ""
}

var opaqueReactionActor = regexp.MustCompile(`^occupant:[0-9a-f]{64}$`)

func validReactions(value engineapi.MessageReactions, account, conversation, message string) bool {
	if value.AccountId != account || value.ConversationId != conversation || value.MessageId != message || value.MaxEmojis < 1 || value.MaxEmojis > 8 || !validReactionSet(value.OwnEmojis) {
		return false
	}
	for _, id := range []*engineapi.NullableReactionRevision{value.RevisionId, value.OwnRevisionId} {
		if id != nil && !validOperationKey(string(*id)) {
			return false
		}
	}
	seen := map[string]bool{}
	own := map[string]bool{}
	for _, e := range value.OwnEmojis {
		own[e] = true
	}
	for _, item := range value.Items {
		if !validReactionSet([]string{item.Emoji}) || item.Count < 1 || seen[item.Emoji] || item.Own != own[item.Emoji] {
			return false
		}
		seen[item.Emoji] = true
	}
	for _, e := range value.OwnEmojis {
		if !seen[e] {
			return false
		}
	}
	if value.Actors != nil {
		actors, totals := map[string]bool{}, map[string]int{}
		ownActors := 0
		for _, actor := range *value.Actors {
			if strings.HasPrefix(actor.Address, "occupant:") &&
				(!opaqueReactionActor.MatchString(actor.Address) || actor.AvatarHash != nil) {
				return false
			}
			if actor.Address == "" || len(actor.Address) > 1023 || !utf8.ValidString(actor.Address) || strings.IndexFunc(actor.Address, unicode.IsControl) >= 0 || actors[actor.Address] || strings.TrimSpace(actor.DisplayName) == "" || len(actor.DisplayName) > 1023 || !utf8.ValidString(actor.DisplayName) || strings.IndexFunc(actor.DisplayName, unicode.IsControl) >= 0 || len(actor.Emojis) == 0 || !validReactionSet(actor.Emojis) || actor.AvatarHash != nil && !validAvatarHash(*actor.AvatarHash) {
				return false
			}
			actors[actor.Address] = true
			if actor.Own {
				ownActors++
				if len(actor.Emojis) != len(value.OwnEmojis) {
					return false
				}
				for _, emoji := range actor.Emojis {
					if !own[emoji] {
						return false
					}
				}
			}
			for _, emoji := range actor.Emojis {
				totals[emoji]++
			}
		}
		if ownActors > 1 || (len(value.OwnEmojis) > 0) != (ownActors == 1) || len(totals) != len(value.Items) {
			return false
		}
		for _, item := range value.Items {
			if totals[item.Emoji] != item.Count {
				return false
			}
		}
	}
	if value.AllowedEmojis != nil {
		if len(*value.AllowedEmojis) > 256 {
			return false
		}
		seenAllowed := map[string]bool{}
		for _, emoji := range *value.AllowedEmojis {
			if !validReactionSet([]string{emoji}) || seenAllowed[emoji] {
				return false
			}
			seenAllowed[emoji] = true
		}
	}
	return true
}
func (s *Server) loadReactions(ctx context.Context, v *view) {
	if v.Preview || v.Selected == nil || len(v.Messages) == 0 {
		return
	}
	found := map[string]engineapi.MessageReactions{}
	wanted := map[string]bool{}
	ids := make([]string, 0, len(v.Messages))
	for _, m := range v.Messages {
		ids = append(ids, m.Id)
		wanted[m.Id] = true
	}
	for start := 0; start < len(ids); start += 100 {
		response, err := s.client.ListConversationReactionsWithResponse(ctx, v.Selected.Id, &engineapi.ListConversationReactionsParams{MessageId: ids[start:min(start+100, len(ids))]})
		if err != nil || response.JSON200 == nil {
			return
		}
		for _, item := range response.JSON200.Items {
			if !wanted[item.MessageId] || !validReactions(item, v.Selected.AccountId, v.Selected.Id, item.MessageId) {
				return
			}
			if _, duplicate := found[item.MessageId]; duplicate {
				return
			}
			found[item.MessageId] = item
		}
	}
	if len(found) == len(wanted) {
		v.Reactions = found
	}
}
func (v view) reactionStateJSON(id string) string {
	if state, ok := v.Reactions[id]; ok {
		data, _ := json.Marshal(state)
		return string(data)
	}
	return "null"
}
func (v view) reactionAllowed(id string) bool {
	state, ok := v.Reactions[id]
	return ok && state.Eligible && state.PendingCommand == nil && !v.Preview
}
func (s *Server) reactionScope(ctx context.Context, engine, account, conversation, message string) (int, bool) {
	for _, id := range []string{engine, account, conversation, message} {
		if !validOperationKey(id) {
			return 400, false
		}
	}
	owner, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || owner.JSON200 == nil {
		return 503, false
	}
	if owner.JSON200.Id != engine {
		return 409, false
	}
	if owner.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		return 409, false
	}
	source, err := s.client.GetMessageWithResponse(ctx, message)
	if err != nil || source.JSON200 == nil {
		return 404, false
	}
	if source.JSON200.AccountId != account || source.JSON200.ConversationId != conversation || source.JSON200.Id != message {
		return 404, false
	}
	return 200, true
}
func reactionJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) prepareReactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	v := s.baseView(r)
	q := r.URL.Query()
	engine, account, conversation, message := q.Get("engineId"), q.Get("accountId"), q.Get("conversationId"), q.Get("messageId")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if status, ok := s.reactionScope(ctx, engine, account, conversation, message); !ok {
		reactionJSON(w, status, reactionResult{Message: v.Text.ReactionUnavailable, Rejected: true})
		return
	}
	response, err := s.client.GetMessageReactionsWithResponse(ctx, message)
	if err != nil || response.JSON200 == nil || !validReactions(*response.JSON200, account, conversation, message) {
		reactionJSON(w, 503, reactionResult{Message: v.Text.ReactionUnavailable, Rejected: true})
		return
	}
	reactionJSON(w, 200, reactionPreparation{EngineID: engine, Snapshot: *response.JSON200, OperationKey: newOperationKey()})
}
func sameReactionSet(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
func validReactionCommand(command *engineapi.MessageReactionCommand, form url.Values, emojis []string) bool {
	if command == nil || command.Id != form.Get("operationKey") || command.MessageId != form.Get("messageId") || command.AccountId != form.Get("accountId") || command.ConversationId != form.Get("conversationId") || reactionRevision(command.ExpectedOwnRevisionId) != form.Get("expectedOwnRevisionId") || !sameReactionSet(command.Emojis, emojis) || !validOperationKey(command.RevisionId) || command.CreatedAt.IsZero() || command.UpdatedAt.IsZero() {
		return false
	}
	switch command.State {
	case "queued", "dispatching", "submitted", "unknown", "failed":
		return true
	}
	return false
}
func reactionCommandResult(command *engineapi.MessageReactionCommand, text copy) reactionResult {
	message := ""
	if command.State == "unknown" {
		message = text.ReactionUnknown
	}
	if command.State == "failed" {
		message = text.ReactionFailed
	}
	return reactionResult{OK: true, Accepted: true, Message: message, Command: command}
}
func (s *Server) setReactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	v := s.baseView(r)
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	result := reactionResult{Message: v.Text.ReactionUnknown}
	respond := func(status int) { reactionJSON(w, status, result) }
	if err := r.ParseForm(); err != nil {
		result.Rejected = true
		result.Message = v.Text.InvalidForm
		respond(400)
		return
	}
	if text, ok := s.catalogs[r.Form.Get("lang")]; ok {
		v.Text = text
	}
	result.Message = v.Text.ReactionUnknown
	if !validCSRF(r) {
		result.Rejected = true
		result.Message = v.Text.CSRFError
		respond(403)
		return
	}
	for _, name := range []string{"engineId", "accountId", "conversationId", "messageId", "operationKey"} {
		if !validOperationKey(r.Form.Get(name)) {
			result.Rejected = true
			result.Message = v.Text.InvalidForm
			respond(422)
			return
		}
	}
	var expected *engineapi.NullableReactionRevision
	if value := r.Form.Get("expectedOwnRevisionId"); value != "" {
		if !validOperationKey(value) {
			result.Rejected = true
			result.Message = v.Text.InvalidForm
			respond(422)
			return
		}
		id := engineapi.NullableReactionRevision(value)
		expected = &id
	}
	var emojis []string
	if json.Unmarshal([]byte(r.Form.Get("emojis")), &emojis) != nil || emojis == nil || !validReactionSet(emojis) {
		result.Rejected = true
		result.Message = v.Text.InvalidForm
		respond(422)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if status, ok := s.reactionScope(ctx, r.Form.Get("engineId"), r.Form.Get("accountId"), r.Form.Get("conversationId"), r.Form.Get("messageId")); !ok {
		result.Message = v.Text.ReactionUnavailable
		result.Rejected = status == 400 || status == 404
		respond(status)
		return
	}
	receipt, err := s.client.GetMessageReactionCommandWithResponse(ctx, r.Form.Get("operationKey"))
	if err != nil {
		respond(503)
		return
	}
	if receipt.JSON200 != nil {
		if !validReactionCommand(receipt.JSON200, r.Form, emojis) {
			result.Code = "idempotency_conflict"
			result.Message = v.Text.OperationConflict
			respond(409)
			return
		}
		result = reactionCommandResult(receipt.JSON200, v.Text)
		respond(200)
		return
	}
	if receipt.StatusCode() != 404 {
		respond(503)
		return
	}
	if r.URL.Path == "/ui/messages/reaction-receipt" {
		result.Code = "not_found"
		respond(404)
		return
	}
	response, err := s.client.SetMessageReactionsWithResponse(ctx, r.Form.Get("messageId"), &engineapi.SetMessageReactionsParams{IdempotencyKey: r.Form.Get("operationKey")}, engineapi.SetMessageReactions{ExpectedOwnRevisionId: expected, Emojis: emojis})
	if err != nil {
		respond(503)
		return
	}
	command := response.JSON202
	if command == nil {
		command = response.JSON200
	}
	if command != nil {
		if !validReactionCommand(command, r.Form, emojis) {
			respond(503)
			return
		}
		result = reactionCommandResult(command, v.Text)
		respond(200)
		return
	}
	status := response.StatusCode()
	if status != 400 && status != 404 && status != 409 && status != 422 {
		respond(503)
		return
	}
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body, &problem)
	result.Code = problem.Code
	result.Rejected = problem.Code != "idempotency_conflict"
	result.Message = v.Text.ReactionUnavailable
	respond(status)
}

func reactionQuickChoices(text copy) []struct{ emoji, label string } {
	return []struct{ emoji, label string }{
		{"👍", text.ReactionThumbsUp},
		{"❤️", text.ReactionHeart},
		{"😂", text.ReactionLaugh},
		{"😮", text.ReactionSurprise},
		{"😢", text.ReactionCry},
		{"🙏", text.ReactionPray},
		{"👏", text.ReactionClap},
	}
}

func reactionSummaryPlacement(own bool) popover.Placement {
	if own {
		return popover.PlacementTopEnd
	}
	return popover.PlacementTopStart
}

func reactionSummaryCount(state engineapi.MessageReactions) int {
	count := 0
	for _, item := range state.Items {
		count += item.Count
	}
	return count
}
func reactionSummaryEmojis(state engineapi.MessageReactions) string {
	emojis := []string{}
	for _, item := range state.Items {
		if len(emojis) == 3 {
			break
		}
		emojis = append(emojis, item.Emoji)
	}
	return strings.Join(emojis, " ")
}
