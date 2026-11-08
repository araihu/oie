package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetMessageContext(ctx context.Context, r engineapi.GetMessageContextRequestObject) (engineapi.GetMessageContextResponseObject, error) {
	limit := 50
	if r.Params.Limit != nil {
		limit = *r.Params.Limit
	}
	result, err := s.engine.MessageContext(ctx, domain.ID(r.ConversationId), domain.ID(r.MessageId), limit)
	if err != nil {
		return nil, err
	}
	out := engineapi.MessageContext{TargetId: string(result.TargetID), Items: make([]engineapi.Message, 0, len(result.Items)), HasOlder: result.HasOlder, HasNewer: result.HasNewer}
	if result.RequestedTargetID != "" {
		requested := string(result.RequestedTargetID)
		out.RequestedTargetId = &requested
	}
	for _, m := range result.Items {
		out.Items = append(out.Items, message(m))
	}
	return engineapi.GetMessageContext200JSONResponse(out), nil
}
