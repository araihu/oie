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
	if err := validateMentionSelections(text, mentions); err != nil {
		return MessageResult{}, err
	}
	if !validID(key) || !validID(conversationID) || !validMessage(text) || (replyTo != "" && !validID(replyTo)) {
		return MessageResult{}, ErrInvalid
	}
	parts := []string{string(conversationID), text}
	if replyTo != "" {
		parts = append(parts, string(replyTo))
	}
	requestFingerprint := fingerprint("send_message", mentionFingerprint(parts, mentions))
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
	frozenMentions, err := e.freezeMentions(ctx, a, c, text, mentions)
	if err != nil {
		return MessageResult{}, err
	}
	var plan *domain.OutboundReply
	if replyTo != "" {
		target, err := repo.Message(ctx, replyTo)
		if err != nil {
			return MessageResult{}, err
		}
		if target.AccountID != c.AccountID || target.ConversationID != c.ID {
			return MessageResult{}, ErrInvalid
		}
		plan = outboundReply(a, c, target, text, e.protocols)
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
	m := domain.Message{ID: id, RevisionID: rev, AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: a.ID, DisplayName: a.Address}, OriginalText: text, CreatedAt: now, IsOwn: true, SendState: "queued", ProviderID: string(id), OutboundReply: plan, Mentions: frozenMentions}
	if plan != nil {
		m.OriginalText = quotedReplyBody(plan)
		m.Reply = &domain.MessageReply{MessageID: plan.TargetID, Available: true, SenderName: plan.SenderName, Excerpt: plan.Excerpt, BodyText: plan.BodyText}
		if plan.Reference != nil {
			m.ReplyReference = &domain.ReplyReference{Target: *plan.Reference, Fallback: append([]domain.TextRange(nil), plan.Fallback...)}
		}
	}
	outgoing := outgoingMessage(m, c)
	if refs, ok := e.protocols.(OutgoingReferenceProvider); ok {
		m.References = refs.OutgoingReferences(a, outgoing)
	}
	result, err := repo.AcceptMessage(ctx, MessageMutation{Operation: Operation{ID: key, ResourceID: id, CreatedAt: now, Kind: "send_message"}, Fingerprint: requestFingerprint, Message: m, ConversationRevisionID: cr})
	if err == nil {
		e.wake()
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
		if lookupErr == nil && existing.IsOwn && existing.AccountID == a.ID && existing.OriginalText == in.OriginalText {
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
	err = r.RecordConnection(ctx, c)
	if c.State == "connected" {
		e.wake()
	}
	return err
}
func (e *Engine) wake() {
	select {
	case e.work <- struct{}{}:
	default:
	}
}

// Run owns one serial dispatcher. Network I/O happens only after an atomic SQL
// claim; no transaction spans a network write. Interrupted claims become unknown.
func (e *Engine) runOutbox(ctx context.Context) error {
	r, err := e.messaging()
	if err != nil {
		return err
	}
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
		if _, err = r.TransitionMessage(ctx, m.ID, "dispatching", "unknown", rev, now, now); err != nil {
			return err
		}
	}
	if err := e.recoverMessageReactions(ctx); err != nil {
		return err
	}
	if err := e.recoverMessageEdits(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e.protocols != nil {
			pending, err := r.PendingMessages(ctx, e.now().UTC())
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			for _, m := range pending {
				a, err := e.Account(ctx, m.AccountID)
				if err != nil {
					return err
				}
				state, err := e.protocols.Connection(ctx, a)
				if err != nil || state.State != "connected" {
					continue
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
						continue
					}
					roomRevision = session.IntentRevisionID
				}
				rev, err := e.newID()
				if err != nil {
					return err
				}
				now := e.now().UTC()
				var claimed domain.Message
				if c.Kind == "room" {
					claimer, ok := e.repo.(RoomMessageClaimer)
					if !ok {
						return ErrProtocolUnavailable
					}
					claimed, err = claimer.ClaimRoomMessage(ctx, m.ID, roomRevision, rev, now)
				} else {
					claimed, err = r.TransitionMessage(ctx, m.ID, "queued", "dispatching", rev, now, now)
				}
				if errors.Is(err, ErrStateConflict) {
					continue
				}
				if err != nil {
					return err
				}
				sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				outgoing := outgoingMessage(claimed, c)
				outgoing.RoomIntentRevisionID = roomRevision
				outcome := e.protocols.Send(sendCtx, a, outgoing)
				cancel()
				stateName := outcome
				if outcome == NotSent {
					stateName = "queued"
				}
				if stateName != "queued" && stateName != Submitted && stateName != Unknown && stateName != Failed {
					stateName = Unknown
				}
				rev, err = e.newID()
				if err != nil {
					return err
				}
				now = e.now().UTC()
				finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_, err = r.TransitionMessage(finishCtx, m.ID, "dispatching", stateName, rev, now, now.Add(time.Second))
				finishCancel()
				if err != nil && !errors.Is(err, ErrStateConflict) {
					return err
				}
			}
		}
		if e.protocols != nil {
			if err := e.dispatchMessageReactions(ctx); err != nil {
				return err
			}
			if err := e.dispatchMessageEdits(ctx); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-e.work:
		case <-ticker.C:
		}
	}
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
