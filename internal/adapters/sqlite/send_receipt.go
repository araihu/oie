package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) AcceptedSendMessage(ctx context.Context, operationID domain.ID) (domain.Message, error) {
	row, err := s.queries.GetOperation(ctx, string(operationID))
	if err != nil {
		return domain.Message{}, notFound(err)
	}
	if row.Kind != "send_message" || row.Result == "null" {
		return domain.Message{}, application.ErrNotFound
	}
	var accepted domain.Message
	if err := json.Unmarshal([]byte(row.Result), &accepted); err != nil {
		return domain.Message{}, err
	}
	if string(accepted.ID) != row.ResourceID || !accepted.IsOwn || accepted.SendState != "queued" {
		return domain.Message{}, errors.New("invalid send acceptance snapshot")
	}
	if accepted.OutboundReply != nil {
		expired, err := s.read.IsExpiredMessage(ctx, string(accepted.OutboundReply.TargetID))
		if err != nil {
			return domain.Message{}, err
		}
		if expired {
			domain.ProjectOutboundReply(&accepted)
			accepted.Reply.OriginalExpired = true
			accepted.Reply.Excerpt, accepted.Reply.SenderName = "", ""
		}
	}
	return accepted, nil
}

func (s *Store) VerifyExpiredSend(ctx context.Context, key domain.ID, fingerprint string) error {
	row, err := s.read.GetOperation(ctx, string(key))
	if err != nil {
		return notFound(err)
	}
	if row.Kind != "send_message" || row.Fingerprint != fingerprint {
		return application.ErrIdempotencyConflict
	}
	if row.Result != "null" {
		return application.ErrNotFound
	}
	return nil
}
