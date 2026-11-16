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
	t.Cleanup(func() { _ = store.Close() })
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

func TestWithdrawnMessageIsMaskedInReceiptsAndConflicts(t *testing.T) {
	store, engine, chat := outboxFixture(t)
	ctx := t.Context()
	source, err := engine.SendMessage(ctx, migrationID(), chat.ID, "forwarded before retraction")
	if err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	forwarded, err := engine.ForwardMessage(ctx, key, source.Message.ID, source.Message.RevisionID, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	copiedID := forwarded.Message.ID
	now := time.Now().UTC()
	if _, err := store.TransitionMessage(ctx, copiedID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionMessage(ctx, copiedID, "dispatching", "submitted", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	retraction := application.IncomingRetraction{
		Scope: "xmpp:correct:sender:alice@localhost", RemoteID: string(copiedID), At: now.Add(time.Second),
	}
	if err := store.RecordMessageRetraction(ctx, chat.AccountID, chat.Kind, chat.Address, retraction); err != nil {
		t.Fatal(err)
	}
	visible, err := engine.Message(ctx, copiedID)
	if err != nil || visible.Retraction == nil {
		t.Fatalf("fixture message was not retracted: %+v %v", visible, err)
	}
	assertMasked := func(t *testing.T, message domain.Message) {
		t.Helper()
		if message.ID != copiedID || message.AccountID != chat.AccountID || message.ConversationID != chat.ID ||
			message.Retraction == nil || !message.Retraction.OriginalAvailable || !message.Retraction.At.Equal(retraction.At) ||
			message.OriginalText != "" || message.Composition != nil {
			t.Fatalf("withdrawn message escaped the retained-original path: %+v", message)
		}
	}
	t.Run("forward replay", func(t *testing.T) {
		replay, err := engine.ForwardMessage(ctx, key, source.Message.ID, source.Message.RevisionID, chat.ID)
		if err != nil || !replay.Replayed {
			t.Fatalf("forward replay: %+v %v", replay, err)
		}
		assertMasked(t, replay.Message)
	})
	t.Run("transactional send replay", func(t *testing.T) {
		row, err := store.queries.GetOperation(ctx, string(key))
		if err != nil {
			t.Fatal(err)
		}
		replay, err := store.AcceptMessage(ctx, application.MessageMutation{
			Operation:   application.Operation{ID: key, ResourceID: copiedID, Kind: "send_message", CreatedAt: now},
			Fingerprint: row.Fingerprint, Message: forwarded.Message,
		})
		if err != nil || !replay.Replayed {
			t.Fatalf("transactional send replay: %+v %v", replay, err)
		}
		assertMasked(t, replay.Message)
	})
	t.Run("cancel conflict", func(t *testing.T) {
		_, err := engine.CancelMessage(ctx, migrationID(), copiedID, visible.RevisionID)
		var conflict application.StateConflict
		if !errors.As(err, &conflict) {
			t.Fatalf("cancel conflict: %v", err)
		}
		assertMasked(t, conflict.Current)
	})
	t.Run("reschedule conflict", func(t *testing.T) {
		at := now.Add(time.Hour)
		_, err := engine.UpdatePendingMessage(ctx, migrationID(), copiedID, visible.RevisionID, nil, &at, false)
		var conflict application.StateConflict
		if !errors.As(err, &conflict) {
			t.Fatalf("reschedule conflict: %v", err)
		}
		assertMasked(t, conflict.Current)
	})
}

func TestProviderRejectionRacingDispatchCompletion(t *testing.T) {
	store, engine, chat := outboxFixture(t)
	ctx := t.Context()
	sent, err := engine.SendMessage(ctx, migrationID(), chat.ID, "rejected by its provider")
	if err != nil {
		t.Fatal(err)
	}
	messageID := sent.Message.ID
	now := time.Now().UTC()
	if _, err = store.TransitionMessage(ctx, messageID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	// Complete the write after Reject reads dispatching, before it records the error.
	receiver, err := application.New(store, "local", func() (domain.ID, error) {
		_, err := store.TransitionMessage(ctx, messageID, "dispatching", "submitted", migrationID(), now, now)
		return migrationID(), err
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err = receiver.Reject(ctx, application.Rejected{AccountID: chat.AccountID, MessageID: string(messageID), Address: chat.Address}); err != nil {
		t.Fatal(err)
	}
	current, err := engine.Message(ctx, messageID)
	if err != nil || current.SendState != "failed" {
		t.Fatalf("rejection racing dispatch left state %q: %v", current.SendState, err)
	}
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
