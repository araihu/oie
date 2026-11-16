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
const freshEventWindow = 30 * time.Second

type deliveryKey struct {
	session, engine, message string
}

type deliveryClaim struct {
	expires time.Time
	remote  bool
}

type registry interface {
	PushRecipients(string) (hostauth.PushKeys, []hostauth.PushRecipient, error)
	ForgetPush(context.Context, hostauth.PushRecipient) error
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

// ClaimForeground arbitrates between a visible page and server push. Multiple
// cooperating pages may claim locally and use their existing IndexedDB dedupe.
func (s *Service) ClaimForeground(session, engine, message string) bool {
	return s.claim(deliveryKey{session, engine, message}, false)
}

func (s *Service) claim(key deliveryKey, remote bool) bool {
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
		return !previous.remote && !remote
	}
	if len(s.claims) >= 4096 {
		return remote
	}
	s.claims[key] = deliveryClaim{expires: now.Add(freshEventWindow), remote: remote}
	return true
}

type eventSource interface {
	EventWake() <-chan struct{}
	Events(context.Context, int64) ([]application.Event, error)
	Message(context.Context, domain.ID) (domain.Message, error)
	Conversation(context.Context, domain.ID) (domain.Conversation, error)
	ConversationReadState(context.Context, domain.ID) (domain.ReadState, error)
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
			if !isLiveMessage(event) {
				continue
			}
			select {
			case jobs <- delivery{event.Message.ID, time.Now()}:
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
	messageID domain.ID
	seenAt    time.Time
}

func (s *Service) deliver(ctx context.Context, source eventSource, engineID string, job delivery) {
	if remaining := time.Until(job.seenAt.Add(foregroundGrace)); remaining > 0 && !wait(ctx, remaining) {
		return
	}
	if time.Since(job.seenAt) > freshEventWindow {
		return
	}
	keys, recipients, err := s.auth.PushRecipients(engineID)
	if err != nil || len(recipients) == 0 {
		return
	}
	message, err := source.Message(ctx, job.messageID)
	if err != nil || message.IsWithdrawn() {
		return
	}
	preferences, err := source.AccountCommunicationPreferences(ctx, message.AccountID)
	if err != nil || preferences.Presence == "dnd" {
		return
	}
	conversation, err := source.Conversation(ctx, message.ConversationID)
	if err != nil || conversation.AccountID != message.AccountID || conversation.RemovedFromChats {
		return
	}
	read, err := source.ConversationReadState(ctx, conversation.ID)
	if err != nil || read.UnreadCount == 0 {
		return
	}

	replyToYou := false
	if message.Reply != nil && message.Reply.Available && conversation.Kind == "room" {
		target, err := source.Message(ctx, message.Reply.MessageID)
		replyToYou = err == nil && target.AccountID == message.AccountID &&
			target.ConversationID == message.ConversationID && target.IsOwn
	}
	for _, recipient := range recipients {
		if ctx.Err() != nil || time.Since(job.seenAt) > freshEventWindow {
			return
		}
		if recipient.StartedAt.After(job.seenAt) || slices.Contains(recipient.MutedConversations, string(conversation.ID)) {
			continue
		}
		silent := !soundsFor(recipient.PushSubscription, conversation, message.MentionsYou || replyToYou)
		data, err := payload(s.sender.origin, engineID, message, recipient, silent)
		if err != nil || !s.claim(deliveryKey{recipient.SessionID, engineID, string(message.ID)}, true) {
			continue
		}
		expired, err := s.sender.send(ctx, keys, recipient, data, message.ID)
		if err == nil && expired {
			_ = s.auth.ForgetPush(ctx, recipient)
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
