package sqlite

import (
	"context"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

// RestoredRoom reuses the generated, account-scoped address lookup.
func (s *Store) RestoredRoom(ctx context.Context, account domain.ID, address string) (domain.Conversation, error) {
	row, err := s.queries.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(account), Address: address})
	if err != nil {
		return domain.Conversation{}, notFound(err)
	}
	return decodeConversation(row)
}

func (s *Store) UpdateRoomTitle(ctx context.Context, id domain.ID, title string, revision domain.ID, at time.Time) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	current, err := readConversation(ctx, q, id)
	if err != nil || current.Kind != "room" || current.Title == title {
		return err
	}
	if err = writeRoomTitle(ctx, q, current, title, revision, at); err != nil {
		return err
	}
	return tx.Commit()
}

func writeRoomTitle(ctx context.Context, q *dbgen.Queries, current domain.Conversation, title string, revision domain.ID, at time.Time) error {
	next := current
	next.Title = title
	return writeConversationDecision(ctx, q, current, next, revision, at)
}
