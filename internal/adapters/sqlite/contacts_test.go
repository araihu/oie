package sqlite

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestContactDirectoryKeepsIdentityRevisionsHistoryAndUnavailableCache(t *testing.T) {
	s, e, c := outboxFixture(t)
	query := domain.ContactQuery{AccountID: c.AccountID, Limit: 100}
	page, err := e.Contacts(t.Context(), query)
	if err != nil || len(page.Items) != 0 || len(page.Sources) != 1 || page.Sources[0].State != "unknown" {
		t.Fatal(page, err)
	}
	beforeID, beforeSeq, _ := s.Snapshot(t.Context())
	input := []application.ContactInput{{Address: c.Address, DisplayName: "Café <img onerror=alert(1)>", Subscription: "both", Groups: []string{"Team", "Team"}}, {Address: "other@localhost", DisplayName: "Other", Subscription: "none", Groups: []string{}}}
	if err = e.ContactsReceived(t.Context(), c.AccountID, input); err != nil {
		t.Fatal(err)
	}
	page, err = e.Contacts(t.Context(), query)
	if err != nil || len(page.Items) != 2 || page.Sources[0].State != "known" || page.Sources[0].CheckedAt.IsZero() {
		t.Fatal(page, err)
	}
	first := page.Items[0]
	if first.Address != c.Address || first.ConversationID != c.ID || len(first.Groups) != 1 || first.Groups[0] != "Team" {
		t.Fatal(first)
	}
	if err = e.ContactsReceived(t.Context(), c.AccountID, input); err != nil {
		t.Fatal(err)
	}
	replay, _ := e.Contacts(t.Context(), query)
	if !reflect.DeepEqual(page.Items, replay.Items) {
		t.Fatal("unchanged observation created new contact revisions")
	}
	input[0].DisplayName = "Renamed"
	input = input[:1]
	if err = e.ContactsReceived(t.Context(), c.AccountID, input); err != nil {
		t.Fatal(err)
	}
	updated, _ := e.Contacts(t.Context(), query)
	if len(updated.Items) != 1 || updated.Items[0].ID != first.ID || updated.Items[0].RevisionID == first.RevisionID {
		t.Fatal(updated)
	}
	rows, _ := s.queries.ListContactRecords(t.Context(), string(c.AccountID))
	if len(rows) != 2 {
		t.Fatal("removed contact observation discarded identity", rows)
	}
	if err = e.ContactsUnavailable(t.Context(), c.AccountID); err != nil {
		t.Fatal(err)
	}
	unavailable, _ := e.Contacts(t.Context(), query)
	if len(unavailable.Items) != 1 || unavailable.Sources[0].State != "unavailable" || unavailable.Sources[0].CheckedAt.IsZero() {
		t.Fatal(unavailable)
	}
	if id, seq, _ := s.Snapshot(t.Context()); id != beforeID || seq != beforeSeq {
		t.Fatal("directory read/import changed messaging event history")
	}
	retained, err := e.Conversation(t.Context(), c.ID)
	if err != nil || retained.ID != c.ID || retained.Address != c.Address {
		t.Fatal("contact removal changed retained conversation", err)
	}
}

func TestContactDirectoryFiltersBeforePagingAndBindsScope(t *testing.T) {
	_, e, c := outboxFixture(t)
	items := []application.ContactInput{}
	for i := range 108 {
		name := "Unrelated"
		if i < 5 {
			name = "Cafe\u0301 durable"
		}
		items = append(items, application.ContactInput{Address: fmt.Sprintf("person%03d@localhost", i), DisplayName: name, Subscription: "none"})
	}
	if err := e.ContactsReceived(t.Context(), c.AccountID, items); err != nil {
		t.Fatal(err)
	}
	query := domain.ContactQuery{AccountID: c.AccountID, Search: " CAFÉ durable ", Limit: 2}
	page, err := e.Contacts(t.Context(), query)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	seen := map[domain.ID]bool{}
	for {
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate contact")
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		query.Cursor = page.NextCursor
		page, err = e.Contacts(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 5 {
		t.Fatal("filter applied after limit", len(seen))
	}
	query.AccountID = ""
	if _, err = e.Contacts(t.Context(), query); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("cursor escaped account scope", err)
	}
	for _, bad := range []domain.ContactQuery{{Search: "\x00", Limit: 10}, {Limit: 101}, {AccountID: "invalid", Limit: 10}, {Cursor: "invalid", Limit: 10}} {
		if _, err = e.Contacts(t.Context(), bad); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("accepted invalid query", bad, err)
		}
	}
}

func TestContactObservationRollsBackFactoryFailureAndPreservesCausalClockRollback(t *testing.T) {
	s, e, c := outboxFixture(t)
	base := time.Now().UTC()
	allocated := 0
	err := s.ObserveContacts(t.Context(), application.ContactObservation{AccountID: c.AccountID, Available: true, ObservedAt: base, NewID: func() (domain.ID, error) {
		allocated++
		if allocated == 4 {
			return "", errors.New("injected allocation failure")
		}
		return migrationID(), nil
	}, Items: []application.ContactInput{{Address: "a@localhost", DisplayName: "A", Subscription: "none", Groups: []string{}}, {Address: "b@localhost", DisplayName: "B", Subscription: "none", Groups: []string{}}}})
	if err == nil {
		t.Fatal("injected failure accepted")
	}
	page, _ := e.Contacts(t.Context(), domain.ContactQuery{Limit: 100})
	if len(page.Items) != 0 || page.Sources[0].State != "unknown" {
		t.Fatal("partial snapshot persisted", page)
	}
	if err = e.ContactsReceived(t.Context(), c.AccountID, []application.ContactInput{{Address: "a@localhost", DisplayName: "New", Subscription: "none"}}); err != nil {
		t.Fatal(err)
	}
	previous, _ := e.Contacts(t.Context(), domain.ContactQuery{Limit: 100})
	if err = s.ObserveContacts(t.Context(), application.ContactObservation{AccountID: c.AccountID, Available: true, ObservedAt: base.Add(-time.Hour), NewID: func() (domain.ID, error) { return migrationID(), nil }, Items: []application.ContactInput{{Address: "a@localhost", DisplayName: "Clock moved back", Subscription: "none", Groups: []string{}}}}); err != nil {
		t.Fatal(err)
	}
	page, _ = e.Contacts(t.Context(), domain.ContactQuery{Limit: 100})
	if len(page.Items) != 1 || page.Items[0].DisplayName != "Clock moved back" || page.Items[0].ID != previous.Items[0].ID || !page.Items[0].UpdatedAt.Before(previous.Items[0].UpdatedAt) {
		t.Fatal("wall clock overrode causal observation order", page)
	}
	var parents string
	if err = s.db.QueryRow("SELECT parents FROM contact_revisions WHERE id=?", page.Items[0].RevisionID).Scan(&parents); err != nil || parents != jsonText([]domain.ID{previous.Items[0].RevisionID}) {
		t.Fatal("clock rollback lost revision ancestry", parents, err)
	}
}
