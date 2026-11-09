package sqlite

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func timelineID(n int) domain.ID { return domain.ID(fmt.Sprintf("019a5df0-1111-7000-8000-%012d", n)) }
func TestMessagesAfterNearestChronologicalTieBreakAndScope(t *testing.T) {
	s, e, c := outboxFixture(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	for i, n := range []int{9, 1, 8, 2, 7, 3, 6, 4, 5} {
		m := domain.Message{ID: timelineID(n), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, OriginalText: fmt.Sprint(n), ProviderID: fmt.Sprint(n), SendState: "received", CreatedAt: base.Add(time.Duration(i/3) * time.Millisecond), Historical: true}
		if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
			t.Fatal(err)
		}
	}
	_, before, _ := s.Snapshot(t.Context())
	original, _ := s.Conversation(t.Context(), c.ID)
	after, err := e.NewerMessages(t.Context(), c.ID, timelineID(9), 2)
	if err != nil || len(after) != 3 || after[0].ID != timelineID(2) || after[1].ID != timelineID(3) || after[2].ID != timelineID(7) {
		t.Fatal("not nearest ASC with lookahead", after, err)
	}
	window, err := e.MessageContext(t.Context(), c.ID, timelineID(3), 3)
	if err != nil || !window.HasOlder || !window.HasNewer || window.Items[0].ID != timelineID(7) || window.Items[2].ID != timelineID(2) {
		t.Fatal(window, err)
	}
	beforeWindow, err := e.Messages(t.Context(), c.ID, window.Items[2].ID, 2)
	if err != nil || len(beforeWindow) != 3 || beforeWindow[0].ID != timelineID(9) {
		t.Fatal(beforeWindow, err)
	}
	afterWindow, err := e.NewerMessages(t.Context(), c.ID, window.Items[0].ID, 2)
	if err != nil || len(afterWindow) != 3 || afterWindow[0].ID != timelineID(4) {
		t.Fatal(afterWindow, err)
	}
	empty, err := e.NewerMessages(t.Context(), c.ID, timelineID(6), 2)
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	for _, cursor := range []domain.ID{"", "invalid"} {
		if _, err := e.NewerMessages(t.Context(), c.ID, cursor, 2); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("invalid cursor", cursor, err)
		}
	}
	for _, limit := range []int{0, 101} {
		if _, err := e.NewerMessages(t.Context(), c.ID, timelineID(1), limit); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("invalid limit", err)
		}
	}
	_, afterWalk, _ := s.Snapshot(t.Context())
	if before != afterWalk {
		t.Fatal("timeline walk emitted events")
	}
	foreign := catalogConversation(t, s, c.AccountID, "foreign@localhost", "Foreign")
	foreignMessage := replySeed(t, s, foreign, "private", base, nil, nil)
	for _, read := range []func() ([]domain.Message, error){func() ([]domain.Message, error) { return e.NewerMessages(t.Context(), c.ID, foreignMessage.ID, 2) }, func() ([]domain.Message, error) { return s.MessagesAfter(t.Context(), c.ID, foreignMessage.ID, 3) }} {
		if rows, err := read(); !errors.Is(err, application.ErrNotFound) || len(rows) != 0 {
			t.Fatal("foreign cursor disclosed neighbors", rows, err)
		}
	}
	// Ignore setup for the foreign fixture when comparing read-only effects.
	_, before, _ = s.Snapshot(t.Context())
	_, _ = e.NewerMessages(t.Context(), c.ID, timelineID(1), 100)
	_, afterSeq, _ := s.Snapshot(t.Context())
	current, _ := s.Conversation(t.Context(), c.ID)
	if before != afterSeq || !reflect.DeepEqual(original, current) {
		t.Fatal("timeline read mutated events/conversation")
	}
	if pending, err := s.PendingMessages(t.Context(), time.Now().Add(time.Hour)); err != nil || len(pending) != 0 {
		t.Fatal("timeline created outbox", err)
	}
}
func TestMessagesAfterCanonicalAliasAndReplyHydration(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().Add(-time.Hour).UTC()
	receiveCorrection(t, e, c, "first", "original", "", "proof:bob", "bob", at)
	source := correctionMessages(t, e, c)[0]
	receiveCorrection(t, e, c, "edit", "edited", "first", "proof:bob", "bob", at.Add(time.Second))
	alias, err := s.queries.FindProviderMessage(t.Context(), dbgen.FindProviderMessageParams{AccountID: string(c.AccountID), ConversationID: string(c.ID), ProviderID: "edit", IsOwn: 0})
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "parent"}
	parent := replySeed(t, s, c, "parent", at.Add(2*time.Second), []domain.MessageReference{ref}, nil)
	reply := replySeed(t, s, c, "answer", at.Add(3*time.Second), nil, &domain.ReplyReference{Target: ref})
	items, err := e.NewerMessages(t.Context(), c.ID, domain.ID(alias.ID), 100)
	if err != nil || len(items) != 2 || items[0].ID != parent.ID || items[1].ID != reply.ID || items[1].Reply == nil || !items[1].Reply.Available || items[1].Reply.MessageID != parent.ID {
		t.Fatal("alias cursor or reply projection lost", items, err)
	}
	old, err := e.Messages(t.Context(), c.ID, "", 100)
	if err != nil || len(old) != 3 || old[2].ID != source.ID || !old[2].Edited {
		t.Fatal("alias leaked into timeline", old, err)
	}
}
