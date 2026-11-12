package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

type SendReceiptRepository interface {
	AcceptedSendMessage(context.Context, domain.ID) (domain.Message, error)
}

// AcceptedSendMessage reads the original committed acceptance without replaying a mutation.
func (e *Engine) AcceptedSendMessage(ctx context.Context, operationID domain.ID) (domain.Message, error) {
	if !validID(operationID) {
		return domain.Message{}, ErrInvalid
	}
	repo, ok := e.repo.(SendReceiptRepository)
	if !ok {
		return domain.Message{}, ErrProtocolUnavailable
	}
	return repo.AcceptedSendMessage(ctx, operationID)
}
