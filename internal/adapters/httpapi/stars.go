package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func star(s domain.MessageStar) engineapi.MessageStar {
	out := engineapi.MessageStar{MessageId: string(s.MessageID), AccountId: string(s.AccountID), ConversationId: string(s.ConversationID), Starred: s.Starred}
	if s.RevisionID != "" {
		revision, at := string(s.RevisionID), s.UpdatedAt
		out.RevisionId, out.UpdatedAt = &revision, &at
	}
	return out
}

func (s *Server) GetMessageStar(ctx context.Context, r engineapi.GetMessageStarRequestObject) (engineapi.GetMessageStarResponseObject, error) {
	state, err := s.engine.MessageStar(ctx, domain.ID(r.MessageId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetMessageStar200JSONResponse(star(state)), nil
}

func (s *Server) SetMessageStar(ctx context.Context, r engineapi.SetMessageStarRequestObject) (engineapi.SetMessageStarResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	var expected domain.ID
	if r.Body.ExpectedRevisionId != nil {
		expected = domain.ID(*r.Body.ExpectedRevisionId)
	}
	result, err := s.engine.SetMessageStar(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.MessageId), expected, r.Body.Starred)
	if err != nil {
		return nil, err
	}
	return engineapi.SetMessageStar200JSONResponse(star(result.Star)), nil
}

func (s *Server) ListConversationMessageStars(ctx context.Context, r engineapi.ListConversationMessageStarsRequestObject) (engineapi.ListConversationMessageStarsResponseObject, error) {
	ids := make([]domain.ID, 0, len(r.Params.MessageId))
	for _, id := range r.Params.MessageId {
		ids = append(ids, domain.ID(id))
	}
	states, err := s.engine.MessageStars(ctx, domain.ID(r.ConversationId), ids)
	if err != nil {
		return nil, err
	}
	page := engineapi.MessageStarPage{Items: make([]engineapi.MessageStar, 0, len(states))}
	for _, state := range states {
		page.Items = append(page.Items, star(state))
	}
	return engineapi.ListConversationMessageStars200JSONResponse(page), nil
}

func (s *Server) ListStarredMessages(ctx context.Context, r engineapi.ListStarredMessagesRequestObject) (engineapi.ListStarredMessagesResponseObject, error) {
	in := application.StarQuery{Limit: 50}
	if r.Params.Limit != nil {
		in.Limit = *r.Params.Limit
	}
	if r.Params.AccountId != nil {
		in.AccountID = domain.ID(*r.Params.AccountId)
	}
	if r.Params.ConversationId != nil {
		in.ConversationID = domain.ID(*r.Params.ConversationId)
	}
	if r.Params.Cursor != nil {
		in.Cursor = domain.ID(*r.Params.Cursor)
	}
	items, err := s.engine.StarredMessages(ctx, in)
	if err != nil {
		return nil, err
	}
	page := engineapi.StarredMessagePage{Items: make([]engineapi.StarredMessage, 0, len(items))}
	if len(items) > in.Limit {
		cursor := string(items[in.Limit-1].Message.ID)
		page.NextCursor = &cursor
		items = items[:in.Limit]
	}
	for _, item := range items {
		page.Items = append(page.Items, engineapi.StarredMessage{Message: message(item.Message), Conversation: conversation(item.Conversation), Star: star(item.Star)})
	}
	return engineapi.ListStarredMessages200JSONResponse(page), nil
}
