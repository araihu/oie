package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type guardedReplyAttempt struct {
	message application.Outgoing
	err     error
}

type guardedReplyProtocol struct {
	outboundProtocol
	claimed   chan application.Outgoing
	proceed   chan struct{}
	attempted chan guardedReplyAttempt
}

func (*guardedReplyProtocol) RequiresOriginalWireAttempt(domain.Account) bool { return true }

func (p *guardedReplyProtocol) Send(ctx context.Context, _ domain.Account, out application.Outgoing) string {
	select {
	case p.claimed <- out:
	case <-ctx.Done():
		return application.NotSent
	}
	select {
	case <-p.proceed:
	case <-ctx.Done():
		return application.NotSent
	}
	err := out.RecordWireAttempt(ctx, "")
	select {
	case p.attempted <- guardedReplyAttempt{message: out, err: err}:
	case <-ctx.Done():
		return application.NotSent
	}
	if err != nil {
		return application.NotSent
	}
	return application.Submitted
}

func TestBackupRestoreResumedReplyReloadsAfterRetractedTarget(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	engine.SetProtocols(&outboundProtocol{wireReply: true})
	const quote = "the withdrawn original"
	const answer = "the retained answer"
	const correctionScope = "xmpp:correct:sender:bob@localhost"
	ref := domain.MessageReference{Scope: "sender:bob@localhost", ID: "reply-target-before-wire", Author: "bob@localhost"}
	if err := engine.Receive(ctx, application.Incoming{
		AccountID: chat.AccountID, Kind: "direct", Address: chat.Address, ProviderID: ref.ID,
		OriginalText: quote, Historical: true, CreatedAt: now, References: []domain.MessageReference{ref},
		Correction: &domain.MessageCorrection{Scope: correctionScope, Author: ref.Author, ID: ref.ID},
	}); err != nil {
		t.Fatal(err)
	}
	target := correctionMessages(t, engine, chat)[0]
	destination := copyRestoreFixture(t, source)
	sent, err := engine.SendMessageWithReply(ctx, migrationID(), chat.ID, answer, target.ID)
	if err != nil || !strings.Contains(sent.Message.OriginalText, quote) {
		t.Fatalf("fixture has no frozen quote: %+v, %v", sent, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if err := destination.ReviewRecoveredSend(ctx, sent.Message.ID, sent.Message.RevisionID, true, now); err != nil {
		t.Fatal(err)
	}
	receiver := testRestoreEngine(t, destination)
	protocol := &guardedReplyProtocol{
		outboundProtocol: outboundProtocol{connected: true, wireReply: true},
		claimed:          make(chan application.Outgoing, 2),
		proceed:          make(chan struct{}),
		attempted:        make(chan guardedReplyAttempt, 2),
	}
	receiver.SetProtocols(protocol)
	stop := startOutboundWorker(t, receiver)
	defer stop()
	select {
	case outgoing := <-protocol.claimed:
		if outgoing.ID != sent.Message.ID || !strings.Contains(outgoing.OriginalText, quote) || outgoing.Reply == nil {
			t.Fatalf("initial dispatch claim did not include the verified quote: %+v", outgoing)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not claim the resumed reply")
	}
	if err := receiver.Receive(ctx, application.Incoming{
		AccountID: chat.AccountID, Kind: "direct", Address: chat.Address,
		Retraction: &application.IncomingRetraction{Scope: correctionScope, RemoteID: ref.ID, At: now},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := receiver.Message(ctx, sent.Message.ID)
	if err != nil || current.SendState != "dispatching" || current.Reply == nil || !current.Reply.OriginalExpired {
		t.Fatalf("retraction did not mask the quote during the active claim: %+v, %v", current, err)
	}
	close(protocol.proceed)
	select {
	case first := <-protocol.attempted:
		if !errors.Is(first.err, application.ErrStateConflict) {
			t.Fatalf("wire gate authorized the stale quote: %+v, %v", first.message, first.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first claim did not reach the wire gate")
	}
	select {
	case retry := <-protocol.attempted:
		if retry.err != nil || retry.message.OriginalText != answer || retry.message.Reply != nil || retry.message.ID != sent.Message.ID {
			t.Fatalf("retry did not send only the retained answer: %+v, %v", retry.message, retry.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not retry the refreshed reply")
	}
	awaitOutbound(t, func() bool {
		message, err := receiver.Message(ctx, sent.Message.ID)
		return err == nil && message.SendState == application.Submitted
	})
}
