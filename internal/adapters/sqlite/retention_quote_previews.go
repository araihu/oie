package sqlite

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	sqlitedriver "modernc.org/sqlite"
)

func init() {
	// An older archive can still contain previews created before a quote was
	// redacted. Allow its derived media only if the redacted message still
	// references the URL in its body or structured attachments.
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_quote_preview_allowed", 2, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		payload, _ := args[0].(string)
		url, _ := args[1].(string)
		var m domain.Message
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			return nil, err
		}
		if m.WireBodySHA256 == "" || m.OutboundReply != nil {
			return true, nil
		}
		m.Reply = nil
		return slices.ContainsFunc(application.MessageSharedReferences(m), func(ref domain.SharedReference) bool {
			return ref.URL == url
		}), nil
	})
}

type redactedQuoteURLs map[domain.ID]map[string]bool

func (removed redactedQuoteURLs) add(message, canonical domain.ID, before, after string) {
	if before == after {
		return
	}
	remaining := application.SharedReferences(after)
	for _, ref := range application.SharedReferences(before) {
		if slices.ContainsFunc(remaining, func(kept domain.SharedReference) bool { return kept.URL == ref.URL }) {
			continue
		}
		for _, id := range []domain.ID{message, canonical} {
			if removed[id] == nil {
				removed[id] = make(map[string]bool)
			}
			removed[id][ref.URL] = true
		}
	}
}

func (removed redactedQuoteURLs) prune(ctx context.Context, q *dbgen.Queries) error {
	for message, candidates := range removed {
		m, err := visibleMessageProjection(ctx, q, message)
		if err == nil {
			if err := hydrateReply(ctx, q, &m); err != nil {
				return err
			}
			for _, ref := range application.MessageSharedReferences(m) {
				delete(candidates, ref.URL)
			}
		} else if !errors.Is(err, application.ErrNotFound) {
			return err
		}
		if len(candidates) == 0 {
			continue
		}
		urls := make([]string, 0, len(candidates))
		for url := range candidates {
			urls = append(urls, url)
		}
		encoded := jsonText(urls)
		if err := q.InvalidateSharedContent(ctx, string(message)); err != nil {
			return err
		}
		if err := q.DeleteRedactedQuoteSharedContent(ctx, dbgen.DeleteRedactedQuoteSharedContentParams{MessageID: string(message), Urls: encoded}); err != nil {
			return err
		}
		ids, err := q.RedactedQuotePreviewIDs(ctx, dbgen.RedactedQuotePreviewIDsParams{MessageID: string(message), Urls: encoded})
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			continue
		}
		previewIDs := jsonText(ids)
		if err := q.DeleteRedactedQuotePreviewEvents(ctx, previewIDs); err != nil {
			return err
		}
		if err := q.DeleteRedactedQuotePreviewRevisions(ctx, previewIDs); err != nil {
			return err
		}
		if err := q.DeleteRedactedQuotePreviews(ctx, previewIDs); err != nil {
			return err
		}
	}
	return nil
}
