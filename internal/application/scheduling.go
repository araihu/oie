package application

import (
	"context"
	"errors"
	"io"
	"mime"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

const MaxStagedAttachmentSize = 20 << 20
const MaxMessageAttachments = 16

var ErrAttachmentTooLarge = errors.New("attachment too large")
var ErrReplySourceChanged = errors.New("reply source changed")

type ScheduledQuery struct {
	ConversationIDs []domain.ID
	AfterTime       time.Time
	AfterID         domain.ID
	Limit           int
}

const MaxScheduledConversationFilters = 200

type ScheduledMessage struct {
	Message      domain.Message
	Conversation domain.Conversation
}

type PendingMutation struct {
	Operation               Operation
	Fingerprint             string
	ExpectedRevisionID      domain.ID
	ExpectedReplyRevisionID domain.ID
	RevisionID              domain.ID
	Composition             *domain.Message
	ScheduledAt             *time.Time
	SendNow                 bool
}

type ReplyReview struct {
	RevisionID    domain.ID `json:"ReplyRevision,omitempty"`
	ExcerptSHA256 string    `json:"ReplyExcerptSHA256,omitempty"`
}

func (r ReplyReview) valid() bool {
	if r.RevisionID == "" {
		return r.ExcerptSHA256 == ""
	}
	return validID(r.RevisionID) && (r.ExcerptSHA256 == "" || domain.ValidReplyExcerptSHA256(r.ExcerptSHA256))
}

type SchedulingRepository interface {
	ReplayMessageMutation(context.Context, domain.ID, string, string) (*domain.Message, error)
	RecordOriginalWireAttempt(context.Context, domain.ID, domain.ID, string) error
	UpdatePendingMessage(context.Context, PendingMutation) (MessageResult, error)
	ScheduledMessages(context.Context, ScheduledQuery) ([]ScheduledMessage, error)
	StageAttachment(context.Context, domain.ID, domain.StagedAttachment, io.Reader) (domain.StagedAttachment, bool, error)
	StagedAttachment(context.Context, domain.ID, domain.ID) (domain.StagedAttachment, error)
	OpenStagedAttachment(context.Context, domain.ID, domain.ID) (domain.StagedAttachment, io.ReadCloser, error)
}

type AttachmentProtocol interface {
	AttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error
}

type OutgoingAttachment struct {
	domain.StagedAttachment
	Open func(context.Context) (io.ReadCloser, error)
}

func (e *Engine) scheduling() (SchedulingRepository, error) {
	repo, ok := e.repo.(SchedulingRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return repo, nil
}

func (e *Engine) StageAttachment(ctx context.Context, conversationID, id domain.ID, name, mediaType string, body io.Reader) (domain.StagedAttachment, bool, error) {
	if !validID(id) || !validID(conversationID) || name != path.Base(name) || name == "." || name == ".." || strings.ContainsRune(name, '\\') ||
		!utf8.ValidString(name) || strings.TrimSpace(name) == "" || len(name) > 255 || strings.ContainsFunc(name, unicode.IsControl) {
		return domain.StagedAttachment{}, false, ErrInvalid
	}
	mediaType, _, err := mime.ParseMediaType(mediaType)
	if err != nil || !strings.Contains(mediaType, "/") || strings.ContainsRune(mediaType, '*') {
		return domain.StagedAttachment{}, false, ErrInvalid
	}
	repo, err := e.scheduling()
	if err != nil {
		return domain.StagedAttachment{}, false, err
	}
	if _, err = e.Conversation(ctx, conversationID); err != nil {
		return domain.StagedAttachment{}, false, err
	}
	return repo.StageAttachment(ctx, conversationID, domain.StagedAttachment{ID: id, FileName: name, MediaType: mediaType}, body)
}

func (e *Engine) OpenStagedAttachment(ctx context.Context, conversationID, id domain.ID) (domain.StagedAttachment, io.ReadCloser, error) {
	if !validID(conversationID) || !validID(id) {
		return domain.StagedAttachment{}, nil, ErrInvalid
	}
	repo, err := e.scheduling()
	if err != nil {
		return domain.StagedAttachment{}, nil, err
	}
	return repo.OpenStagedAttachment(ctx, conversationID, id)
}

func validateComposition(in domain.OutgoingComposition) error {
	if !utf8.ValidString(in.OriginalText) || utf8.RuneCountInString(in.OriginalText) > 16384 || strings.ContainsRune(in.OriginalText, 0) ||
		(strings.TrimSpace(in.OriginalText) == "" && len(in.AttachmentIDs) == 0) ||
		(in.ReplyTo != "" && !validID(in.ReplyTo)) || len(in.AttachmentIDs) > MaxMessageAttachments {
		return ErrInvalid
	}
	for index, id := range in.AttachmentIDs {
		if !validID(id) || slices.Contains(in.AttachmentIDs[:index], id) {
			return ErrInvalid
		}
	}
	return validateMentionSelections(in.OriginalText, in.Mentions)
}

func normalizeScheduledAt(at *time.Time) (*time.Time, error) {
	if at == nil {
		return nil, nil
	}
	utc := at.UTC()
	if utc.Year() < 0 || utc.Year() > 9999 {
		return nil, ErrInvalid
	}
	return &utc, nil
}

func (e *Engine) prepareComposition(ctx context.Context, a domain.Account, c domain.Conversation, m domain.Message, in domain.OutgoingComposition, review ReplyReview) (domain.Message, error) {
	previous := m.Composition
	m.Composition, m.OriginalText = &in, in.OriginalText
	m.Reply, m.ReplyReference, m.References = nil, nil, nil
	if previous == nil || !slices.Equal(previous.Mentions, in.Mentions) || !sameMentionText(previous.OriginalText, in.OriginalText, in.Mentions) {
		frozen, err := e.freezeMentions(ctx, a, c, in.OriginalText, in.Mentions)
		if err != nil {
			return m, err
		}
		m.Mentions = frozen
	}
	switch {
	case in.ReplyTo == "":
		m.OutboundReply = nil
	case review.RevisionID == "" && previous != nil && previous.ReplyTo == in.ReplyTo && m.OutboundReply != nil:
		plan := *m.OutboundReply
		plan.BodyText = in.OriginalText
		m.OutboundReply = &plan
	default:
		target, err := e.Message(ctx, in.ReplyTo)
		if err != nil {
			if review.RevisionID != "" && errors.Is(err, ErrNotFound) {
				return m, ErrReplySourceChanged
			}
			return m, err
		}
		if target.AccountID != c.AccountID || target.ConversationID != c.ID {
			return m, ErrInvalid
		}
		if review.RevisionID != "" && (target.ID != in.ReplyTo || target.RevisionID != review.RevisionID) {
			return m, ErrReplySourceChanged
		}
		m.OutboundReply = outboundReply(a, c, target, in.OriginalText, e.protocols)
		if review.ExcerptSHA256 != "" && domain.ReplyExcerptSHA256(m.OutboundReply.Excerpt) != review.ExcerptSHA256 {
			return m, ErrReplySourceChanged
		}
		m.OutboundReply.Reviewed = review.RevisionID != ""
		m.OutboundReply.ExpectedExcerptSHA256 = review.ExcerptSHA256
	}
	if plan := m.OutboundReply; plan != nil {
		m.OriginalText = quotedReplyBody(plan)
		domain.ProjectOutboundReply(&m)
		if plan.Reference != nil {
			m.ReplyReference = &domain.ReplyReference{Target: *plan.Reference, Fallback: slices.Clone(plan.Fallback)}
		}
	}
	if provider, ok := e.protocols.(OutgoingReferenceProvider); ok {
		m.References = provider.OutgoingReferences(a, outgoingMessage(m, c))
	}
	if previous != nil && slices.Equal(previous.AttachmentIDs, in.AttachmentIDs) {
		return m, nil
	}
	m.Attachments = nil
	if len(in.AttachmentIDs) == 0 {
		return m, nil
	}
	repo, err := e.scheduling()
	if err != nil {
		return m, err
	}
	for _, id := range in.AttachmentIDs {
		file, err := repo.StagedAttachment(ctx, c.ID, id)
		if err != nil {
			return m, err
		}
		m.Attachments = append(m.Attachments, file)
	}
	uploader, ok := e.protocols.(AttachmentProtocol)
	if !ok {
		return m, ErrProtocolUnavailable
	}
	return m, uploader.AttachmentAvailability(ctx, a, m.Attachments)
}

func sameMentionText(before, after string, mentions []domain.MentionSelection) bool {
	old, next := []rune(before), []rune(after)
	for _, mention := range mentions {
		if mention.End > len(old) || mention.End > len(next) ||
			!slices.Equal(old[mention.Begin:mention.End], next[mention.Begin:mention.End]) {
			return false
		}
	}
	return true
}

func (e *Engine) UpdatePendingMessage(ctx context.Context, key, id, expected domain.ID, composition *domain.OutgoingComposition, at *time.Time, sendNow bool) (MessageResult, error) {
	return e.UpdatePendingMessageWithReplyReview(ctx, key, id, expected, composition, at, sendNow, ReplyReview{})
}

func (e *Engine) UpdatePendingMessageWithReplyReview(ctx context.Context, key, id, expected domain.ID, composition *domain.OutgoingComposition, at *time.Time, sendNow bool, review ReplyReview) (result MessageResult, err error) {
	if !validID(key) || !validID(id) || !validID(expected) || (!sendNow && composition == nil && at == nil) {
		return MessageResult{}, ErrInvalid
	}
	if !review.valid() || (review.RevisionID != "" && (sendNow || composition == nil || composition.ReplyTo == "")) {
		return MessageResult{}, ErrInvalid
	}
	if composition != nil {
		if err := validateComposition(*composition); err != nil {
			return MessageResult{}, err
		}
	}
	at, err = normalizeScheduledAt(at)
	if err != nil {
		return MessageResult{}, err
	}
	kind := "update_pending_message"
	if sendNow {
		kind = "send_scheduled_message_now"
	}
	hash := fingerprint(kind, struct {
		ID, Expected domain.ID
		Composition  *domain.OutgoingComposition
		At           *time.Time
		ReplyReview
	}{id, expected, composition, at, review})
	repo, err := e.scheduling()
	if err != nil {
		return MessageResult{}, err
	}
	m, err := repo.ReplayMessageMutation(ctx, key, kind, hash)
	if err != nil {
		return MessageResult{}, err
	}
	if m != nil {
		return MessageResult{Message: *m, Replayed: true}, nil
	}
	// Preparation runs outside the write transaction. A matching request may
	// have committed after the first receipt read, making a precondition stale.
	defer func() {
		if err == nil || ctx.Err() != nil || errors.Is(err, ErrIdempotencyConflict) {
			return
		}
		m, replayErr := repo.ReplayMessageMutation(ctx, key, kind, hash)
		if errors.Is(replayErr, ErrIdempotencyConflict) {
			err = replayErr
		} else if replayErr == nil && m != nil {
			result, err = MessageResult{Message: *m, Replayed: true}, nil
		}
	}()
	now := e.now().UTC()
	if at != nil && !at.After(now) {
		return MessageResult{}, ErrInvalid
	}
	rev, err := e.newID()
	if err != nil {
		return MessageResult{}, err
	}
	in := PendingMutation{Operation: Operation{ID: key, ResourceID: id, Kind: kind, CreatedAt: now}, Fingerprint: hash, ExpectedRevisionID: expected, ExpectedReplyRevisionID: review.RevisionID, RevisionID: rev, ScheduledAt: at, SendNow: sendNow}
	if composition != nil {
		current, err := e.Message(ctx, id)
		if err != nil {
			return MessageResult{}, err
		}
		if current.ID != id || !current.IsOwn || current.SendState != "queued" || current.RevisionID != expected {
			return MessageResult{}, StateConflict{Current: current}
		}
		c, err := e.Conversation(ctx, current.ConversationID)
		if err != nil {
			return MessageResult{}, err
		}
		a, err := e.Account(ctx, c.AccountID)
		if err != nil {
			return MessageResult{}, err
		}
		updated, err := e.prepareComposition(ctx, a, c, current, *composition, review)
		if err != nil {
			return MessageResult{}, err
		}
		in.Composition = &updated
	}
	out, err := repo.UpdatePendingMessage(ctx, in)
	if err == nil {
		e.outgoingWorkCommitted()
	}
	return out, err
}

func (e *Engine) ScheduledMessages(ctx context.Context, query ScheduledQuery) ([]ScheduledMessage, error) {
	if query.Limit < 1 || query.Limit > 100 || len(query.ConversationIDs) > MaxScheduledConversationFilters || (!query.AfterTime.IsZero() && !validID(query.AfterID)) {
		return nil, ErrInvalid
	}
	seen := make(map[domain.ID]bool, len(query.ConversationIDs))
	for _, id := range query.ConversationIDs {
		if seen[id] {
			return nil, ErrInvalid
		}
		seen[id] = true
		if _, err := e.Conversation(ctx, id); err != nil {
			return nil, err
		}
	}
	repo, err := e.scheduling()
	if err != nil {
		return nil, err
	}
	query.Limit++
	return repo.ScheduledMessages(ctx, query)
}

func (e *Engine) attachOutgoingFiles(out *Outgoing, message domain.Message) error {
	repo, ok := e.repo.(SchedulingRepository)
	if ok {
		out.RecordWireAttempt = func(ctx context.Context, body string) error {
			return repo.RecordOriginalWireAttempt(ctx, message.ID, message.RevisionID, body)
		}
	}
	if len(message.Attachments) == 0 {
		return nil
	}
	if !ok {
		return ErrProtocolUnavailable
	}
	for _, attachment := range message.Attachments {
		out.Attachments = append(out.Attachments, OutgoingAttachment{
			StagedAttachment: attachment,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				_, body, err := repo.OpenStagedAttachment(ctx, message.ConversationID, attachment.ID)
				return body, err
			},
		})
	}
	return nil
}
