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

type MessageSearchRepository interface {
	SearchMessages(context.Context, domain.MessageSearchQuery) (domain.MessageSearchPage, error)
}

// MessageSearchCursor is a position within a query scope, not permission to read it.
// Current hits have an invariant rank/key independent of transport-state revisions.
type MessageSearchCursor struct {
	Scope        string    `json:"scope"`
	Milliseconds int64     `json:"ms"`
	ID           domain.ID `json:"id"`
	Current      int64     `json:"current"`
	RevisionAt   string    `json:"at"`
	RevisionID   domain.ID `json:"revision"`
}

func EncodeMessageSearchCursor(c MessageSearchCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func ParseMessageSearchCursor(value, scope string) (MessageSearchCursor, error) {
	var c MessageSearchCursor
	if value == "" {
		return c, nil
	}
	if len(value) > 2048 {
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
	switch c.Current {
	case 1:
		if c.RevisionAt != "" || c.RevisionID != "" {
			return c, ErrInvalid
		}
	case 0:
		at, err := time.Parse(time.RFC3339Nano, c.RevisionAt)
		if err != nil || at.Location() != time.UTC || !validID(c.RevisionID) {
			return c, ErrInvalid
		}
	default:
		return c, ErrInvalid
	}
	return c, nil
}
func (e *Engine) SearchMessages(ctx context.Context, q domain.MessageSearchQuery) (domain.MessageSearchPage, error) {
	empty := domain.MessageSearchPage{Items: []domain.MessageSearchHit{}}
	q.Search = strings.TrimSpace(q.Search)
	if q.Search == "" || !utf8.ValidString(q.Search) || strings.ContainsRune(q.Search, 0) || utf8.RuneCountInString(q.Search) > 200 || q.Limit < 1 || q.Limit > 100 {
		return empty, ErrInvalid
	}
	accountIDs, err := e.validateSearchAccounts(ctx, q.AccountID, q.AccountIDs)
	if err != nil {
		return empty, err
	}
	q.AccountIDs = accountIDs
	if q.ConversationID != "" {
		if !validID(q.ConversationID) {
			return empty, ErrInvalid
		}
		c, err := e.Conversation(ctx, q.ConversationID)
		if err != nil {
			return empty, err
		}
		if q.AccountID != "" && c.AccountID != q.AccountID || len(q.AccountIDs) > 0 && !slices.Contains(q.AccountIDs, c.AccountID) {
			return empty, ErrInvalid
		}
	}
	snapshot, err := e.Snapshot(ctx)
	if err != nil {
		return empty, err
	}
	q.ScopeHash = fingerprint("message_search", struct {
		EngineID                  domain.ID
		Query                     string
		AccountID, ConversationID domain.ID
		History                   bool
		AccountIDs                []domain.ID `json:",omitempty"`
	}{snapshot.ID, q.Search, q.AccountID, q.ConversationID, q.IncludeEditHistory, q.AccountIDs})
	if _, err = ParseMessageSearchCursor(q.Cursor, q.ScopeHash); err != nil {
		return empty, err
	}
	r, ok := e.repo.(MessageSearchRepository)
	if !ok {
		return empty, ErrProtocolUnavailable
	}
	return r.SearchMessages(ctx, q)
}

func normalizeSearchAccountIDs(account domain.ID, selected []domain.ID) ([]domain.ID, error) {
	if account != "" && (!validID(account) || len(selected) > 0) || len(selected) > 100 {
		return nil, ErrInvalid
	}
	ids := slices.Clone(selected)
	slices.Sort(ids)
	for i, id := range ids {
		if !validID(id) || i > 0 && id == ids[i-1] {
			return nil, ErrInvalid
		}
	}
	return ids, nil
}

func (e *Engine) validateSearchAccounts(ctx context.Context, account domain.ID, selected []domain.ID) ([]domain.ID, error) {
	ids, err := normalizeSearchAccountIDs(account, selected)
	if err != nil {
		return nil, err
	}
	owners := ids
	if account != "" {
		owners = []domain.ID{account}
	}
	for _, owner := range owners {
		if _, err := e.Account(ctx, owner); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
