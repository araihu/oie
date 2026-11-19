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

func TestLegacyRoomReplyMigrationRecoversArchiveAliasAndExpiry(t *testing.T) {
	store, _, chat := outboxFixture(t)
	engine, chat := promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	target := domain.MessageReference{Scope: "room:" + chat.Address, Author: chat.Address + "/Bob", ID: "original-in-room-archive"}
	reply := replySeed(t, store, chat, "> quoted\nanswer", at, nil, &domain.ReplyReference{
		Target: target, Fallback: []domain.TextRange{{Start: 0, End: 9}},
	})
	check := func(id domain.ID, want bool) {
		t.Helper()
		got, err := engine.Message(t.Context(), reply.ID)
		if err != nil || got.Reply == nil || got.Reply.Available != want || got.Reply.MessageID != id ||
			want && (got.Reply.SenderName != "Bob" || got.Reply.Excerpt != "quoted" || got.Reply.BodyText != "answer") {
			t.Fatalf("unexpected reply: %+v, %v", got.Reply, err)
		}
	}
	check("", false)
	// The retained original predates SID discovery. A later archive copy
	// attached its UID to this live row without rewriting the payload.
	old := at.AddDate(0, 0, -9)
	original := seedPortableRoomMessage(t, store, chat, old, "quoted", "archive-provider", "", "")
	seedPortableRoomMessage(t, store, chat, old, "quoted", "archive-provider", "", target.ID)
	check("", false)
	migrateFixtureToVersion(t, store, 54)
	if err := migrateDatabase(t.Context(), store.db, store.dir); err != nil {
		t.Fatal(err)
	}
	check(original.ID, true)
	wrongAuthor := target
	wrongAuthor.Author = chat.Address + "/Alice"
	other := replySeed(t, store, chat, "another answer", at, nil, &domain.ReplyReference{Target: wrongAuthor})
	got, err := engine.Message(t.Context(), other.ID)
	if err != nil || got.Reply == nil || got.Reply.Available {
		t.Fatalf("matched another occupant: %+v, %v", got.Reply, err)
	}

	days := 7
	if _, err := engine.SetConversationRetention(t.Context(), chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < domain.RoomRetentionMinimum; i++ {
		seedPortableRoomMessage(t, store, chat, at, "newer", fmt.Sprint("recent-", i), "", "")
	}
	count, err := store.PurgeExpiredHistory(t.Context(), at, func() (domain.ID, error) { return migrationID(), nil })
	if err != nil || count != 1 {
		t.Fatalf("purge count=%d, error=%v", count, err)
	}
	got, err = engine.Message(t.Context(), reply.ID)
	if err != nil || got.Reply == nil || !got.Reply.OriginalExpired || got.Reply.Available ||
		got.Reply.Excerpt != "" || got.OriginalText != "answer" {
		t.Fatalf("expired room quote: %+v, %v", got, err)
	}
}

func TestReceivedRoomReplyChecksBothArchiveAndStanzaOwners(t *testing.T) {
	store, _, chat := outboxFixture(t)
	engine, chat := promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	ref := domain.MessageReference{Scope: "room:" + chat.Address, Author: chat.Address + "/Bob", ID: "room-original"}
	archived := seedPortableRoomMessage(t, store, chat, at, "archive original", "archive-client", "", ref.ID)
	explicit := seedPortableRoomMessage(t, store, chat, at, "another message", "live-client", "", "")
	// Earlier profiles could retain different physical owners for these keys.
	// Populate the observed SID directly so this also exercises that upgrade.
	if err := recordReplyMetadata(t.Context(), store.queries, string(explicit.ID), domain.Message{
		AccountID: chat.AccountID, ConversationID: chat.ID, OriginalText: explicit.OriginalText, References: []domain.MessageReference{ref},
	}, false); err != nil {
		t.Fatal(err)
	}
	reply := replySeed(t, store, chat, "answer", at, nil, &domain.ReplyReference{Target: ref})
	// A modern archive observation whose parser did not validate any SID
	// cannot block a different, verified live source.
	resolved, err := engine.Message(t.Context(), reply.ID)
	if err != nil || resolved.Reply == nil || !resolved.Reply.Available || resolved.Reply.MessageID != explicit.ID {
		t.Fatalf("unverified archive suppressed a verified live reference: %+v, %v", resolved.Reply, err)
	}
	migrateFixtureToVersion(t, store, 54)
	if err := migrateDatabase(t.Context(), store.db, store.dir); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Message(t.Context(), reply.ID)
	if err != nil || got.Reply == nil || got.Reply.Available || got.Reply.MessageID != "" {
		t.Fatalf("chose among conflicting room targets %s/%s: %+v, %v", archived.ID, explicit.ID, got.Reply, err)
	}
	if err := hydrateReferences(t.Context(), store.queries, &explicit); err != nil || len(explicit.References) != 0 {
		t.Fatalf("ambiguous outbound reply target: %+v, %v", explicit.References, err)
	}
}

func TestDiscardedRoomArchiveUIDScrubsSurvivingReply(t *testing.T) {
	store, _, chat := outboxFixture(t)
	engine, chat := promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	days := 7
	if _, err := engine.SetConversationRetention(t.Context(), chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "room:" + chat.Address, ID: "archive-only", Author: chat.Address + "/Bob"}
	reply := replySeed(t, store, chat, "> quoted\nanswer", at, nil, &domain.ReplyReference{
		Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 9}},
	})
	old := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: chat.AccountID, Historical: true,
		ArchiveScope: chat.Address, ArchiveID: ref.ID, SendState: "received", OriginalText: "quoted",
		CreatedAt: at.AddDate(0, 0, -9), ReceivedAt: at, References: []domain.MessageReference{ref}}
	if err := store.ReceiveMessage(t.Context(), chat, old, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Message(t.Context(), reply.ID)
	if err != nil || got.Reply == nil || !got.Reply.OriginalExpired || got.Reply.Excerpt != "" || got.OriginalText != "answer" {
		t.Fatalf("discarded original exposed a quote: %+v, %v", got, err)
	}
}

func TestLegacyRoomArchiveWithoutDiscoveryResolvesOnNewReply(t *testing.T) {
	store, _, chat := outboxFixture(t)
	engine, chat := promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	original := seedPortableRoomMessage(t, store, chat, at, "quoted", "room:provider", "", "uid")
	migrateFixtureToVersion(t, store, 54)
	if err := migrateDatabase(t.Context(), store.db, store.dir); err != nil {
		t.Fatal(err)
	}
	if err := hydrateReferences(t.Context(), store.queries, &original); err != nil || len(original.References) != 0 {
		t.Fatalf("acquired a room reference without discovery evidence: %+v, %v", original.References, err)
	}
	ref := domain.MessageReference{Scope: "room:" + chat.Address, Author: chat.Address + "/Bob", ID: "uid"}
	reply := replySeed(t, store, chat, "answer", at, nil, &domain.ReplyReference{Target: ref})
	resolved, err := engine.Message(t.Context(), reply.ID)
	if err != nil || resolved.Reply == nil || resolved.Reply.MessageID != original.ID || !resolved.Reply.Available {
		t.Fatalf("new verified reply did not resolve the retained original: %+v, %v", resolved.Reply, err)
	}
}

func TestReplayOfPurgedLegacyRoomOriginalScrubsReply(t *testing.T) {
	store, _, chat := outboxFixture(t)
	engine, chat := promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	old := seedPortableRoomMessage(t, store, chat, at.AddDate(0, 0, -9), "quoted", "old", "", "room-uid")
	if _, err := store.db.Exec("DELETE FROM messages WHERE id=?", old.ID); err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "room:" + chat.Address, Author: chat.Address + "/Bob", ID: "room-uid"}
	reply := replySeed(t, store, chat, "> quoted\nanswer", at, nil, &domain.ReplyReference{
		Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 9}},
	})
	old.References, old.ReceivedAt = []domain.MessageReference{ref}, at
	if err := store.ReceiveMessage(t.Context(), chat, old, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Message(t.Context(), reply.ID)
	if err != nil || got.Reply == nil || !got.Reply.OriginalExpired || got.Reply.Excerpt != "" || got.OriginalText != "answer" {
		t.Fatalf("replayed deletion left a retained quotation: %+v, %v", got, err)
	}
}

func TestRestoreLegacyRoomArchiveReplyAfterMessageIDTranslation(t *testing.T) {
	src, _, sc := outboxFixture(t)
	dst, _, dc := outboxFixture(t)
	_, sc = promoteBackupRoomFixture(t, src, sc)
	engine, dc := promoteBackupRoomFixture(t, dst, dc)
	at := time.Now().UTC()
	const uid = "source-archive-uid"
	original := seedPortableRoomMessage(t, src, sc, at, "quoted", "source-provider", "", uid)
	retained := seedPortableRoomMessage(t, dst, dc, at, "quoted", "destination-provider", "", uid)
	if retained.ID == original.ID {
		t.Fatal("profiles unexpectedly share a local message ID")
	}
	migrateFixtureToVersion(t, src, 54)
	if err := migrateDatabase(t.Context(), src.db, src.dir); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, src, false)
	if _, err := dst.mergeBackup(t.Context(), archive, scratch, manifest, at); err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "room:" + dc.Address, Author: dc.Address + "/Bob", ID: uid}
	reply := replySeed(t, dst, dc, "answer", at, nil, &domain.ReplyReference{Target: ref})
	resolved, err := engine.Message(t.Context(), reply.ID)
	if err != nil || resolved.Reply == nil || resolved.Reply.MessageID != retained.ID || !resolved.Reply.Available {
		t.Fatalf("new reply did not resolve the translated source: %+v, %v", resolved.Reply, err)
	}
}
