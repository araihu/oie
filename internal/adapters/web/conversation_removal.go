package web

import (
	"context"
	"net/http"
	"net/url"

	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/icon/heroicons"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) removeConversationFromChats(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id, account := r.Form.Get("conversationId"), r.Form.Get("accountId")
	key, revision := r.Form.Get("operationKey"), r.Form.Get("expectedRevisionId")
	if !validOperationKey(id) || !validOperationKey(account) || !validOperationKey(key) || !validOperationKey(revision) {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	status, body, err := s.requestConversationRemoval(ctx, r.URL.Path, id, account, key, revision)
	if err != nil {
		return http.StatusServiceUnavailable, mutationResult{Message: text.RemoveFromChatsUnavailable}
	}
	if status != http.StatusNoContent {
		result := problemResult(text, status, body)
		result.Message = text.RemoveFromChatsUnavailable
		if status == http.StatusConflict {
			result.Message = text.RemoveFromChatsConflict
			result.Rejected = true
			current, err := s.client.GetConversationWithResponse(ctx, id)
			if err == nil && current.JSON200 != nil && current.JSON200.Id == id && current.JSON200.AccountId == account {
				result.Conversation = current.JSON200
			}
		}
		return status, result
	}
	return http.StatusOK, mutationResult{OK: true, Redirect: conversationListDestination(r)}
}

func conversationListDestination(r *http.Request) string {
	back, _ := url.Parse(safeReturn(r.Form.Get("returnTo")))
	q := back.Query()
	for _, name := range []string{"chat", "message", "focus", "open", "inviteRoom"} {
		q.Del(name)
	}
	return (&url.URL{Path: "/", RawQuery: q.Encode()}).String()
}

func (s *Server) requestConversationRemoval(ctx context.Context, path, id, account, key, revision string) (int, []byte, error) {
	if path == "/ui/rooms/remove" {
		response, err := s.client.RemoveRoomFromChatsWithResponse(ctx, id, &engineapi.RemoveRoomFromChatsParams{IdempotencyKey: key},
			engineapi.RemoveRoomFromChatsJSONRequestBody{AccountId: account, ExpectedRevisionId: revision})
		if err != nil {
			return 0, nil, err
		}
		return response.StatusCode(), response.Body, nil
	}
	response, err := s.client.RemoveDirectFromChatsWithResponse(ctx, id, &engineapi.RemoveDirectFromChatsParams{IdempotencyKey: key},
		engineapi.RemoveDirectFromChatsJSONRequestBody{AccountId: account, ExpectedRevisionId: revision})
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode(), response.Body, nil
}

func (v view) canRemoveFromChats() bool {
	return v.Selected != nil && !v.Preview && !v.conversationRemoved()
}

func (v view) conversationRemoved() bool {
	return removedConversation(v.Selected)
}

func (v view) conversationRemovalPath() string {
	if v.Selected.Kind == "room" {
		return "/ui/rooms/remove"
	}
	return "/ui/conversations/remove"
}

func (v view) removeFromChatsItem(id string) dropdown.Item {
	return dropdown.Item{
		ID: id, Label: v.Text.RemoveFromChats, Icon: glyph(heroicons.Icon16SolidTrash),
		OnClick: "$dispatch('oie:conversation-remove-open')",
	}
}

func removedConversation(c *engineapi.Conversation) bool {
	return c != nil && c.RemovedFromChats != nil && *c.RemovedFromChats
}
