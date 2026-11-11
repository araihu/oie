package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetMentionCandidates(ctx context.Context, r engineapi.GetMentionCandidatesRequestObject) (engineapi.GetMentionCandidatesResponseObject, error) {
	result, err := s.engine.MentionCandidates(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	bareNicknameSpans := true
	out := engineapi.MentionCandidates{ConversationId: string(result.ConversationID), AccountId: string(result.AccountID), State: engineapi.MentionCandidatesState(result.State), Items: []engineapi.MentionCandidate{}, Truncated: result.Truncated, BareNicknameSpans: &bareNicknameSpans}
	for _, item := range result.Items {
		out.Items = append(out.Items, engineapi.MentionCandidate{Id: item.ID, DisplayName: item.DisplayName, InsertText: item.InsertText, ReferenceMode: engineapi.MentionCandidateReferenceMode(item.ReferenceMode)})
	}
	return engineapi.GetMentionCandidates200JSONResponse(out), nil
}
