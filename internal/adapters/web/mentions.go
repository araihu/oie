package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed mentions.js
var mentionsScript []byte

//go:embed mentions.css
var mentionsStylesheet []byte

func (s *Server) mentionCandidates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	q := r.URL.Query()
	conversation, account := q.Get("conversationId"), q.Get("accountId")
	fail := func(status int) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": s.catalogs[locale(r)].MentionFailed})
	}
	if !validOperationKey(conversation) || !validOperationKey(account) || !validOperationKey(q.Get("engineId")) {
		fail(400)
		return
	}
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil {
		fail(503)
		return
	}
	if engine.JSON200.Id != q.Get("engineId") || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		fail(409)
		return
	}
	owner, err := s.client.GetConversationWithResponse(ctx, conversation)
	if err != nil || owner.JSON200 == nil {
		fail(503)
		return
	}
	if owner.JSON200.AccountId != account || owner.JSON200.Kind != "room" {
		fail(404)
		return
	}
	response, err := s.client.GetMentionCandidatesWithResponse(ctx, conversation)
	if err != nil || response.JSON200 == nil {
		fail(503)
		return
	}
	result := response.JSON200
	if result.AccountId != account || result.ConversationId != conversation {
		fail(503)
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		EngineID string `json:"engineId"`
		*engineapi.MentionCandidates
	}{engine.JSON200.Id, result})
}

func parseMentionSelections(raw, body string) ([]engineapi.MentionSelection, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 48*1024 {
		return nil, io.ErrShortBuffer
	}
	var items []engineapi.MentionSelection
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&items); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	if len(items) > 32 {
		return nil, io.ErrShortBuffer
	}
	n := utf8.RuneCountInString(body)
	end := 0
	for _, item := range items {
		if item.ParticipantId == "" || len(item.ParticipantId) > 1024 || item.Begin < end || item.End <= item.Begin || item.End > n {
			return nil, io.ErrUnexpectedEOF
		}
		end = item.End
	}
	return items, nil
}

func matchingMentionSelections(message engineapi.Message, selections []engineapi.MentionSelection) bool {
	var actual []engineapi.MessageMention
	if message.Mentions != nil {
		actual = *message.Mentions
	}
	if len(actual) != len(selections) {
		return false
	}
	for i, item := range selections {
		if actual[i].ParticipantId != item.ParticipantId || actual[i].Begin != item.Begin || actual[i].End != item.End {
			return false
		}
	}
	return true
}
