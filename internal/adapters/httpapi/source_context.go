package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetMessageSourceContext(ctx context.Context, r engineapi.GetMessageSourceContextRequestObject) (engineapi.GetMessageSourceContextResponseObject, error) {
	id := domain.ID(r.ConversationId)
	identity, err := s.engineIdentity(ctx)
	if err != nil {
		return nil, err
	}
	owner, err := s.engine.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	window, err := s.engine.MessageContext(ctx, id, domain.ID(r.MessageId), domain.SourceContextPageSize)
	if err != nil {
		return nil, err
	}
	out := engineapi.MessageSourceContext{
		Engine:       identity,
		Conversation: conversation(owner),
		Context:      messageContext(window),
	}
	s.sourceConversationMetadata(ctx, &out)
	s.sourceMessageMetadata(ctx, &out)
	return engineapi.GetMessageSourceContext200JSONResponse(out), nil
}

func (s *Server) sourceConversationMetadata(ctx context.Context, out *engineapi.MessageSourceContext) {
	id, account := out.Conversation.Id, out.Conversation.AccountId
	if details, err := s.engine.ConversationDetails(ctx, domain.ID(id)); err == nil {
		value := conversationDetails(details)
		out.Details = &value
	}
	if report, err := s.engine.AccountCapabilities(ctx, domain.ID(account)); err == nil {
		value := capabilities(report)
		out.Capabilities = &value
		for _, feature := range value.Features {
			if feature.Id == "send_corrections" && (feature.AppSupport == "supported" || feature.AppSupport == "partial") &&
				(feature.ProtocolSupport == "supported" || feature.ProtocolSupport == "not_required") {
				if target, err := s.engine.ConversationEditTarget(ctx, domain.ID(id)); err == nil {
					value := editTarget(target)
					out.EditTarget = &value
				}
				break
			}
		}
	}
	if out.Conversation.Kind == "room" {
		if session, err := s.engine.RoomSession(ctx, domain.ID(id)); err == nil {
			value := roomSession(session)
			out.RoomSession = &value
		}
		if archive, err := s.engine.RoomArchive(ctx, domain.ID(id)); err == nil {
			value := roomArchive(archive)
			out.RoomArchive = &value
		}
	}
}

func (s *Server) sourceMessageMetadata(ctx context.Context, out *engineapi.MessageSourceContext) {
	id := domain.ID(out.Conversation.Id)
	ids := make([]domain.ID, 0, len(out.Context.Items))
	for _, m := range out.Context.Items {
		ids = append(ids, domain.ID(m.Id))
	}
	if previews, err := s.engine.ConversationPreviews(ctx, id, ids); err == nil {
		items := make([]engineapi.MessagePreview, 0, len(previews))
		for _, item := range previews {
			items = append(items, preview(item))
		}
		out.Previews = &items
	}
	if videos, ok := s.sourceVideos(ctx, out.Conversation, ids); ok {
		out.Videos = &videos
	}
	if stars, err := s.engine.MessageStars(ctx, id, ids); err == nil {
		items := make([]engineapi.MessageStar, 0, len(stars))
		for _, item := range stars {
			items = append(items, star(item))
		}
		out.Stars = &items
	}
	if states, err := s.engine.ConversationReactions(ctx, id, ids); err == nil {
		items := make([]engineapi.MessageReactions, 0, len(states))
		for _, item := range states {
			items = append(items, reactions(item))
		}
		out.Reactions = &items
	}
}

func (s *Server) sourceVideos(ctx context.Context, c engineapi.Conversation, ids []domain.ID) ([]engineapi.SharedContentItem, bool) {
	query := domain.SharedContentQuery{Type: "video", Limit: 100, ConversationID: domain.ID(c.Id), AccountID: domain.ID(c.AccountId), MessageIDs: ids}
	items := []engineapi.SharedContentItem{}
	counts := map[domain.ID]int{}
	for range 3 {
		page, err := s.engine.SharedContent(ctx, query)
		if err != nil {
			return nil, false
		}
		for _, item := range page.Items {
			if counts[item.Message.ID] < 3 {
				items = append(items, sharedContentItem(item))
				counts[item.Message.ID]++
			}
		}
		if page.NextCursor == "" || page.NextCursor == query.Cursor {
			break
		}
		query.Cursor = page.NextCursor
	}
	return items, true
}
