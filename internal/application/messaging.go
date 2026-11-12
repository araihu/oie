package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

var ErrProtocolUnavailable = errors.New("protocol unavailable")
var ErrStateConflict = errors.New("message state conflict")

type StateConflict struct{ Current domain.Message }

func (e StateConflict) Error() string { return ErrStateConflict.Error() }
func (e StateConflict) Unwrap() error { return ErrStateConflict }

type ConnectionInput struct{ Password, Endpoint string }
type Incoming struct {
	Reaction                                  *IncomingReaction
	SenderAddress                             string
	MediaThumbnails                           []domain.MediaThumbnail
	AccountID                                 domain.ID
	Address, ProviderID, OriginalText         string
	CreatedAt                                 time.Time
	IsOwn                                     bool
	Historical                                bool
	Kind, SenderName, ArchiveScope, ArchiveID string
	OwnRoomNickname                           string
	References                                []domain.MessageReference
	Reply                                     *domain.ReplyReference
	CanonicalID                               domain.ID
	Correction                                *domain.MessageCorrection
}
type Rejected struct {
	AccountID          domain.ID
	MessageID, Address string
	FromServer         bool
	FromParticipant    bool
}

type Outgoing struct {
	Reaction              *domain.OutgoingReaction
	ReactionEmojis        []string
	ID                    domain.ID
	Address, OriginalText string
	Kind                  string
	RoomIntentRevisionID  domain.ID
	Reply                 *domain.ReplyReference
	Mentions              []domain.MessageMention
	Correction            *domain.OutgoingCorrection
	Attachments           []OutgoingAttachment
	RecordWireAttempt     func(context.Context, string) error
}

// Outcomes distinguish definitely unsent failures from ambiguous external effects.
const (
	Submitted = "submitted"
	Unknown   = "unknown"
	NotSent   = "not_sent"
	Failed    = "failed"
)

type ProtocolHost interface {
	Account(context.Context, domain.ID) (domain.Account, error)
	Receive(context.Context, Incoming) error
	Reject(context.Context, Rejected) error
	ConnectionChanged(context.Context, domain.Connection) error
}
type Protocols interface {
	NormalizeAddress(string, string) (string, error)
	Configure(context.Context, domain.Account, ConnectionInput) (domain.Connection, error)
	Disconnect(context.Context, domain.Account) (domain.Connection, error)
	Connection(context.Context, domain.Account) (domain.Connection, error)
	Send(context.Context, domain.Account, Outgoing) string
}

// OriginalWireAttemptProtocol identifies adapters that require a supplied
// RecordWireAttempt callback to succeed before writing the original message.
type OriginalWireAttemptProtocol interface {
	RequiresOriginalWireAttempt(domain.Account) bool
}
type ConversationQuery struct {
	Cursor                              int64
	AccountID                           domain.ID
	Query, Kind                         string
	Archived, FavoritesOnly, UnreadOnly bool
	Limit                               int
}
type ConversationMutation struct {
	Operation    Operation
	Fingerprint  string
	Conversation domain.Conversation
}
type MessageMutation struct {
	Operation              Operation
	Fingerprint            string
	Message                domain.Message
	ConversationRevisionID domain.ID
}
type CancelMutation struct {
	Operation                                 Operation
	Fingerprint                               string
	MessageID, ExpectedRevisionID, RevisionID domain.ID
}
type ConversationResult struct {
	Conversation       domain.Conversation
	Replayed, Existing bool
}
type MessageResult struct {
	Message  domain.Message
	Replayed bool
}

type MessagingRepository interface {
	OpenConversation(context.Context, ConversationMutation) (ConversationResult, error)
	Conversation(context.Context, domain.ID) (domain.Conversation, error)
	Conversations(context.Context, ConversationQuery) ([]domain.Conversation, error)
	AcceptMessage(context.Context, MessageMutation) (MessageResult, error)
	Message(context.Context, domain.ID) (domain.Message, error)
	Messages(context.Context, domain.ID, domain.ID, int) ([]domain.Message, error)
	ReceiveMessage(context.Context, domain.Conversation, domain.Message, domain.ID) error
	CancelMessage(context.Context, CancelMutation) (MessageResult, error)
	PendingMessages(context.Context, time.Time) ([]domain.Message, error)
	InterruptedMessages(context.Context) ([]domain.Message, error)
	TransitionMessage(context.Context, domain.ID, string, string, domain.ID, time.Time, time.Time) (domain.Message, error)
	RecordConnection(context.Context, domain.Connection) error
}

type PendingMessageCursor struct {
	NextAttemptAt time.Time
	MessageID     domain.ID
}

type attachmentOutboxRepository interface {
	PendingMessagesByAttachments(context.Context, time.Time, bool, PendingMessageCursor) ([]domain.Message, PendingMessageCursor, error)
	ClaimDispatchMessage(ctx context.Context, id, expected, roomIntent, revision domain.ID, at time.Time, guardWire bool) (domain.Message, error)
	RecoverInterruptedMessage(ctx context.Context, id, expected, revision domain.ID, at time.Time) error
}

// Physical sources retain the body addressed by a client wire identity. A
// canonical projection may already display a different, corrected body.
type MessageSourceRepository interface {
	MessageSource(context.Context, domain.ID) (domain.Message, error)
}

func (e *Engine) SetProtocols(p Protocols) { e.protocols = p } // Configure once, before serving.
func (e *Engine) messaging() (MessagingRepository, error) {
	r, ok := e.repo.(MessagingRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r, nil
}
func (e *Engine) canonical(protocol, address string) (string, error) {
	if e.protocols == nil {
		return "", ErrProtocolUnavailable
	}
	value, err := e.protocols.NormalizeAddress(protocol, strings.TrimSpace(address))
	if err != nil {
		return "", ErrInvalid
	}
	return value, nil
}
func fingerprint(kind string, v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(append([]byte(kind+":"), data...))
	return hex.EncodeToString(sum[:])
}
func validID(id domain.ID) bool { _, err := domain.ParseID(string(id)); return err == nil }
func validMessage(text string) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != "" && utf8.RuneCountInString(text) <= 16384 && !strings.ContainsRune(text, 0)
}
func (e *Engine) Connection(ctx context.Context, id domain.ID) (domain.Connection, error) {
	a, err := e.Account(ctx, id)
	if err != nil {
		return domain.Connection{}, err
	}
	if e.protocols == nil {
		return domain.Connection{AccountID: id, State: "not_connected", UpdatedAt: a.CreatedAt}, nil
	}
	return e.protocols.Connection(ctx, a)
}
func (e *Engine) ConfigureConnection(ctx context.Context, id domain.ID, in ConnectionInput) (domain.Connection, error) {
	a, err := e.Account(ctx, id)
	if err != nil {
		return domain.Connection{}, err
	}
	if in.Password == "" || len(in.Password) > 4096 || len(in.Endpoint) > 255 || strings.ContainsAny(in.Password, "\x00") {
		return domain.Connection{}, ErrInvalid
	}
	if e.protocols == nil {
		return domain.Connection{}, ErrProtocolUnavailable
	}
	return e.protocols.Configure(ctx, a, in)
}
func (e *Engine) Disconnect(ctx context.Context, id domain.ID) (domain.Connection, error) {
	a, err := e.Account(ctx, id)
	if err != nil {
		return domain.Connection{}, err
	}
	if e.protocols == nil {
		return e.Connection(ctx, id)
	}
	return e.protocols.Disconnect(ctx, a)
}
func (e *Engine) OpenConversation(ctx context.Context, key, accountID domain.ID, address string) (ConversationResult, error) {
	if !validID(key) {
		return ConversationResult{}, ErrInvalid
	}
	a, err := e.Account(ctx, accountID)
	if err != nil {
		return ConversationResult{}, err
	}
	address, err = e.canonical(a.Protocol, address)
	if err != nil {
		return ConversationResult{}, err
	}
	repo, err := e.messaging()
	if err != nil {
		return ConversationResult{}, err
	}
	id, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	rev, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	peer, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	now := e.now().UTC()
	c := domain.Conversation{ID: id, RevisionID: rev, AccountID: accountID, PeerID: peer, Kind: "direct", Address: address, Title: address, UpdatedAt: now}
	return repo.OpenConversation(ctx, ConversationMutation{Operation: Operation{ID: key, ResourceID: id, CreatedAt: now, Kind: "open_conversation"}, Fingerprint: fingerprint("open_conversation", []string{string(accountID), address}), Conversation: c})
}
func (e *Engine) Conversation(ctx context.Context, id domain.ID) (domain.Conversation, error) {
	if !validID(id) {
		return domain.Conversation{}, ErrInvalid
	}
	repo, err := e.messaging()
	if err != nil {
		return domain.Conversation{}, err
	}
	return repo.Conversation(ctx, id)
}
func (e *Engine) Conversations(ctx context.Context, cursor string, q ConversationQuery) ([]domain.Conversation, error) {
	if q.Limit < 1 || q.Limit > 100 || utf8.RuneCountInString(q.Query) > 200 || (q.AccountID != "" && !validID(q.AccountID)) || (q.Kind != "" && q.Kind != "direct" && q.Kind != "room") {
		return nil, ErrInvalid
	}
	q.Cursor = math.MaxInt64
	if cursor != "" {
		state, err := e.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		id, seq, ok := strings.Cut(cursor, ":")
		n, err := strconv.ParseInt(seq, 10, 64)
		if !ok || err != nil || id != string(state.ID) || n <= 0 || n > state.Sequence || strconv.FormatInt(n, 10) != seq {
			return nil, ErrInvalidCursor
		}
		q.Cursor = n
	}
	repo, err := e.messaging()
	if err != nil {
		return nil, err
	}
	q.Limit++
	return repo.Conversations(ctx, q)
}
func (e *Engine) SendMessage(ctx context.Context, key, conversationID domain.ID, text string) (MessageResult, error) {
	return e.SendMessageWithReply(ctx, key, conversationID, text, "")
}
func (e *Engine) SendMessageWithReply(ctx context.Context, key, conversationID domain.ID, text string, replyTo domain.ID) (MessageResult, error) {
	return e.SendMessageWithMentions(ctx, key, conversationID, text, replyTo, nil)
}

func (e *Engine) SendMessageWithMentions(ctx context.Context, key, conversationID domain.ID, text string, replyTo domain.ID, mentions []domain.MentionSelection) (MessageResult, error) {
	return e.SendComposition(ctx, key, conversationID, domain.OutgoingComposition{OriginalText: text, ReplyTo: replyTo, Mentions: mentions}, nil)
}

func (e *Engine) SendComposition(ctx context.Context, key, conversationID domain.ID, in domain.OutgoingComposition, at *time.Time) (MessageResult, error) {
	return e.SendCompositionWithReplyReview(ctx, key, conversationID, in, at, ReplyReview{})
}

func (e *Engine) SendCompositionWithReplyReview(ctx context.Context, key, conversationID domain.ID, in domain.OutgoingComposition, at *time.Time, review ReplyReview) (result MessageResult, err error) {
	if !validID(key) || !validID(conversationID) {
		return MessageResult{}, ErrInvalid
	}
	if err := validateComposition(in); err != nil {
		return MessageResult{}, err
	}
	at, err = normalizeScheduledAt(at)
	if err != nil {
		return MessageResult{}, err
	}
	if !review.valid() || (review.RevisionID != "" && (in.ReplyTo == "" || at == nil)) {
		return MessageResult{}, ErrInvalid
	}
	parts := []string{string(conversationID), in.OriginalText}
	if in.ReplyTo != "" {
		parts = append(parts, string(in.ReplyTo))
	}
	requestFingerprint := fingerprint("send_message", mentionFingerprint(parts, in.Mentions))
	if at != nil || len(in.AttachmentIDs) != 0 {
		requestFingerprint = fingerprint("send_composition", struct {
			Destination domain.ID
			Composition domain.OutgoingComposition
			At          *time.Time
			ReplyReview
		}{conversationID, in, at, review})
	}
	repo, err := e.messaging()
	if err != nil {
		return MessageResult{}, err
	}
	if replay, ok := e.repo.(MessageReplayRepository); ok {
		m, err := replay.ReplayAcceptedMessage(ctx, key, requestFingerprint)
		if err != nil {
			return MessageResult{}, err
		}
		if m != nil {
			return MessageResult{Message: *m, Replayed: true}, nil
		}
		defer func() {
			// Another request may have committed before the source changed.
			if !errors.Is(err, ErrReplySourceChanged) || ctx.Err() != nil {
				return
			}
			m, replayErr := replay.ReplayAcceptedMessage(ctx, key, requestFingerprint)
			if replayErr != nil {
				err = replayErr
			} else if m != nil {
				result, err = MessageResult{Message: *m, Replayed: true}, nil
			}
		}()
	}
	c, err := e.Conversation(ctx, conversationID)
	if err != nil {
		return MessageResult{}, err
	}
	if c.Kind != "direct" && c.Kind != "room" {
		return MessageResult{}, ErrProtocolUnavailable
	}
	if c.Kind == "room" {
		if _, ok := e.protocols.(RoomProtocol); !ok {
			return MessageResult{}, ErrProtocolUnavailable
		}
		intent, err := e.roomIntent(ctx, c.ID)
		if err != nil {
			return MessageResult{}, err
		}
		if !intent.Active {
			return MessageResult{}, ErrRoomNotJoined
		}
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return MessageResult{}, err
	}
	id, err := e.newID()
	if err != nil {
		return MessageResult{}, err
	}
	rev, err := e.newID()
	if err != nil {
		return MessageResult{}, err
	}
	cr, err := e.newID()
	if err != nil {
		return MessageResult{}, err
	}
	now := e.now().UTC()
	if at != nil && !at.After(now) {
		return MessageResult{}, ErrInvalid
	}
	m := domain.Message{ID: id, RevisionID: rev, AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: a.ID, DisplayName: a.Address}, CreatedAt: now, IsOwn: true, SendState: "queued", ProviderID: string(id), ScheduledAt: at}
	m, err = e.prepareComposition(ctx, a, c, m, in, review)
	if err != nil {
		return MessageResult{}, err
	}
	result, err = repo.AcceptMessage(ctx, MessageMutation{Operation: Operation{ID: key, ResourceID: id, CreatedAt: now, Kind: "send_message"}, Fingerprint: requestFingerprint, Message: m, ConversationRevisionID: cr})
	if err == nil {
		e.outgoingWorkCommitted()
	}
	return result, err
}

func (e *Engine) Message(ctx context.Context, id domain.ID) (domain.Message, error) {
	if !validID(id) {
		return domain.Message{}, ErrInvalid
	}
	r, err := e.messaging()
	if err != nil {
		return domain.Message{}, err
	}
	return r.Message(ctx, id)
}
func (e *Engine) Messages(ctx context.Context, id, cursor domain.ID, limit int) ([]domain.Message, error) {
	if (cursor != "" && !validID(cursor)) || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	if _, err := e.Conversation(ctx, id); err != nil {
		return nil, err
	}
	r, err := e.messaging()
	if err != nil {
		return nil, err
	}
	return r.Messages(ctx, id, cursor, limit+1)
}
func (e *Engine) CancelMessage(ctx context.Context, key, id, expected domain.ID) (MessageResult, error) {
	if !validID(key) || !validID(id) || !validID(expected) {
		return MessageResult{}, ErrInvalid
	}
	r, err := e.messaging()
	if err != nil {
		return MessageResult{}, err
	}
	rev, err := e.newID()
	if err != nil {
		return MessageResult{}, err
	}
	now := e.now().UTC()
	return r.CancelMessage(ctx, CancelMutation{Operation: Operation{ID: key, ResourceID: id, CreatedAt: now, Kind: "cancel_message"}, Fingerprint: fingerprint("cancel_message", []domain.ID{id, expected}), MessageID: id, ExpectedRevisionID: expected, RevisionID: rev})
}
func (e *Engine) Receive(ctx context.Context, in Incoming) error {
	if in.Reaction != nil {
		if in.Reaction.AccountID != in.AccountID {
			return ErrInvalid
		}
		return e.ReceiveReaction(ctx, *in.Reaction)
	}
	if !utf8.ValidString(in.OriginalText) || strings.TrimSpace(in.OriginalText) == "" || strings.ContainsRune(in.OriginalText, 0) || len(in.OriginalText) > (1<<20) || len(in.ProviderID) > (1<<20) {
		return ErrInvalid
	}
	a, err := e.Account(ctx, in.AccountID)
	if err != nil {
		return err
	}
	address, err := e.canonical(a.Protocol, in.Address)
	if err != nil {
		return err
	}
	r, err := e.messaging()
	if err != nil {
		return err
	}
	if in.CanonicalID != "" && validID(in.CanonicalID) {
		existing, lookupErr := r.Message(ctx, in.CanonicalID)
		if sources, ok := e.repo.(MessageSourceRepository); ok {
			existing, lookupErr = sources.MessageSource(ctx, in.CanonicalID)
		}
		if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
			return lookupErr
		}
		if lookupErr == nil && existing.IsOwn && existing.AccountID == a.ID && (existing.OriginalText == in.OriginalText || (existing.AttachmentWireText != "" && existing.AttachmentWireText == in.OriginalText)) {
			chat, err := r.Conversation(ctx, existing.ConversationID)
			if err != nil {
				return err
			}
			if chat.Address == address && chat.Kind == in.Kind {
				in.ProviderID = string(existing.ID)
			}
		}
		if errors.Is(lookupErr, ErrNotFound) && in.IsOwn {
			if edits, ok := e.repo.(MessageEditRepository); ok {
				pending, editErr := edits.MessageEdit(ctx, in.CanonicalID)
				if editErr != nil && !errors.Is(editErr, ErrNotFound) {
					return editErr
				}
				if editErr == nil && pending.Command.AccountID == a.ID && pending.Command.OriginalText == in.OriginalText {
					chat, err := r.Conversation(ctx, pending.Command.ConversationID)
					if err != nil {
						return err
					}
					if chat.Address == address && chat.Kind == in.Kind {
						in.ProviderID = string(in.CanonicalID)
					}
				}
			}
		}
	}
	ids := make([]domain.ID, 6)
	for i := range ids {
		ids[i], err = e.newID()
		if err != nil {
			return err
		}
	}
	if in.Kind == "" {
		in.Kind = "direct"
	}
	if in.Kind != "direct" && in.Kind != "room" {
		return ErrInvalid
	}
	now := e.now().UTC()
	created := in.CreatedAt.UTC()
	if created.IsZero() {
		created = now
	}
	c := domain.Conversation{ID: ids[0], RevisionID: ids[1], PeerID: ids[2], AccountID: a.ID, Kind: in.Kind, Address: address, Title: address, UpdatedAt: created}
	if in.SenderAddress != "" {
		if !validText(in.SenderAddress, 1023) {
			return ErrInvalid
		}
		if e.protocols != nil {
			in.SenderAddress, err = e.canonical(a.Protocol, in.SenderAddress)
			if err != nil {
				return err
			}
		}
		if in.Kind == "room" && in.SenderAddress == address {
			return ErrInvalid
		}
	}
	m := domain.Message{ID: ids[3], RevisionID: ids[4], AccountID: a.ID, Sender: domain.Sender{DisplayName: address, PersonalAddress: in.SenderAddress}, OriginalText: in.OriginalText, CreatedAt: created, IsOwn: in.IsOwn, SendState: "received", ProviderID: in.ProviderID, ArchiveScope: in.ArchiveScope, ArchiveID: in.ArchiveID, Historical: in.Historical, MediaThumbnails: normalizeMediaThumbnails(in.OriginalText, in.MediaThumbnails), References: in.References, ReplyReference: in.Reply, Correction: in.Correction}
	m.MentionsYou = incomingRoomMention(in)
	if in.Kind == "room" && !in.IsOwn {
		m.ObservedRoomNickname = in.OwnRoomNickname
	}
	if in.SenderName != "" {
		m.Sender.DisplayName = in.SenderName
	}
	if in.IsOwn {
		m.Sender.ID = a.ID
		m.SendState = "submitted"
	}
	if err = r.ReceiveMessage(ctx, c, m, ids[5]); err != nil {
		return err
	}
	if edits, ok := e.repo.(MessageEditRepository); ok && in.IsOwn && in.Correction != nil {
		return edits.ObserveMessageEdit(ctx, a.ID, in.ProviderID, *in.Correction, in.OriginalText, ids[5], now)
	}
	return nil
}
func (e *Engine) ConnectionChanged(ctx context.Context, c domain.Connection) error {
	r, err := e.messaging()
	if err != nil {
		return err
	}
	if err := r.RecordConnection(ctx, c); err != nil {
		return err
	}
	if c.State == "connected" {
		e.wakeRooms()
		e.outgoingWorkCommitted()
	} else {
		e.notifyEvents()
	}
	return nil
}
func (e *Engine) outgoingWorkCommitted() {
	select {
	case e.work <- struct{}{}:
	default:
	}
	select {
	case e.attachmentWork <- struct{}{}:
	default:
	}
	e.notifyEvents()
}

// Recover interrupted claims before either lane can dispatch. Network I/O
// starts after an atomic claim and never spans a repository transaction.
func (e *Engine) runOutbox(ctx context.Context) error {
	r, err := e.messaging()
	if err != nil {
		return err
	}
	split, _ := e.repo.(attachmentOutboxRepository)
	interrupted, err := r.InterruptedMessages(ctx)
	if err != nil {
		return err
	}
	for _, m := range interrupted {
		rev, err := e.newID()
		if err != nil {
			return err
		}
		now := e.now().UTC()
		if split != nil {
			err = split.RecoverInterruptedMessage(ctx, m.ID, m.RevisionID, rev, now)
		} else {
			_, err = r.TransitionMessage(ctx, m.ID, "dispatching", Unknown, rev, now, now)
		}
		if err != nil && !errors.Is(err, ErrStateConflict) {
			return err
		}
	}
	if len(interrupted) != 0 {
		e.notifyEvents()
	}
	if err := e.recoverMessageReactions(ctx); err != nil {
		return err
	}
	if err := e.recoverMessageEdits(ctx); err != nil {
		return err
	}
	if split == nil {
		return e.dispatchOutbox(ctx, r, nil, false)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	attachmentsDone := make(chan error, 1)
	go func() {
		attachmentsDone <- e.dispatchOutbox(ctx, r, split, true)
		cancel()
	}()
	err = e.dispatchOutbox(ctx, r, split, false)
	cancel()
	attachmentsErr := <-attachmentsDone
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	if errors.Is(attachmentsErr, context.Canceled) {
		attachmentsErr = nil
	}
	return errors.Join(err, attachmentsErr)
}

func (e *Engine) dispatchOutbox(ctx context.Context, r MessagingRepository, split attachmentOutboxRepository, withAttachments bool) error {
	wake := e.work
	if withAttachments {
		wake = e.attachmentWork
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e.protocols != nil {
			if err := e.dispatchDueMessages(ctx, r, split, withAttachments, e.now().UTC()); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-ticker.C:
		}
	}
}

func (e *Engine) dispatchDueMessages(ctx context.Context, r MessagingRepository, split attachmentOutboxRepository, withAttachments bool, now time.Time) error {
	var after PendingMessageCursor
	for ctx.Err() == nil {
		var pending []domain.Message
		var next PendingMessageCursor
		var err error
		if split != nil {
			pending, next, err = split.PendingMessagesByAttachments(ctx, now, withAttachments, after)
		} else {
			pending, err = r.PendingMessages(ctx, now)
		}
		if err != nil {
			return err
		}
		for _, message := range pending {
			if ctx.Err() != nil {
				return nil
			}
			if err = e.dispatchMessage(ctx, r, split, message); err != nil {
				return err
			}
		}
		if !withAttachments {
			if err := e.dispatchMessageReactions(ctx); err != nil {
				return err
			}
			if err := e.dispatchMessageEdits(ctx); err != nil {
				return err
			}
		}
		if next.MessageID == "" {
			return nil
		}
		// Skipped claims remain pending. Keep scanning past them within this
		// pass so disconnected accounts or unjoined rooms cannot hold others.
		after = next
	}
	return nil
}

func (e *Engine) dispatchMessage(ctx context.Context, r MessagingRepository, split attachmentOutboxRepository, m domain.Message) error {
	a, err := e.Account(ctx, m.AccountID)
	if err != nil {
		return err
	}
	state, err := e.protocols.Connection(ctx, a)
	if err != nil || state.State != "connected" {
		return nil
	}
	c, err := e.Conversation(ctx, m.ConversationID)
	if err != nil {
		return err
	}
	var roomRevision domain.ID
	if c.Kind == "room" {
		session, err := e.RoomSession(ctx, c.ID)
		if err != nil {
			return err
		}
		if !session.Active || session.State != "joined" {
			return nil
		}
		roomRevision = session.IntentRevisionID
	}
	rev, err := e.newID()
	if err != nil {
		return err
	}
	now := e.now().UTC()
	var claimed domain.Message
	switch {
	case split != nil:
		guardWire := false
		if protocol, ok := e.protocols.(OriginalWireAttemptProtocol); ok {
			_, canRecord := e.repo.(SchedulingRepository)
			guardWire = canRecord && protocol.RequiresOriginalWireAttempt(a)
		}
		// An edit can move a message to the other lane before the claim.
		claimed, err = split.ClaimDispatchMessage(ctx, m.ID, m.RevisionID, roomRevision, rev, now, guardWire)
	case c.Kind == "room":
		claimer, ok := e.repo.(RoomMessageClaimer)
		if !ok {
			return ErrProtocolUnavailable
		}
		claimed, err = claimer.ClaimRoomMessage(ctx, m.ID, roomRevision, rev, now)
	default:
		claimed, err = r.TransitionMessage(ctx, m.ID, "queued", "dispatching", rev, now, now)
	}
	if errors.Is(err, ErrStateConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	// The protocol bounds each upload independently. A single fixed deadline
	// for the composition can starve the later files of a valid upload.
	sendCtx := ctx
	if len(claimed.Attachments) == 0 {
		var cancel context.CancelFunc
		sendCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	outgoing := outgoingMessage(claimed, c)
	outgoing.RoomIntentRevisionID = roomRevision
	outcome := Failed
	if err = e.attachOutgoingFiles(&outgoing, claimed); err == nil {
		outcome = e.protocols.Send(sendCtx, a, outgoing)
	}
	stateName := outcome
	switch outcome {
	case NotSent:
		stateName = "queued"
	case Submitted, Unknown, Failed:
	default:
		stateName = Unknown
	}
	rev, err = e.newID()
	if err != nil {
		return err
	}
	now = e.now().UTC()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	_, err = r.TransitionMessage(finishCtx, m.ID, "dispatching", stateName, rev, now, now.Add(time.Second))
	if err != nil && !errors.Is(err, ErrStateConflict) {
		return err
	}
	e.notifyEvents()
	return nil
}

// Reject accepts protocol error evidence only for this account and its intended
// peer (or its authenticated server), never by an unscoped stanza identifier.
func (e *Engine) Reject(ctx context.Context, in Rejected) error {
	id, err := domain.ParseID(in.MessageID)
	if err != nil {
		return nil
	}
	if reactions, ok := e.repo.(MessageReactionRepository); ok {
		pending, lookupErr := reactions.MessageReactionCommand(ctx, id)
		if lookupErr == nil {
			if pending.Command.AccountID != in.AccountID {
				return nil
			}
			c, err := e.Conversation(ctx, pending.Command.ConversationID)
			if err != nil {
				return err
			}
			if c.Kind == "room" && in.FromParticipant {
				return nil
			}
			if !in.FromServer && c.Address != in.Address {
				return nil
			}
			revision, err := e.newID()
			if err != nil {
				return err
			}
			return reactions.RejectMessageReaction(ctx, id, revision, e.now().UTC())
		}
		if !errors.Is(lookupErr, ErrNotFound) {
			return lookupErr
		}
	}
	if edits, ok := e.repo.(MessageEditRepository); ok {
		pending, lookupErr := edits.MessageEdit(ctx, id)
		if lookupErr == nil {
			if pending.Command.AccountID != in.AccountID {
				return nil
			}
			c, err := e.Conversation(ctx, pending.Command.ConversationID)
			if err != nil {
				return err
			}
			if c.Kind == "room" && in.FromParticipant {
				return nil
			}
			if !in.FromServer && c.Address != in.Address {
				return nil
			}
			revision, err := e.newID()
			if err != nil {
				return err
			}
			return edits.RejectMessageEdit(ctx, id, revision, e.now().UTC())
		}
		if !errors.Is(lookupErr, ErrNotFound) {
			return lookupErr
		}
	}
	m, err := e.Message(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.AccountID != in.AccountID || !m.IsOwn {
		return nil
	}
	c, err := e.Conversation(ctx, m.ConversationID)
	if err != nil {
		return err
	}
	if c.Kind == "room" && in.FromParticipant {
		return nil
	}
	if !in.FromServer && c.Address != in.Address {
		return nil
	}
	if m.SendState != "dispatching" && m.SendState != "submitted" && m.SendState != "unknown" {
		return nil
	}
	r, err := e.messaging()
	if err != nil {
		return err
	}
	rev, err := e.newID()
	if err != nil {
		return err
	}
	now := e.now().UTC()
	_, err = r.TransitionMessage(ctx, m.ID, m.SendState, "failed", rev, now, now)
	if errors.Is(err, ErrStateConflict) {
		return nil
	}
	return err
}
