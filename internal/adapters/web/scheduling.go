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
	"path"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed scheduling.js
var schedulingScript []byte

//go:embed scheduling.css
var schedulingStylesheet []byte

//go:embed wavesurfer/player.mjs
var waveSurferPlayer []byte

//go:embed wavesurfer/record.mjs
var waveSurferRecorder []byte

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
	if includeDestinations && v.ScheduledDestinationsSupported {
		p.IncludeDestinations = &includeDestinations
	}
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
	// Keep recent conversations available, including those without a pending send.
	seen := make(map[string]bool)
	add := func(d engineapi.ScheduledDestination) {
		if !seen[d.Id] {
			seen[d.Id] = true
			v.ScheduleDestinations = append(v.ScheduleDestinations, d)
		}
	}
	addConversation := func(c engineapi.Conversation) {
		add(engineapi.ScheduledDestination{Id: c.Id, AccountId: c.AccountId, Title: c.Title})
	}
	limit := 100
	conversations, err := s.client.ListConversationsWithResponse(ctx, &engineapi.ListConversationsParams{Limit: &limit})
	if err == nil && conversations.JSON200 != nil {
		for _, c := range conversations.JSON200.Items {
			addConversation(c)
		}
	}
	if destinations := v.SchedulePage.Destinations; destinations != nil {
		for _, d := range *destinations {
			add(d)
		}
	} else {
		for _, item := range v.SchedulePage.Items {
			addConversation(item.Conversation)
		}
	}
	for _, id := range v.ScheduleConversations {
		if seen[id] {
			continue
		}
		if c, err := s.client.GetConversationWithResponse(ctx, id); err == nil && c.JSON200 != nil {
			addConversation(*c.JSON200)
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

// Restore the same video type used by the picker for files staged with a generic MIME.
func stagedVideoType(header http.Header) string {
	kind, _, _ := mime.ParseMediaType(header.Get("X-Oie-Attachment-Type"))
	if kind == "" || kind == "application/octet-stream" {
		_, params, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
		// Keep this aligned with draftVideoType even on hosts without a MIME database.
		switch strings.ToLower(path.Ext(params["filename"])) {
		case ".mp4", ".m4v":
			return "video/mp4"
		case ".mov":
			return "video/quicktime"
		case ".webm":
			return "video/webm"
		case ".ogv":
			return "video/ogg"
		case ".mkv":
			return "video/x-matroska"
		case ".avi":
			return "video/x-msvideo"
		}
	}
	switch kind {
	case "video/x-m4v":
		return "video/mp4"
	case "video/mp4", "video/ogg", "video/webm", "video/quicktime", "video/x-matroska", "video/x-msvideo", "video/avi":
		return kind
	}
	return ""
}

// Reset the idle deadline as the upstream transport consumes the multipart body.
type stageUploadReader struct {
	io.Reader
	connection *http.ResponseController
	deadline   *time.Timer
	idle       time.Duration
}

func (r stageUploadReader) Read(p []byte) (int, error) {
	// A stalled HTTP/1 body read must be interrupted at the socket. Canceling
	// the upstream request alone leaves its transport waiting for this read.
	_ = r.connection.SetReadDeadline(time.Now().Add(r.idle))
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.deadline.Reset(r.idle)
	}
	return n, err
}

// Time only reads from the engine, so a paused player can keep its connection.
type stageDownloadReader struct {
	io.Reader
	deadline *time.Timer
}

func (r stageDownloadReader) Read(p []byte) (int, error) {
	r.deadline.Reset(90 * time.Second)
	defer r.deadline.Stop()
	return r.Reader.Read(p)
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
	ctx := r.Context()
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
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		const idle = 90 * time.Second
		connection := http.NewResponseController(w)
		defer func() { _ = connection.SetReadDeadline(time.Time{}) }()
		deadline := time.AfterFunc(idle, cancel)
		defer deadline.Stop()
		response, err := s.archiveClient.StageMessageAttachmentWithBodyWithResponse(ctx, conversation, attachment, r.Header.Get("Content-Type"), stageUploadReader{r.Body, connection, deadline, idle})
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
	params := &engineapi.GetStagedMessageAttachmentParams{}
	if value := r.Header.Get("Range"); !q.Has("download") && value != "" && !strings.Contains(value, ",") {
		params.Range = &value
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	response, err := s.streamClient.GetStagedMessageAttachment(ctx, conversation, attachment, params)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		w.Header().Set("Content-Range", response.Header.Get("Content-Range"))
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		w.WriteHeader(response.StatusCode)
		return
	}
	// A staged filename and its declared media type come from the uploader.
	// Inline types are limited to recognized raster images, supported audio and explicitly requested video.
	deadline := time.AfterFunc(90*time.Second, cancel)
	defer deadline.Stop()
	body := bufio.NewReaderSize(stageDownloadReader{response.Body, deadline}, 512)
	prefix, _ := body.Peek(512)
	contentType := http.DetectContentType(prefix)
	declared := response.Header.Get("X-Oie-Attachment-Type")
	playType := ""
	if q.Has("play") && !q.Has("download") {
		playType = stagedVideoType(response.Header)
	}
	if playType != "" {
		contentType = playType
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	} else if !q.Has("download") && !rasterAttachment(contentType) {
		if audio := audioMediaType(declared); audio != "" {
			contentType = audio
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		}
	}
	if q.Has("download") || playType == "" && !rasterAttachment(contentType) && audioMediaType(contentType) == "" {
		contentType = "application/octet-stream"
		disposition := response.Header.Get("Content-Disposition")
		kind, params, err := mime.ParseMediaType(disposition)
		if err != nil || kind != "attachment" || params["filename"] == "" {
			// Older engines returned only the bytes. The download link still supplies the draft filename.
			disposition = "attachment"
		}
		w.Header().Set("Content-Disposition", disposition)
	}
	for _, header := range []string{"Content-Length", "Accept-Ranges", "Content-Range"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(response.StatusCode)
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
