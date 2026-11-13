package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) ListSharedContent(ctx context.Context, r engineapi.ListSharedContentRequestObject) (engineapi.ListSharedContentResponseObject, error) {
	q := domain.SharedContentQuery{Type: "media", Limit: 50, AccountIDs: searchAccountIDs(r.Params.AccountIds)}
	if r.Params.Type != nil {
		q.Type = string(*r.Params.Type)
	}
	if r.Params.AccountId != nil {
		q.AccountID = domain.ID(*r.Params.AccountId)
	}
	if r.Params.ConversationId != nil {
		q.ConversationID = domain.ID(*r.Params.ConversationId)
	}
	if r.Params.MessageId != nil {
		for _, id := range *r.Params.MessageId {
			q.MessageIDs = append(q.MessageIDs, domain.ID(id))
		}
	}
	if r.Params.Q != nil {
		q.Search = *r.Params.Q
	}
	if r.Params.Cursor != nil {
		q.Cursor = *r.Params.Cursor
	}
	if r.Params.Limit != nil {
		q.Limit = *r.Params.Limit
	}
	page, err := s.engine.SharedContent(ctx, q)
	if err != nil {
		return nil, err
	}
	out := engineapi.SharedContentPage{Items: []engineapi.SharedContentItem{}, Indexing: engineapi.SharedContentIndexing{State: engineapi.SharedContentIndexingState(page.Indexing.State), IndexedMessages: page.Indexing.IndexedMessages, TotalMessages: page.Indexing.TotalMessages}}
	for _, item := range page.Items {
		value := engineapi.SharedContentItem{Id: string(item.ID), RevisionId: string(item.RevisionID), Kind: engineapi.SharedContentItemKind(item.Kind), Url: item.URL, FileName: item.FileName, AccountLabel: item.AccountLabel, ConversationTitle: item.ConversationTitle, Message: message(item.Message)}
		if item.Preview != nil {
			if item.Preview.Kind == "video" {
				p := videoThumbnail(*item.Preview)
				value.Thumbnail = &p
			} else {
				p := preview(*item.Preview)
				value.Preview = &p
			}
		}
		out.Items = append(out.Items, value)
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return engineapi.ListSharedContent200JSONResponse(out), nil
}
