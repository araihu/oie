package web

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/icon/heroicons"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed forwarding.js
var forwardingScript []byte

func forwardAction(v view, prefix, id string) dropdown.Item {
	return dropdown.Item{ID: "forward-message-" + prefix + id, Label: v.Text.Forward, Icon: glyph(heroicons.Icon16SolidArrowTopRightOnSquare), OnClick: "void 0"}
}

func (s *Server) prepareForward(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	sourceID, engineID := q.Get("messageId"), q.Get("engineId")
	if !validOperationKey(sourceID) || !validOperationKey(engineID) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if engine.JSON200.Id != engineID || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		w.WriteHeader(http.StatusConflict)
		return
	}
	source, err := s.client.GetMessageWithResponse(ctx, sourceID)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if source.JSON200 == nil {
		w.WriteHeader(source.StatusCode())
		return
	}
	m := source.JSON200
	if m.Id != sourceID || !validOperationKey(m.RevisionId) || m.SendState == "cancelled" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	v := s.baseView(r)
	v.EngineID = engineID
	v.CSRF = csrfToken(w, r)
	limit := 100
	params := &engineapi.ListAccountsParams{Limit: &limit}
	seen := make(map[string]bool)
	for {
		accounts, err := s.client.ListAccountsWithResponse(ctx, params)
		if err != nil || accounts.JSON200 == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		v.Accounts = append(v.Accounts, accounts.JSON200.Items...)
		if accounts.JSON200.NextCursor == nil {
			break
		}
		cursor := *accounts.JSON200.NextCursor
		if seen[cursor] {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		seen[cursor] = true
		params.After = &cursor
	}
	rooms, ok := s.forwardRooms(ctx)
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var out bytes.Buffer
	if err := forwardForm(v, *m, rooms).Render(presentationContext(ctx, v), &out); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = out.WriteTo(w)
}

func (s *Server) forwardRooms(ctx context.Context) ([]engineapi.Conversation, bool) {
	limit, kind := 100, engineapi.ListConversationsParamsKindRoom
	var rooms []engineapi.Conversation
	seen := make(map[string]bool)
	for _, archived := range []bool{false, true} {
		params := &engineapi.ListConversationsParams{Limit: &limit, Kind: &kind, Archived: &archived}
		cursors := make(map[string]bool)
		for {
			response, err := s.client.ListConversationsWithResponse(ctx, params)
			if err != nil || response.JSON200 == nil {
				return nil, false
			}
			for _, room := range response.JSON200.Items {
				if room.Kind == "room" && room.Request == nil && validOperationKey(room.Id) && !seen[room.Id] {
					rooms = append(rooms, room)
					seen[room.Id] = true
				}
			}
			cursor := response.JSON200.NextCursor
			if cursor == nil {
				break
			}
			if cursors[*cursor] {
				return nil, false
			}
			cursors[*cursor] = true
			params.Cursor = cursor
		}
	}
	slices.SortStableFunc(rooms, func(a, b engineapi.Conversation) int {
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	return rooms, true
}

func (s *Server) submitForward(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	key, source := r.Form.Get("operationKey"), r.Form.Get("messageId")
	revision, destination := r.Form.Get("revisionId"), r.Form.Get("conversationId")
	if !validOperationKey(key) || !validOperationKey(source) || !validOperationKey(revision) || !validOperationKey(destination) {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm}
	}
	result, err := s.archiveClient.ForwardMessageWithResponse(ctx, source, &engineapi.ForwardMessageParams{IdempotencyKey: key},
		engineapi.ForwardMessage{ExpectedRevisionId: revision, ConversationId: destination})
	if err != nil {
		return http.StatusServiceUnavailable, mutationResult{Message: text.ForwardUnconfirmed}
	}
	accepted := result.JSON202
	if accepted == nil {
		accepted = result.JSON200
	}
	if accepted != nil && accepted.ConversationId == destination && validOperationKey(accepted.Id) {
		return http.StatusOK, mutationResult{OK: true, Accepted: true, Message: text.ForwardAccepted}
	}
	switch result.StatusCode() {
	case http.StatusRequestEntityTooLarge:
		return result.StatusCode(), mutationResult{Message: text.AttachmentTooLarge}
	case http.StatusConflict:
		var problem engineapi.Problem
		if json.Unmarshal(result.Body, &problem) == nil && problem.CurrentRoomSession != nil && !problem.CurrentRoomSession.Active {
			return result.StatusCode(), mutationResult{Message: text.RoomJoinRequired}
		}
		return result.StatusCode(), mutationResult{Message: text.ForwardChanged}
	case http.StatusNotFound, http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusUnsupportedMediaType, http.StatusBadGateway:
		return result.StatusCode(), mutationResult{Message: text.ForwardFailed}
	default:
		return http.StatusServiceUnavailable, mutationResult{Message: text.ForwardUnconfirmed}
	}
}
