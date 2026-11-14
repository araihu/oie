package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) RecordRoomModeration(ctx context.Context, account domain.ID, room string, in application.IncomingRoomModeration) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := q.InsertRoomModeration(ctx, dbgen.InsertRoomModerationParams{
		AccountID: string(account), RoomAddress: room, StanzaID: in.StanzaID,
		ModeratedAt: stamp(in.At), Reason: in.Reason,
	})
	if err != nil || n == 0 {
		return err
	}
	sources, err := q.ModeratedRoomMessageSources(ctx, dbgen.ModeratedRoomMessageSourcesParams{
		AccountID: string(account), RoomAddress: room, StanzaID: in.StanzaID,
	})
	if err != nil {
		return err
	}
	seen := map[domain.ID]bool{}
	for _, source := range sources {
		id, err := resolveMessageAlias(ctx, q, domain.ID(source))
		if err != nil {
			return err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		row, err := q.GetMessage(ctx, string(id))
		if err != nil {
			return err
		}
		message, err := decodeMessage(row)
		if err != nil {
			return err
		}
		if err := q.RemoveModeratedSharedContent(ctx, row.ID); err != nil {
			return err
		}
		if err := q.InvalidateSharedContent(ctx, row.ID); err != nil {
			return err
		}
		if _, err := emitCanonicalMessageEvent(ctx, q, "message.updated", message, in.At); err != nil {
			return err
		}
		if err := refreshConversationSummary(ctx, q, message.ConversationID, in.At); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func hydrateRoomModeration(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	if m.Moderation != nil {
		return nil
	}
	row, err := q.GetRoomMessageModeration(ctx, string(m.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, row.ModeratedAt)
	if err != nil {
		return err
	}
	m.Moderation = &domain.MessageModeration{At: at, Reason: row.Reason}
	m.OriginalText, m.AttachmentWireText = "", ""
	m.MediaThumbnails, m.Attachments, m.Mentions = nil, nil, nil
	m.Composition, m.OutboundReply, m.Reply, m.ReplyReference = nil, nil, nil, nil
	m.Edited, m.MentionsYou = false, false
	return nil
}
