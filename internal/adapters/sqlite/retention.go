package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func readGlobalRetention(ctx context.Context, q *dbgen.Queries) (domain.GlobalRetention, error) {
	value, err := q.GetGlobalRetention(ctx)
	return domain.GlobalRetention{Revision: strconv.FormatInt(value.Revision, 10), RoomDays: int(value.RoomsDays), DirectDays: int(value.DirectDays)}, err
}

func (s *Store) ArchiveRetentionRevision(ctx context.Context, account domain.ID) (int64, error) {
	return s.read.GetArchiveRetentionRevision(ctx, string(account))
}

func (s *Store) GlobalRetention(ctx context.Context) (domain.GlobalRetention, error) {
	return readGlobalRetention(ctx, s.read)
}

func readConversationRetention(ctx context.Context, q *dbgen.Queries, id string) (domain.ConversationRetention, error) {
	row, err := q.GetConversationRetention(ctx, id)
	value := domain.ConversationRetention{Revision: strconv.FormatInt(row.Revision, 10), EffectiveDays: int(row.EffectiveDays), GlobalDays: int(row.GlobalDays)}
	if row.Days != nil {
		days := int(*row.Days)
		value.Days = &days
	}
	return value, notFound(err)
}

func (s *Store) ConversationRetention(ctx context.Context, id domain.ID) (domain.ConversationRetention, error) {
	return readConversationRetention(ctx, s.read, string(id))
}

func retentionDays(ctx context.Context, q *dbgen.Queries, account domain.ID, address, kind string) (int, error) {
	days, err := q.FindEffectiveRetentionDays(ctx, dbgen.FindEffectiveRetentionDaysParams{AccountID: string(account), Address: address, Kind: kind})
	if !errors.Is(err, sql.ErrNoRows) {
		return int(days), err
	}
	global, err := readGlobalRetention(ctx, q)
	if kind == "room" {
		return global.RoomDays, err
	}
	return global.DirectDays, err
}

func (s *Store) RetentionDays(ctx context.Context, account domain.ID, address, kind string) (int, error) {
	return retentionDays(ctx, s.read, account, address, kind)
}

func recordDiscardedMessageReferences(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message) (bool, error) {
	refs := m.References
	inserted := false
	if len(refs) > 8 {
		refs = refs[:8]
	}
	if correctionValid(m.Correction) {
		refs = append(slices.Clone(refs), domain.MessageReference{Scope: m.Correction.Scope, ID: m.Correction.ID, Author: m.Correction.Author})
	}
	for _, ref := range refs {
		if !referenceValid(ref) || ref.Scope == "" {
			continue
		}
		n, err := q.RecordDiscardedMessageReference(ctx, dbgen.RecordDiscardedMessageReferenceParams{
			AccountID: string(c.AccountID), Address: c.Address, Kind: c.Kind, Scope: ref.Scope, RemoteID: ref.ID, Author: ref.Author, AgeMs: m.CreatedAt.UnixMilli(),
		})
		if err != nil {
			return false, err
		}
		inserted = inserted || n > 0
	}
	return inserted, nil
}

func discardOutdatedMessage(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message) error {
	inserted, err := recordDiscardedMessageReferences(ctx, q, c, m)
	if err != nil || !inserted {
		return err
	}
	row, err := q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(c.AccountID), Address: c.Address})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	chat, err := decodeConversation(row)
	if err != nil || chat.Kind != c.Kind {
		return err
	}
	scrubbed, err := scrubInboundQuotes(ctx, q, row.ID)
	if err != nil || !scrubbed {
		return err
	}
	// A newer client may already have the reply mounted.
	_, err = change(ctx, q, "conversation.history_pruned", domain.HistoryPruned{ConversationID: chat.ID, MessageIDs: []domain.ID{}}, m.ReceivedAt)
	return err
}

func (s *Store) DirectRetentionPeers(ctx context.Context, account domain.ID) ([]application.RetentionPeer, error) {
	rows, err := s.read.ListDirectRetentionPeers(ctx, string(account))
	if err != nil {
		return nil, err
	}
	peers := make([]application.RetentionPeer, 0, len(rows))
	for _, row := range rows {
		peers = append(peers, application.RetentionPeer{Address: row.Address, Days: int(row.Days)})
	}
	return peers, nil
}

func (s *Store) SetGlobalRetention(ctx context.Context, in domain.GlobalRetention, now time.Time) (domain.GlobalRetention, []domain.ID, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return in, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := readGlobalRetention(ctx, q)
	if err != nil || current == in {
		return current, nil, err
	}
	if current.Revision != in.Revision {
		if current.RoomDays == in.RoomDays && current.DirectDays == in.DirectDays {
			return current, nil, nil
		}
		return current, nil, application.ErrStateConflict
	}
	rows, err := q.ListRetentionAccountScopes(ctx)
	if err != nil {
		return current, nil, err
	}
	accounts := make([]domain.ID, 0, len(rows))
	for _, id := range rows {
		accounts = append(accounts, domain.ID(id))
	}
	if current.DirectDays != in.DirectDays {
		if err := q.ClearAllDirectRetentionCheckpoints(ctx); err != nil {
			return current, nil, err
		}
		if err := q.ResetAccountDiscoveryCheckpoints(ctx); err != nil {
			return current, nil, err
		}
	}
	if current.RoomDays != in.RoomDays {
		rooms, err := q.ListInheritedRoomArchiveProgress(ctx)
		if err != nil {
			return current, nil, err
		}
		for _, room := range rooms {
			if err := applyRoomRetention(ctx, q, room.ConversationID, room.Progress, current.RoomDays, in.RoomDays, now); err != nil {
				return current, nil, err
			}
		}
	}
	if err := q.BumpArchiveRetentionRevision(ctx); err != nil {
		return current, nil, err
	}
	if err := q.PutGlobalRetention(ctx, dbgen.PutGlobalRetentionParams{RoomsDays: int64(in.RoomDays), DirectDays: int64(in.DirectDays)}); err != nil {
		return current, nil, err
	}
	value, err := readGlobalRetention(ctx, q)
	if err != nil {
		return current, nil, err
	}
	return value, accounts, tx.Commit()
}

func (s *Store) SetConversationRetention(ctx context.Context, id domain.ID, expected string, days *int, now time.Time) (domain.ConversationRetention, []domain.ID, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.ConversationRetention{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := readConversationRetention(ctx, q, string(id))
	if err != nil {
		return current, nil, err
	}
	if days != nil && current.Days != nil && *current.Days == *days {
		return current, nil, nil
	}
	if current.Revision != expected {
		return current, nil, application.ErrStateConflict
	}
	if days == nil && current.Days == nil {
		return current, nil, nil
	}
	var raw *int64
	if days != nil {
		value := int64(*days)
		raw = &value
	}
	if err := q.PutConversationRetention(ctx, dbgen.PutConversationRetentionParams{ConversationID: string(id), Days: raw}); err != nil {
		return current, nil, err
	}
	value, err := readConversationRetention(ctx, q, string(id))
	if err != nil {
		return current, nil, err
	}
	var accounts []domain.ID
	if value.EffectiveDays != current.EffectiveDays {
		chat, err := q.GetConversation(ctx, string(id))
		if err != nil {
			return current, nil, err
		}
		accounts = []domain.ID{domain.ID(chat.AccountID)}
		conversation, err := decodeConversation(chat)
		if err != nil {
			return current, nil, err
		}
		if err := q.BumpAccountArchiveRetentionRevision(ctx, chat.AccountID); err != nil {
			return current, nil, err
		}
		if conversation.Kind == "room" {
			archive, err := q.GetRoomArchive(ctx, string(id))
			if err == nil {
				err = applyRoomRetention(ctx, q, string(id), archive.Progress, current.EffectiveDays, value.EffectiveDays, now)
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return current, nil, err
			}
		} else {
			if err := q.ClearPeerRetentionCheckpoints(ctx, dbgen.ClearPeerRetentionCheckpointsParams{
				AccountID: chat.AccountID, PeerAddress: chat.Address,
			}); err != nil {
				return current, nil, err
			}
		}
	}
	return value, accounts, tx.Commit()
}

func applyRoomRetention(ctx context.Context, q *dbgen.Queries, id, payload string, previous, days int, now time.Time) error {
	var progress domain.RoomArchiveProgress
	if err := json.Unmarshal([]byte(payload), &progress); err != nil {
		return err
	}
	if progress.RetentionDays == nil {
		progress.RetentionDays = &previous
	}
	// Reconcile any saved coverage that predates the current period before
	// applying the new period. The room may have been offline during expiry.
	progress.ApplyRetention(previous, now)
	progress.ApplyRetention(days, now)
	return q.PutRoomArchiveProgress(ctx, dbgen.PutRoomArchiveProgressParams{ConversationID: id, Progress: jsonText(progress)})
}

func (s *Store) PurgeExpiredHistory(ctx context.Context, now time.Time, newID application.NewID) (int, error) {
	count := 0
	for cursor := ""; ; {
		chats, err := s.read.ListFiniteRetentionConversations(ctx, cursor)
		if err != nil || len(chats) == 0 {
			return count, err
		}
		for _, chat := range chats {
			cursor = chat.ConversationID
			for {
				removed, err := s.purgeConversationBatch(ctx, cursor, chat.Kind, now, newID)
				count += removed
				if err != nil {
					return count, err
				}
				if removed == 0 {
					break
				}
			}
		}
	}
}

func (s *Store) purgeConversationBatch(ctx context.Context, id, kind string, now time.Time, newID application.NewID) (int, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	policy, err := readConversationRetention(ctx, q, id)
	if errors.Is(err, application.ErrNotFound) {
		return 0, nil
	}
	if err != nil || policy.EffectiveDays == 0 {
		return 0, err
	}
	var minimum int64
	if kind == "room" {
		minimum = domain.RoomRetentionMinimum
	}
	cutoff := domain.RetentionCutoff(now, policy.EffectiveDays).UnixMilli()
	ids, err := q.ExpiryCandidates(ctx, dbgen.ExpiryCandidatesParams{ConversationID: id, CutoffMs: cutoff, MinimumMessages: minimum, BatchLimit: 128})
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	aliases, err := q.ExpirySourceIDs(ctx, jsonText(ids))
	if err != nil {
		return 0, err
	}
	all := append(slices.Clone(ids), aliases...)
	encoded := jsonText(all)
	if err := q.DropExpiredMessageEvents(ctx, encoded); err != nil {
		return 0, err
	}
	if err := q.DeleteExpiredMessages(ctx, encoded); err != nil {
		return 0, err
	}
	if err := q.ScrubMessageOutboundQuotes(ctx, dbgen.ScrubMessageOutboundQuotesParams{ConversationID: id, ExpiredIds: encoded}); err != nil {
		return 0, err
	}
	if err := q.ScrubRevisionOutboundQuotes(ctx, dbgen.ScrubRevisionOutboundQuotesParams{OwnerID: id, ExpiredIds: encoded}); err != nil {
		return 0, err
	}
	if err := q.ScrubEventOutboundQuotes(ctx, dbgen.ScrubEventOutboundQuotesParams{OwnerID: id, ExpiredIds: encoded}); err != nil {
		return 0, err
	}
	if err := q.ScrubReceiptOutboundQuotes(ctx, dbgen.ScrubReceiptOutboundQuotesParams{OwnerID: id, ExpiredIds: encoded}); err != nil {
		return 0, err
	}
	if err := scrubRecoveredSendQuotes(ctx, q, id, encoded); err != nil {
		return 0, err
	}
	if _, err := scrubInboundQuotes(ctx, q, id); err != nil {
		return 0, err
	}
	if err := q.ScrubConversationEventPreviews(ctx, id); err != nil {
		return 0, err
	}
	if err := q.ScrubConversationRevisionPreviews(ctx, id); err != nil {
		return 0, err
	}
	if err := q.ScrubConversationReceiptPreviews(ctx, id); err != nil {
		return 0, err
	}
	if err := repairExpiredConversation(ctx, q, id, kind, now, newID); err != nil {
		return 0, err
	}
	pruned := domain.HistoryPruned{ConversationID: domain.ID(id), MessageIDs: make([]domain.ID, 0, len(all))}
	for _, id := range all {
		pruned.MessageIDs = append(pruned.MessageIDs, domain.ID(id))
	}
	if _, err := change(ctx, q, "conversation.history_pruned", pruned, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func scrubInboundQuotes(ctx context.Context, q *dbgen.Queries, id string) (bool, error) {
	quotes, err := q.ListInboundQuotesInConversation(ctx, id)
	if err != nil || len(quotes) == 0 {
		return false, err
	}
	conversation, err := q.GetConversation(ctx, id)
	if err != nil {
		return false, err
	}
	scrubbed := false
	removedURLs := make(redactedQuoteURLs)
	for _, quote := range quotes {
		var ref domain.ReplyReference
		if err := json.Unmarshal([]byte(quote.Payload), &ref); err != nil {
			return false, err
		}
		expired, err := unavailableExpiredQuote(ctx, q, domain.ID(conversation.AccountID), domain.ID(id), ref.Target)
		if err != nil {
			return false, err
		}
		if !expired {
			continue
		}
		bodies, err := q.UnredactedQuoteBodies(ctx, dbgen.UnredactedQuoteBodiesParams{MessageID: quote.MessageID, BodyHash: quote.BodyHash})
		if err != nil {
			return false, err
		}
		for _, before := range bodies {
			after, _ := domain.ReplyBody(before, ref.Fallback)
			removedURLs.add(domain.ID(quote.MessageID), domain.ID(quote.CanonicalID), before, after)
		}
		if err := q.ScrubMessageInboundQuote(ctx, dbgen.ScrubMessageInboundQuoteParams{SourceID: quote.MessageID, ReplyMetadata: quote.Payload, BodyHash: quote.BodyHash}); err != nil {
			return false, err
		}
		if err := q.ScrubRevisionInboundQuote(ctx, dbgen.ScrubRevisionInboundQuoteParams{SourceID: quote.MessageID, ReplyMetadata: quote.Payload, BodyHash: quote.BodyHash}); err != nil {
			return false, err
		}
		if err := q.ScrubEventInboundQuote(ctx, dbgen.ScrubEventInboundQuoteParams{SourceID: quote.MessageID, CanonicalID: quote.CanonicalID, ReplyMetadata: quote.Payload, BodyHash: quote.BodyHash}); err != nil {
			return false, err
		}
		if err := q.ScrubContentHeadInboundQuote(ctx, dbgen.ScrubContentHeadInboundQuoteParams{SourceID: quote.MessageID, CanonicalID: quote.CanonicalID, ReplyMetadata: quote.Payload, BodyHash: quote.BodyHash}); err != nil {
			return false, err
		}
		if err := q.ScrubInboundQuoteMetadata(ctx, dbgen.ScrubInboundQuoteMetadataParams{SourceID: quote.MessageID, BodyHash: quote.BodyHash}); err != nil {
			return false, err
		}
		scrubbed = true
	}
	return scrubbed, removedURLs.prune(ctx, q)
}

func repairExpiredConversation(ctx context.Context, q *dbgen.Queries, id, kind string, now time.Time, newID application.NewID) error {
	row, err := q.GetConversation(ctx, id)
	if err != nil {
		return err
	}
	c, err := decodeConversation(row)
	if err != nil {
		return err
	}
	if kind == "direct" && c.Request == "" {
		if err := q.MarkAcceptedDirectHistory(ctx, id); err != nil {
			return err
		}
	}
	summary, err := q.ConversationUnreadSummary(ctx, id)
	if err != nil {
		return err
	}
	c.UnreadCount, c.UnreadMention = int(summary.UnreadCount), summary.UnreadMention != 0
	latest, err := q.LatestLogicalMessage(ctx, id)
	c.LastMessage = ""
	if err == nil {
		m, err := decodeMessage(latest)
		if err != nil {
			return err
		}
		if err := hydrateMessageProjection(ctx, q, &m); err != nil {
			return err
		}
		if err := hydrateReply(ctx, q, &m); err != nil {
			return err
		}
		c.LastMessage, c.UpdatedAt = domain.DisplayBody(m), m.TimelineTime()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	revision, err := newID()
	if err != nil {
		return err
	}
	parents := jsonText([]domain.ID{c.RevisionID})
	c.RevisionID = revision
	payload := jsonText(c)
	if err := q.UpdateConversation(ctx, dbgen.UpdateConversationParams{ID: id, RevisionID: string(revision), Payload: payload, ActivitySequence: c.ActivitySequence, UnreadCount: int64(c.UnreadCount)}); err != nil {
		return err
	}
	return q.InsertConversationRevision(ctx, dbgen.InsertConversationRevisionParams{ID: string(revision), EntityID: id, Parents: parents, Payload: payload, CreatedAt: stamp(now)})
}
