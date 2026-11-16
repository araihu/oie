package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestAcceptedSendReceiptMasksWithdrawnMessageAndQuotedOriginal(t *testing.T) {
	store, engine, chat := outboxFixture(t)
	engine.SetProtocols(&outboundProtocol{wireReply: true})
	ctx := t.Context()
	const original, answer = "original text before retraction", "this reply keeps its own text"
	originalKey, replyKey := migrationID(), migrationID()
	sent, err := engine.SendMessage(ctx, originalKey, chat.ID, original)
	if err != nil {
		t.Fatal(err)
	}
	verify := func(key domain.ID, body string) error {
		return engine.VerifyExpiredSend(ctx, key, chat.ID, domain.OutgoingComposition{OriginalText: body}, nil, application.ReplyReview{})
	}
	if err = verify(originalKey, original); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("visible message accepted as unavailable: %v", err)
	}
	now := time.Now().UTC()
	if _, err = store.TransitionMessage(ctx, sent.Message.ID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TransitionMessage(ctx, sent.Message.ID, "dispatching", "submitted", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	reply, err := engine.SendMessageWithReply(ctx, replyKey, chat.ID, answer, sent.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := engine.AcceptedSendMessage(ctx, replyKey)
	if err != nil || !strings.Contains(before.OriginalText, original) || before.OutboundReply == nil || before.OutboundReply.Excerpt != original {
		t.Fatalf("fixture did not retain the initial quote: %+v %v", before, err)
	}
	retraction := application.IncomingRetraction{
		Scope: "xmpp:correct:sender:alice@localhost", RemoteID: string(sent.Message.ID), At: now.Add(time.Second),
	}
	if err = store.RecordMessageRetraction(ctx, chat.AccountID, chat.Kind, chat.Address, retraction); err != nil {
		t.Fatal(err)
	}
	if err = verify(originalKey, original); err != nil {
		t.Fatalf("exact withdrawn acceptance proof: %v", err)
	}
	if err = verify(originalKey, answer); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatalf("mismatched withdrawn intent was accepted: %v", err)
	}
	missingKey := migrationID()
	if err = verify(missingKey, original); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("missing receipt was accepted: %v", err)
	}
	if _, err = engine.Operation(ctx, missingKey); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("verification created an operation: %v", err)
	}
	accepted, err := engine.AcceptedSendMessage(ctx, originalKey)
	if err != nil || accepted.ID != sent.Message.ID || accepted.SendState != "queued" || accepted.Retraction == nil ||
		!accepted.Retraction.OriginalAvailable || accepted.OriginalText != "" || accepted.Composition != nil {
		t.Fatalf("accepted message receipt escaped masking: %+v %v", accepted, err)
	}
	accepted, err = engine.AcceptedSendMessage(ctx, replyKey)
	if err != nil || accepted.ID != reply.Message.ID || accepted.SendState != "queued" || accepted.OriginalText != answer ||
		accepted.Reply == nil || !accepted.Reply.OriginalExpired || accepted.Reply.Excerpt != "" || accepted.Reply.BodyText != answer ||
		accepted.OutboundReply == nil || accepted.OutboundReply.Excerpt != "" {
		t.Fatalf("accepted reply receipt lost the reply or disclosed its quote: %+v %v", accepted, err)
	}
}
