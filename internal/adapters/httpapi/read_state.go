package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func readState(s domain.ReadState) engineapi.ConversationReadState {
	out := engineapi.ConversationReadState{AccountId: string(s.AccountID), ConversationId: string(s.ConversationID), UnreadCount: s.UnreadCount}
	if s.RevisionID != "" {
		id, at := string(s.RevisionID), s.UpdatedAt
		out.RevisionId = &id
		out.UpdatedAt = &at
	}
	return out
}
func (s *Server) GetConversationReadState(ctx context.Context, r engineapi.GetConversationReadStateRequestObject) (engineapi.GetConversationReadStateResponseObject, error) {
	state, err := s.engine.ConversationReadState(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	out := readState(state)
	out.CanMarkLatestRead = new(true)
	return engineapi.GetConversationReadState200JSONResponse(out), nil
}
func (s *Server) MarkConversationMessagesRead(ctx context.Context, r engineapi.MarkConversationMessagesReadRequestObject) (engineapi.MarkConversationMessagesReadResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	key, conversation := domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId)
	var result application.ReadResult
	var err error
	if match := r.Params.IfMatch; match != nil {
		revision := strings.Trim(*match, `"`)
		if *match != `"`+revision+`"` || len(r.Body.MessageIds) != 1 {
			return nil, application.ErrInvalid
		}
		latest := application.ReadLatest{MessageID: domain.ID(r.Body.MessageIds[0]), ConversationRevisionID: domain.ID(revision)}
		result, err = s.engine.MarkLatestRead(ctx, key, conversation, latest)
		if errors.Is(err, application.ErrStateConflict) {
			return engineapi.MarkConversationMessagesRead412ApplicationProblemPlusJSONResponse(newProblem(http.StatusPreconditionFailed, "state_conflict")), nil
		}
	} else {
		ids := make([]domain.ID, 0, len(r.Body.MessageIds))
		for _, id := range r.Body.MessageIds {
			ids = append(ids, domain.ID(id))
		}
		result, err = s.engine.MarkMessagesRead(ctx, key, conversation, ids)
	}
	if err != nil {
		return nil, err
	}
	return engineapi.MarkConversationMessagesRead200JSONResponse(readState(result.State)), nil
}
