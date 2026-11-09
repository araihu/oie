package web

import (
	"bytes"
	"context"
	_ "embed"
	"net/http"
	"net/url"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed sender.js
var senderScript []byte

type senderInfoView struct {
	Message  messageView
	Address  string
	Presence *engineapi.PresenceObservation
}

func (v view) senderHref(m messageView) string {
	return v.href("chat", m.ConversationId, "open", "sender", "senderMessage", m.Id)
}
func (v view) senderFragmentHref(m messageView) string {
	return "/ui/sender?" + url.Values{"engineId": {v.EngineID}, "accountId": {m.AccountId}, "conversationId": {m.ConversationId}, "messageId": {m.Id}, "lang": {v.Locale}}.Encode()
}

// Only canonical message and conversation data establish the source. A room
// display name cannot associate a historical author with a current occupant.
func (s *Server) loadSenderInformation(ctx context.Context, v *view, engine, account, conversation, message string) (*senderInfoView, int) {
	for _, id := range []string{engine, account, conversation, message} {
		if !validOperationKey(id) {
			return nil, 400
		}
	}
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		return nil, 503
	}
	if current.JSON200.Id != engine {
		return nil, 409
	}
	source, err := s.client.GetMessageWithResponse(ctx, message)
	if err != nil {
		return nil, 503
	}
	if source.StatusCode() == 404 {
		return nil, 404
	}
	if source.JSON200 == nil {
		return nil, 503
	}
	m := source.JSON200
	if m.Id != message || m.AccountId != account || m.ConversationId != conversation {
		return nil, 404
	}
	owner, err := s.client.GetConversationWithResponse(ctx, conversation)
	if err != nil {
		return nil, 503
	}
	if owner.StatusCode() == 404 {
		return nil, 404
	}
	if owner.JSON200 == nil {
		return nil, 503
	}
	c := owner.JSON200
	if c.Id != conversation || c.AccountId != account || (c.Kind != "direct" && c.Kind != "room") {
		return nil, 404
	}
	a, err := s.client.GetAccountWithResponse(ctx, account)
	if err != nil {
		return nil, 503
	}
	if a.StatusCode() == 404 {
		return nil, 404
	}
	if a.JSON200 == nil {
		return nil, 503
	}
	if a.JSON200.Id != account {
		return nil, 404
	}
	v.EngineID = engine
	v.Selected = c
	found := false
	for _, existing := range v.Accounts {
		if existing.Id == account {
			found = true
		}
	}
	if !found {
		v.Accounts = append(v.Accounts, *a.JSON200)
	}
	result := &senderInfoView{Message: messageView{Message: *m}}
	if m.IsOwn {
		result.Address = a.JSON200.Address
	} else if c.Kind == "direct" {
		result.Address = c.Address
	}
	if result.Address != "" {
		s.loadAvatars(ctx, v, account)
	}
	if !m.IsOwn && c.Kind == "direct" {
		s.loadConversationDetails(ctx, v)
		if v.ConversationInfo != nil {
			result.Presence = v.ConversationInfo.PeerPresence
		}
	}
	return result, 200
}

func (s *Server) senderInformation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	v := s.baseView(r)
	q := r.URL.Query()
	info, status := s.loadSenderInformation(ctx, &v, q.Get("engineId"), q.Get("accountId"), q.Get("conversationId"), q.Get("messageId"))
	v.SenderInfo = info
	var out bytes.Buffer
	if err := senderContent(v).Render(presentationContext(ctx, v), &out); err != nil {
		w.WriteHeader(503)
		return
	}
	w.WriteHeader(status)
	_, _ = out.WriteTo(w)
}
