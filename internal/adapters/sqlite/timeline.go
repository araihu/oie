package sqlite

import (
	"context"
	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) MessagesAfter(ctx context.Context, conversation, cursor domain.ID, limit int) ([]domain.Message, error) {
	if limit < 1 || limit > 101 || cursor == "" {
		return nil, application.ErrInvalid
	}
	tx, q, err := begin(ctx, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	canonical, err := resolveMessageAlias(ctx, q, cursor)
	if err != nil {
		return nil, err
	}
	anchor, err := q.GetMessage(ctx, string(canonical))
	if err != nil {
		return nil, notFound(err)
	}
	if anchor.ConversationID != string(conversation) {
		return nil, application.ErrNotFound
	}
	rows, err := q.ListMessagesAfter(ctx, dbgen.ListMessagesAfterParams{ConversationID: string(conversation), CursorID: string(canonical), PageLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	items, err := decodeMessages(rows)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := hydrateMessageProjection(ctx, q, &items[i]); err != nil {
			return nil, err
		}
		if err := hydrateReply(ctx, q, &items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}
