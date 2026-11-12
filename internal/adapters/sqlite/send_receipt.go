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
	if row.Kind != "send_message" {
		return domain.Message{}, application.ErrNotFound
	}
	var accepted domain.Message
	if err := json.Unmarshal([]byte(row.Result), &accepted); err != nil {
		return domain.Message{}, err
	}
	if string(accepted.ID) != row.ResourceID || !accepted.IsOwn || accepted.SendState != "queued" {
		return domain.Message{}, errors.New("invalid send acceptance snapshot")
	}
	return accepted, nil
}
