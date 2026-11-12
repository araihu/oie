package application

import (
	"context"
	"errors"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type CorrectionProvider interface {
	PrepareCorrection(context.Context, domain.Account, domain.Conversation, domain.Message) (domain.OutgoingCorrection, error)
}

type MessageEditCandidate struct {
	Target domain.MessageEditTarget
	Source domain.Message
}
type MessageEditMutation struct {
	Pending     domain.PendingMessageEdit
	Fingerprint string
}
type MessageEditResult struct {
	Command  domain.MessageEditCommand
	Replayed bool
}
type EditConflict struct{ Target domain.MessageEditTarget }

func (e EditConflict) Error() string { return ErrStateConflict.Error() }
func (e EditConflict) Unwrap() error { return ErrStateConflict }

type MessageEditRepository interface {
	ReadMessageEditTarget(context.Context, domain.ID, bool) (MessageEditCandidate, error)
	ReplayMessageEdit(context.Context, domain.ID, string) (*domain.MessageEditCommand, error)
	AcceptMessageEdit(context.Context, MessageEditMutation) (MessageEditResult, error)
	MessageEdit(context.Context, domain.ID) (domain.PendingMessageEdit, error)
	PendingMessageEdits(context.Context, time.Time) ([]domain.PendingMessageEdit, error)
	InterruptedMessageEdits(context.Context) ([]domain.PendingMessageEdit, error)
	ClaimMessageEdit(context.Context, domain.ID, domain.ID, time.Time) (domain.PendingMessageEdit, error)
	FinishMessageEdit(context.Context, domain.ID, string, domain.ID, time.Time, time.Time, string) (domain.MessageEditCommand, error)
	ObserveMessageEdit(context.Context, domain.ID, string, domain.MessageCorrection, string, domain.ID, time.Time) error
	RejectMessageEdit(context.Context, domain.ID, domain.ID, time.Time) error
}

func (e *Engine) editRepository() (MessageEditRepository, error) {
	r, ok := e.repo.(MessageEditRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r, nil
}

func (e *Engine) prepareMessageEdit(ctx context.Context, id domain.ID, conversation bool) (MessageEditCandidate, domain.OutgoingCorrection, error) {
	r, err := e.editRepository()
	if err != nil {
		return MessageEditCandidate{}, domain.OutgoingCorrection{}, err
	}
	candidate, err := r.ReadMessageEditTarget(ctx, id, conversation)
	if err != nil {
		return candidate, domain.OutgoingCorrection{}, err
	}
	if !candidate.Target.Eligible {
		return candidate, domain.OutgoingCorrection{}, nil
	}
	unsupported := func(reason string) (MessageEditCandidate, domain.OutgoingCorrection, error) {
		candidate.Target.Eligible = false
		candidate.Target.Reason = reason
		return candidate, domain.OutgoingCorrection{}, nil
	}
	provider, ok := e.protocols.(CorrectionProvider)
	if !ok {
		return unsupported("protocol_unavailable")
	}
	c, err := e.Conversation(ctx, candidate.Target.ConversationID)
	if err != nil {
		return candidate, domain.OutgoingCorrection{}, err
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return candidate, domain.OutgoingCorrection{}, err
	}
	var roomIntent domain.ID
	if c.Kind == "room" {
		session, err := e.RoomSession(ctx, c.ID)
		if err != nil || !session.Active || session.State != "joined" {
			return unsupported("session_changed")
		}
		roomIntent = session.IntentRevisionID
	}
	plan, err := provider.PrepareCorrection(ctx, a, c, candidate.Source)
	if err != nil {
		return unsupported("protocol_unavailable")
	}
	if plan.TargetMessageID != candidate.Target.MessageID || plan.OriginalWireID != candidate.Source.ProviderID || plan.Scope == "" || plan.Author == "" || plan.SessionID == "" {
		return unsupported("protocol_unavailable")
	}
	plan.RoomIntentRevisionID = roomIntent
	return candidate, plan, nil
}

func (e *Engine) MessageEditTarget(ctx context.Context, id domain.ID) (domain.MessageEditTarget, error) {
	if !validID(id) {
		return domain.MessageEditTarget{}, ErrInvalid
	}
	candidate, _, err := e.prepareMessageEdit(ctx, id, false)
	return candidate.Target, err
}
func (e *Engine) ConversationEditTarget(ctx context.Context, id domain.ID) (domain.MessageEditTarget, error) {
	if !validID(id) {
		return domain.MessageEditTarget{}, ErrInvalid
	}
	candidate, _, err := e.prepareMessageEdit(ctx, id, true)
	return candidate.Target, err
}
func (e *Engine) MessageEdit(ctx context.Context, id domain.ID) (domain.MessageEditCommand, error) {
	if !validID(id) {
		return domain.MessageEditCommand{}, ErrInvalid
	}
	r, err := e.editRepository()
	if err != nil {
		return domain.MessageEditCommand{}, err
	}
	pending, err := r.MessageEdit(ctx, id)
	return pending.Command, err
}
func (e *Engine) EditMessage(ctx context.Context, key, id, expected domain.ID, text string) (MessageEditResult, error) {
	if !validID(key) || !validID(id) || !validID(expected) || !validMessage(text) {
		return MessageEditResult{}, ErrInvalid
	}
	r, err := e.editRepository()
	if err != nil {
		return MessageEditResult{}, err
	}
	digest := fingerprint("edit_message", []string{string(id), string(expected), text})
	replay, err := r.ReplayMessageEdit(ctx, key, digest)
	if err != nil {
		return MessageEditResult{}, err
	}
	if replay != nil {
		return MessageEditResult{Command: *replay, Replayed: true}, nil
	}
	candidate, plan, err := e.prepareMessageEdit(ctx, id, false)
	if err != nil {
		return MessageEditResult{}, err
	}
	if !candidate.Target.Eligible || candidate.Target.CurrentRevisionID != expected {
		return MessageEditResult{}, EditConflict{Target: candidate.Target}
	}
	revision, err := e.newID()
	if err != nil {
		return MessageEditResult{}, err
	}
	content, err := e.newID()
	if err != nil {
		return MessageEditResult{}, err
	}
	now := e.now().UTC()
	command := domain.MessageEditCommand{ID: key, RevisionID: revision, MessageID: candidate.Target.MessageID, AccountID: candidate.Target.AccountID, ConversationID: candidate.Target.ConversationID, ExpectedRevisionID: expected, OriginalText: text, State: "queued", CreatedAt: now, UpdatedAt: now}
	result, err := r.AcceptMessageEdit(ctx, MessageEditMutation{Pending: domain.PendingMessageEdit{Command: command, Plan: plan, ContentRevisionID: content}, Fingerprint: digest})
	if err == nil {
		e.outgoingWorkCommitted()
	}
	return result, err
}

// Corrections have a separate durable state machine. Interrupted or ambiguous
// writes never return to the automatic retry queue.
func (e *Engine) recoverMessageEdits(ctx context.Context) error {
	r, err := e.editRepository()
	if err != nil {
		return nil
	}
	pending, err := r.InterruptedMessageEdits(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		revision, err := e.newID()
		if err != nil {
			return err
		}
		now := e.now().UTC()
		if _, err = r.FinishMessageEdit(ctx, p.Command.ID, Unknown, revision, now, now, "interrupted"); err != nil && !errors.Is(err, ErrStateConflict) {
			return err
		}
	}
	return nil
}
func (e *Engine) dispatchMessageEdits(ctx context.Context) error {
	r, err := e.editRepository()
	if err != nil {
		return nil
	}
	pending, err := r.PendingMessageEdits(ctx, e.now().UTC())
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
		provider, ok := e.protocols.(CorrectionProvider)
		outcome, reason := "", ""
		if !ok {
			outcome, reason = Failed, "protocol_unavailable"
		} else {
			candidate, readErr := r.ReadMessageEditTarget(ctx, p.Command.MessageID, false)
			if readErr != nil {
				return readErr
			}
			plan, prepareErr := provider.PrepareCorrection(ctx, a, c, candidate.Source)
			plan.RoomIntentRevisionID = p.Plan.RoomIntentRevisionID
			if prepareErr != nil || plan != p.Plan {
				outcome, reason = Failed, "session_changed"
			}
		}
		revision, err := e.newID()
		if err != nil {
			return err
		}
		claimed, err := r.ClaimMessageEdit(ctx, p.Command.ID, revision, e.now().UTC())
		if errors.Is(err, ErrStateConflict) {
			continue
		}
		if err != nil {
			return err
		}
		if outcome == "" {
			sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			outcome = e.protocols.Send(sendCtx, a, Outgoing{ID: claimed.Command.ID, Address: c.Address, OriginalText: claimed.Command.OriginalText, Kind: c.Kind, RoomIntentRevisionID: claimed.Plan.RoomIntentRevisionID, Correction: &claimed.Plan})
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
		_, err = r.FinishMessageEdit(finishCtx, p.Command.ID, state, revision, now, now.Add(time.Second), reason)
		cancel()
		if err != nil && !errors.Is(err, ErrStateConflict) {
			return err
		}
		e.notifyEvents()
	}
	return nil
}
