package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/araihu/oie/internal/domain"
)

var ErrHistoryChanged = errors.New("message content history changed")

type MessageEditHistoryRepository interface {
	// ReadMessageEditHistory resolves aliases and returns at most 128 verified
	// content sources in one read transaction, without provider requests.
	ReadMessageEditHistory(context.Context, domain.ID) (domain.MessageEditHistoryPage, error)
}

type editHistoryCursor struct {
	Scope      string    `json:"scope"`
	Snapshot   string    `json:"snapshot"`
	At         string    `json:"at"`
	RevisionID domain.ID `json:"revision"`
}

func parseEditHistoryCursor(value string) (editHistoryCursor, error) {
	var out editHistoryCursor
	if value == "" {
		return out, nil
	}
	if len(value) > 2048 {
		return out, ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return out, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || !validID(out.RevisionID) || !historyDigest(out.Scope) || !historyDigest(out.Snapshot) {
		return out, ErrInvalid
	}
	at, err := time.Parse(time.RFC3339Nano, out.At)
	if err != nil || at.Location() != time.UTC || at.Format(time.RFC3339Nano) != out.At {
		return out, ErrInvalid
	}
	return out, nil
}
func historyDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	if err != nil {
		return false
	}
	for _, r := range s {
		if r >= 'A' && r <= 'F' {
			return false
		}
	}
	return true
}

func (e *Engine) MessageEditHistory(ctx context.Context, id domain.ID, cursor string, limit int) (domain.MessageEditHistoryPage, error) {
	empty := domain.MessageEditHistoryPage{Items: []domain.MessageEditHistoryItem{}}
	if !validID(id) || limit < 1 || limit > 50 {
		return empty, ErrInvalid
	}
	position, err := parseEditHistoryCursor(cursor)
	if err != nil {
		return empty, err
	}
	repo, ok := e.repo.(MessageEditHistoryRepository)
	if !ok {
		return empty, ErrProtocolUnavailable
	}
	page, err := repo.ReadMessageEditHistory(ctx, id)
	if err != nil {
		return empty, err
	}
	engine, err := e.Snapshot(ctx)
	if err != nil {
		return empty, err
	}
	scope := fingerprint("message_edit_history_scope", []domain.ID{engine.ID, page.AccountID, page.ConversationID, page.MessageID})
	slices.SortFunc(page.Items, func(a, b domain.MessageEditHistoryItem) int {
		if at := b.CreatedAt.Compare(a.CreatedAt); at != 0 {
			return at
		}
		if a.RevisionID > b.RevisionID {
			return -1
		}
		if a.RevisionID < b.RevisionID {
			return 1
		}
		return 0
	})
	type contentProof struct {
		RevisionID, SourceMessageID domain.ID
		At                          time.Time
		BodyHash                    string
		Sender                      domain.Sender
		Current, Original           bool
		Parents                     []domain.ID
	}
	proofs := make([]contentProof, 0, len(page.Items))
	for _, item := range page.Items {
		sum := sha256.Sum256([]byte(item.OriginalText))
		proofs = append(proofs, contentProof{item.RevisionID, item.SourceMessageID, item.CreatedAt, hex.EncodeToString(sum[:]), item.Sender, item.IsCurrent, item.IsOriginal, item.ParentRevisionIDs})
	}
	page.Snapshot = fingerprint("message_edit_history_snapshot", struct {
		Scope   string
		Current domain.ID
		Proofs  []contentProof
	}{scope, page.CurrentRevisionID, proofs})
	if cursor != "" {
		if position.Scope != scope {
			return empty, ErrInvalid
		}
		if position.Snapshot != page.Snapshot {
			return empty, ErrHistoryChanged
		}
		anchor := -1
		for i, item := range page.Items {
			if item.RevisionID == position.RevisionID && item.CreatedAt.Format(time.RFC3339Nano) == position.At {
				anchor = i
				break
			}
		}
		if anchor < 0 {
			return empty, ErrInvalid
		}
		page.Items = page.Items[anchor+1:]
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		data, _ := json.Marshal(editHistoryCursor{Scope: scope, Snapshot: page.Snapshot, At: last.CreatedAt.Format(time.RFC3339Nano), RevisionID: last.RevisionID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}
