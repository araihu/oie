package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) RetainedOMEMO(ctx context.Context, account domain.ID, address, providerID string, own bool) (bool, error) {
	chat, err := s.read.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(account), Address: address})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	conversation, err := decodeConversation(chat)
	if err != nil || conversation.Kind != "direct" {
		return false, err
	}
	row, err := s.read.FindProviderMessage(ctx, dbgen.FindProviderMessageParams{
		AccountID: string(account), ConversationID: chat.ID, ProviderID: providerID, IsOwn: number(own),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	message, err := decodeMessage(row)
	return message.DirectEncryption == "omemo", err
}
