package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestCommunicationPreferencesRejectStaleNoOpAndDeletedAccount(t *testing.T) {
	s, e, c := outboxFixture(t)
	ctx := t.Context()
	initial, err := e.AccountCommunicationPreferences(ctx, c.AccountID)
	if err != nil || initial.Presence != "available" || !initial.SendTyping || initial.RevisionID != "" {
		t.Fatal(initial, err)
	}
	saved, err := e.SetAccountCommunicationPreferences(ctx, c.AccountID, initial.RevisionID, "dnd", new(false))
	if err != nil || saved.RevisionID == "" || saved.SendTyping {
		t.Fatal(saved, err)
	}
	if _, err := e.SetAccountCommunicationPreferences(ctx, c.AccountID, initial.RevisionID, "dnd", nil); !errors.Is(err, application.ErrStateConflict) {
		t.Fatalf("stale no-op: %v", err)
	}
	if got, err := e.SetAccountCommunicationPreferences(ctx, c.AccountID, saved.RevisionID, "dnd", nil); err != nil || got != saved {
		t.Fatal(got, err)
	}
	if removed, err := s.RemoveAccount(ctx, c.AccountID, time.Now()); err != nil || !removed {
		t.Fatal(removed, err)
	}
	if _, err := s.AccountCommunicationPreferences(ctx, c.AccountID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("deleted account read: %v", err)
	}
	if _, err := s.SaveAccountCommunicationPreferences(ctx, c.AccountID, "", domain.AccountCommunicationPreferences{Presence: "available"}, nil); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("deleted account save: %v", err)
	}
}
