package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetMessageEditHistory(ctx context.Context, r engineapi.GetMessageEditHistoryRequestObject) (engineapi.GetMessageEditHistoryResponseObject, error) {
	limit, cursor := 20, ""
	if r.Params.Limit != nil {
		limit = *r.Params.Limit
	}
	if r.Params.Cursor != nil {
		cursor = *r.Params.Cursor
	}
	includeRetracted := r.Params.IncludeRetracted != nil && *r.Params.IncludeRetracted
	page, err := s.engine.MessageEditHistory(ctx, domain.ID(r.MessageId), cursor, limit, includeRetracted)
	if err != nil {
		return nil, err
	}
	out := engineapi.MessageEditHistoryPage{RequestedMessageId: string(page.RequestedMessageID), MessageId: string(page.MessageID), AccountId: string(page.AccountID), ConversationId: string(page.ConversationID), CurrentRevisionId: string(page.CurrentRevisionID), Snapshot: page.Snapshot, Items: []engineapi.MessageEditHistoryItem{}}
	if page.Retraction != nil {
		out.Retraction = &engineapi.MessageRetraction{At: page.Retraction.At, OriginalAvailable: page.Retraction.OriginalAvailable}
	}
	for _, item := range page.Items {
		parents := make([]string, 0, len(item.ParentRevisionIDs))
		for _, parent := range item.ParentRevisionIDs {
			parents = append(parents, string(parent))
		}
		version := engineapi.MessageEditHistoryItem{RevisionId: string(item.RevisionID), SourceMessageId: string(item.SourceMessageID), CreatedAt: item.CreatedAt, OriginalText: item.OriginalText, Sender: engineapi.Sender{Id: string(item.Sender.ID), DisplayName: item.Sender.DisplayName}, IsCurrent: item.IsCurrent, IsOriginal: item.IsOriginal, ParentRevisionIds: parents}
		if len(item.Attachments) > 0 {
			files := make([]engineapi.StagedAttachment, 0, len(item.Attachments))
			for _, file := range item.Attachments {
				files = append(files, stagedAttachment(file))
			}
			version.Attachments = &files
		}
		out.Items = append(out.Items, version)
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return engineapi.GetMessageEditHistory200JSONResponse(out), nil
}
