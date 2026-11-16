package web

import (
	"context"
	"net/http"
	"net/url"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) removeRoomFromChats(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id, account := r.Form.Get("conversationId"), r.Form.Get("accountId")
	key, revision := r.Form.Get("operationKey"), r.Form.Get("expectedRevisionId")
	if !validOperationKey(id) || !validOperationKey(account) || !validOperationKey(key) || !validOperationKey(revision) {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	response, err := s.client.RemoveRoomFromChatsWithResponse(ctx, id, &engineapi.RemoveRoomFromChatsParams{IdempotencyKey: key},
		engineapi.RemoveRoomFromChatsJSONRequestBody{AccountId: account, ExpectedRevisionId: revision})
	if err != nil {
		return http.StatusServiceUnavailable, mutationResult{Message: text.RoomRemoveUnavailable}
	}
	if response.StatusCode() != http.StatusNoContent {
		result := problemResult(text, response.StatusCode(), response.Body)
		result.Message = text.RoomRemoveUnavailable
		if response.StatusCode() == http.StatusConflict {
			result.Message = text.RoomRemoveConflict
		}
		return response.StatusCode(), result
	}
	back, _ := url.Parse(safeReturn(r.Form.Get("returnTo")))
	q := back.Query()
	for _, name := range []string{"chat", "message", "focus", "open", "inviteRoom"} {
		q.Del(name)
	}
	return http.StatusOK, mutationResult{OK: true, Redirect: (&url.URL{Path: "/", RawQuery: q.Encode()}).String()}
}

func removedRoom(c *engineapi.Conversation) bool {
	return c != nil && c.RemovedFromChats != nil && *c.RemovedFromChats
}
