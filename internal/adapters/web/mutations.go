package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/google/uuid"
)

const csrfCookie = "oie_csrf"

func newOperationKey() string {
	id, err := uuid.NewV7()
	if err != nil {
		return ""
	}
	return id.String()
}
func validOperationKey(key string) bool {
	id, err := uuid.Parse(key)
	return err == nil && id.Version() == 7 && id.String() == key
}
func csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil {
		if data, err := base64.RawURLEncoding.DecodeString(c.Value); err == nil && len(data) == 32 {
			return c.Value
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	return token
}
func validCSRF(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	c, err := r.Cookie(csrfCookie)
	if err != nil {
		return false
	}
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = r.Form.Get("csrf")
	}
	data, err := base64.RawURLEncoding.DecodeString(c.Value)
	return err == nil && len(data) == 32 && len(token) == len(c.Value) && subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) == 1
}

type mutationResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Code      string `json:"code,omitempty"`
	Redirect  string `json:"redirect,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	State     string `json:"state,omitempty"`
	Accepted  bool   `json:"accepted,omitempty"`
	Rejected  bool   `json:"rejected,omitempty"`
	NextKey   string `json:"nextKey,omitempty"`
}

func (s *Server) mutate(w http.ResponseWriter, r *http.Request) {

	if !isMutationPath(r.URL.Path) {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	text := s.catalogs[locale(r)]
	if err := r.ParseForm(); err != nil {
		s.mutationResponse(w, r, 400, mutationResult{Message: text.InvalidForm, Code: "invalid_request", Rejected: true})
		return
	}
	if !validCSRF(r) {
		s.mutationResponse(w, r, 403, mutationResult{Message: text.CSRFError, Code: "csrf"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil {
		s.mutationResponse(w, r, 503, mutationResult{Message: text.PendingAcceptance, Code: "unavailable"})
		return
	}
	if engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		s.mutationResponse(w, r, 409, mutationResult{Message: text.SendingUnavailable, Code: "preview", Rejected: true})
		return
	}
	if r.Form.Get("engineId") != engine.JSON200.Id {
		s.mutationResponse(w, r, 409, mutationResult{Message: text.EngineChanged, Code: "engine_changed"})
		return
	}
	result := mutationResult{OK: true, NextKey: newOperationKey()}
	status := 200
	switch r.URL.Path {
	case "/ui/accounts/register":
		key := r.Form.Get("operationKey")
		if !validOperationKey(key) {
			status = 400
			result = mutationResult{Message: text.InvalidForm, Rejected: true}
			break
		}
		response, err := s.client.RegisterAccountWithResponse(ctx, &engineapi.RegisterAccountParams{IdempotencyKey: key}, engineapi.RegisterAccount{Protocol: "xmpp", Address: r.Form.Get("address"), Label: r.Form.Get("label")})
		if err != nil {
			status = 503
			result = mutationResult{Message: text.NativeError}
			break
		}
		if response.JSON200 == nil && response.JSON201 == nil {
			status = response.StatusCode()
			result = problemResult(text, status, response.Body)
			break
		}
		result.Message = text.AccountSaved
		result.Redirect = "/?open=accounts"
	case "/ui/accounts/connect":
		password := r.Form.Get("password")
		if password == "" {
			status = 422
			result = mutationResult{Message: text.InvalidForm, Rejected: true}
			break
		}
		body := engineapi.ConfigureConnection{Password: &password}
		if endpoint := strings.TrimSpace(r.Form.Get("endpoint")); endpoint != "" {
			body.Endpoint = &endpoint
		}
		response, err := s.client.ConfigureAccountConnectionWithResponse(ctx, r.Form.Get("accountId"), body)
		if err != nil {
			status = 503
			result = mutationResult{Message: text.NativeError}
			break
		}
		if response.JSON202 == nil {
			status = response.StatusCode()
			result = problemResult(text, status, response.Body)
			break
		}
		result.Message = text.ConnectionRequested
		result.Redirect = "/?open=accounts"
	case "/ui/accounts/disconnect":
		response, err := s.client.DisconnectAccountWithResponse(ctx, r.Form.Get("accountId"))
		if err != nil {
			status = 503
			result = mutationResult{Message: text.NativeError}
			break
		}
		if response.StatusCode() != 200 && response.StatusCode() != 202 {
			status = response.StatusCode()
			result = problemResult(text, status, response.Body)
			break
		}
		result.Message = text.Disconnected
		result.Redirect = "/?open=accounts"
	case "/ui/conversations/open":
		key := r.Form.Get("operationKey")
		if !validOperationKey(key) {
			status = 400
			result = mutationResult{Message: text.InvalidForm, Rejected: true}
			break
		}
		response, err := s.client.OpenConversationWithResponse(ctx, &engineapi.OpenConversationParams{IdempotencyKey: key}, engineapi.OpenConversation{AccountId: r.Form.Get("accountId"), Address: r.Form.Get("address")})
		if err != nil {
			status = 503
			result = mutationResult{Message: text.NativeError}
			break
		}
		c := response.JSON200
		if c == nil {
			c = response.JSON201
		}
		if c == nil {
			status = response.StatusCode()
			result = problemResult(text, status, response.Body)
			break
		}
		result.Redirect = "/?chat=" + url.QueryEscape(c.Id) + "&account=" + url.QueryEscape(c.AccountId)
	case "/ui/messages/send":
		status, result = s.send(ctx, r, text)
	case "/ui/messages/cancel":
		status, result = s.cancelSend(ctx, r, text)
	}
	if status < 100 {
		status = 503
	}
	s.mutationResponse(w, r, status, result)
}
func problemResult(text copy, status int, body []byte) mutationResult {
	var p engineapi.Problem
	_ = json.Unmarshal(body, &p)
	result := mutationResult{Code: string(p.Code), Rejected: status == 400 || status == 404 || status == 422, Message: text.InvalidForm}
	switch p.Code {
	case "idempotency_conflict":
		result.Message = text.OperationConflict
		result.Rejected = false
	case "account_exists":
		result.Message = text.AccountExists
	case "not_found":
		result.Message = text.MessageUnavailable
	case "unavailable", "protocol_unavailable":
		result.Message = text.UnavailableBody
	case "state_conflict":
		result.Message = text.CancelConflict
	}
	if status >= 500 {
		result.Message = text.UnavailableBody
		result.Rejected = false
	}
	if p.CurrentMessage != nil {
		result.State = string(p.CurrentMessage.SendState)
		v := view{Text: text}
		result.Message = text.CancelConflict + v.stateLabel(p.CurrentMessage.SendState)
	}
	return result
}
func (s *Server) reconcileMessage(ctx context.Context, key, kind string) (*engineapi.Message, bool) {
	receipt, err := s.client.GetOperationWithResponse(ctx, key)
	if err != nil || receipt.JSON200 == nil {
		return nil, false
	}
	if string(receipt.JSON200.Kind) != kind {
		return nil, true
	}
	current, err := s.client.GetMessageWithResponse(ctx, receipt.JSON200.ResourceId)
	if err != nil || current.JSON200 == nil {
		return nil, true
	}
	return current.JSON200, true
}
func (s *Server) send(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	key, body, conversationID := r.Form.Get("operationKey"), r.Form.Get("originalText"), r.Form.Get("conversationId")
	if !validOperationKey(key) || strings.TrimSpace(body) == "" || len([]rune(body)) > 16384 {
		return 422, mutationResult{Message: text.InvalidMessage, Code: "invalid_request", Rejected: true}
	}
	conversation, err := s.client.GetConversationWithResponse(ctx, conversationID)
	if err != nil {
		return 503, mutationResult{Message: text.PendingAcceptance}
	}
	if conversation.JSON200 == nil {
		return conversation.StatusCode(), problemResult(text, conversation.StatusCode(), conversation.Body)
	}
	if conversation.JSON200.AccountId != r.Form.Get("accountId") {
		return 409, mutationResult{Message: text.OperationConflict}
	}
	if message, found := s.reconcileMessage(ctx, key, "send_message"); found {
		if message != nil && message.ConversationId == conversationID && message.OriginalText == body {
			return acceptedResult(text, *message)
		}
		return 409, mutationResult{Message: text.OperationConflict, Code: "idempotency_conflict"}
	}
	response, err := s.client.SendMessageWithResponse(ctx, conversationID, &engineapi.SendMessageParams{IdempotencyKey: key}, engineapi.SendMessage{OriginalText: body})
	if err != nil {
		if message, _ := s.reconcileMessage(ctx, key, "send_message"); message != nil && message.ConversationId == conversationID && message.OriginalText == body {
			return acceptedResult(text, *message)
		}
		return 503, mutationResult{Message: text.PendingAcceptance, Code: "unavailable"}
	}
	message := response.JSON202
	if message == nil {
		message = response.JSON200
	}
	if message == nil {
		return response.StatusCode(), problemResult(text, response.StatusCode(), response.Body)
	}
	return acceptedResult(text, *message)
}
func acceptedResult(text copy, m engineapi.Message) (int, mutationResult) {
	return 200, mutationResult{OK: true, Accepted: true, Message: text.MessageAccepted, MessageID: m.Id, State: string(m.SendState), NextKey: newOperationKey()}
}
func (s *Server) cancelSend(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	key := r.Form.Get("operationKey")
	if !validOperationKey(key) {
		return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	id := r.Form.Get("messageId")
	response, err := s.client.CancelMessageWithResponse(ctx, id, &engineapi.CancelMessageParams{IdempotencyKey: key}, engineapi.CancelMessage{ExpectedRevisionId: r.Form.Get("expectedRevisionId")})
	if err != nil {
		if m, _ := s.reconcileMessage(ctx, key, "cancel_message"); m != nil && m.Id == id && m.SendState == "cancelled" {
			return 200, mutationResult{OK: true, Message: text.CancelSuccess, State: "cancelled", NextKey: newOperationKey()}
		}
		return 503, mutationResult{Message: text.NativeError}
	}
	if response.JSON200 == nil {
		return response.StatusCode(), problemResult(text, response.StatusCode(), response.Body)
	}
	if response.JSON200.SendState != "cancelled" {
		v := view{Text: text}
		return 409, mutationResult{Message: text.CancelConflict + v.stateLabel(response.JSON200.SendState), State: string(response.JSON200.SendState), Code: "state_conflict"}
	}
	return 200, mutationResult{OK: true, Message: text.CancelSuccess, State: string(response.JSON200.SendState), NextKey: newOperationKey()}
}
func safeReturn(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/" {
		return "/"
	}
	return u.String()
}
func (s *Server) mutationResponse(w http.ResponseWriter, r *http.Request, status int, result mutationResult) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if result.OK {
		target := result.Redirect
		if target == "" {
			target = safeReturn(r.Form.Get("returnTo"))
		}
		http.Redirect(w, r, target, 303)
		return
	}
	target := safeReturn(r.Form.Get("returnTo"))
	u, _ := url.Parse(target)
	clone := r.Clone(r.Context())
	clone.URL = u
	v := s.baseView(clone)
	v.CSRF = csrfToken(w, r)
	v.ErrorMessage = result.Message
	v.FormTarget = r.Form.Get("formTarget")
	for _, key := range []string{"address", "label", "endpoint", "accountId", "conversationId", "originalText", "operationKey"} {
		v.Values[key] = r.Form.Get(key)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	s.load(ctx, clone, &v)
	v.EngineID = r.Form.Get("engineId")
	var buf strings.Builder
	if err := page(v).Render(presentationContext(r.Context(), v), &buf); err != nil {
		w.WriteHeader(503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

func isMutationPath(path string) bool {
	switch path {
	case "/ui/accounts/register", "/ui/accounts/connect", "/ui/accounts/disconnect", "/ui/conversations/open", "/ui/messages/send", "/ui/messages/cancel":
		return true
	}
	return false
}
