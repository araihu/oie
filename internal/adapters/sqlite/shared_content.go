package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
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
		row, err := q.GetTimelineMessage(ctx, string(in.Message.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return application.ErrStateConflict
		}
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
	accountIDs := jsonText(nonNilIDs(query.AccountIDs))
	params := dbgen.ListSharedContentParams{
		ParserVersion: application.SharedContentVersion, MessageIds: jsonText(nonNilIDs(query.MessageIDs)),
		AccountID: string(query.AccountID), AccountIds: accountIDs, ConversationID: string(query.ConversationID),
		ContentType: query.Type, CursorID: string(cursor.ID), CursorMs: cursor.Milliseconds,
		Search: strings.TrimSpace(query.Search), PageLimit: int64(query.Limit + 1),
	}
	scope, browse := catalogBrowseScope(query, params.Search)
	var rows []dbgen.ListSharedContentRow
	if browse {
		beforeMS, beforeID := cursor.Milliseconds, string(cursor.ID)
		if beforeID == "" {
			beforeMS, beforeID = math.MaxInt64, "~"
		}
		ordered, listErr := queries.ListCatalogPage(ctx, dbgen.ListCatalogPageParams{
			Scope: scope, Tab: query.Type, ParserVersion: params.ParserVersion,
			BeforeMs: beforeMS, BeforeID: beforeID, PageLimit: params.PageLimit,
		})
		if listErr != nil {
			return page, listErr
		}
		rows = make([]dbgen.ListSharedContentRow, 0, len(ordered))
		for _, row := range ordered {
			rows = append(rows, dbgen.ListSharedContentRow{
				ID: row.ID, RevisionID: row.RevisionID, Kind: row.Kind, Url: row.Url, FileName: row.FileName,
				MessagePayload: row.MessagePayload, TimelineAtMs: row.TimelineAtMs,
				ConversationPayload: row.ConversationPayload, AccountLabel: row.AccountLabel, PreviewPayload: row.PreviewPayload,
			})
		}
		if len(ordered) > query.Limit && query.Limit > 0 {
			last := ordered[query.Limit-1]
			page.NextCursor = application.SharedContentCursor(last.PositionMs, domain.ID(last.ID))
		}
	} else {
		rows, err = queries.ListSharedContent(ctx, params)
		if err != nil {
			return page, err
		}
	}
	more := len(rows) > query.Limit
	if more {
		rows = rows[:query.Limit]
	}
	if query.IncludeFirstPageCounts {
		page.FirstPageCounts, err = sharedContentFirstPageCounts(ctx, queries, params, query.Limit, len(rows), scope, browse)
		if err != nil {
			return page, err
		}
	}
	for _, row := range rows {
		item := domain.SharedContentItem{SharedReference: domain.SharedReference{ID: domain.ID(row.ID), RevisionID: domain.ID(row.RevisionID), URL: row.Url, Kind: row.Kind, FileName: row.FileName}, AccountLabel: row.AccountLabel}
		var conversation domain.Conversation
		if err = json.Unmarshal([]byte(row.MessagePayload), &item.Message); err != nil {
			return page, err
		}
		setTimelineTime(&item.Message, row.TimelineAtMs)
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
	if more && page.NextCursor == "" {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = application.SharedContentCursor(last.Message.TimelineTime().UnixMilli(), last.ID)
	}
	page.Indexing, err = sharedCatalogProgress(ctx, queries, params.AccountID, accountIDs, params.ConversationID, params.MessageIds)
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}

func catalogBrowseScope(query domain.SharedContentQuery, search string) (string, bool) {
	if search != "" || len(query.MessageIDs) != 0 || query.Type == "video" || len(query.AccountIDs) > 1 {
		return "", false
	}
	if query.ConversationID != "" {
		return "conversation:" + string(query.ConversationID), true
	}
	if query.AccountID != "" {
		return "account:" + string(query.AccountID), true
	}
	if len(query.AccountIDs) == 1 {
		return "account:" + string(query.AccountIDs[0]), true
	}
	return "", true
}

func sharedCatalogProgress(ctx context.Context, q *dbgen.Queries, account, accounts, conversation, messages string) (domain.SharedContentIndex, error) {
	const version = application.SharedContentVersion
	out := domain.SharedContentIndex{State: "complete"}
	if messages == "[]" {
		row, err := q.SharedCatalogProgress(ctx, dbgen.SharedCatalogProgressParams{ParserVersion: version, AccountID: account, AccountIds: accounts, ConversationID: conversation})
		if err != nil {
			return out, err
		}
		out.TotalMessages, out.IndexedMessages = row.TotalMessages, row.IndexedMessages
	} else {
		row, err := q.SharedCatalogMessageProgress(ctx, dbgen.SharedCatalogMessageProgressParams{ParserVersion: version, AccountID: account, AccountIds: accounts, ConversationID: conversation, MessageIds: messages})
		if err != nil {
			return out, err
		}
		out.TotalMessages, out.IndexedMessages = row.TotalMessages, row.IndexedMessages
	}
	if out.IndexedMessages < out.TotalMessages {
		out.State = "pending"
	}
	return out, nil
}

func sharedContentFirstPageCounts(ctx context.Context, queries *dbgen.Queries, active dbgen.ListSharedContentParams, limit, currentSize int, scope string, browse bool) (*domain.SharedContentFirstPageCounts, error) {
	counts := &domain.SharedContentFirstPageCounts{}
	params := dbgen.CountSharedContentFirstPageParams{
		ParserVersion: active.ParserVersion, MessageIds: active.MessageIds,
		AccountID: active.AccountID, AccountIds: active.AccountIds, ConversationID: active.ConversationID,
		Search: active.Search, PageLimit: int64(limit),
	}
	for _, tab := range []struct {
		kind  string
		count *int
	}{{"media", &counts.Media}, {"links", &counts.Links}, {"documents", &counts.Documents}} {
		if active.CursorID == "" && active.ContentType == tab.kind {
			*tab.count = currentSize
			continue
		}
		params.ContentType = tab.kind
		var count int64
		var err error
		if browse {
			count, err = queries.CountCatalogPage(ctx, dbgen.CountCatalogPageParams{
				Scope: scope, Tab: tab.kind, ParserVersion: params.ParserVersion, PageLimit: params.PageLimit,
			})
		} else {
			count, err = queries.CountSharedContentFirstPage(ctx, params)
		}
		if err != nil {
			return nil, err
		}
		*tab.count = int(count)
	}
	return counts, nil
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
	setTimelineTime(&item.Message, row.TimelineAtMs)
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
