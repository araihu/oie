package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func reactionRevision(id domain.ID) *engineapi.NullableReactionRevision {
	if id == "" {
		return nil
	}
	value := engineapi.NullableReactionRevision(id)
	return &value
}

func reactionEmojis(values []string) engineapi.ReactionEmojiSet {
	return append(engineapi.ReactionEmojiSet{}, values...)
}

func reactionCommand(c domain.MessageReactionCommand) engineapi.MessageReactionCommand {
	out := engineapi.MessageReactionCommand{
		Id: string(c.ID), RevisionId: string(c.RevisionID), MessageId: string(c.MessageID),
		AccountId: string(c.AccountID), ConversationId: string(c.ConversationID),
		ExpectedOwnRevisionId: reactionRevision(c.ExpectedOwnRevisionID), Emojis: reactionEmojis(c.Emojis),
		State: engineapi.MessageReactionCommandState(c.State), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.LastErrorCode != "" {
		out.LastErrorCode = &c.LastErrorCode
	}
	return out
}

func reactions(r domain.MessageReactions) engineapi.MessageReactions {
	out := engineapi.MessageReactions{
		MessageId: string(r.MessageID), AccountId: string(r.AccountID), ConversationId: string(r.ConversationID),
		RevisionId: reactionRevision(r.RevisionID), OwnRevisionId: reactionRevision(r.OwnRevisionID),
		OwnEmojis: reactionEmojis(r.OwnEmojis), Items: []engineapi.ReactionItem{}, Eligible: r.Eligible, MaxEmojis: r.MaxEmojis,
	}
	for _, item := range r.Items {
		out.Items = append(out.Items, engineapi.ReactionItem{Emoji: item.Emoji, Count: item.Count, Own: item.Own})
	}
	if r.Reason != "" {
		reason := engineapi.MessageReactionsReason(r.Reason)
		out.Reason = &reason
	}
	if r.AllowedEmojis != nil {
		allowed := append([]string{}, r.AllowedEmojis...)
		out.AllowedEmojis = &allowed
	}
	if r.PendingCommand != nil {
		command := reactionCommand(*r.PendingCommand)
		out.PendingCommand = &command
	}
	return out
}

func (s *Server) GetMessageReactions(ctx context.Context, r engineapi.GetMessageReactionsRequestObject) (engineapi.GetMessageReactionsResponseObject, error) {
	snapshot, err := s.engine.MessageReactions(ctx, domain.ID(r.MessageId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetMessageReactions200JSONResponse(reactions(snapshot)), nil
}

func (s *Server) ListConversationReactions(ctx context.Context, r engineapi.ListConversationReactionsRequestObject) (engineapi.ListConversationReactionsResponseObject, error) {
	ids := make([]domain.ID, len(r.Params.MessageId))
	for i, id := range r.Params.MessageId {
		ids[i] = domain.ID(id)
	}
	items, err := s.engine.ConversationReactions(ctx, domain.ID(r.ConversationId), ids)
	if err != nil {
		return nil, err
	}
	out := engineapi.MessageReactionsList{Items: []engineapi.MessageReactions{}}
	for _, item := range items {
		out.Items = append(out.Items, reactions(item))
	}
	return engineapi.ListConversationReactions200JSONResponse(out), nil
}

func (s *Server) SetMessageReactions(ctx context.Context, r engineapi.SetMessageReactionsRequestObject) (engineapi.SetMessageReactionsResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	var expected domain.ID
	if r.Body.ExpectedOwnRevisionId != nil {
		expected = domain.ID(*r.Body.ExpectedOwnRevisionId)
	}
	result, err := s.engine.SetMessageReactions(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.MessageId), expected, r.Body.Emojis)
	if err != nil {
		return nil, err
	}
	if result.Replayed {
		return engineapi.SetMessageReactions200JSONResponse(reactionCommand(result.Command)), nil
	}
	return engineapi.SetMessageReactions202JSONResponse(reactionCommand(result.Command)), nil
}

func (s *Server) GetMessageReactionCommand(ctx context.Context, r engineapi.GetMessageReactionCommandRequestObject) (engineapi.GetMessageReactionCommandResponseObject, error) {
	command, err := s.engine.MessageReactionCommand(ctx, domain.ID(r.CommandId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetMessageReactionCommand200JSONResponse(reactionCommand(command)), nil
}
