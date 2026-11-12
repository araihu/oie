package web

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed scheduling.js
var schedulingScript []byte

//go:embed scheduling.css
var schedulingStylesheet []byte

func (s *Server) conversationSchedules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	engineID, conversationID, cursor := q.Get("engineId"), q.Get("conversationId"), q.Get("cursor")
	if !validOperationKey(engineID) || !validOperationKey(conversationID) || len(cursor) > 8192 || (q.Has("cursor") && cursor == "") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	p := &engineapi.ListScheduledMessagesParams{ConversationId: &conversationID}
	if cursor != "" {
		p.Cursor = &cursor
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := s.client.ListScheduledMessagesWithResponse(ctx, p)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if result.JSON200 == nil {
		status := result.StatusCode()
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		return
	}
	if result.JSON200.EngineId != engineID || slices.ContainsFunc(result.JSON200.Items, func(item engineapi.ScheduledMessageItem) bool {
		return item.Conversation.Id != conversationID || item.Message.ConversationId != conversationID
	}) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	v := s.baseView(r)
	v.EngineID, v.SchedulePage = engineID, result.JSON200
	v.CSRF = csrfToken(w, r)
	var fragment bytes.Buffer
	if err := conversationSchedulePage(v, conversationID).Render(presentationContext(ctx, v), &fragment); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fragment.WriteTo(w)
}

func (s *Server) loadScheduled(ctx context.Context, v *view, includeDestinations bool) {
	if v.Preview {
		return
	}
	p := &engineapi.ListScheduledMessagesParams{}
	switch len(v.ScheduleConversations) {
	case 0:
	case 1:
		p.ConversationId = &v.ScheduleConversations[0]
	default:
		p.ConversationIds = &v.ScheduleConversations
	}
	if v.ScheduleCursor != "" {
		p.Cursor = &v.ScheduleCursor
	}
	response, err := s.client.ListScheduledMessagesWithResponse(ctx, p)
	if err != nil || response.JSON200 == nil || response.JSON200.EngineId != v.EngineID {
		v.ScheduleUnavailable = true
		return
	}
	v.SchedulePage = response.JSON200
	if !includeDestinations {
		return
	}
	limit := 100
	conversations, err := s.client.ListConversationsWithResponse(ctx, &engineapi.ListConversationsParams{Limit: &limit})
	if err == nil && conversations.JSON200 != nil {
		v.ScheduleDestinations = conversations.JSON200.Items
	}
	for _, item := range v.SchedulePage.Items {
		if !slices.ContainsFunc(v.ScheduleDestinations, func(c engineapi.Conversation) bool { return c.Id == item.Conversation.Id }) {
			v.ScheduleDestinations = append(v.ScheduleDestinations, item.Conversation)
		}
	}
	for _, filter := range v.ScheduleConversations {
		if slices.ContainsFunc(v.ScheduleDestinations, func(c engineapi.Conversation) bool { return c.Id == filter }) {
			continue
		}
		if c, err := s.client.GetConversationWithResponse(ctx, filter); err == nil && c.JSON200 != nil {
			v.ScheduleDestinations = append(v.ScheduleDestinations, *c.JSON200)
		}
	}
}

func (s *Server) loadScheduleEdit(ctx context.Context, v *view, id string) {
	if id == "" || v.Selected == nil || !v.scheduleAllowed() {
		return
	}
	if !validOperationKey(id) {
		v.ErrorMessage = v.Text.MessageUnavailable
		return
	}
	response, err := s.client.GetMessageWithResponse(ctx, id)
	if err == nil && response.JSON200 != nil {
		m := response.JSON200
		if m.Id == id && m.ConversationId == v.ChatID && m.AccountId == v.Selected.AccountId &&
			m.IsOwn && pendingSendMutable(*m) && m.ScheduledAt != nil && m.Composition != nil {
			v.ScheduleEdit = m
			return
		}
	}
	v.ErrorMessage = v.Text.MessageUnavailable
}

func (v view) scheduleAllowed() bool {
	return v.composeAllowed() && v.messagingImplemented("schedules")
}

func (v view) conversationSchedulesAvailable() bool {
	return v.Selected != nil && !v.Unavailable && !v.Preview && v.messagingImplemented("schedules")
}

func (v view) conversationScheduleAgendaHref() string {
	v.Section = "scheduled"
	v.ScheduleConversations = []string{v.Selected.Id}
	return v.scheduleHref("")
}

func (v view) scheduleHref(cursor string) string {
	return v.href("section", "scheduled", "chat", "", "q", "", "account", "", "scheduleCursor", cursor)
}

func (v view) scheduleConversationHref(c engineapi.Conversation, edit string) string {
	return v.href("section", "chats", "chat", c.Id, "account", c.AccountId, "editSchedule", edit, "q", "", "filter", "all")
}

func (v view) scheduleExitHref() string {
	return v.href("chat", v.Selected.Id, "lang", v.Locale, "theme", v.Theme)
}

func (v view) stagedAttachmentHref(conversation, id string, download bool) string {
	q := url.Values{"engineId": {v.EngineID}, "conversationId": {conversation}, "attachmentId": {id}}
	if download {
		q.Set("download", "1")
	}
	return "/ui/scheduling/file?" + q.Encode()
}

func rasterAttachment(contentType string) bool {
	switch contentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "image/avif":
		return true
	}
	return false
}

func (s *Server) serveStagedAttachment(w http.ResponseWriter, r *http.Request) {
	stage := r.URL.Path == "/ui/scheduling/stage"
	method := http.MethodGet
	if stage {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if stage && !validCSRF(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	engineID, conversation, attachment := q.Get("engineId"), q.Get("conversationId"), q.Get("attachmentId")
	if !validOperationKey(engineID) || !validOperationKey(conversation) || !validOperationKey(attachment) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
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
	if stage {
		if kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || kind != "multipart/form-data" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		response, err := s.archiveClient.StageMessageAttachmentWithBodyWithResponse(ctx, conversation, attachment, r.Header.Get("Content-Type"), http.MaxBytesReader(w, r.Body, (20<<20)+(64<<10)))
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		value := response.JSON201
		if value == nil {
			value = response.JSON200
		}
		if value == nil {
			w.WriteHeader(response.StatusCode())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
		return
	}
	response, err := s.streamClient.GetStagedMessageAttachment(ctx, conversation, attachment)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		w.WriteHeader(response.StatusCode)
		return
	}
	// A staged filename and its declared media type come from the uploader.
	// Only bytes recognized as raster images are served inline on this origin.
	body := bufio.NewReaderSize(response.Body, 512)
	prefix, _ := body.Peek(512)
	contentType := http.DetectContentType(prefix)
	if q.Has("download") || !rasterAttachment(contentType) {
		contentType = "application/octet-stream"
		w.Header().Set("Content-Disposition", "attachment")
	}
	if size := response.Header.Get("Content-Length"); size != "" {
		w.Header().Set("Content-Length", size)
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = io.Copy(w, body)
}

func scheduleInput(r *http.Request) (ids []string, at *time.Time, valid bool) {
	if raw := r.Form.Get("attachmentIds"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &ids); err != nil || len(ids) > 16 {
			return nil, nil, false
		}
		for i, id := range ids {
			if !validOperationKey(id) || slices.Contains(ids[:i], id) {
				return nil, nil, false
			}
		}
	}
	if raw := r.Form.Get("scheduledAt"); raw != "" {
		instant, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return nil, nil, false
		}
		at = &instant
	}
	return ids, at, true
}

func matchingComposition(m engineapi.Message, conversation, account string, in engineapi.SendMessage) bool {
	at := in.ScheduledAt
	if (m.ScheduledAt == nil) != (at == nil) || (at != nil && !m.ScheduledAt.Equal(*at)) {
		return false
	}
	if !sameOptionalText(m.ExpectedReplyRevisionId, in.ExpectedReplyRevisionId) ||
		!sameOptionalText(m.ExpectedReplyExcerptSha256, in.ExpectedReplyExcerptSha256) {
		return false
	}
	var attachments []string
	if in.AttachmentIds != nil {
		attachments = *in.AttachmentIds
	}
	if m.Composition != nil && m.Composition.AttachmentIds != nil {
		if !slices.Equal(*m.Composition.AttachmentIds, attachments) {
			return false
		}
	} else if len(attachments) != 0 {
		return false
	}
	var target string
	if in.ReplyToMessageId != nil {
		target = *in.ReplyToMessageId
	}
	var mentions []engineapi.MentionSelection
	if in.Mentions != nil {
		mentions = *in.Mentions
	}
	return matchingSend(m, conversation, account, in.OriginalText, target, mentions)
}

func sameOptionalText(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (s *Server) changeSchedule(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	key, id, revision := r.Form.Get("operationKey"), r.Form.Get("messageId"), r.Form.Get("expectedRevisionId")
	replyRevision, replyExcerptSHA256 := r.Form.Get("expectedReplyRevisionId"), r.Form.Get("expectedReplyExcerptSha256")
	invalid := mutationResult{Message: text.InvalidForm, Rejected: true}
	if !validOperationKey(key) || !validOperationKey(id) || !validOperationKey(revision) ||
		(replyExcerptSHA256 != "" && (replyRevision == "" || !validReplyExcerptSHA256(replyExcerptSHA256))) {
		return 422, invalid
	}
	var message *engineapi.Message
	var status int
	var responseBody []byte
	if r.URL.Path == "/ui/scheduling/send-now" {
		if replyRevision != "" {
			return 422, invalid
		}
		response, err := s.client.SendScheduledMessageNowWithResponse(ctx, id, &engineapi.SendScheduledMessageNowParams{IdempotencyKey: key}, engineapi.SendScheduledMessageNow{ExpectedRevisionId: revision})
		if err != nil {
			return 503, mutationResult{Message: text.PendingAcceptance}
		}
		message, status, responseBody = response.JSON200, response.StatusCode(), response.Body
	} else {
		ids, at, valid := scheduleInput(r)
		if !valid || (replyRevision != "" && (r.Form.Get("replaceComposition") != "1" || !validOperationKey(replyRevision))) {
			return 422, invalid
		}
		body := engineapi.UpdatePendingMessage{ExpectedRevisionId: revision, ScheduledAt: at}
		if r.Form.Get("replaceComposition") == "1" {
			original, target := r.Form.Get("originalText"), r.Form.Get("replyToMessageId")
			mentions, err := parseMentionSelections(r.Form.Get("mentions"), original)
			if err != nil || (strings.TrimSpace(original) == "" && len(ids) == 0) || (target != "" && !validOperationKey(target)) {
				return 422, invalid
			}
			body.Composition = &engineapi.OutgoingComposition{OriginalText: original}
			if len(ids) != 0 {
				body.Composition.AttachmentIds = &ids
			}
			if len(mentions) != 0 {
				body.Composition.Mentions = &mentions
			}
			if target != "" {
				body.Composition.ReplyToMessageId = &target
			}
			if replyRevision != "" {
				if target == "" {
					return 422, invalid
				}
				body.ExpectedReplyRevisionId = &replyRevision
				if replyExcerptSHA256 != "" {
					body.ExpectedReplyExcerptSha256 = &replyExcerptSHA256
				}
			}
		}
		if at == nil && body.Composition == nil {
			return 422, invalid
		}
		response, err := s.client.UpdatePendingMessageWithResponse(ctx, id, &engineapi.UpdatePendingMessageParams{IdempotencyKey: key}, body)
		if err != nil {
			return 503, mutationResult{Message: text.PendingAcceptance}
		}
		message, status, responseBody = response.JSON200, response.StatusCode(), response.Body
	}
	if message == nil {
		result := problemResult(text, status, responseBody)
		if result.Code == "state_conflict" || result.Code == "revision_conflict" {
			result.Rejected = true
		}
		return status, result
	}
	if r.Form.Get("replaceComposition") == "1" {
		if replyRevision != "" && (message.ExpectedReplyRevisionId == nil || *message.ExpectedReplyRevisionId != replyRevision) {
			return 503, mutationResult{Message: text.PendingAcceptance, Code: "unavailable"}
		}
		if replyExcerptSHA256 != "" && (message.ExpectedReplyExcerptSha256 == nil || *message.ExpectedReplyExcerptSha256 != replyExcerptSHA256) {
			return 503, mutationResult{Message: text.PendingAcceptance, Code: "unavailable"}
		}
		status, result := acceptedResult(text, *message)
		result.Redirect = safeReturn(r.Form.Get("returnTo"))
		return status, result
	}
	return 200, mutationResult{OK: true, Redirect: safeReturn(r.Form.Get("returnTo")), NextKey: newOperationKey()}
}

func composerAction(v view) string {
	if v.ScheduleEdit != nil {
		return "/ui/scheduling/update"
	}
	return "/ui/messages/send"
}

func composerReturn(v view) string {
	if v.ScheduleEdit != nil {
		return v.scheduleExitHref()
	}
	return v.href()
}

func scheduleQuoteSender(reply *engineapi.MessageReply) string {
	if reply.SenderName != nil {
		return *reply.SenderName
	}
	return ""
}

func (v view) scheduleVersion() string {
	if v.SchedulePage == nil {
		return ""
	}
	var version strings.Builder
	for _, item := range v.SchedulePage.Items {
		version.WriteString(item.Message.RevisionId)
		version.WriteString(item.Conversation.RevisionId)
	}
	if v.SchedulePage.NextCursor != nil {
		version.WriteString(*v.SchedulePage.NextCursor)
	}
	return version.String()
}

func sendInput(body, target, replyRevision, replyExcerptSHA256 string, mentions []engineapi.MentionSelection, attachments []string, at *time.Time) engineapi.SendMessage {
	payload := engineapi.SendMessage{OriginalText: body, ScheduledAt: at}
	if target != "" {
		payload.ReplyToMessageId = &target
	}
	if replyRevision != "" {
		payload.ExpectedReplyRevisionId = &replyRevision
	}
	if replyExcerptSHA256 != "" {
		payload.ExpectedReplyExcerptSha256 = &replyExcerptSHA256
	}
	if len(mentions) != 0 {
		payload.Mentions = &mentions
	}
	if len(attachments) != 0 {
		payload.AttachmentIds = &attachments
	}
	return payload
}

func pendingSendMutable(m engineapi.Message) bool {
	return m.SendState == "queued" && (m.DispatchStarted == nil || !*m.DispatchStarted)
}
