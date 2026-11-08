package sqlite

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func replySeed(t *testing.T, s *Store, c domain.Conversation, body string, at time.Time, refs []domain.MessageReference, reply *domain.ReplyReference) domain.Message {
	t.Helper()
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{DisplayName: "Bob"}, OriginalText: body, CreatedAt: at, SendState: "received", ProviderID: string(migrationID()), Historical: true, References: refs, ReplyReference: reply}
	if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestReplyLateOriginalReplayAndCollisionIsolation(t *testing.T) {
	s, e, c := outboxFixture(t)
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "origin-1"}
	raw := "> quoted 🍰\nmy answer"
	reply := replySeed(t, s, c, raw, time.Now().UTC(), nil, &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 11}}})
	before, err := e.Message(t.Context(), reply.ID)
	if err != nil || before.Reply == nil || before.Reply.Available || before.Reply.BodyText != "my answer" || before.OriginalText != raw {
		t.Fatal(before, err)
	}
	parent := replySeed(t, s, c, "the actual original", time.Now().Add(-time.Hour).UTC(), []domain.MessageReference{ref}, nil)
	got, err := e.Message(t.Context(), reply.ID)
	if err != nil || !got.Reply.Available || got.Reply.MessageID != parent.ID || got.Reply.Excerpt != "the actual original" || got.RevisionID != before.RevisionID || got.OriginalText != raw {
		t.Fatal(got, err)
	}
	_, seq, _ := s.Snapshot(t.Context())
	if err = s.ReceiveMessage(t.Context(), c, parent, migrationID()); err != nil {
		t.Fatal(err)
	}
	_, after, _ := s.Snapshot(t.Context())
	if seq != after {
		t.Fatal("metadata replay duplicated an event")
	}
	// A colliding origin cannot be resolved by first-writer-wins or quoted text.
	replySeed(t, s, c, "different original", time.Now().UTC(), []domain.MessageReference{ref}, nil)
	got, err = e.Message(t.Context(), reply.ID)
	if err != nil || got.Reply.Available || got.Reply.MessageID != "" {
		t.Fatal("ambiguous reference navigates", got, err)
	}
	if pending, err := s.PendingMessages(t.Context(), time.Now().Add(time.Hour)); err != nil || len(pending) != 0 {
		t.Fatal("reply metadata created sends", err)
	}
	chat, err := s.Conversation(t.Context(), c.ID)
	if err != nil || chat.UnreadCount != 0 {
		t.Fatal("historical reply metadata changed unread", err)
	}
}
func TestReplyMetadataEnrichesRetainedAliasWithoutContentMutation(t *testing.T) {
	s, e, c := outboxFixture(t)
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "original"}
	parent := replySeed(t, s, c, "original", time.Now().Add(-time.Hour).UTC(), []domain.MessageReference{ref}, nil)
	m := replySeed(t, s, c, "> original\nanswer", time.Now().UTC(), nil, nil)
	old, _ := e.Message(t.Context(), m.ID)
	m.ReplyReference = &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 11}}}
	if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err := e.Message(t.Context(), m.ID)
	if err != nil || got.Reply == nil || got.Reply.MessageID != parent.ID || got.RevisionID != old.RevisionID {
		t.Fatal(got, err)
	}
	got.Reply = nil
	if !reflect.DeepEqual(got, old) {
		t.Fatal("metadata enrichment changed retained source")
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM message_revisions WHERE entity_id=?", m.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("metadata invented content edits", count, err)
	}
	// Same wire ID with a different body remains collision evidence, never a link
	// to whichever content happened to be retained first.
	parent.OriginalText = "colliding body"
	if err = s.ReceiveMessage(t.Context(), c, parent, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err = e.Message(t.Context(), m.ID)
	if err != nil || got.Reply.Available {
		t.Fatal("wire ID collision resolved incorrectly", err)
	}
}
func TestMessageContextIsBoundedChronologicalAndConversationScoped(t *testing.T) {
	s, e, c := outboxFixture(t)
	base := time.Now().Add(-time.Hour).UTC()
	ids := []domain.ID{}
	for i := range 80 {
		m := replySeed(t, s, c, fmt.Sprint(i), base.Add(time.Duration(i)*time.Second), nil, nil)
		ids = append(ids, m.ID)
	}
	w, err := e.MessageContext(t.Context(), c.ID, ids[30], 5)
	if err != nil || len(w.Items) != 5 || !w.HasOlder || !w.HasNewer || w.Items[2].ID != ids[30] || w.Items[0].ID != ids[32] {
		t.Fatal(w, err)
	}
	w, err = e.MessageContext(t.Context(), c.ID, ids[0], 1)
	if err != nil || len(w.Items) != 1 || w.HasOlder || !w.HasNewer || w.Items[0].ID != ids[0] {
		t.Fatal(w, err)
	}
	w, err = e.MessageContext(t.Context(), c.ID, ids[79], 5)
	if err != nil || w.HasNewer || !w.HasOlder {
		t.Fatal(w, err)
	}
	if _, err = e.MessageContext(t.Context(), migrationID(), ids[30], 5); err == nil {
		t.Fatal("foreign conversation accepted")
	}
	if _, err = e.MessageContext(t.Context(), c.ID, ids[30], 0); err != application.ErrInvalid {
		t.Fatal(err)
	}
}

func TestReplyArchiveAliasEnrichmentRejectsConversationChanges(t *testing.T) {
	s, e, c := outboxFixture(t)
	ref := domain.MessageReference{Scope: "room:room@localhost", ID: "room-original", Author: "room@localhost/member"}
	parent := replySeed(t, s, c, "original", time.Now().Add(-time.Hour).UTC(), []domain.MessageReference{ref}, nil)
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, OriginalText: "> original\nanswer", CreatedAt: time.Now().UTC(), Historical: true, SendState: "received", ArchiveScope: "account", ArchiveID: "reply-archive"}
	if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	replay := m
	replay.ID = migrationID()
	replay.RevisionID = migrationID()
	replay.ReplyReference = &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 11}}}
	foreign := c
	foreign.ID = migrationID()
	foreign.Address = "unrelated@localhost"
	if err := s.ReceiveMessage(t.Context(), foreign, replay, migrationID()); err != application.ErrInvalid {
		t.Fatal("cross-conversation archive alias accepted", err)
	}
	unchanged, err := e.Message(t.Context(), m.ID)
	if err != nil || unchanged.Reply != nil {
		t.Fatal("rejected replay changed reply", err)
	}
	if err := s.ReceiveMessage(t.Context(), c, replay, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err := e.Message(t.Context(), m.ID)
	if err != nil || got.Reply == nil || !got.Reply.Available || got.Reply.MessageID != parent.ID || got.RevisionID != m.RevisionID {
		t.Fatal("archive replay failed to enrich retained identity", err)
	}
}
