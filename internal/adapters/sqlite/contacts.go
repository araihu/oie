package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) ObserveContacts(ctx context.Context, in application.ContactObservation) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	source, err := q.GetContactSource(ctx, string(in.AccountID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// The adapter proves current session ownership and serializes observations.
	// Wall-clock timestamps describe observations; clock rollback must not make
	// a causally later provider snapshot lose to its predecessor.
	state := "unavailable"
	checked := source.CheckedAt
	if in.Available {
		if in.NewID == nil {
			return application.ErrInvalid
		}
		state = "known"
		checked = stamp(in.ObservedAt)
		rows, readErr := q.ListContactRecords(ctx, string(in.AccountID))
		if readErr != nil {
			return readErr
		}
		existing := map[string]domain.Contact{}
		for _, row := range rows {
			var c domain.Contact
			if err = json.Unmarshal([]byte(row.Payload), &c); err != nil {
				return err
			}
			existing[c.Address] = c
		}
		persist := func(c domain.Contact, parent domain.ID) error {
			c.RevisionID, err = in.NewID()
			if err != nil {
				return err
			}
			if _, err = domain.ParseID(string(c.RevisionID)); err != nil {
				return err
			}
			c.UpdatedAt = in.ObservedAt
			active := int64(0)
			if c.Active {
				active = 1
			}
			if err = q.PutContact(ctx, dbgen.PutContactParams{ID: string(c.ID), RevisionID: string(c.RevisionID), AccountID: string(c.AccountID), Address: c.Address, DisplayName: c.DisplayName, Subscription: c.Subscription, GroupsJson: jsonText(c.Groups), Active: active, Payload: jsonText(c)}); err != nil {
				return err
			}
			parents := []domain.ID{}
			if parent != "" {
				parents = append(parents, parent)
			}
			return q.InsertContactRevision(ctx, dbgen.InsertContactRevisionParams{ID: string(c.RevisionID), EntityID: string(c.ID), Parents: jsonText(parents), Payload: jsonText(c), CreatedAt: stamp(in.ObservedAt)})
		}
		for _, item := range in.Items {
			old, found := existing[item.Address]
			delete(existing, item.Address)
			if found && old.Active && old.DisplayName == item.DisplayName && old.Subscription == item.Subscription && slices.Equal(old.Groups, item.Groups) {
				continue
			}
			c := domain.Contact{ID: old.ID, AccountID: in.AccountID, Address: item.Address, DisplayName: item.DisplayName, Subscription: item.Subscription, Groups: item.Groups, Active: true}
			if !found {
				c.ID, err = in.NewID()
				if err != nil {
					return err
				}
				if _, err = domain.ParseID(string(c.ID)); err != nil {
					return err
				}
			}
			if err = persist(c, old.RevisionID); err != nil {
				return err
			}
		}
		for _, old := range existing {
			if old.Active {
				parent := old.RevisionID
				old.Active = false
				if err = persist(old, parent); err != nil {
					return err
				}
			}
		}
	}
	if err = q.PutContactSource(ctx, dbgen.PutContactSourceParams{AccountID: string(in.AccountID), State: state, CheckedAt: checked, UpdatedAt: stamp(in.ObservedAt)}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Contacts(ctx context.Context, in domain.ContactQuery) (domain.ContactPage, error) {
	page := domain.ContactPage{Items: []domain.Contact{}, Sources: []domain.ContactSource{}}
	cursor, err := application.ParseContactCursor(in.Cursor, in.ScopeHash)
	if err != nil {
		return page, err
	}
	tx, q, err := begin(ctx, s)
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := q.SearchContacts(ctx, dbgen.SearchContactsParams{AccountID: string(in.AccountID), CursorID: string(cursor.ID), Search: in.Search, PageLimit: int64(in.Limit + 1)})
	if err != nil {
		return page, err
	}
	more := len(rows) > in.Limit
	if more {
		rows = rows[:in.Limit]
	}
	for _, row := range rows {
		var c domain.Contact
		if err = json.Unmarshal([]byte(row.Payload), &c); err != nil {
			return page, err
		}
		c.AccountLabel = row.AccountLabel
		c.ConversationID = domain.ID(row.ConversationID)
		page.Items = append(page.Items, c)
	}
	if more {
		page.NextCursor = application.EncodeContactCursor(application.ContactCursor{Scope: in.ScopeHash, ID: page.Items[len(page.Items)-1].ID})
	}
	sources, err := q.ListContactSources(ctx, string(in.AccountID))
	if err != nil {
		return page, err
	}
	for _, row := range sources {
		source := domain.ContactSource{AccountID: domain.ID(row.AccountID), State: row.State}
		if row.CheckedAt != "" {
			source.CheckedAt, err = time.Parse(time.RFC3339Nano, row.CheckedAt)
			if err != nil {
				return page, err
			}
		}
		page.Sources = append(page.Sources, source)
	}
	return page, tx.Commit()
}
