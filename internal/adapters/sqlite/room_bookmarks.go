package sqlite

import (
	"context"

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
