package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) RoomArchive(ctx context.Context, id domain.ID) (domain.RoomArchive, error) {
	a := domain.RoomArchive{ConversationID: id, Mode: "gradual"}
	row, err := s.queries.GetRoomArchive(ctx, string(id))
	if err == nil {
		a.Mode = row.Mode
		err = json.Unmarshal([]byte(row.Progress), &a.RoomArchiveProgress)
	} else if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if a.Availability == "" {
		a.Availability = "unknown"
	}
	if a.TextSearch == "" {
		a.TextSearch = "unknown"
	}
	return a, err
}

func (s *Store) SetRoomArchiveMode(ctx context.Context, id domain.ID, mode string) error {
	return s.queries.PutRoomArchiveMode(ctx, dbgen.PutRoomArchiveModeParams{ConversationID: string(id), Mode: mode})
}

func (s *Store) StoreRoomArchiveProgress(ctx context.Context, id domain.ID, progress domain.RoomArchiveProgress) error {
	return s.queries.PutRoomArchiveProgress(ctx, dbgen.PutRoomArchiveProgressParams{ConversationID: string(id), Progress: jsonText(progress)})
}

func (s *Store) CountRoomArchiveMessages(ctx context.Context, id domain.ID, start, end time.Time) (int, error) {
	n, err := s.queries.CountRoomArchiveMessages(ctx, dbgen.CountRoomArchiveMessagesParams{ConversationID: string(id), FromMs: start.UnixMilli(), ToMs: end.UnixMilli()})
	return int(n), err
}

func (s *Store) ListRoomArchiveConversations(ctx context.Context, account domain.ID) ([]domain.Conversation, error) {
	rows, err := s.queries.ListRoomArchiveConversations(ctx, string(account))
	if err != nil {
		return nil, err
	}
	conversations := make([]domain.Conversation, 0, len(rows))
	for _, row := range rows {
		c, err := decodeConversation(row)
		if err != nil {
			return nil, err
		}
		conversations = append(conversations, c)
	}
	return conversations, nil
}

func (s *Store) ArchivedRoomMessage(ctx context.Context, c domain.Conversation, archiveID string) (domain.Message, error) {
	id, err := s.queries.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{AccountID: string(c.AccountID), Scope: c.Address, ArchiveID: archiveID})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Message{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Message{}, err
	}
	m, err := s.Message(ctx, domain.ID(id))
	if err == nil && (m.AccountID != c.AccountID || m.ConversationID != c.ID) {
		return domain.Message{}, application.ErrNotFound
	}
	return m, err
}
