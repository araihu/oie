package sqlite

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"golang.org/x/text/cases"
	sqlitedriver "modernc.org/sqlite"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_casefold", 1, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		s, _ := args[0].(string)
		return cases.Fold().String(s), nil
	})
}
func (s *Store) SharedContentCandidates(ctx context.Context, version, limit int) ([]domain.Message, error) {
	rows, err := s.queries.SharedContentCandidates(ctx, dbgen.SharedContentCandidatesParams{ParserVersion: int64(version), BatchLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	items, err := decodeMessages(rows)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := hydrateMessageProjection(ctx, s.queries, &items[i]); err != nil {
			return nil, err
		}
	}
	return s.hydrateReplies(ctx, items)
}
func (s *Store) IndexSharedContent(ctx context.Context, batch []domain.SharedContentMutation) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, in := range batch {
		row, err := q.GetMessage(ctx, string(in.Message.ID))
		if err != nil {
			return notFound(err)
		}
		current, err := decodeMessage(row)
		if err != nil {
			return err
		}
		if err = hydrateMessageProjection(ctx, q, &current); err != nil {
			return err
		}
		if err = hydrateReply(ctx, q, &current); err != nil {
			return err
		}
		if current.ID != in.Message.ID || current.AccountID != in.Message.AccountID || current.ConversationID != in.Message.ConversationID || domain.DisplayBody(current) != domain.DisplayBody(in.Message) {
			return application.ErrStateConflict
		}
		urls := make([]string, 0, len(in.References))
		for _, ref := range in.References {
			urls = append(urls, ref.URL)
			if err = q.PutSharedContent(ctx, dbgen.PutSharedContentParams{ID: string(ref.ID), RevisionID: string(ref.RevisionID), MessageID: string(current.ID), Url: ref.URL, Kind: ref.Kind, FileName: ref.FileName}); err != nil {
				return err
			}
		}
		if err = q.DeleteObsoleteSharedContent(ctx, dbgen.DeleteObsoleteSharedContentParams{MessageID: string(current.ID), CurrentUrls: jsonText(urls)}); err != nil {
			return err
		}
		if err = q.MarkSharedContentIndexed(ctx, dbgen.MarkSharedContentIndexedParams{MessageID: string(current.ID), ParserVersion: int64(in.ParserVersion), Caption: domain.DisplayBody(current)}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) SharedContent(ctx context.Context, query domain.SharedContentQuery) (domain.SharedContentPage, error) {
	page := domain.SharedContentPage{Items: []domain.SharedContentItem{}}
	tx, queries, err := begin(ctx, s)
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	cursor, err := application.ParseSharedContentCursor(query.Cursor)
	if err != nil {
		return page, err
	}
	rows, err := queries.ListSharedContent(ctx, dbgen.ListSharedContentParams{ParserVersion: application.SharedContentVersion, MessageIds: jsonText(nonNilIDs(query.MessageIDs)), AccountID: string(query.AccountID), ConversationID: string(query.ConversationID), ContentType: query.Type, CursorID: string(cursor.ID), CursorMs: cursor.Milliseconds, Search: strings.TrimSpace(query.Search), PageLimit: int64(query.Limit + 1)})
	if err != nil {
		return page, err
	}
	more := len(rows) > query.Limit
	if more {
		rows = rows[:query.Limit]
	}
	for _, row := range rows {
		item := domain.SharedContentItem{SharedReference: domain.SharedReference{ID: domain.ID(row.ID), RevisionID: domain.ID(row.RevisionID), URL: row.Url, Kind: row.Kind, FileName: row.FileName}, AccountLabel: row.AccountLabel}
		var conversation domain.Conversation
		if err = json.Unmarshal([]byte(row.MessagePayload), &item.Message); err != nil {
			return page, err
		}
		if err = json.Unmarshal([]byte(row.ConversationPayload), &conversation); err != nil {
			return page, err
		}
		item.ConversationTitle = conversation.Title
		if err = hydrateMessageProjection(ctx, queries, &item.Message); err != nil {
			return page, err
		}
		if err = hydrateReply(ctx, queries, &item.Message); err != nil {
			return page, err
		}
		if row.PreviewPayload != nil {
			var p domain.MessagePreview
			if err = json.Unmarshal([]byte(*row.PreviewPayload), &p); err != nil {
				return page, err
			}
			item.Preview = &p
		}
		page.Items = append(page.Items, item)
	}
	if more {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = application.SharedContentCursor(last.Message.CreatedAt.UnixMilli(), last.ID)
	}
	status, err := queries.SharedContentIndexStatus(ctx, dbgen.SharedContentIndexStatusParams{ParserVersion: application.SharedContentVersion, MessageIds: jsonText(nonNilIDs(query.MessageIDs)), AccountID: string(query.AccountID), ConversationID: string(query.ConversationID)})
	if err != nil {
		return page, err
	}
	state := "complete"
	if status.IndexedMessages < status.TotalMessages {
		state = "pending"
	}
	page.Indexing = domain.SharedContentIndex{State: state, IndexedMessages: status.IndexedMessages, TotalMessages: status.TotalMessages}
	return page, tx.Commit()
}

func nonNilIDs(ids []domain.ID) []domain.ID {
	if ids == nil {
		return []domain.ID{}
	}
	return ids
}
func (s *Store) SharedContentItem(ctx context.Context, id domain.ID) (domain.SharedContentItem, error) {
	row, err := s.queries.GetSharedContentItem(ctx, dbgen.GetSharedContentItemParams{ParserVersion: application.SharedContentVersion, ItemID: string(id)})
	if err != nil {
		return domain.SharedContentItem{}, notFound(err)
	}
	item := domain.SharedContentItem{SharedReference: domain.SharedReference{ID: domain.ID(row.ID), RevisionID: domain.ID(row.RevisionID), URL: row.Url, Kind: row.Kind, FileName: row.FileName}}
	if err = json.Unmarshal([]byte(row.MessagePayload), &item.Message); err != nil {
		return item, err
	}
	if err = hydrateMessageProjection(ctx, s.queries, &item.Message); err != nil {
		return item, err
	}
	if err = hydrateReply(ctx, s.queries, &item.Message); err != nil {
		return item, err
	}
	for _, ref := range application.SharedReferences(domain.DisplayBody(item.Message)) {
		if ref.URL == item.URL && ref.Kind == item.Kind {
			return item, nil
		}
	}
	return domain.SharedContentItem{}, application.ErrNotFound
}
