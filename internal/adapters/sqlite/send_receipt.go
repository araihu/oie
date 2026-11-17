package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) AcceptedSendMessage(ctx context.Context, operationID domain.ID) (domain.Message, error) {
	tx, q, err := beginRead(ctx, s)
	if err != nil {
		return domain.Message{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := q.GetOperation(ctx, string(operationID))
	if err != nil {
		return domain.Message{}, notFound(err)
	}
	if row.Kind != "send_message" || row.Result == "null" {
		return domain.Message{}, application.ErrNotFound
	}
	accepted, err := decodeAcceptedSendSnapshot(row.Result, row.ResourceID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := maskWithdrawnProjection(ctx, q, &accepted); err != nil {
		return domain.Message{}, err
	}
	if accepted.OutboundReply != nil {
		reply := accepted
		if err := hydrateReply(ctx, q, &reply); err != nil {
			return domain.Message{}, err
		}
		if reply.Reply.OriginalExpired {
			accepted.OriginalText = reply.Reply.BodyText
			accepted.Reply = reply.Reply
			accepted.Reply.Excerpt, accepted.Reply.SenderName = "", ""
			plan := *accepted.OutboundReply
			plan.Excerpt, plan.SenderName, plan.Fallback = "", "", nil
			accepted.OutboundReply, accepted.ReplyReference = &plan, nil
		}
	}
	return accepted, tx.Commit()
}

func decodeAcceptedSendSnapshot(payload, resourceID string) (domain.Message, error) {
	var accepted domain.Message
	if err := json.Unmarshal([]byte(payload), &accepted); err != nil {
		return domain.Message{}, err
	}
	if string(accepted.ID) != resourceID || !accepted.IsOwn || accepted.SendState != "queued" {
		return domain.Message{}, errors.New("invalid send acceptance snapshot")
	}
	return accepted, nil
}

func (s *Store) VerifyExpiredSend(ctx context.Context, key domain.ID, fingerprint string) error {
	tx, q, err := beginRead(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := q.GetOperation(ctx, string(key))
	if err != nil {
		return notFound(err)
	}
	if row.Kind != "send_message" || row.Fingerprint != fingerprint {
		return application.ErrIdempotencyConflict
	}
	if row.Result == "null" {
		held, err := q.HasRecoveredSendReceipt(ctx, string(key))
		if err != nil {
			return err
		}
		if held {
			return application.ErrStateConflict
		}
		return tx.Commit()
	}
	accepted, err := decodeAcceptedSendSnapshot(row.Result, row.ResourceID)
	if err != nil {
		return err
	}
	current, err := visibleMessageProjection(ctx, q, accepted.ID)
	if err != nil {
		return err
	}
	if !current.IsOwn || !current.IsWithdrawn() ||
		current.AccountID != accepted.AccountID || current.ConversationID != accepted.ConversationID {
		return application.ErrNotFound
	}
	return tx.Commit()
}
