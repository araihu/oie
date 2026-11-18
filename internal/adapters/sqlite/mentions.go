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
	if m.WireBodySHA256 != "" {
		return domain.RoomNicknameMention(m.ObservedRoomNickname, m.OriginalText, nil), nil
	}
	payload, err := q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: string(source), BodyHash: m.SourceBodyHash()})
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
func refreshMentionAfterReply(ctx context.Context, q *dbgen.Queries, source *domain.Message) error {
	changed, err := refreshMessageMention(ctx, q, source)
	if err != nil || !changed {
		return err
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

// Match fallback ranges to each stored body, including the active correction.
func refreshMessageMention(ctx context.Context, q *dbgen.Queries, source *domain.Message) (bool, error) {
	if source.IsOwn {
		return false, nil
	}
	changed := false
	if source.ObservedRoomNickname != "" {
		mentioned, err := mentionWithStoredReply(ctx, q, source.ID, *source)
		if err != nil {
			return false, err
		}
		if mentioned != source.MentionsYou {
			source.MentionsYou = mentioned
			if err := q.UpdateMessage(ctx, dbgen.UpdateMessageParams{ID: string(source.ID), RevisionID: string(source.RevisionID), State: source.SendState, Payload: jsonText(source)}); err != nil {
				return false, err
			}
			changed = true
		}
	}
	canonical, err := resolveMessageAlias(ctx, q, source.ID)
	if err != nil {
		return false, err
	}
	head, err := q.GetContentHead(ctx, string(canonical))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && head.SourceID == string(source.ID) {
		var m domain.Message
		if err := json.Unmarshal([]byte(head.Payload), &m); err != nil {
			return false, err
		}
		if m.ObservedRoomNickname == "" {
			return changed, nil
		}
		mentioned, err := mentionWithStoredReply(ctx, q, source.ID, m)
		if err != nil {
			return false, err
		}
		if mentioned != m.MentionsYou {
			m.MentionsYou = mentioned
			if err := q.PutContentHead(ctx, dbgen.PutContentHeadParams{CanonicalID: head.CanonicalID, SourceID: head.SourceID, RevisionID: head.RevisionID, Payload: jsonText(m), EditedAt: head.EditedAt}); err != nil {
				return false, err
			}
			changed = true
		}
	}
	return changed, nil
}
