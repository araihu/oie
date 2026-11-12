package sqlite

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func readMessage(t *testing.T, s *Store, c domain.Conversation, historical, own bool) domain.Message {
	t.Helper()
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob"}, ProviderID: string(migrationID()), OriginalText: "visible message", CreatedAt: time.Now().UTC(), Historical: historical, IsOwn: own, SendState: "received"}
	if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestReadEvidenceKeepsUnseenMessagesAndActivity(t *testing.T) {
	s, e, c := outboxFixture(t)
	first := readMessage(t, s, c, false, false)
	second := readMessage(t, s, c, false, false)
	last := readMessage(t, s, c, false, false)
	historical := readMessage(t, s, c, true, false)
	own := readMessage(t, s, c, false, true)
	before, _ := s.Conversation(t.Context(), c.ID)
	source, _ := s.Message(t.Context(), first.ID)
	key := migrationID()
	result, err := e.MarkMessagesRead(t.Context(), key, c.ID, []domain.ID{second.ID, first.ID, historical.ID, own.ID})
	if err != nil || result.State.UnreadCount != 1 || result.State.RevisionID == "" {
		t.Fatal(result, err)
	}
	after, _ := s.Conversation(t.Context(), c.ID)
	if after.ActivitySequence != before.ActivitySequence || !after.UpdatedAt.Equal(before.UpdatedAt) || after.LastMessage != before.LastMessage {
		t.Fatal("reading reordered conversation", before, after)
	}
	got, _ := s.Message(t.Context(), first.ID)
	if !reflect.DeepEqual(source, got) {
		t.Fatal("read changed source")
	}
	incoming := readMessage(t, s, c, false, false)
	replay, err := e.MarkMessagesRead(t.Context(), key, c.ID, []domain.ID{own.ID, historical.ID, first.ID, second.ID})
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.State, result.State) {
		t.Fatal("lost original receipt", replay, err)
	}
	state, err := e.ConversationReadState(t.Context(), c.ID)
	if err != nil || state.UnreadCount != 2 {
		t.Fatal("later arrivals were cleared", state, err)
	}
	if _, err = e.MarkMessagesRead(t.Context(), key, c.ID, []domain.ID{last.ID}); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("key was reused for another intent", err)
	}
	_, seq, _ := s.Snapshot(t.Context())
	if _, err = e.MarkMessagesRead(t.Context(), migrationID(), c.ID, []domain.ID{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	_, seqAfter, _ := s.Snapshot(t.Context())
	if seqAfter != seq {
		t.Fatal("duplicate read emitted another event")
	}
	state, _ = e.ConversationReadState(t.Context(), c.ID)
	if state.UnreadCount != 2 {
		t.Fatal(state)
	}
	stateResult, err := e.MarkMessagesRead(t.Context(), migrationID(), c.ID, []domain.ID{incoming.ID, last.ID})
	if err != nil || stateResult.State.UnreadCount != 0 {
		t.Fatal(stateResult, err)
	}
	pending, err := s.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("reading queued protocol sends", err)
	}
}

func TestUnreadRoomMentionsFollowCanonicalContentAndReadEvidence(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	ctx := t.Context()
	at := time.Now().UTC()
	receive := func(id, body, target string, own, historical bool) {
		t.Helper()
		at = at.Add(time.Second)
		in := application.Incoming{AccountID: room.AccountID, Kind: "room", Address: room.Address, OwnRoomNickname: "Ána", ProviderID: id, OriginalText: body, IsOwn: own, Historical: historical, CreatedAt: at}
		if historical {
			in.OwnRoomNickname = ""
		} else if id == "renamed" {
			in.OwnRoomNickname = "Bia"
		}
		if !own {
			in.Correction = &domain.MessageCorrection{Scope: "room:" + room.Address, Author: "bob", ID: id, TargetID: target}
		}
		if err := e.Receive(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	expectUnread := func(count int, mentioned bool) {
		t.Helper()
		chat, err := e.Conversation(ctx, room.ID)
		if err != nil || chat.UnreadCount != count || chat.UnreadMention != mentioned {
			t.Fatalf("unread count and mention = %d, %v, want %d, %v (%v)", chat.UnreadCount, chat.UnreadMention, count, mentioned, err)
		}
	}
	receive("history", "Ána", "", false, true)
	receive("own", "@Ána", "", true, false)
	expectUnread(0, false)
	receive("ordinary", "Olá, Ánabela", "", false, false)
	expectUnread(1, false)
	receive("one", "ÁNA, oi!", "", false, false)
	first := correctionMessages(t, e, room)[0]
	receive("two", "Olá @Ána", "", false, false)
	second := correctionMessages(t, e, room)[0]
	receive("two", "Olá @Ána", "", false, false)
	expectUnread(3, true)
	if !first.MentionsYou || !second.MentionsYou {
		t.Fatal("room nickname mentions were lost", first, second)
	}
	if _, err := e.MarkMessagesRead(ctx, migrationID(), room.ID, []domain.ID{first.ID}); err != nil {
		t.Fatal(err)
	}
	expectUnread(2, true)
	receive("renamed", "Bia, oi!", "two", false, false)
	receive("retain", "Ainda falando com Bia.", "renamed", false, true)
	expectUnread(2, true)
	receive("remove", "Olá", "retain", false, true)
	expectUnread(2, false)
	receive("restore", "Bia!", "remove", false, true)
	expectUnread(2, true)
	if _, err := e.MarkMessagesRead(ctx, migrationID(), room.ID, []domain.ID{second.ID}); err != nil {
		t.Fatal(err)
	}
	expectUnread(1, false)
	message, err := e.Message(ctx, second.ID)
	if err != nil || !message.MentionsYou || message.OriginalText != "Bia!" {
		t.Fatal("reading cleared the message highlight", message, err)
	}
}

func TestReplyEnrichmentReclassifiesRoomMention(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	ctx := t.Context()
	quote := "> Ana, oi\n"
	in := application.Incoming{AccountID: room.AccountID, Kind: "room", Address: room.Address, OwnRoomNickname: "Ana", ProviderID: "reply", OriginalText: quote + "Oi Bob!", CreatedAt: time.Now().UTC()}
	if err := e.Receive(ctx, in); err != nil {
		t.Fatal(err)
	}
	before := correctionMessages(t, e, room)[0]
	if !before.MentionsYou {
		t.Fatal("unmarked source should initially match")
	}
	in.OwnRoomNickname = ""
	in.Historical = true
	in.Reply = &domain.ReplyReference{Target: domain.MessageReference{ID: "earlier"}, Fallback: []domain.TextRange{{Start: 0, End: len(quote)}}}
	if err := e.Receive(ctx, in); err != nil {
		t.Fatal(err)
	}
	after, err := e.Message(ctx, before.ID)
	if err != nil || after.MentionsYou || after.Reply == nil || after.Reply.BodyText != "Oi Bob!" || after.RevisionID != before.RevisionID {
		t.Fatal("quote enrichment changed the content revision or kept its mention", after, err)
	}
	chat, err := e.Conversation(ctx, room.ID)
	if err != nil || chat.UnreadMention || chat.UnreadCount != 1 {
		t.Fatal("quote enrichment should clear only the mention mark", chat, err)
	}
}
func TestConcurrentReadBatchesMergeAndPreserveIncoming(t *testing.T) {
	s, e, c := outboxFixture(t)
	a := readMessage(t, s, c, false, false)
	b := readMessage(t, s, c, false, false)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for _, id := range []domain.ID{a.ID, b.ID} {
		wg.Go(func() { _, err := e.MarkMessagesRead(t.Context(), migrationID(), c.ID, []domain.ID{id}); errs <- err })
	}
	wg.Go(func() {
		m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID}, ProviderID: string(migrationID()), OriginalText: "racing arrival", CreatedAt: time.Now().UTC(), SendState: "received"}
		errs <- s.ReceiveMessage(t.Context(), c, m, migrationID())
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := e.ConversationReadState(t.Context(), c.ID)
	if err != nil || state.UnreadCount != 1 {
		t.Fatal("arrival/read race lost unread", state, err)
	}
}
func TestReadBatchValidationAndRollback(t *testing.T) {
	for _, failure := range []string{"BEFORE INSERT ON conversation_read_revisions", "BEFORE UPDATE ON conversations", "BEFORE INSERT ON changes", "BEFORE UPDATE ON operations"} {
		t.Run(failure, func(t *testing.T) {
			s, e, c := outboxFixture(t)
			m := readMessage(t, s, c, false, false)
			before, _ := s.Conversation(t.Context(), c.ID)
			_, seq, _ := s.Snapshot(t.Context())
			if _, err := s.db.Exec("CREATE TRIGGER fail_read " + failure + " BEGIN SELECT RAISE(ABORT,'read failure'); END;"); err != nil {
				t.Fatal(err)
			}
			key := migrationID()
			if _, err := e.MarkMessagesRead(t.Context(), key, c.ID, []domain.ID{m.ID}); err == nil {
				t.Fatal("injected failure accepted")
			}
			if _, err := e.Operation(t.Context(), key); !errors.Is(err, application.ErrNotFound) {
				t.Fatal("partial receipt", err)
			}
			after, _ := s.Conversation(t.Context(), c.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("partial conversation write")
			}
			_, afterSeq, _ := s.Snapshot(t.Context())
			if seq != afterSeq {
				t.Fatal("partial read event")
			}
			for _, table := range []string{"message_reads", "conversation_read_revisions", "conversation_read_states"} {
				var n int
				if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
					t.Fatal(table, n, err)
				}
			}
		})
	}
	s, e, c := outboxFixture(t)
	m := readMessage(t, s, c, false, false)
	if _, err := e.MarkMessagesRead(t.Context(), migrationID(), c.ID, []domain.ID{m.ID, migrationID()}); !errors.Is(err, application.ErrNotFound) {
		t.Fatal(err)
	}
	state, _ := e.ConversationReadState(t.Context(), c.ID)
	if state.UnreadCount != 1 || state.RevisionID != "" {
		t.Fatal("invalid batch partially read", state)
	}
	foreign := c
	foreign.ID = migrationID()
	foreign.Address = "other@localhost"
	foreign.RevisionID = migrationID()
	opened, err := s.OpenConversation(t.Context(), application.ConversationMutation{Operation: application.Operation{ID: migrationID(), ResourceID: foreign.ID, Kind: "open_conversation", CreatedAt: time.Now().UTC()}, Fingerprint: "other", Conversation: foreign})
	if err != nil {
		t.Fatal(err)
	}
	other := readMessage(t, s, opened.Conversation, false, false)
	if _, err = e.MarkMessagesRead(t.Context(), migrationID(), c.ID, []domain.ID{m.ID, other.ID}); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("cross-conversation read", err)
	}
	state, _ = e.ConversationReadState(t.Context(), c.ID)
	if state.UnreadCount != 1 || state.RevisionID != "" {
		t.Fatal(state)
	}
}
