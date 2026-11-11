package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func editTarget(t domain.MessageEditTarget) engineapi.MessageEditTarget {
	out := engineapi.MessageEditTarget{MessageId: string(t.MessageID), AccountId: string(t.AccountID), ConversationId: string(t.ConversationID), CurrentRevisionId: string(t.CurrentRevisionID), OriginalText: t.OriginalText, Eligible: t.Eligible}
	if t.Reason != "" {
		reason := engineapi.MessageEditTargetReason(t.Reason)
		out.Reason = &reason
	}
	return out
}

func editCommand(c domain.MessageEditCommand) engineapi.MessageEditCommand {
	out := engineapi.MessageEditCommand{Id: string(c.ID), RevisionId: string(c.RevisionID), MessageId: string(c.MessageID), AccountId: string(c.AccountID), ConversationId: string(c.ConversationID), ExpectedRevisionId: string(c.ExpectedRevisionID), OriginalText: c.OriginalText, State: engineapi.MessageEditCommandState(c.State), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.LastErrorCode != "" {
		out.LastErrorCode = &c.LastErrorCode
	}
	return out
}

func (s *Server) GetMessageEditTarget(ctx context.Context, r engineapi.GetMessageEditTargetRequestObject) (engineapi.GetMessageEditTargetResponseObject, error) {
	target, err := s.engine.MessageEditTarget(ctx, domain.ID(r.MessageId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetMessageEditTarget200JSONResponse(editTarget(target)), nil
}

func (s *Server) GetConversationEditTarget(ctx context.Context, r engineapi.GetConversationEditTargetRequestObject) (engineapi.GetConversationEditTargetResponseObject, error) {
	target, err := s.engine.ConversationEditTarget(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetConversationEditTarget200JSONResponse(editTarget(target)), nil
}

func (s *Server) EditMessage(ctx context.Context, r engineapi.EditMessageRequestObject) (engineapi.EditMessageResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	result, err := s.engine.EditMessage(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.MessageId), domain.ID(r.Body.ExpectedRevisionId), r.Body.OriginalText)
	if err != nil {
		return nil, err
	}
	if result.Replayed {
		return engineapi.EditMessage200JSONResponse(editCommand(result.Command)), nil
	}
	location := "/api/v1/message-edits/" + string(result.Command.ID)
	return engineapi.EditMessage202JSONResponse{Body: editCommand(result.Command), Headers: engineapi.EditMessage202ResponseHeaders{Location: &location}}, nil
}

func (s *Server) GetMessageEdit(ctx context.Context, r engineapi.GetMessageEditRequestObject) (engineapi.GetMessageEditResponseObject, error) {
	command, err := s.engine.MessageEdit(ctx, domain.ID(r.EditId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetMessageEdit200JSONResponse(editCommand(command)), nil
}
