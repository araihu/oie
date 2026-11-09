package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) SearchMessages(ctx context.Context, r engineapi.SearchMessagesRequestObject) (engineapi.SearchMessagesResponseObject, error) {
	q := domain.MessageSearchQuery{Search: r.Params.Q, Limit: 50}
	if r.Params.AccountId != nil {
		q.AccountID = domain.ID(*r.Params.AccountId)
	}
	if r.Params.ConversationId != nil {
		q.ConversationID = domain.ID(*r.Params.ConversationId)
	}
	if r.Params.IncludeEditHistory != nil {
		q.IncludeEditHistory = *r.Params.IncludeEditHistory
	}
	if r.Params.Cursor != nil {
		q.Cursor = *r.Params.Cursor
	}
	if r.Params.Limit != nil {
		q.Limit = *r.Params.Limit
	}
	page, err := s.engine.SearchMessages(ctx, q)
	if err != nil {
		return nil, err
	}
	out := engineapi.MessageSearchPage{Query: page.Query, IncludeEditHistory: page.IncludeEditHistory, Items: []engineapi.MessageSearchHit{}, MetadataIndexing: engineapi.SharedContentIndexing{State: engineapi.SharedContentIndexingState(page.MetadataIndexing.State), IndexedMessages: page.MetadataIndexing.IndexedMessages, TotalMessages: page.MetadataIndexing.TotalMessages}}
	if page.AccountID != "" {
		id := string(page.AccountID)
		out.AccountId = &id
	}
	if page.ConversationID != "" {
		id := string(page.ConversationID)
		out.ConversationId = &id
	}
	for _, hit := range page.Items {
		out.Items = append(out.Items, engineapi.MessageSearchHit{Message: message(hit.Message), AccountLabel: hit.AccountLabel, ConversationTitle: hit.ConversationTitle, MatchedRevisionId: string(hit.MatchedRevisionID), MatchedAt: hit.MatchedAt, Historical: hit.Historical, MatchKind: engineapi.MessageSearchHitMatchKind(hit.MatchKind), Excerpt: hit.Excerpt})
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return engineapi.SearchMessages200JSONResponse(out), nil
}
