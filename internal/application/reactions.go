package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/araihu/oie/internal/domain"
)

type IncomingReaction struct {
	AccountID                                          domain.ID
	Address, Kind, ProviderID, ArchiveScope, ArchiveID string
	CommandEchoID                                      string
	CreatedAt                                          time.Time
	Historical, IsOwn                                  bool
	ArchivePrepend                                     bool     `json:"-"`
	ArchiveEarlier, ArchiveLater                       []string `json:"-"`
	Target                                             domain.MessageReference
	Actor, ActorName                                   string
	Emojis                                             []string
}
type ReactionProvider interface {
	PrepareReaction(context.Context, domain.Account, domain.Conversation, domain.Message) (domain.OutgoingReaction, error)
}

// RoomReactionPrivacy distinguishes a verified identity mode from permission
// to show real addresses, which requires current room presence.
type RoomReactionPrivacy struct {
	Mode   string
	Public bool
}

const (
	RoomReactionsNonanonymous  = "nonanonymous"
	RoomReactionsSemianonymous = "semianonymous"
	RoomReactionsAnonymous     = "anonymous"
)

type RoomReactionPrivacyHost interface {
	SetRoomReactionPrivacy(context.Context, domain.ID, string, RoomReactionPrivacy) error
}

func (e *Engine) SetRoomReactionPrivacy(ctx context.Context, account domain.ID, address string, privacy RoomReactionPrivacy) error {
	repo, ok := e.repo.(RoomReactionPrivacyHost)
	if !ok {
		return ErrProtocolUnavailable
	}
	return repo.SetRoomReactionPrivacy(ctx, account, address, privacy)
}

type MessageReactionCandidate struct {
	Snapshot domain.MessageReactions
	Source   domain.Message
}
type MessageReactionMutation struct {
	Pending     domain.PendingMessageReaction
	Fingerprint string
}
type MessageReactionResult struct {
	Command  domain.MessageReactionCommand
	Replayed bool
}
type MessageReactionObservation struct {
	Incoming   IncomingReaction
	RevisionID domain.ID
	ObservedAt time.Time
}
type MessageReactionRepository interface {
	ReadMessageReactions(context.Context, domain.ID) (MessageReactionCandidate, error)
	ReplayMessageReaction(context.Context, domain.ID, string) (*domain.MessageReactionCommand, error)
	AcceptMessageReaction(context.Context, MessageReactionMutation) (MessageReactionResult, error)
	MessageReactionCommand(context.Context, domain.ID) (domain.PendingMessageReaction, error)
	PendingMessageReactions(context.Context, time.Time) ([]domain.PendingMessageReaction, error)
	InterruptedMessageReactions(context.Context) ([]domain.PendingMessageReaction, error)
	ClaimMessageReaction(context.Context, domain.ID, domain.ID, time.Time) (domain.PendingMessageReaction, error)
	FinishMessageReaction(context.Context, domain.ID, string, domain.ID, time.Time, time.Time, string) (domain.MessageReactionCommand, error)
	ObserveMessageReaction(context.Context, MessageReactionObservation) error
	RejectMessageReaction(context.Context, domain.ID, domain.ID, time.Time) error
}

type ReactionConflict struct{ Current domain.MessageReactions }

func (e ReactionConflict) Error() string { return ErrStateConflict.Error() }
func (e ReactionConflict) Unwrap() error { return ErrStateConflict }
func (e *Engine) reactionRepository() (MessageReactionRepository, error) {
	r, ok := e.repo.(MessageReactionRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r, nil
}
func (e *Engine) prepareReaction(ctx context.Context, id domain.ID) (MessageReactionCandidate, domain.OutgoingReaction, error) {
	r, err := e.reactionRepository()
	if err != nil {
		return MessageReactionCandidate{}, domain.OutgoingReaction{}, err
	}
	candidate, err := r.ReadMessageReactions(ctx, id)
	if err != nil {
		return candidate, domain.OutgoingReaction{}, err
	}
	deny := func(reason string) (MessageReactionCandidate, domain.OutgoingReaction, error) {
		candidate.Snapshot.Eligible = false
		candidate.Snapshot.Reason = reason
		return candidate, domain.OutgoingReaction{}, nil
	}
	if candidate.Snapshot.PendingCommand != nil {
		return deny("reaction_pending")
	}
	provider, ok := e.protocols.(ReactionProvider)
	if !ok {
		return deny("protocol_unavailable")
	}
	c, err := e.Conversation(ctx, candidate.Source.ConversationID)
	if err != nil {
		return candidate, domain.OutgoingReaction{}, err
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return candidate, domain.OutgoingReaction{}, err
	}
	var intent domain.ID
	if c.Kind == "room" {
		session, err := e.RoomSession(ctx, c.ID)
		if err != nil || !session.Active || session.State != "joined" {
			return deny("session_changed")
		}
		intent = session.IntentRevisionID
	}
	plan, err := provider.PrepareReaction(ctx, a, c, candidate.Source)
	if err != nil || plan.TargetMessageID != candidate.Source.ID || plan.Reference.ID == "" || plan.Actor == "" || plan.Actor != a.Address || plan.SessionID == "" {
		return deny("target_unavailable")
	}
	if plan.RoomActor != "" && (c.Kind != "room" || !domain.IsRoomReactionActor(plan.RoomActor) || !validRoomReactionName(plan.ActorName)) {
		return deny("target_unavailable")
	}
	if plan.MaxEmojis <= 0 || plan.AllowedEmojis != nil && len(plan.AllowedEmojis) == 0 {
		return deny("target_unavailable")
	}
	plan.RoomIntentRevisionID = intent
	candidate.Snapshot.MaxEmojis = plan.MaxEmojis
	if candidate.Snapshot.MaxEmojis <= 0 || candidate.Snapshot.MaxEmojis > 8 {
		candidate.Snapshot.MaxEmojis = 8
	}
	candidate.Snapshot.AllowedEmojis = plan.AllowedEmojis
	candidate.Snapshot.Eligible = true
	candidate.Snapshot.Reason = ""
	return candidate, plan, nil
}
func (e *Engine) MessageReactions(ctx context.Context, id domain.ID) (domain.MessageReactions, error) {
	if !validID(id) {
		return domain.MessageReactions{}, ErrInvalid
	}
	c, _, err := e.prepareReaction(ctx, id)
	return c.Snapshot, err
}
func (e *Engine) ConversationReactions(ctx context.Context, id domain.ID, ids []domain.ID) ([]domain.MessageReactions, error) {
	if !validID(id) || len(ids) == 0 || len(ids) > 100 {
		return nil, ErrInvalid
	}
	if _, err := e.Conversation(ctx, id); err != nil {
		return nil, err
	}
	out := []domain.MessageReactions{}
	seen := map[domain.ID]bool{}
	for _, source := range ids {
		state, err := e.MessageReactions(ctx, source)
		if err != nil {
			return nil, err
		}
		if state.ConversationID != id {
			return nil, ErrNotFound
		}
		if !seen[state.MessageID] {
			seen[state.MessageID] = true
			out = append(out, state)
		}
	}
	return out, nil
}
func (e *Engine) MessageReactionCommand(ctx context.Context, id domain.ID) (domain.MessageReactionCommand, error) {
	if !validID(id) {
		return domain.MessageReactionCommand{}, ErrInvalid
	}
	r, err := e.reactionRepository()
	if err != nil {
		return domain.MessageReactionCommand{}, err
	}
	p, err := r.MessageReactionCommand(ctx, id)
	return p.Command, err
}
func (e *Engine) SetMessageReactions(ctx context.Context, key, id, expected domain.ID, values []string) (MessageReactionResult, error) {
	if !validID(key) || !validID(id) || expected != "" && !validID(expected) {
		return MessageReactionResult{}, ErrInvalid
	}
	emojis, err := NormalizeReactionEmojis(values)
	if err != nil {
		return MessageReactionResult{}, err
	}
	r, err := e.reactionRepository()
	if err != nil {
		return MessageReactionResult{}, err
	}
	digest := fingerprint("set_message_reactions", []any{id, expected, emojis})
	replay, err := r.ReplayMessageReaction(ctx, key, digest)
	if err != nil {
		return MessageReactionResult{}, err
	}
	if replay != nil {
		return MessageReactionResult{Command: *replay, Replayed: true}, nil
	}
	candidate, plan, err := e.prepareReaction(ctx, id)
	if err != nil {
		return MessageReactionResult{}, err
	}
	if !candidate.Snapshot.Eligible || candidate.Snapshot.OwnRevisionID != expected || !reactionPlanAllows(plan, emojis) {
		return MessageReactionResult{}, ReactionConflict{Current: candidate.Snapshot}
	}
	revision, err := e.newID()
	if err != nil {
		return MessageReactionResult{}, err
	}
	now := e.now().UTC()
	command := domain.MessageReactionCommand{ID: key, RevisionID: revision, MessageID: candidate.Source.ID, AccountID: candidate.Source.AccountID, ConversationID: candidate.Source.ConversationID, ExpectedOwnRevisionID: expected, Emojis: emojis, State: "queued", CreatedAt: now, UpdatedAt: now}
	result, err := r.AcceptMessageReaction(ctx, MessageReactionMutation{Pending: domain.PendingMessageReaction{Command: command, Plan: plan}, Fingerprint: digest})
	if err == nil {
		e.wake()
	}
	return result, err
}
func validRoomReactionName(name string) bool {
	return len(name) <= 1023 && strings.TrimSpace(name) != "" && validText(name, 1023) &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

func (e *Engine) ReceiveReaction(ctx context.Context, in IncomingReaction) error {
	if !validID(in.AccountID) || !validText(in.Actor, 1023) || !validText(in.Target.ID, 4096) || in.Kind != "direct" && in.Kind != "room" || in.ProviderID == "" && in.ArchiveID == "" || len(in.ProviderID) > 4096 || len(in.ArchiveID) > 4096 || len(in.ArchiveScope) > 4096 || len(in.Target.Scope) > 4096 || len(in.Target.Author) > 4096 {
		return ErrInvalid
	}
	if in.CommandEchoID != "" && (!in.IsOwn || in.Kind != "room" || !validText(in.CommandEchoID, 4096)) {
		return ErrInvalid
	}
	emojis, err := NormalizeReactionEmojis(in.Emojis)
	if err != nil {
		return err
	}
	in.Emojis = emojis
	a, err := e.Account(ctx, in.AccountID)
	if err != nil {
		return err
	}
	in.Address, err = e.canonical(a.Protocol, in.Address)
	if err != nil {
		return err
	}
	if domain.IsRoomReactionActor(in.Actor) {
		if in.Kind != "room" || in.Target.Scope != "room:"+in.Address || !validRoomReactionName(in.ActorName) {
			return ErrInvalid
		}
	} else {
		actor, err := e.canonical(a.Protocol, in.Actor)
		if err != nil || actor != in.Actor || in.IsOwn != (actor == a.Address) {
			return ErrInvalid
		}
	}
	now := e.now().UTC()
	if in.CreatedAt.IsZero() {
		if in.Historical {
			return ErrInvalid
		}
		in.CreatedAt = now
	}
	in.CreatedAt = in.CreatedAt.UTC()
	if in.CreatedAt.After(now.Add(5 * time.Minute)) {
		return ErrInvalid
	}
	revision, err := e.newID()
	if err != nil {
		return err
	}
	r, err := e.reactionRepository()
	if err != nil {
		return err
	}
	return r.ObserveMessageReaction(ctx, MessageReactionObservation{Incoming: in, RevisionID: revision, ObservedAt: now})
}
func (e *Engine) recoverMessageReactions(ctx context.Context) error {
	r, err := e.reactionRepository()
	if err != nil {
		return nil
	}
	pending, err := r.InterruptedMessageReactions(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		revision, err := e.newID()
		if err != nil {
			return err
		}
		now := e.now().UTC()
		if _, err = r.FinishMessageReaction(ctx, p.Command.ID, Unknown, revision, now, now, "interrupted"); err != nil && !errors.Is(err, ErrStateConflict) {
			return err
		}
	}
	return nil
}
func (e *Engine) dispatchMessageReactions(ctx context.Context) error {
	r, err := e.reactionRepository()
	if err != nil {
		return nil
	}
	pending, err := r.PendingMessageReactions(ctx, e.now().UTC())
	if err != nil {
		return err
	}
	for _, p := range pending {
		a, err := e.Account(ctx, p.Command.AccountID)
		if err != nil {
			return err
		}
		connection, err := e.protocols.Connection(ctx, a)
		if err != nil || connection.State != "connected" {
			continue
		}
		c, err := e.Conversation(ctx, p.Command.ConversationID)
		if err != nil {
			return err
		}
		candidate, err := r.ReadMessageReactions(ctx, p.Command.MessageID)
		if err != nil {
			return err
		}
		provider, ok := e.protocols.(ReactionProvider)
		outcome, reason := "", ""
		var plan domain.OutgoingReaction
		if ok {
			plan, err = provider.PrepareReaction(ctx, a, c, candidate.Source)
			plan.RoomIntentRevisionID = p.Plan.RoomIntentRevisionID
		}
		if !ok || err != nil || !reflect.DeepEqual(plan, p.Plan) || !reactionPlanAllows(plan, p.Command.Emojis) {
			outcome, reason = Failed, "session_changed"
		}
		revision, err := e.newID()
		if err != nil {
			return err
		}
		claimed, err := r.ClaimMessageReaction(ctx, p.Command.ID, revision, e.now().UTC())
		if errors.Is(err, ErrStateConflict) {
			continue
		}
		if err != nil {
			return err
		}
		if outcome == "" {
			sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			outcome = e.protocols.Send(sendCtx, a, Outgoing{ID: claimed.Command.ID, Address: c.Address, Kind: c.Kind, RoomIntentRevisionID: claimed.Plan.RoomIntentRevisionID, Reaction: &claimed.Plan, ReactionEmojis: claimed.Command.Emojis})
			cancel()
		}
		state := outcome
		if state == NotSent {
			state = "queued"
		}
		if state != "queued" && state != Submitted && state != Unknown && state != Failed {
			state = Unknown
		}
		if state == Failed && reason == "" {
			reason = "protocol_rejected"
		}
		if state == Unknown {
			reason = "unknown_outcome"
		}
		revision, err = e.newID()
		if err != nil {
			return err
		}
		now := e.now().UTC()
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, err = r.FinishMessageReaction(finishCtx, p.Command.ID, state, revision, now, now.Add(time.Second), reason)
		cancel()
		if err != nil && !errors.Is(err, ErrStateConflict) {
			return err
		}
	}
	return nil
}
