package sqlite

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func outboxFixture(t *testing.T) (*Store, *application.Engine, domain.Conversation) {
	t.Helper()
	store, err := Open(t.Context(), t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e, err := application.New(store, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	c := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: a.Account.ID, PeerID: migrationID(), Address: "bob@localhost", Title: "Bob", Kind: "direct", UpdatedAt: now}
	result, err := store.OpenConversation(t.Context(), application.ConversationMutation{Operation: application.Operation{ID: migrationID(), ResourceID: c.ID, CreatedAt: now, Kind: "open_conversation"}, Fingerprint: "fixture", Conversation: c})
	if err != nil {
		t.Fatal(err)
	}
	return store, e, result.Conversation
}
func TestMessageAndOutboxAcceptanceRollBackTogether(t *testing.T) {
	for _, failure := range []string{"BEFORE INSERT ON message_revisions", "BEFORE UPDATE ON conversations"} {
		t.Run(failure, func(t *testing.T) {
			store, e, c := outboxFixture(t)
			_, before, _ := store.Snapshot(t.Context())
			if _, err := store.db.Exec("CREATE TRIGGER fail_message " + failure + " BEGIN SELECT RAISE(ABORT,'injected acceptance failure'); END;"); err != nil {
				t.Fatal(err)
			}
			key := migrationID()
			if _, err := e.SendMessage(t.Context(), key, c.ID, "must not be partially accepted"); err == nil {
				t.Fatal("injected failure unexpectedly accepted")
			}
			if _, err := e.Operation(t.Context(), key); !errors.Is(err, application.ErrNotFound) {
				t.Fatal("receipt survived failed acceptance")
			}
			for _, table := range []string{"messages", "message_revisions", "outbox"} {
				var n int
				if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("partial %s rows: %d %v", table, n, err)
				}
			}
			_, after, _ := store.Snapshot(t.Context())
			if after != before {
				t.Fatal("failed acceptance emitted a durable event")
			}
			current, err := store.Conversation(t.Context(), c.ID)
			if err != nil || current.RevisionID != c.RevisionID || current.LastMessage != "" {
				t.Fatal("failed acceptance changed conversation projection")
			}
		})
	}
}
func TestCancellationAndDispatchClaimHaveOneAtomicWinner(t *testing.T) {
	store, e, c := outboxFixture(t)
	for range 12 {
		accepted, err := e.SendMessage(t.Context(), migrationID(), c.ID, "cutoff race")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.TransitionMessage(t.Context(), accepted.Message.ID, "queued", "dispatching", migrationID(), time.Now(), time.Now())
			results <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := e.CancelMessage(t.Context(), migrationID(), accepted.Message.ID, accepted.Message.RevisionID)
			results <- err
		}()
		close(start)
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			} else if !errors.Is(err, application.ErrStateConflict) {
				t.Fatal(err)
			}
		}
		if success != 1 {
			t.Fatalf("%d cutoff winners", success)
		}
		m, err := store.Message(t.Context(), accepted.Message.ID)
		if err != nil || (m.SendState != "cancelled" && m.SendState != "dispatching") {
			t.Fatal("cutoff left an impossible state")
		}
	}
}
