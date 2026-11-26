package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
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
		// A bookmark can supply a missing name when history or entry saved the address first.
		if old.Kind == "room" && (old.Title == old.Address || old.Title == "") && c.Title != "" && c.Title != c.Address {
			if err := writeRoomTitle(ctx, q, old, c.Title, c.RevisionID, c.UpdatedAt); err != nil {
				return err
			}
			return tx.Commit()
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

func verifiedRoomOccurrence(ctx context.Context, q *dbgen.Queries, c domain.Conversation, id string) (string, error) {
	owners, err := q.FindVerifiedRoomOccurrence(ctx, dbgen.FindVerifiedRoomOccurrenceParams{
		AccountID: string(c.AccountID), RoomAddress: c.Address, RemoteID: id,
	})
	// A contested SID cannot identify a physical source. The provider and
	// archive keys can still recognize their own replays without joining them.
	if err != nil || len(owners) != 1 {
		return "", err
	}
	return owners[0], nil
}

func ownRoomEdit(m domain.Message) bool {
	return m.Correction != nil && m.Correction.TargetID != ""
}

func linkRoomArchiveEcho(ctx context.Context, q *dbgen.Queries, c domain.Conversation, uid string) error {
	echo, err := q.LinkRoomArchiveEcho(ctx, dbgen.LinkRoomArchiveEchoParams{
		AccountID: string(c.AccountID), RoomAddress: c.Address, RoomUid: uid,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	if err := refreshConversationSummary(ctx, q, c.ID, at); err != nil {
		return err
	}
	_, err = change(ctx, q, "conversation.history_pruned", domain.HistoryPruned{ConversationID: c.ID, MessageIDs: []domain.ID{domain.ID(echo)}}, at)
	return err
}

func priorRoomArchiveSource(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message, uid string) (string, error) {
	id, err := q.GetLegacyRoomArchiveSource(ctx, dbgen.GetLegacyRoomArchiveSourceParams{AccountID: string(c.AccountID), Scope: c.Address, ArchiveID: uid})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	row, err := q.GetMessage(ctx, id)
	if err != nil {
		return "", err
	}
	archived, err := decodeMessage(row)
	if err != nil {
		return "", err
	}
	if archived.ConversationID != c.ID || archived.ArchiveScope != c.Address || archived.ArchiveID != uid ||
		archived.Sender.DisplayName != m.Sender.DisplayName || archived.SourceBodyHash() != m.SourceBodyHash() || ownRoomEdit(archived) {
		return "", nil
	}
	ok, err := roomSourceAcceptsIdentity(ctx, q, c, m, id, uid)
	if !ok || err != nil {
		return "", err
	}
	return id, nil
}

func roomSourceAcceptsIdentity(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message, id, uid string) (bool, error) {
	for _, ref := range m.References {
		if ref.Scope == "room:"+c.Address && ref.ID == uid {
			return q.RoomSourceAcceptsIdentity(ctx, dbgen.RoomSourceAcceptsIdentityParams{
				MessageID: id, SourceHash: m.SourceBodyHash(), WireBody: m.OriginalText, Author: ref.Author,
				RoomAddress: &c.Address, PersonalAddress: m.Sender.PersonalAddress, IsOwn: number(m.IsOwn), Uid: uid,
			})
		}
	}
	return false, nil
}

// The same room archive key can be replayed with conflicting verified facts.
// Keep both physical sources and prevent either one acquiring the other's metadata.
func disputedRoomArchiveReplay(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message, echo, uid string) (bool, error) {
	if uid == "" || m.ArchiveScope != c.Address {
		return false, nil
	}
	owner, err := q.RoomArchiveEchoOwner(ctx, echo)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || owner == "" {
		return true, err
	}
	if uid == m.ArchiveID {
		compatible, err := roomSourceAcceptsIdentity(ctx, q, c, m, owner, uid)
		if err != nil || compatible {
			return false, err
		}
	}
	if err := q.DisputeRoomArchiveEcho(ctx, echo); err != nil {
		return true, err
	}
	row, err := q.GetMessage(ctx, echo)
	if err != nil {
		return true, err
	}
	id := domain.ID(row.ConversationID)
	at := time.Now().UTC()
	if err := refreshConversationSummary(ctx, q, id, at); err != nil {
		return true, err
	}
	_, err = change(ctx, q, "conversation.history_pruned", domain.HistoryPruned{ConversationID: id, MessageIDs: []domain.ID{}}, at)
	return true, err
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
	match, err := q.MatchExcludedXMPPOccurrence(ctx, dbgen.MatchExcludedXMPPOccurrenceParams{
		AccountID: string(c.AccountID), Chat: c.Address, Kind: c.Kind, ArchiveScope: m.ArchiveScope, ArchiveID: m.ArchiveID, StanzaID: stanzaID,
	})
	if err != nil || match != "observed" {
		return stanzaID, match == "equivalent", err
	}
	distinctRoomIDs := c.Kind == "room" && m.ArchiveScope == c.Address && m.ArchiveID != "" && stanzaID != "" && m.ArchiveID != stanzaID
	for _, key := range []struct{ kind, authority, id string }{{"stanza-id", c.Address, stanzaID}, {"mam", m.ArchiveScope, m.ArchiveID}} {
		if key.id == "" || key.kind == "stanza-id" && c.Kind == "room" && m.ArchiveScope == c.Address && key.id == m.ArchiveID {
			continue
		}
		if err := q.RememberExcludedXMPPKey(ctx, dbgen.RememberExcludedXMPPKeyParams{
			AccountID: string(c.AccountID), Chat: c.Address, Kind: c.Kind, KeyType: key.kind, Authority: key.authority, RemoteID: key.id,
		}); err != nil {
			return "", false, err
		}
		if distinctRoomIDs {
			if err := q.RememberDistinctRoomExclusionKey(ctx, dbgen.RememberDistinctRoomExclusionKeyParams{
				AccountID: string(c.AccountID), Chat: c.Address, KeyType: key.kind, RemoteID: key.id,
			}); err != nil {
				return "", false, err
			}
		}
	}
	return stanzaID, true, nil
}
