package web

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web/appicons"
)

//go:embed forwarding.js
var forwardingScript []byte

func forwardAction(v view, prefix, id string) dropdown.Item {
	return dropdown.Item{
		ID:      "forward-message-" + prefix + id,
		Label:   v.Text.Forward,
		Icon:    appicons.Icon(appicons.Config{Symbol: appicons.IconHeroiconsForward16SolidArrowUturnRight, Decorative: true, RootClass: "oie-icon"}),
		OnClick: "void 0",
	}
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
	if m.Id != sourceID || !validOperationKey(m.RevisionId) || m.SendState == "cancelled" || m.Retraction != nil || m.Moderation != nil {
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
	enrichment, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	v.PreviewScope = "forward"
	v.Selected = &engineapi.Conversation{Id: m.ConversationId, AccountId: m.AccountId}
	preview := messageView{Message: *m}
	var loaders sync.WaitGroup
	loaders.Go(func() {
		if messageBodyURLs.MatchString(preview.displayBody()) {
			v.Messages = []messageView{preview}
			s.loadMessagePreviews(enrichment, &v)
			s.loadMessageVideos(enrichment, &v)
			preview.PresentationKnown = v.PreviewsKnown
		} else {
			preview.PresentationKnown = true
		}
	})
	loaders.Go(func() {
		accounts := make(map[string]bool)
		for _, room := range rooms {
			if !accounts[room.AccountId] && enrichment.Err() == nil {
				accounts[room.AccountId] = true
				s.loadAvatars(enrichment, &v, room.AccountId)
			}
		}
	})
	s.loadForwardTopics(enrichment, rooms)
	loaders.Wait()
	v.formatMessage(ctx, &preview)

	var out bytes.Buffer
	if err := forwardForm(v, preview, rooms).Render(presentationContext(ctx, v), &out); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = out.WriteTo(w)
}

type forwardRoom struct {
	engineapi.Conversation
	Topic string
}

func (s *Server) loadForwardTopics(ctx context.Context, rooms []forwardRoom) {
	jobs := make(chan *forwardRoom, len(rooms))
	for i := range rooms {
		jobs <- &rooms[i]
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(6, len(rooms)) {
		workers.Go(func() {
			for room := range jobs {
				if ctx.Err() != nil {
					return
				}
				if room.Kind != "room" {
					continue
				}
				if details := s.conversationDetails(ctx, room.Conversation); details != nil {
					room.Topic = strings.TrimSpace(previewValue(details.Topic))
				}
			}
		})
	}
	workers.Wait()
}

func (s *Server) forwardRooms(ctx context.Context) ([]forwardRoom, bool) {
	limit := 100
	var rooms []forwardRoom
	seen := make(map[string]bool)
	for _, archived := range []bool{false, true} {
		params := &engineapi.ListConversationsParams{Limit: &limit, Archived: &archived}
		cursors := make(map[string]bool)
		for {
			response, err := s.client.ListConversationsWithResponse(ctx, params)
			if err != nil || response.JSON200 == nil {
				return nil, false
			}
			for _, room := range response.JSON200.Items {
				if (room.Kind == "room" || room.Kind == "direct") && room.Request == nil && (room.RemovedFromChats == nil || !*room.RemovedFromChats) && validOperationKey(room.Id) && !seen[room.Id] {
					if room.Title == "" {
						room.Title = room.Address
					}
					rooms = append(rooms, forwardRoom{Conversation: room})
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
	slices.SortStableFunc(rooms, func(a, b forwardRoom) int {
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
		return result.StatusCode(), mutationResult{Message: text.AttachmentTooLarge, Rejected: true}
	case http.StatusConflict:
		var problem engineapi.Problem
		if json.Unmarshal(result.Body, &problem) == nil {
			if current := problem.CurrentMessage; problem.Code == "state_conflict" && current != nil && validOperationKey(current.Id) &&
				validOperationKey(current.RevisionId) && (current.Id != source || current.RevisionId != revision || current.SendState == "cancelled" || current.Retraction != nil || current.Moderation != nil) {
				return result.StatusCode(), mutationResult{Message: text.ForwardChanged, Rejected: true, Code: "source_changed"}
			}
			if problem.CurrentRoomSession != nil && !problem.CurrentRoomSession.Active {
				return result.StatusCode(), mutationResult{Message: text.RoomJoinRequired, Rejected: true}
			}
		}
		return result.StatusCode(), mutationResult{Message: text.ForwardChanged, Rejected: true}
	case http.StatusNotFound, http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusUnsupportedMediaType, http.StatusBadGateway:
		return result.StatusCode(), mutationResult{Message: text.ForwardFailed, Rejected: true}
	default:
		return http.StatusServiceUnavailable, mutationResult{Message: text.ForwardUnconfirmed}
	}
}
