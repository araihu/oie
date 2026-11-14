package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestRetentionPurgesOnlyOutsideRoomMinimumAndStars(t *testing.T) {
	s, e, direct := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	room := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: direct.AccountID, PeerID: migrationID(), Address: "room@conference.localhost", Title: "Room", Kind: "room", UpdatedAt: now}
	if err := s.RestoreConversation(ctx, room); err != nil {
		t.Fatal(err)
	}
	star := starMessage(t, s, room, now.AddDate(0, 0, -300))
	if _, err := e.SetMessageStar(ctx, migrationID(), star.ID, "", true); err != nil {
		t.Fatal(err)
	}
	expired := replySeed(t, s, room, "expired text and https://example.test/old", now.AddDate(0, 0, -301), nil, nil)
	for n := range 80 {
		starMessage(t, s, room, now.AddDate(0, 0, -299+n))
	}
	for n := range 20 {
		starMessage(t, s, room, now.AddDate(0, 0, -25+n))
	}
	keptDirect := starMessage(t, s, direct, now.AddDate(0, 0, -200))
	purge := func() int {
		t.Helper()
		n, err := s.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := purge(); n != 1 {
		t.Fatalf("expired %d messages", n)
	}
	messages, err := s.Messages(ctx, room.ID, "", 110)
	if err != nil || len(messages) != 101 {
		t.Fatalf("retained %d messages: %v", len(messages), err)
	}
	for _, id := range []domain.ID{star.ID, keptDirect.ID} {
		if _, err := e.Message(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Message(ctx, expired.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var invalidated bool
	for _, event := range events {
		if strings.Contains(jsonText(event), "expired text") {
			t.Fatal("expired content survived in event replay")
		}
		if event.HistoryPruned != nil && event.HistoryPruned.ConversationID == room.ID {
			invalidated = len(event.HistoryPruned.MessageIDs) == 1 && event.HistoryPruned.MessageIDs[0] == expired.ID
		}
	}
	if !invalidated || purge() != 0 {
		t.Fatal("missing invalidation or non-idempotent expiry")
	}
}

func TestRetentionHidesQuoteAndKeepsAcceptedDirect(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	now := time.Now().UTC()
	days := 7
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "old"}
	original := replySeed(t, s, c, "original", now.AddDate(0, 0, -9), []domain.MessageReference{ref}, nil)
	reply := replySeed(t, s, c, "> original\nMy answer", now, nil, &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 11}}})
	if err := e.Receive(ctx, application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: original.ProviderID, OriginalText: original.OriginalText, CreatedAt: original.CreatedAt, Historical: true}); err != nil {
		t.Fatal(err)
	}
	purge := func(at time.Time) {
		t.Helper()
		if _, err := s.PurgeExpiredHistory(ctx, at, func() (domain.ID, error) { return migrationID(), nil }); err != nil {
			t.Fatal(err)
		}
	}
	purge(now)
	m, err := e.Message(ctx, reply.ID)
	if err != nil || m.Reply == nil || m.Reply.Available || !m.Reply.OriginalExpired || m.Reply.Excerpt != "" || m.Reply.BodyText != "My answer" {
		t.Fatalf("reply after expiry: %+v, %v", m.Reply, err)
	}
	search, err := e.SearchMessages(ctx, domain.MessageSearchQuery{Search: "original", Limit: 10})
	if err != nil || len(search.Items) != 0 {
		t.Fatalf("search exposed the expired quote: %+v %v", search, err)
	}
	purge(now.AddDate(0, 0, 10))
	if err := e.Receive(ctx, application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "new", OriginalText: "back", CheckDirectHistory: true}); err != nil {
		t.Fatal(err)
	}
	current, err := e.Conversation(ctx, c.ID)
	if err != nil || current.Request != "" || current.LastMessage != "back" {
		t.Fatalf("accepted conversation was lost: %+v %v", current, err)
	}
}

func TestRetentionRejectsOldIngressAndRedactsSendReceipt(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	now := time.Now().UTC()
	days := 7
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", nil); !errors.Is(err, application.ErrStateConflict) {
		t.Fatalf("stale configuration: %v", err)
	}
	for _, days := range []int{-1, domain.MaxRetentionDays + 1} {
		if _, err := e.SetConversationRetention(ctx, c.ID, "1", &days); !errors.Is(err, application.ErrInvalid) {
			t.Fatalf("invalid period accepted: %d %v", days, err)
		}
	}
	if err := e.Receive(ctx, application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "expired", OriginalText: "expired archive result", CreatedAt: now.AddDate(0, 0, -8), Historical: true}); err != nil {
		t.Fatal(err)
	}
	if messages, err := e.Messages(ctx, c.ID, "", 10); err != nil || len(messages) != 0 {
		t.Fatalf("stored expired archive result: %v %v", messages, err)
	}
	key := migrationID()
	const message = "should disappear from receipts"
	sent, err := e.SendMessage(ctx, key, c.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionMessage(ctx, sent.Message.ID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionMessage(ctx, sent.Message.ID, "dispatching", "submitted", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeExpiredHistory(ctx, now.AddDate(0, 0, 9), func() (domain.ID, error) { return migrationID(), nil }); err != nil || n != 1 {
		t.Fatalf("purged %d: %v", n, err)
	}
	if _, err := e.AcceptedSendMessage(ctx, key); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("expired receipt contents still exposed: %v", err)
	}
	if _, err := e.SendMessage(ctx, key, c.ID, message); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("retry of expired message: %v", err)
	}
	if receipt, err := e.Operation(ctx, key); err != nil || receipt.ResourceID != sent.Message.ID {
		t.Fatalf("lost idempotency receipt: %+v %v", receipt, err)
	}
}

func TestRetentionAgesScheduledMessageFromSubmission(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	now := time.Now().UTC()
	days := 3
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	schedule := now.Add(15 * 24 * time.Hour)
	sent, err := e.SendComposition(ctx, migrationID(), c.ID, domain.OutgoingComposition{OriginalText: "scheduled message"}, &schedule)
	if err != nil {
		t.Fatal(err)
	}
	check := func(at time.Time, count int) {
		t.Helper()
		got, err := s.PurgeExpiredHistory(ctx, at, func() (domain.ID, error) { return migrationID(), nil })
		if err != nil || got != count {
			t.Fatalf("expire at %s: %d, %v (expected %d)", at, got, err, count)
		}
	}
	check(schedule.Add(-time.Hour), 0)
	if _, err := s.TransitionMessage(ctx, sent.Message.ID, "queued", "dispatching", migrationID(), schedule, schedule); err != nil {
		t.Fatal(err)
	}
	check(schedule.Add(time.Hour), 0)
	submitted := schedule.Add(2 * time.Hour)
	if _, err := s.TransitionMessage(ctx, sent.Message.ID, "dispatching", "submitted", migrationID(), submitted, submitted); err != nil {
		t.Fatal(err)
	}
	check(submitted.AddDate(0, 0, 3), 0)
	check(submitted.AddDate(0, 0, 4), 1)
}
