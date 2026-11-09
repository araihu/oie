package web

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed message-correction.css
var messageCorrectionStylesheet []byte

type editPreparation struct {
	EngineID     string                      `json:"engineId"`
	Target       engineapi.MessageEditTarget `json:"target"`
	OperationKey string                      `json:"operationKey"`
}
type editResult struct {
	OK       bool                          `json:"ok"`
	Accepted bool                          `json:"accepted"`
	Rejected bool                          `json:"rejected"`
	Message  string                        `json:"message"`
	Code     string                        `json:"code,omitempty"`
	Command  *engineapi.MessageEditCommand `json:"command,omitempty"`
}

func validEditTarget(value engineapi.MessageEditTarget, account, conversation, message string) bool {
	return validOperationKey(value.MessageId) && validOperationKey(value.CurrentRevisionId) && value.AccountId == account && value.ConversationId == conversation && (message == "" || value.MessageId == message) && utf8.ValidString(value.OriginalText) && len(value.OriginalText) <= 1<<20 && (!value.Eligible || validEditText(value.OriginalText))
}
func validEditText(text string) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != "" && !strings.ContainsRune(text, 0) && utf8.RuneCountInString(text) <= 16384
}
func (s *Server) latestEditTarget(ctx context.Context, v *view) {
	if v.Selected == nil || v.Preview || !v.messagingImplemented("send_corrections") {
		return
	}
	response, err := s.client.GetConversationEditTargetWithResponse(ctx, v.Selected.Id)
	if err == nil && response.JSON200 != nil && validEditTarget(*response.JSON200, v.Selected.AccountId, v.Selected.Id, "") {
		v.LatestEditTarget = response.JSON200
	}
}
func (v view) editTargetHref(message string) string {
	q := url.Values{"engineId": {v.EngineID}, "accountId": {selectedAccountID(v)}, "conversationId": {v.ChatID}, "lang": {v.Locale}}
	if message != "" {
		q.Set("messageId", message)
	}
	return "/ui/message-edit-target?" + q.Encode()
}
func (v view) editableMessage(id string) bool {
	return !v.Preview && v.LatestEditTarget != nil && v.LatestEditTarget.Eligible && v.LatestEditTarget.MessageId == id
}
func (v view) latestEditMessage() string {
	if v.LatestEditTarget != nil && v.LatestEditTarget.Eligible {
		return v.LatestEditTarget.MessageId
	}
	return ""
}
func (s *Server) prepareMessageEdit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	v := s.baseView(r)
	v.CSRF = csrfToken(w, r)
	q := r.URL.Query()
	engine, account, conversation, message := q.Get("engineId"), q.Get("accountId"), q.Get("conversationId"), q.Get("messageId")
	result := editResult{Message: v.Text.EditUnavailable, Rejected: true}
	fail := func(status int) { s.editResponse(w, r, v, status, nil, q, result) }
	for _, id := range []string{engine, account, conversation} {
		if !validOperationKey(id) {
			fail(400)
			return
		}
	}
	if message != "" && !validOperationKey(message) {
		fail(400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	owner, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || owner.JSON200 == nil {
		fail(503)
		return
	}
	if owner.JSON200.Id != engine {
		fail(409)
		return
	}
	var target *engineapi.MessageEditTarget
	status := 503
	if message == "" {
		response, e := s.client.GetConversationEditTargetWithResponse(ctx, conversation)
		if e == nil {
			target = response.JSON200
			status = response.StatusCode()
		}
	} else {
		response, e := s.client.GetMessageEditTargetWithResponse(ctx, message)
		if e == nil {
			target = response.JSON200
			status = response.StatusCode()
		}
	}
	if target == nil {
		if status != 404 && status != 400 {
			status = 503
		}
		fail(status)
		return
	}
	if !validEditTarget(*target, account, conversation, message) {
		fail(503)
		return
	}
	preparation := editPreparation{EngineID: engine, Target: *target, OperationKey: newOperationKey()}
	s.editResponse(w, r, v, 200, &preparation, q, editResult{})
}
func validEditCommand(command *engineapi.MessageEditCommand, form url.Values) bool {
	if command == nil || command.Id != form.Get("operationKey") || command.MessageId != form.Get("messageId") || command.AccountId != form.Get("accountId") || command.ConversationId != form.Get("conversationId") || command.ExpectedRevisionId != form.Get("expectedRevisionId") || command.OriginalText != form.Get("originalText") || !validOperationKey(command.RevisionId) || command.CreatedAt.IsZero() || command.UpdatedAt.IsZero() {
		return false
	}
	switch command.State {
	case "queued", "dispatching", "submitted", "unknown", "failed":
		return true
	}
	return false
}
func editCommandResult(command *engineapi.MessageEditCommand, text copy) editResult {
	message := text.EditUnknown
	switch command.State {
	case "queued":
		message = text.EditQueued
	case "dispatching":
		message = text.EditDispatching
	case "submitted":
		message = text.EditSubmitted
	case "failed":
		message = text.EditFailed
	}
	return editResult{OK: true, Accepted: true, Message: message, Command: command}
}
func (s *Server) correctMessage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	v := s.baseView(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	result := editResult{Message: v.Text.EditAcceptanceUnknown}
	respond := func(status int) { s.editResponse(w, r, v, status, nil, r.Form, result) }
	if err := r.ParseForm(); err != nil {
		result.Rejected = true
		result.Message = v.Text.InvalidForm
		respond(400)
		return
	}
	if text, ok := s.catalogs[r.Form.Get("lang")]; ok {
		v.Text = text
		v.Locale = r.Form.Get("lang")
	}
	result.Message = v.Text.EditAcceptanceUnknown
	if !validCSRF(r) {
		result.Rejected = true
		result.Message = v.Text.CSRFError
		respond(403)
		return
	}
	v.CSRF = r.Form.Get("csrf")
	if v.CSRF == "" {
		v.CSRF = r.Header.Get("X-CSRF-Token")
	}
	for _, name := range []string{"engineId", "accountId", "conversationId", "messageId", "expectedRevisionId", "operationKey"} {
		if !validOperationKey(r.Form.Get(name)) {
			result.Rejected = true
			result.Message = v.Text.InvalidForm
			respond(422)
			return
		}
	}
	if !validEditText(r.Form.Get("originalText")) {
		result.Rejected = true
		result.Message = v.Text.InvalidMessage
		respond(422)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	owner, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || owner.JSON200 == nil {
		respond(503)
		return
	}
	if owner.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		result.Rejected = true
		result.Code = "preview"
		result.Message = v.Text.EditUnavailable
		respond(409)
		return
	}
	if owner.JSON200.Id != r.Form.Get("engineId") {
		result.Code = "engine_changed"
		result.Message = v.Text.EngineChanged
		respond(409)
		return
	}
	receipt, err := s.client.GetMessageEditWithResponse(ctx, r.Form.Get("operationKey"))
	if err != nil {
		respond(503)
		return
	}
	if receipt.JSON200 != nil {
		if !validEditCommand(receipt.JSON200, r.Form) {
			result.Code = "idempotency_conflict"
			result.Message = v.Text.OperationConflict
			respond(409)
			return
		}
		result = editCommandResult(receipt.JSON200, v.Text)
		respond(200)
		return
	}
	if receipt.StatusCode() != 404 {
		respond(503)
		return
	}
	if r.URL.Path == "/ui/messages/edit-receipt" {
		result.Code = "not_found"
		respond(404)
		return
	}
	// The engine atomically rechecks ownership, latest-message eligibility and the
	// selected content head. A receipt is checked first so replay survives edits.
	response, err := s.client.EditMessageWithResponse(ctx, r.Form.Get("messageId"), &engineapi.EditMessageParams{IdempotencyKey: r.Form.Get("operationKey")}, engineapi.EditMessage{ExpectedRevisionId: r.Form.Get("expectedRevisionId"), OriginalText: r.Form.Get("originalText")})
	if err != nil {
		respond(503)
		return
	}
	command := response.JSON202
	if command == nil {
		command = response.JSON200
	}
	if command != nil {
		if !validEditCommand(command, r.Form) {
			respond(503)
			return
		}
		result = editCommandResult(command, v.Text)
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
	result.Message = v.Text.EditChanged
	if problem.Code == "idempotency_conflict" {
		result.Message = v.Text.OperationConflict
	}
	respond(status)
}
func (s *Server) editResponse(w http.ResponseWriter, r *http.Request, v view, status int, preparation *editPreparation, values url.Values, result editResult) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if r.Method == http.MethodHead {
			return
		}
		if preparation != nil {
			_ = json.NewEncoder(w).Encode(preparation)
		} else {
			_ = json.NewEncoder(w).Encode(result)
		}
		return
	}
	var out bytes.Buffer
	if err := messageEditDocument(v, preparation, values, result).Render(presentationContext(r.Context(), v), &out); err != nil {
		w.WriteHeader(503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = out.WriteTo(w)
	}
}
