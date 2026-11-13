package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func compositionInput(text string, target *engineapi.ID, mentions *[]engineapi.MentionSelection, files *engineapi.StagedAttachmentIDs) domain.OutgoingComposition {
	out := domain.OutgoingComposition{OriginalText: text}
	if target != nil {
		out.ReplyTo = domain.ID(*target)
	}
	if mentions != nil {
		for _, m := range *mentions {
			out.Mentions = append(out.Mentions, domain.MentionSelection{ParticipantID: m.ParticipantId, Begin: m.Begin, End: m.End})
		}
	}
	if files != nil {
		for _, id := range *files {
			out.AttachmentIDs = append(out.AttachmentIDs, domain.ID(id))
		}
	}
	return out
}

func stagedAttachment(in domain.StagedAttachment) engineapi.StagedAttachment {
	return engineapi.StagedAttachment{Id: string(in.ID), FileName: in.FileName, MediaType: in.MediaType, Size: in.Size, Sha256: in.SHA256}
}

func addSchedulingFields(out *engineapi.Message, in domain.Message) {
	out.ScheduledAt = in.ScheduledAt
	if in.DispatchStarted {
		out.DispatchStarted = &in.DispatchStarted
	}
	if len(in.Attachments) != 0 {
		files := make([]engineapi.StagedAttachment, 0, len(in.Attachments))
		for _, file := range in.Attachments {
			files = append(files, stagedAttachment(file))
		}
		out.Attachments = &files
	}
	if in.Composition == nil || in.SendState != "queued" {
		return
	}
	content := engineapi.OutgoingComposition{OriginalText: in.Composition.OriginalText}
	if in.Composition.ReplyTo != "" {
		target := string(in.Composition.ReplyTo)
		content.ReplyToMessageId = &target
	}
	if len(in.Composition.Mentions) != 0 {
		mentions := make([]engineapi.MentionSelection, 0, len(in.Composition.Mentions))
		for _, m := range in.Composition.Mentions {
			mentions = append(mentions, engineapi.MentionSelection{ParticipantId: m.ParticipantID, Begin: m.Begin, End: m.End})
		}
		content.Mentions = &mentions
	}
	if len(in.Composition.AttachmentIDs) != 0 {
		files := make(engineapi.StagedAttachmentIDs, 0, len(in.Composition.AttachmentIDs))
		for _, id := range in.Composition.AttachmentIDs {
			files = append(files, string(id))
		}
		content.AttachmentIds = &files
	}
	out.Composition = &content
}

type singleAttachment struct {
	part *multipart.Part
	form *multipart.Reader
}

func (p singleAttachment) Read(data []byte) (int, error) {
	n, err := p.part.Read(data)
	if errors.Is(err, io.EOF) {
		if _, more := p.form.NextPart(); !errors.Is(more, io.EOF) {
			return n, errors.Join(application.ErrInvalid, more)
		}
	} else if err != nil {
		return n, errors.Join(application.ErrInvalid, err)
	}
	return n, err
}

func (s *Server) StageMessageAttachment(ctx context.Context, r engineapi.StageMessageAttachmentRequestObject) (engineapi.StageMessageAttachmentResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	part, err := r.Body.NextPart()
	if err != nil {
		return nil, errors.Join(application.ErrInvalid, err)
	}
	if part.FormName() != "file" || part.FileName() == "" {
		return nil, application.ErrInvalid
	}
	mediaType := part.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	file, replayed, err := s.engine.StageAttachment(ctx, domain.ID(r.ConversationId), domain.ID(r.AttachmentId), part.FileName(), mediaType, singleAttachment{part: part, form: r.Body})
	if err != nil {
		return nil, err
	}
	body := stagedAttachment(file)
	if replayed {
		return engineapi.StageMessageAttachment200JSONResponse(body), nil
	}
	location := "/api/v1/conversations/" + r.ConversationId + "/staged-attachments/" + r.AttachmentId
	return engineapi.StageMessageAttachment201JSONResponse{Body: body, Headers: engineapi.StageMessageAttachment201ResponseHeaders{Location: &location}}, nil
}

func (s *Server) GetStagedMessageAttachment(ctx context.Context, r engineapi.GetStagedMessageAttachmentRequestObject) (engineapi.GetStagedMessageAttachmentResponseObject, error) {
	file, body, err := s.engine.OpenStagedAttachment(ctx, domain.ID(r.ConversationId), domain.ID(r.AttachmentId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetStagedMessageAttachment200ApplicationoctetStreamResponse{
		Body: body, ContentLength: file.Size,
		Headers: engineapi.GetStagedMessageAttachment200ResponseHeaders{
			ContentDisposition: mime.FormatMediaType("attachment", map[string]string{"filename": file.FileName}),
		},
	}, nil
}

type scheduleCursor struct {
	Engine, Conversation domain.ID
	Selection            string `json:",omitempty"`
	Time                 time.Time
	ID                   domain.ID
}

func (s *Server) ListScheduledMessages(ctx context.Context, r engineapi.ListScheduledMessagesRequestObject) (engineapi.ListScheduledMessagesResponseObject, error) {
	snapshot, err := s.engine.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	query := application.ScheduledQuery{Limit: 50}
	if r.Params.Limit != nil {
		query.Limit = *r.Params.Limit
	}
	key := scheduleCursor{Engine: snapshot.ID}
	if r.Params.ConversationId != nil {
		if r.Params.ConversationIds != nil {
			return nil, application.ErrInvalid
		}
		key.Conversation = domain.ID(*r.Params.ConversationId)
		query.ConversationIDs = []domain.ID{key.Conversation}
	} else if r.Params.ConversationIds != nil {
		ids := slices.Clone(*r.Params.ConversationIds)
		if len(ids) < 1 || len(ids) > application.MaxScheduledConversationFilters {
			return nil, application.ErrInvalid
		}
		slices.Sort(ids)
		for _, id := range ids {
			query.ConversationIDs = append(query.ConversationIDs, domain.ID(id))
		}
		if len(ids) == 1 {
			key.Conversation = domain.ID(ids[0])
		} else {
			digest := sha256.Sum256([]byte(strings.Join(ids, "\n")))
			key.Selection = base64.RawURLEncoding.EncodeToString(digest[:])
		}
	}
	if r.Params.Cursor != nil {
		var previous scheduleCursor
		data, err := base64.RawURLEncoding.DecodeString(*r.Params.Cursor)
		if err != nil || json.Unmarshal(data, &previous) != nil || previous.Engine != key.Engine || previous.Conversation != key.Conversation || previous.Selection != key.Selection || previous.Time.IsZero() {
			return nil, application.ErrInvalid
		}
		query.AfterTime, query.AfterID = previous.Time, previous.ID
	}
	items, err := s.engine.ScheduledMessages(ctx, query)
	if err != nil {
		return nil, err
	}
	page := engineapi.ScheduledMessagePage{EngineId: string(snapshot.ID), Items: make([]engineapi.ScheduledMessageItem, 0, min(query.Limit, len(items)))}
	if len(items) > query.Limit {
		last := items[query.Limit-1].Message
		key.Time, key.ID = *last.ScheduledAt, last.ID
		data, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		next := base64.RawURLEncoding.EncodeToString(data)
		page.NextCursor = &next
		items = items[:query.Limit]
	}
	for _, item := range items {
		page.Items = append(page.Items, engineapi.ScheduledMessageItem{Message: message(item.Message), Conversation: conversation(item.Conversation)})
	}
	return engineapi.ListScheduledMessages200JSONResponse(page), nil
}

func (s *Server) UpdatePendingMessage(ctx context.Context, r engineapi.UpdatePendingMessageRequestObject) (engineapi.UpdatePendingMessageResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	var content *domain.OutgoingComposition
	if c := r.Body.Composition; c != nil {
		in := compositionInput(c.OriginalText, c.ReplyToMessageId, c.Mentions, c.AttachmentIds)
		content = &in
	}
	review := replyReviewInput(r.Body.ExpectedReplyRevisionId, r.Body.ExpectedReplyExcerptSha256)
	out, err := s.engine.UpdatePendingMessageWithReplyReview(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.MessageId), domain.ID(r.Body.ExpectedRevisionId), content, r.Body.ScheduledAt, false, review)
	if err != nil {
		return nil, err
	}
	return engineapi.UpdatePendingMessage200JSONResponse{PendingMessageUpdatedJSONResponse: engineapi.PendingMessageUpdatedJSONResponse(message(out.Message))}, nil
}

func (s *Server) SendScheduledMessageNow(ctx context.Context, r engineapi.SendScheduledMessageNowRequestObject) (engineapi.SendScheduledMessageNowResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	out, err := s.engine.UpdatePendingMessage(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.MessageId), domain.ID(r.Body.ExpectedRevisionId), nil, nil, true)
	if err != nil {
		return nil, err
	}
	return engineapi.SendScheduledMessageNow200JSONResponse{PendingMessageUpdatedJSONResponse: engineapi.PendingMessageUpdatedJSONResponse(message(out.Message))}, nil
}
