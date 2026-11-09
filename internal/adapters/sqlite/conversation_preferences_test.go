package sqlite

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func preferenceBool(value bool) *bool { return &value }
func preferenceInput(c domain.Conversation, archived, favorite *bool) application.ConversationPreferencesInput {
	return application.ConversationPreferencesInput{AccountID: c.AccountID, ExpectedRevisionID: c.RevisionID, Archived: archived, Favorite: favorite}
}
func preferenceCounts(t *testing.T, s *Store) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range []string{"operations", "changes", "conversation_revisions", "messages", "message_revisions", "outbox"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		out[table] = count
	}
	return out
}
func TestConversationPreferencesFieldIsolationReplayNoopAndRestart(t *testing.T) {
	s, e, c := outboxFixture(t)
	pending, err := e.SendMessage(t.Context(), migrationID(), c.ID, "pending body")
	if err != nil {
		t.Fatal(err)
	}
	source := readMessage(t, s, c, false, false)
	before, err := e.Conversation(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	input := preferenceInput(before, preferenceBool(true), nil)
	first, err := e.UpdateConversationPreferences(t.Context(), key, c.ID, input)
	if err != nil || !first.Conversation.Archived || first.Conversation.Favorite || first.Replayed {
		t.Fatal(first, err)
	}
	actual := first.Conversation
	if actual.RevisionID == before.RevisionID || actual.ActivitySequence != before.ActivitySequence || actual.UpdatedAt != before.UpdatedAt || actual.LastMessage != before.LastMessage || actual.UnreadCount != before.UnreadCount {
		t.Fatal("metadata changed message activity", before, actual)
	}
	var parents string
	if err := s.db.QueryRowContext(t.Context(), "SELECT parents FROM conversation_revisions WHERE id=?", string(actual.RevisionID)).Scan(&parents); err != nil || parents != jsonText([]domain.ID{before.RevisionID}) {
		t.Fatal("missing immutable parent", parents, err)
	}
	favorite, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, preferenceInput(actual, nil, preferenceBool(true)))
	if err != nil || !favorite.Conversation.Archived || !favorite.Conversation.Favorite {
		t.Fatal(favorite, err)
	}
	counts := preferenceCounts(t, s)
	noop, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, preferenceInput(favorite.Conversation, preferenceBool(true), nil))
	if err != nil || noop.Replayed || !reflect.DeepEqual(noop.Conversation, favorite.Conversation) {
		t.Fatal("no-op changed projection", noop, err)
	}
	afterCounts := preferenceCounts(t, s)
	if afterCounts["operations"] != counts["operations"]+1 {
		t.Fatal("no-op did not retain receipt")
	}
	afterCounts["operations"]--
	if !reflect.DeepEqual(counts, afterCounts) {
		t.Fatal("no-op emitted revision or event", counts, afterCounts)
	}
	replay, err := e.UpdateConversationPreferences(t.Context(), key, c.ID, input)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Conversation, first.Conversation) {
		t.Fatal("replay did not retain original acceptance", replay, err)
	}
	changed := input
	changed.Favorite = preferenceBool(false)
	if _, err := e.UpdateConversationPreferences(t.Context(), key, c.ID, changed); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("omitted flag conflated with false", err)
	}
	conflict, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, input)
	var stale application.ConversationPreferencesConflict
	if !errors.As(err, &stale) || !reflect.DeepEqual(stale.Current, favorite.Conversation) || conflict.Replayed {
		t.Fatal("stale no-op hid changed head", stale, err)
	}
	rows, err := e.Conversations(t.Context(), "", application.ConversationQuery{Archived: true, FavoritesOnly: true, Limit: 100})
	if err != nil || len(rows) != 1 || rows[0].ID != c.ID {
		t.Fatal("columns and payload disagree", rows, err)
	}
	rows, err = e.Conversations(t.Context(), "", application.ConversationQuery{Limit: 100})
	if err != nil || len(rows) != 0 {
		t.Fatal("archive leaked into inbox", rows, err)
	}
	got, err := e.Message(t.Context(), pending.Message.ID)
	if err != nil || !reflect.DeepEqual(got, pending.Message) {
		t.Fatal("metadata mutated pending send", got, err)
	}
	got, err = e.Message(t.Context(), source.ID)
	if err != nil || !reflect.DeepEqual(got, source) {
		t.Fatal("metadata mutated received source", got, err)
	}
	queued, err := s.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(queued) != 1 || queued[0].ID != pending.Message.ID {
		t.Fatal("metadata changed outbox", queued, err)
	}
	var database string
	if err := s.db.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), filepath.Dir(database), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	e2, err := application.New(reopened, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	replay, err = e2.UpdateConversationPreferences(t.Context(), key, c.ID, input)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Conversation, first.Conversation) {
		t.Fatal("restart lost acceptance", replay, err)
	}
	current, err := e2.Conversation(t.Context(), c.ID)
	if err != nil || !reflect.DeepEqual(current, favorite.Conversation) {
		t.Fatal("replay replaced current state", current, err)
	}
}
func TestConversationPreferencesAtomicRollbackAndCASConflictEvidence(t *testing.T) {
	for _, failure := range []string{"BEFORE UPDATE ON conversations", "BEFORE INSERT ON conversation_revisions", "BEFORE INSERT ON changes", "BEFORE UPDATE ON operations"} {
		t.Run(failure, func(t *testing.T) {
			s, e, c := outboxFixture(t)
			before := preferenceCounts(t, s)
			if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER fail_preferences "+failure+" BEGIN SELECT RAISE(ABORT,'injected preference failure'); END;"); err != nil {
				t.Fatal(err)
			}
			key := migrationID()
			if _, err := e.UpdateConversationPreferences(t.Context(), key, c.ID, preferenceInput(c, preferenceBool(true), preferenceBool(true))); err == nil {
				t.Fatal("partial mutation accepted")
			}
			current, err := e.Conversation(t.Context(), c.ID)
			if err != nil || !reflect.DeepEqual(current, c) {
				t.Fatal("failed mutation changed projection", current, err)
			}
			if _, err := e.Operation(t.Context(), key); !errors.Is(err, application.ErrNotFound) {
				t.Fatal("failed mutation retained receipt", err)
			}
			if after := preferenceCounts(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("partial effect committed", before, after)
			}
		})
	}
	s, e, c := outboxFixture(t)
	if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER suppress_preferences BEFORE UPDATE OF archived ON conversations BEGIN SELECT RAISE(IGNORE); END;"); err != nil {
		t.Fatal(err)
	}
	_, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, preferenceInput(c, preferenceBool(true), nil))
	var conflict application.ConversationPreferencesConflict
	if !errors.As(err, &conflict) || !reflect.DeepEqual(conflict.Current, c) {
		t.Fatal("CAS miss fabricated attempted revision as current", conflict, err)
	}
}
func TestConversationPreferencesSerializeConcurrentIndependentFields(t *testing.T) {
	_, e, c := outboxFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, in := range []application.ConversationPreferencesInput{preferenceInput(c, preferenceBool(true), nil), preferenceInput(c, nil, preferenceBool(true))} {
		wg.Add(1)
		go func(input application.ConversationPreferencesInput) {
			defer wg.Done()
			<-start
			_, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, input)
			results <- err
		}(in)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		var conflict application.ConversationPreferencesConflict
		if err == nil {
			succeeded++
		} else if errors.As(err, &conflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatal("concurrent heads did not serialize", succeeded, conflicted)
	}
	current, err := e.Conversation(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, preferenceInput(current, preferenceBool(true), preferenceBool(true)))
	if err != nil || !next.Conversation.Archived || !next.Conversation.Favorite {
		t.Fatal("rebased field change lost independent winner", next, err)
	}
}
func TestConversationPreferencesRaceIncomingAndReadPreservesUnrelatedState(t *testing.T) {
	s, e, c := outboxFixture(t)
	first := readMessage(t, s, c, false, false)
	before, err := e.Conversation(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	incoming := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob"}, ProviderID: string(migrationID()), OriginalText: "new incoming body", CreatedAt: time.Now().Add(time.Hour).UTC(), SendState: "received"}
	start := make(chan struct{})
	results := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); <-start; results <- s.ReceiveMessage(t.Context(), c, incoming, migrationID()) }()
	go func() {
		defer wg.Done()
		<-start
		_, err := e.MarkMessagesRead(t.Context(), migrationID(), c.ID, []domain.ID{first.ID})
		results <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, preferenceInput(before, preferenceBool(true), preferenceBool(true)))
		var conflict application.ConversationPreferencesConflict
		if errors.As(err, &conflict) {
			err = nil
		}
		results <- err
	}()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := e.Conversation(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	final, err := e.UpdateConversationPreferences(t.Context(), migrationID(), c.ID, preferenceInput(current, preferenceBool(true), preferenceBool(true)))
	if err != nil {
		t.Fatal(err)
	}
	if !final.Conversation.Archived || !final.Conversation.Favorite || final.Conversation.LastMessage != incoming.OriginalText || !final.Conversation.UpdatedAt.Equal(incoming.CreatedAt) || final.Conversation.UnreadCount != 1 || final.Conversation.ActivitySequence != current.ActivitySequence {
		t.Fatal("concurrent mutations lost independent state", final.Conversation)
	}
	readMessage(t, s, c, false, false)
	current, err = e.Conversation(t.Context(), c.ID)
	if err != nil || !current.Archived || !current.Favorite || current.UnreadCount != 2 {
		t.Fatal("new arrival changed retained preferences", current, err)
	}
}
