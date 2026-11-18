package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"strings"
)

func (s *Store) History(ctx context.Context, id domain.ID) (domain.HistoryStatus, error) {
	h := domain.HistoryStatus{AccountID: id, State: "not_started"}
	payload, err := s.queries.GetHistoryStatus(ctx, string(id))
	if err == nil {
		err = json.Unmarshal([]byte(payload), &h)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return h, err
	}
	counts, err := s.queries.GetHistoryCounts(ctx, string(id))
	h.RetainedMessages = int(counts.RetainedMessages)
	h.Conversations = int(counts.Conversations)
	return h, err
}
func (s *Store) RecordHistory(ctx context.Context, h domain.HistoryStatus) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	counts, err := q.GetHistoryCounts(ctx, string(h.AccountID))
	if err != nil {
		return err
	}
	h.RetainedMessages = int(counts.RetainedMessages)
	h.Conversations = int(counts.Conversations)
	if err = q.PutHistoryStatus(ctx, dbgen.PutHistoryStatusParams{AccountID: string(h.AccountID), Payload: jsonText(h)}); err != nil {
		return err
	}
	if _, err = change(ctx, q, "account.history_changed", h, h.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) HistoryCheckpoint(ctx context.Context, id domain.ID, scope string) (string, error) {
	v, err := s.queries.GetArchiveCheckpoint(ctx, dbgen.GetArchiveCheckpointParams{AccountID: string(id), Scope: scope})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (s *Store) CommitHistoryCheckpoint(ctx context.Context, id domain.ID, scope, after string) error {
	if revision, guarded := application.ArchiveRetentionRevisionFrom(ctx); guarded {
		n, err := s.queries.PutGuardedArchiveCheckpoint(ctx, dbgen.PutGuardedArchiveCheckpointParams{
			AccountID: string(id), Scope: scope, AfterID: after, RetentionRevision: revision,
		})
		if err == nil && n == 0 {
			return application.ErrStateConflict
		}
		return err
	}
	return s.queries.PutArchiveCheckpoint(ctx, dbgen.PutArchiveCheckpointParams{AccountID: string(id), Scope: scope, AfterID: after})
}
func (s *Store) RestoreConversation(ctx context.Context, c domain.Conversation) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	existing, err := q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(c.AccountID), Address: c.Address})
	if err == nil {
		old, err := decodeConversation(existing)
		if err != nil {
			return err
		}
		if old.Kind != c.Kind {
			return application.ErrStateConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = insertConversation(ctx, q, c); err != nil {
		return err
	}
	return tx.Commit()
}
func archiveAlias(ctx context.Context, q *dbgen.Queries, m domain.Message, id string) error {
	if m.ArchiveID == "" {
		return nil
	}
	return q.InsertArchiveAlias(ctx, dbgen.InsertArchiveAliasParams{AccountID: string(m.AccountID), Scope: m.ArchiveScope, ArchiveID: m.ArchiveID, MessageID: id})
}

// Check portable exclusions before materializing a conversation or message.
// A verified replay can supply a second key for already removed content.
func checkXMPPOccurrence(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message) (stanzaID string, excluded bool, err error) {
	if c.Kind == "room" {
		for _, ref := range m.References {
			if ref.Scope == "room:"+c.Address && strings.HasPrefix(ref.Author, c.Address+"/") && referenceValid(ref) {
				if stanzaID != "" && ref.ID != stanzaID {
					return "", false, application.ErrInvalid
				}
				stanzaID = ref.ID
			}
		}
	}
	if stanzaID == "" && m.ArchiveID == "" {
		return "", false, nil
	}
	excluded, err = q.IsExcludedXMPPOccurrence(ctx, dbgen.IsExcludedXMPPOccurrenceParams{
		AccountID: string(c.AccountID), Chat: c.Address, Kind: c.Kind, ArchiveScope: m.ArchiveScope, ArchiveID: m.ArchiveID, StanzaID: stanzaID,
	})
	if err != nil || !excluded {
		return stanzaID, excluded, err
	}
	for _, key := range []struct{ kind, authority, id string }{{"stanza-id", c.Address, stanzaID}, {"mam", m.ArchiveScope, m.ArchiveID}} {
		if key.id == "" {
			continue
		}
		if err := q.RememberExcludedXMPPKey(ctx, dbgen.RememberExcludedXMPPKeyParams{
			AccountID: string(c.AccountID), Chat: c.Address, Kind: c.Kind, KeyType: key.kind, Authority: key.authority, RemoteID: key.id,
		}); err != nil {
			return "", false, err
		}
	}
	return stanzaID, true, nil
}
