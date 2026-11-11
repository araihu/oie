package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func mentionWithStoredReply(ctx context.Context, q *dbgen.Queries, source domain.ID, m domain.Message) (bool, error) {
	payload, err := q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: string(source), BodyHash: bodyHash(m.OriginalText)})
	var ref domain.ReplyReference
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	default:
		if err = json.Unmarshal([]byte(payload), &ref); err != nil {
			return false, err
		}
	}
	return domain.RoomNicknameMention(m.ObservedRoomNickname, m.OriginalText, ref.Fallback), nil
}

// A later reply fact can move the only occurrence into a quote. Update
// derived metadata without creating a new content revision or read receipt.
func refreshMentionAfterReply(ctx context.Context, q *dbgen.Queries, source *domain.Message, fallback []domain.TextRange) error {
	if source.IsOwn || len(fallback) == 0 {
		return nil
	}
	changed := false
	if source.ObservedRoomNickname != "" {
		mentioned := domain.RoomNicknameMention(source.ObservedRoomNickname, source.OriginalText, fallback)
		if mentioned != source.MentionsYou {
			source.MentionsYou = mentioned
			if err := q.UpdateMessage(ctx, dbgen.UpdateMessageParams{ID: string(source.ID), RevisionID: string(source.RevisionID), State: source.SendState, Payload: jsonText(source)}); err != nil {
				return err
			}
			changed = true
		}
	}
	canonical, err := resolveMessageAlias(ctx, q, source.ID)
	if err != nil {
		return err
	}
	head, err := q.GetContentHead(ctx, string(canonical))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && head.SourceID == string(source.ID) {
		var m domain.Message
		if err := json.Unmarshal([]byte(head.Payload), &m); err != nil {
			return err
		}
		mentioned := domain.RoomNicknameMention(m.ObservedRoomNickname, m.OriginalText, fallback)
		if m.ObservedRoomNickname != "" && mentioned != m.MentionsYou {
			m.MentionsYou = mentioned
			if err := q.PutContentHead(ctx, dbgen.PutContentHeadParams{CanonicalID: head.CanonicalID, SourceID: head.SourceID, RevisionID: head.RevisionID, Payload: jsonText(m), EditedAt: head.EditedAt}); err != nil {
				return err
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	row, err := q.GetConversation(ctx, string(source.ConversationID))
	if err != nil {
		return err
	}
	c, err := decodeConversation(row)
	if err != nil {
		return err
	}
	summary, err := q.ConversationUnreadSummary(ctx, string(c.ID))
	if err != nil {
		return err
	}
	mentioned := summary.UnreadMention != 0
	if mentioned == c.UnreadMention {
		return nil
	}
	c.UnreadMention = mentioned
	if err := q.UpdateConversation(ctx, dbgen.UpdateConversationParams{ID: string(c.ID), RevisionID: string(c.RevisionID), Payload: jsonText(c), ActivitySequence: c.ActivitySequence, UnreadCount: int64(c.UnreadCount)}); err != nil {
		return err
	}
	_, err = change(ctx, q, "conversation.updated", c, time.Now().UTC())
	return err
}
