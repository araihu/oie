// Package browserpush delivers user-visible notifications for a hosted engine.
package browserpush

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/araihu/oie/internal/adapters/hostauth"
	"github.com/araihu/oie/internal/adapters/previewfetch"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

const foregroundGrace = 1500 * time.Millisecond
const foregroundLease = 2 * time.Second
const displayLease = 2500 * time.Millisecond
const freshEventWindow = 30 * time.Second

type deliveryKey struct {
	session, engine, message string
}

type claimState uint8

const (
	foregroundPending claimState = iota
	foregroundDisplaying
	foregroundHandled
	foregroundSuppress
	legacyForeground
	remoteClaimed
)

type deliveryClaim struct {
	expires, reservedUntil time.Time
	state                  claimState
}

type registry interface {
	PushRecipients(string) (hostauth.PushKeys, []hostauth.PushRecipient, error)
	CurrentPushRecipient(string, string) (*hostauth.PushRecipient, error)
	ExpirePush(context.Context, hostauth.PushRecipient) error
}

type Service struct {
	auth      registry
	sender    sender
	mu        sync.Mutex
	claims    map[deliveryKey]deliveryClaim
	nextPrune time.Time
}

func New(auth *hostauth.Auth, origin string) *Service {
	return &Service{auth: auth, sender: sender{origin: origin, client: previewfetch.PublicHTTPSClient()}, claims: make(map[deliveryKey]deliveryClaim)}
}

// ClaimForeground reserves a short window for the browser to finish handling
// a message. An abandoned reservation leaves the message eligible for push.
func (s *Service) ClaimForeground(session, engine, message, phase string) bool {
	var state claimState
	switch phase {
	case "reserve":
		state = foregroundPending
	case "display":
		state = foregroundDisplaying
	case "shown":
		state = foregroundHandled
	case "suppress":
		state = foregroundSuppress
	case "":
		state = legacyForeground
	default:
		return false
	}
	ok, _ := s.claim(deliveryKey{session, engine, message}, state)
	return ok
}

func (s *Service) claim(key deliveryKey, state claimState) (bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.After(s.nextPrune) {
		for key, claim := range s.claims {
			if !now.Before(claim.expires) {
				delete(s.claims, key)
			}
		}
		s.nextPrune = now.Add(freshEventWindow)
	}
	if previous, ok := s.claims[key]; ok && now.Before(previous.expires) {
		pending := previous.state == foregroundPending || previous.state == foregroundDisplaying
		active := pending && now.Before(previous.reservedUntil)
		switch {
		case state == remoteClaimed && pending:
			if active {
				return false, previous.reservedUntil
			}
			previous.state = remoteClaimed
		case state == foregroundDisplaying && active && previous.state == foregroundPending:
			previous.state, previous.reservedUntil = foregroundDisplaying, now.Add(displayLease)
		case state == foregroundHandled && active && previous.state == foregroundDisplaying,
			state == foregroundSuppress && active && previous.state == foregroundPending:
			previous.state = foregroundHandled
		case state == foregroundHandled && previous.state == foregroundHandled:
			return true, time.Time{}
		default:
			return false, time.Time{}
		}
		s.claims[key] = previous
		return true, time.Time{}
	}
	if state != foregroundPending && state != legacyForeground && state != remoteClaimed {
		return false, time.Time{}
	}
	if len(s.claims) >= 4096 {
		return state == remoteClaimed, time.Time{}
	}
	claim := deliveryClaim{expires: now.Add(freshEventWindow), state: state}
	if state == foregroundPending {
		claim.reservedUntil = now.Add(foregroundLease)
	}
	s.claims[key] = claim
	return true, time.Time{}
}

type eventSource interface {
	EventWake() <-chan struct{}
	Events(context.Context, int64) ([]application.Event, error)
	Message(context.Context, domain.ID) (domain.Message, error)
	Conversation(context.Context, domain.ID) (domain.Conversation, error)
	HasMessageRead(context.Context, domain.ID) (bool, error)
	AccountCommunicationPreferences(context.Context, domain.ID) (domain.AccountCommunicationPreferences, error)
}

// Run follows new committed events for this engine instance. Browser claims
// arrive during a short grace period. No notification can block XMPP receipt.
func (s *Service) Run(ctx context.Context, source eventSource, snapshot application.Snapshot) {
	jobs := make(chan delivery, 128)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for event := range jobs {
				if ctx.Err() == nil {
					s.deliver(ctx, source, string(snapshot.ID), event)
				}
			}
		})
	}
	defer func() {
		close(jobs)
		workers.Wait()
	}()

	cursor := snapshot.Sequence
	for ctx.Err() == nil {
		wake := source.EventWake()
		events, err := source.Events(ctx, cursor)
		if err != nil {
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		for _, event := range events {
			cursor = event.Sequence
			if !isLiveMessage(event) || time.Since(event.OccurredAt) > freshEventWindow {
				continue
			}
			select {
			case jobs <- delivery{event.Message.ID, event.OccurredAt, time.Now()}:
			default:
				// Prefer current notifications to processing an unbounded backlog.
			}
		}
		if len(events) == 0 {
			select {
			case <-wake:
			case <-ctx.Done():
			}
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func isLiveMessage(event application.Event) bool {
	message := event.Message
	return event.Type == "message.created" && message != nil && message.SendState == "received" &&
		!message.IsOwn && !message.Historical && !message.IsWithdrawn()
}

type delivery struct {
	messageID              domain.ID
	receivedAt, observedAt time.Time
}

func (s *Service) deliver(ctx context.Context, source eventSource, engineID string, job delivery) {
	if remaining := time.Until(job.observedAt.Add(foregroundGrace)); remaining > 0 && !wait(ctx, remaining) {
		return
	}
	keys, recipients, err := s.auth.PushRecipients(engineID)
	if err != nil || len(recipients) == 0 {
		return
	}
	for _, candidate := range recipients {
		if ctx.Err() != nil {
			return
		}
		key := deliveryKey{candidate.SessionID, engineID, string(job.messageID)}
		ok, pendingUntil := s.claim(key, remoteClaimed)
		for !pendingUntil.IsZero() {
			if !wait(ctx, time.Until(pendingUntil)) {
				return
			}
			ok, pendingUntil = s.claim(key, remoteClaimed)
		}
		if !ok {
			continue
		}
		message, err := source.Message(ctx, job.messageID)
		if err != nil || message.IsWithdrawn() || time.Since(job.receivedAt) > freshEventWindow {
			return
		}
		conversation, err := source.Conversation(ctx, message.ConversationID)
		if err != nil || conversation.AccountID != message.AccountID || conversation.RemovedFromChats {
			return
		}
		read, err := source.HasMessageRead(ctx, message.ID)
		if err != nil || read {
			return
		}
		preferences, err := source.AccountCommunicationPreferences(ctx, message.AccountID)
		if err != nil || preferences.Presence == "dnd" {
			return
		}
		replyToYou := false
		if message.Reply != nil && message.Reply.Available && conversation.Kind == "room" {
			target, err := source.Message(ctx, message.Reply.MessageID)
			replyToYou = err == nil && target.AccountID == message.AccountID &&
				target.ConversationID == message.ConversationID && target.IsOwn
		}
		recipient, err := s.auth.CurrentPushRecipient(engineID, candidate.SessionID)
		if err != nil || recipient == nil || recipient.StartedAt.After(job.receivedAt) ||
			slices.Contains(recipient.MutedConversations, string(conversation.ID)) {
			continue
		}
		silent := !soundsFor(recipient.PushSubscription, conversation, message.MentionsYou || replyToYou)
		data, err := payload(s.sender.origin, engineID, message, *recipient, silent)
		if err != nil || time.Since(job.receivedAt) > freshEventWindow {
			continue
		}
		expired, err := s.sender.send(ctx, keys, *recipient, data, message.ID)
		if err == nil && expired {
			_ = s.auth.ExpirePush(ctx, *recipient)
		}
	}
}

func soundsFor(subscription hostauth.PushSubscription, conversation domain.Conversation, attention bool) bool {
	if !subscription.Sound {
		return false
	}
	if conversation.Kind == "direct" {
		return subscription.DirectSound
	}
	return conversation.Kind == "room" && (!conversation.Archived || subscription.ArchivedSound) &&
		(subscription.RoomSound == "all" || subscription.RoomSound == "attention" && attention)
}
