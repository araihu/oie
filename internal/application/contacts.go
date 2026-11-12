package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

type ContactInput struct {
	Address, DisplayName, Subscription string
	Groups                             []string
}

// ContactHost accepts complete provider observations. Reading a directory never
// calls this port or asks the provider to change a roster or subscription.
type ContactHost interface {
	ContactsReceived(context.Context, domain.ID, []ContactInput) error
	ContactsUnavailable(context.Context, domain.ID) error
}

type ContactObservation struct {
	AccountID  domain.ID
	Items      []ContactInput
	Available  bool
	ObservedAt time.Time
	NewID      NewID
}

type ContactRepository interface {
	ObserveContacts(context.Context, ContactObservation) error
	Contacts(context.Context, domain.ContactQuery) (domain.ContactPage, error)
}

func (e *Engine) ContactsReceived(ctx context.Context, accountID domain.ID, items []ContactInput) error {
	if len(items) > 10000 {
		return ErrInvalid
	}
	if _, err := e.Account(ctx, accountID); err != nil {
		return err
	}
	normalized := make([]ContactInput, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if !validText(item.Address, 1023) || seen[item.Address] || !utf8.ValidString(item.DisplayName) || utf8.RuneCountInString(item.DisplayName) > 256 || strings.ContainsRune(item.DisplayName, 0) || len(item.Groups) > 64 {
			return ErrInvalid
		}
		seen[item.Address] = true
		switch item.Subscription {
		case "none", "to", "from", "both":
		default:
			return ErrInvalid
		}
		item.DisplayName = strings.TrimSpace(item.DisplayName)
		if item.DisplayName == "" {
			item.DisplayName = item.Address
		}
		item.Groups = slices.Clone(item.Groups)
		for _, group := range item.Groups {
			if !validText(group, 256) {
				return ErrInvalid
			}
		}
		slices.Sort(item.Groups)
		item.Groups = slices.Compact(item.Groups)
		if item.Groups == nil {
			item.Groups = []string{}
		}
		normalized = append(normalized, item)
	}
	slices.SortFunc(normalized, func(a, b ContactInput) int { return strings.Compare(a.Address, b.Address) })
	r, ok := e.repo.(ContactRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	return r.ObserveContacts(ctx, ContactObservation{AccountID: accountID, Items: normalized, Available: true, ObservedAt: e.now().UTC(), NewID: e.newID})
}

func (e *Engine) ContactsUnavailable(ctx context.Context, accountID domain.ID) error {
	if _, err := e.Account(ctx, accountID); err != nil {
		return err
	}
	r, ok := e.repo.(ContactRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	return r.ObserveContacts(ctx, ContactObservation{AccountID: accountID, ObservedAt: e.now().UTC()})
}

type ContactCursor struct {
	Scope string    `json:"scope"`
	ID    domain.ID `json:"id"`
}

func EncodeContactCursor(c ContactCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func ParseContactCursor(value, scope string) (ContactCursor, error) {
	var c ContactCursor
	if value == "" {
		return c, nil
	}
	if len(value) > 512 {
		return c, ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return c, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Scope != scope || !validID(c.ID) {
		return c, ErrInvalid
	}
	return c, nil
}
func (e *Engine) Contacts(ctx context.Context, q domain.ContactQuery) (domain.ContactPage, error) {
	empty := domain.ContactPage{Items: []domain.Contact{}, Sources: []domain.ContactSource{}}
	q.Search = strings.TrimSpace(q.Search)
	if !utf8.ValidString(q.Search) || utf8.RuneCountInString(q.Search) > 200 || strings.ContainsRune(q.Search, 0) || q.Limit < 1 || q.Limit > 100 {
		return empty, ErrInvalid
	}
	accountIDs, err := e.validateSearchAccounts(ctx, q.AccountID, q.AccountIDs)
	if err != nil {
		return empty, err
	}
	q.AccountIDs = accountIDs
	snapshot, err := e.Snapshot(ctx)
	if err != nil {
		return empty, err
	}
	q.ScopeHash = fingerprint("contacts", struct {
		EngineID, AccountID domain.ID
		Query               string
		AccountIDs          []domain.ID `json:",omitempty"`
	}{snapshot.ID, q.AccountID, q.Search, q.AccountIDs})
	if _, err = ParseContactCursor(q.Cursor, q.ScopeHash); err != nil {
		return empty, err
	}
	r, ok := e.repo.(ContactRepository)
	if !ok {
		return empty, ErrProtocolUnavailable
	}
	return r.Contacts(ctx, q)
}
