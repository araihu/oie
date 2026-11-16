package application

import (
	"context"
	"time"

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

// VerifyExpiredSend checks an exact durable acceptance for an expired or
// currently withdrawn message. It never queues a message.
func (e *Engine) VerifyExpiredSend(ctx context.Context, key, conversation domain.ID, in domain.OutgoingComposition, at *time.Time, review ReplyReview) error {
	if !validID(key) || !validID(conversation) {
		return ErrInvalid
	}
	fingerprint, _, err := sendIntentFingerprint(conversation, in, at, review)
	if err != nil {
		return err
	}
	repo, ok := e.repo.(interface {
		VerifyExpiredSend(context.Context, domain.ID, string) error
	})
	if !ok {
		return ErrProtocolUnavailable
	}
	return repo.VerifyExpiredSend(ctx, key, fingerprint)
}
