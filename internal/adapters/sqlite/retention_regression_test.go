package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestRetentionExpiredUnstarReceipt(t *testing.T) {
	s, e, c := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	days := 7
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	m := starMessage(t, s, c, now.AddDate(0, 0, -9))
	star, err := e.SetMessageStar(ctx, migrationID(), m.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	if _, err = e.SetMessageStar(ctx, key, m.ID, star.Star.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	count, err := s.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
	if err != nil || count != 1 {
		t.Fatalf("purge count %d: %v", count, err)
	}
	if _, err := e.Message(ctx, m.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("not deleted: %v", err)
	}
	if _, err := e.SetMessageStar(ctx, key, m.ID, star.Star.RevisionID, false); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("expired unstar receipt returned %v", err)
	}
}

func TestRetentionScrubsInboundQuoteSnapshots(t *testing.T) {
	s, e, c := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	days := 7
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "retention-review-old"}
	const secret = "quoted secret marker"
	_ = replySeed(t, s, c, secret, now.AddDate(0, 0, -9), []domain.MessageReference{ref}, nil)
	text := "> " + secret + "\n"
	reply := replySeed(t, s, c, text+"Answer independent of it", now, nil, &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: len(text)}}})
	count, err := s.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
	if err != nil || count != 1 {
		t.Fatalf("purge count %d: %v", count, err)
	}
	for _, table := range []string{"messages", "message_revisions", "changes"} {
		var count int
		err := s.readDB.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE instr(payload,?)>0", table), secret).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Errorf("%s still stores %d payloads with the automatic quote", table, count)
		}
	}
	events, err := s.Events(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Message != nil && event.Message.ID == reply.ID && strings.Contains(jsonText(event), secret) {
			t.Errorf("durable engine event replay survives raw with quote though projection hides it: %s", jsonText(event))
		}
	}
}

func TestRetentionRejectsStaleInheritance(t *testing.T) {
	s, e, c := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	_ = starMessage(t, s, c, now.AddDate(0, 0, -20))
	unlimited := 0
	local, err := e.SetConversationRetention(ctx, c.ID, "0", &unlimited)
	if err != nil {
		t.Fatal(err)
	}
	if local.EffectiveDays != 0 || local.GlobalDays != 0 {
		t.Fatalf("%+v", local)
	}
	global, err := e.GlobalRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	global.DirectDays = 7
	if _, err := e.SetGlobalRetention(ctx, global); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetConversationRetention(ctx, c.ID, local.Revision, nil); !errors.Is(err, application.ErrStateConflict) {
		t.Fatalf("stale observed inheritance did not conflict: %v", err)
	}
}

func TestRetentionScheduledReplySendsOnlyAuthoredText(t *testing.T) {
	s, e, c := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	p := &outboundProtocol{connected: true}
	e.SetProtocols(p)
	days := 7
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	const secret = "secret expired quoted source"
	m := replySeed(t, s, c, secret, now.AddDate(0, 0, -9), nil, nil)
	at := time.Now().UTC().Add(750 * time.Millisecond)
	queued, err := e.SendComposition(ctx, migrationID(), c.ID, domain.OutgoingComposition{OriginalText: "Reply body", ReplyTo: m.ID}, &at)
	if err != nil {
		t.Fatal(err)
	}
	count, err := s.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
	if err != nil || count != 1 {
		t.Fatalf("purge count %d: %v", count, err)
	}
	hidden, err := e.Message(ctx, queued.Message.ID)
	if err != nil || hidden.Reply == nil || !hidden.Reply.OriginalExpired {
		t.Fatalf("reply didn't become unavailable: %+v %v", hidden.Reply, err)
	}
	stop := startOutboundWorker(t, e)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		sends := append([]application.Outgoing(nil), p.sent...)
		p.mu.Unlock()
		if len(sends) > 0 {
			if sends[0].ID != queued.Message.ID {
				t.Fatalf("unexpected message sent: %+v", sends[0])
			}
			if sends[0].OriginalText != "Reply body" || sends[0].Reply != nil {
				t.Fatalf("expired auto-quote submitted to provider: %q", sends[0].OriginalText)
			}
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("scheduled message wasn't dispatched")
}

func TestRetentionRejectsCheckpointFromOldDiscovery(t *testing.T) {
	s, e, c := outboxFixture(t)
	ctx := t.Context()
	e.SetProtocols(&outboundProtocol{})
	if _, err := e.SetGlobalRetention(ctx, domain.GlobalRetention{Revision: "0", RoomDays: 180, DirectDays: 30}); err != nil {
		t.Fatal(err)
	}
	if err := e.CommitHistoryCheckpoint(ctx, c.AccountID, "alice@localhost", "before-page"); err != nil {
		t.Fatal(err)
	}
	revision, err := e.ArchiveRetentionRevision(ctx, c.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	discovery := application.WithArchiveRetentionRevision(ctx, revision)
	// The active discovery pass finishes processing a page under the old cutoff.
	// A timestamp in the expanded retention window must be retried after changing it.
	in := application.Incoming{AccountID: c.AccountID, Address: "new-peer@localhost", OriginalText: "newly eligible", CreatedAt: time.Now().AddDate(0, 0, -60), Historical: true}
	if err := e.Receive(ctx, in); err != nil {
		t.Fatal(err)
	}
	retained, err := s.DirectRetentionPeers(ctx, c.AccountID)
	if err != nil || len(retained) != 1 {
		t.Fatalf("old page created a new peer: %v, %v", retained, err)
	}
	if _, err = e.SetGlobalRetention(ctx, domain.GlobalRetention{Revision: "1", RoomDays: 180, DirectDays: 180}); err != nil {
		t.Fatal(err)
	}
	if pos, err := e.HistoryCheckpoint(ctx, c.AccountID, "alice@localhost"); err != nil || pos != "" {
		t.Fatalf("policy did not reset: %q %v", pos, err)
	}
	if err := e.CommitHistoryCheckpoint(discovery, c.AccountID, "alice@localhost", "after-newly-eligible"); !errors.Is(err, application.ErrStateConflict) {
		t.Fatalf("expected the stale page to fail, got %v", err)
	}
	if pos, err := e.HistoryCheckpoint(ctx, c.AccountID, "alice@localhost"); err != nil || pos != "" {
		t.Fatalf("the next discovery pass will resume after a now-eligible message: %q %v", pos, err)
	}
}

func TestRetentionOfflineRoomReopensExpiredCoverage(t *testing.T) {
	s, e, c := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	room := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), PeerID: migrationID(), AccountID: c.AccountID,
		Kind: "room", Address: "room@conference.localhost", UpdatedAt: now}
	if err := s.RestoreConversation(ctx, room); err != nil {
		t.Fatal(err)
	}
	days := 180
	old := now.AddDate(0, 0, -60)
	progress := domain.RoomArchiveProgress{RetentionDays: &days, OlderCoveredThrough: now.AddDate(0, 0, -180), OlderComplete: true, InitialComplete: true,
		RecentComplete: true, Boundary: old, CaughtUpThrough: now}
	if err := s.StoreRoomArchiveProgress(ctx, room.ID, progress); err != nil {
		t.Fatal(err)
	}
	lost := starMessage(t, s, room, old)
	for i := range 100 {
		starMessage(t, s, room, now.Add(-time.Duration(i+1)*time.Minute))
	}
	short := 7
	first, err := e.SetConversationRetention(ctx, room.ID, "0", &short)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
	if err != nil || n != 1 {
		t.Fatalf("short policy did not purge: %d %v", n, err)
	}
	if _, err := e.Message(ctx, lost.ID); err == nil {
		t.Fatal("expected 60-day-old message to be purged")
	}
	if _, err := e.SetConversationRetention(ctx, room.ID, first.Revision, &days); err != nil {
		t.Fatal(err)
	}
	after, err := s.RoomArchive(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.OlderComplete || after.RetentionDays == nil || *after.RetentionDays != 180 || after.ResumeEnd.IsZero() {
		t.Fatalf("on reconnect XMPP will see completed coverage under the exact same 180-day policy even though a 60-day message was purged: %+v", after.RoomArchiveProgress)
	}
}

func TestRetentionIncomingDiscardedOriginalHasNoQuote(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	now := time.Now().UTC()
	seven := 7
	if _, err := e.SetConversationRetention(ctx, c.ID, "0", &seven); err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "old"}
	original := application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "old", OriginalText: "original",
		CreatedAt: now.AddDate(0, 0, -9), Historical: true, References: []domain.MessageReference{ref}}
	if err := e.Receive(ctx, original); err != nil {
		t.Fatal(err)
	}
	reply := application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "reply", OriginalText: "> original\nMy answer",
		CreatedAt: now, Historical: true, Reply: &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 11}}}}
	if err := e.Receive(ctx, reply); err != nil {
		t.Fatal(err)
	}
	messages, err := s.Messages(ctx, c.ID, "", 10)
	if err != nil || len(messages) != 1 || messages[0].Reply == nil {
		t.Fatalf("expected one retained reply, got %+v, %v", messages, err)
	}
	retained := messages[0].Reply
	if retained.Excerpt != "" || !retained.OriginalExpired || retained.BodyText != "My answer" {
		t.Fatalf("discarded source still appears in quote: %+v", retained)
	}
}
