package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) loadMessageStars(ctx context.Context, v *view) {
	if v.Preview || v.Selected == nil || len(v.Messages) == 0 {
		return
	}
	wanted := map[string]bool{}
	ids := make([]string, 0, len(v.Messages))
	for _, m := range v.Messages {
		wanted[m.Id] = true
		ids = append(ids, m.Id)
	}
	found := map[string]engineapi.MessageStar{}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		response, err := s.client.ListConversationMessageStarsWithResponse(ctx, v.Selected.Id, &engineapi.ListConversationMessageStarsParams{MessageId: ids[start:end]})
		if err != nil || response.JSON200 == nil {
			return
		}
		for _, star := range response.JSON200.Items {
			if !wanted[star.MessageId] || star.AccountId != v.Selected.AccountId || star.ConversationId != v.Selected.Id {
				return
			}
			if star.RevisionId != nil && !validOperationKey(*star.RevisionId) {
				return
			}
			if star.Starred && star.RevisionId == nil {
				return
			}
			found[star.MessageId] = star
		}
	}
	if len(found) != len(wanted) {
		return
	}
	v.Stars = found
	v.StarsKnown = true
}
func (v view) starLabel(id string) string {
	if v.Stars[id].Starred {
		return v.Text.UnstarMessage
	}
	return v.Text.StarMessage
}
func starRevision(star engineapi.MessageStar) string {
	if star.RevisionId != nil {
		return *star.RevisionId
	}
	return ""
}

// Replayed PUT bodies are operation receipts, not a current projection. Always
// read the current star after a write or transport uncertainty before returning
// visible state. An unresolved operation retains its exact retry key/body.
func (s *Server) setStar(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id, key := r.Form.Get("messageId"), r.Form.Get("operationKey")
	value, err := strconv.ParseBool(r.Form.Get("starred"))
	if err != nil || !validOperationKey(id) || !validOperationKey(key) {
		return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	var expected *string
	if revision := r.Form.Get("expectedRevisionId"); revision != "" {
		if !validOperationKey(revision) {
			return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
		}
		expected = &revision
	}
	response, writeErr := s.client.SetMessageStarWithResponse(ctx, id, &engineapi.SetMessageStarParams{IdempotencyKey: key}, engineapi.SetMessageStar{Starred: value, ExpectedRevisionId: expected})
	committed := writeErr == nil && response.JSON200 != nil
	conflict := writeErr == nil && response.StatusCode() == 409
	rejected := writeErr == nil && (response.StatusCode() == 400 || response.StatusCode() == 404 || response.StatusCode() == 422)
	if writeErr != nil {
		receipt, err := s.client.GetOperationWithResponse(ctx, key)
		committed = err == nil && receipt.JSON200 != nil && receipt.JSON200.Kind == "set_message_star" && receipt.JSON200.ResourceId == id
	}
	current, readErr := s.client.GetMessageStarWithResponse(ctx, id)
	var star *engineapi.MessageStar
	if readErr == nil && current.JSON200 != nil && current.JSON200.MessageId == id {
		star = current.JSON200
	}
	if conflict {
		return 409, mutationResult{Message: text.StarConflict, Code: "state_conflict", Star: star, Rejected: true}
	}
	if rejected {
		return response.StatusCode(), mutationResult{Message: text.StarUnavailable, Star: star, Rejected: true}
	}
	if committed && star != nil {
		return 200, mutationResult{OK: true, Message: text.StarRefreshed, Star: star}
	}
	return 503, mutationResult{Message: text.StarUncertain, Code: "star_unknown", Star: star, StarPending: true}
}

func (s *Server) readStar(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.URL.Query().Get("messageId")
	if !validOperationKey(id) {
		w.WriteHeader(422)
		return
	}
	current, err := s.client.GetMessageStarWithResponse(r.Context(), id)
	if err != nil || current.JSON200 == nil {
		w.WriteHeader(503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(current.JSON200)
}
