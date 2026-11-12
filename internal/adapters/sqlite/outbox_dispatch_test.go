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

type blockedUploadProtocol struct {
	editProtocol
	started chan domain.ID
	release chan struct{}
}

func (*blockedUploadProtocol) Connection(context.Context, domain.Account) (domain.Connection, error) {
	return domain.Connection{State: "connected"}, nil
}

func (*blockedUploadProtocol) AttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error {
	return nil
}

func (*blockedUploadProtocol) RequiresOriginalWireAttempt(domain.Account) bool { return true }

func (p *blockedUploadProtocol) Send(ctx context.Context, a domain.Account, out application.Outgoing) string {
	if out.Correction != nil {
		return p.editProtocol.Send(ctx, a, out)
	}
	body := ""
	if len(out.Attachments) != 0 {
		p.started <- out.ID
		select {
		case <-p.release:
		case <-ctx.Done():
			return application.NotSent
		}
		body = "https://upload.example.org/finished"
	}
	if err := out.RecordWireAttempt(ctx, body); err != nil {
		return application.Failed
	}
	return application.Submitted
}

func TestAttachmentDispatchDoesNotHoldLaterText(t *testing.T) {
	store, engine, chat := outboxFixture(t)
	protocol := &blockedUploadProtocol{started: make(chan domain.ID, 1), release: make(chan struct{})}
	engine.SetProtocols(protocol)
	ctx := t.Context()
	defer startOutboundWorker(t, engine)()
	prior, err := engine.SendMessage(ctx, migrationID(), chat.ID, "text sent before the upload")
	if err != nil {
		t.Fatal(err)
	}
	awaitOutbound(t, func() bool {
		m, err := engine.Message(ctx, prior.Message.ID)
		return err == nil && m.SendState == application.Submitted
	})
	file := migrationID()
	if _, _, err := engine.StageAttachment(ctx, chat.ID, file, "document.txt", "text/plain", strings.NewReader("file")); err != nil {
		t.Fatal(err)
	}
	media, err := engine.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{AttachmentIDs: []domain.ID{file}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case started := <-protocol.started:
		if started != media.Message.ID {
			t.Fatal("another message began upload", started)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("media did not start")
	}
	if _, err := engine.CancelMessage(ctx, migrationID(), media.Message.ID, media.Message.RevisionID); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("claim did not close the cancellation cutoff", err)
	}
	target, err := engine.ConversationEditTarget(ctx, chat.ID)
	if err != nil || !target.Eligible || target.MessageID != prior.Message.ID {
		t.Fatalf("an untransmitted attachment took the correction target: %+v %v", target, err)
	}
	edit, err := engine.EditMessage(ctx, migrationID(), target.MessageID, target.CurrentRevisionID, "edited while an upload waits")
	if err != nil {
		t.Fatal(err)
	}
	awaitOutbound(t, func() bool {
		command, err := engine.MessageEdit(ctx, edit.Command.ID)
		return err == nil && command.State == application.Submitted
	})
	text, err := engine.SendMessage(ctx, migrationID(), chat.ID, "text while an upload waits")
	if err != nil {
		t.Fatal(err)
	}
	awaitOutbound(t, func() bool {
		m, err := engine.Message(ctx, text.Message.ID)
		return err == nil && m.SendState == application.Submitted
	})
	close(protocol.release)
	awaitOutbound(t, func() bool {
		m, err := engine.Message(ctx, media.Message.ID)
		return err == nil && m.SendState == application.Submitted
	})
	latest, err := store.ReadMessageEditTarget(ctx, chat.ID, true)
	if err != nil || latest.Source.ID != media.Message.ID {
		t.Fatalf("latest transmission ignored actual wire order: %+v %v", latest.Source, err)
	}
}

func TestDispatchClaimRejectsEditedRevisionFromAnotherLane(t *testing.T) {
	store, engine, chat := outboxFixture(t)
	engine.SetProtocols(&blockedUploadProtocol{})
	ctx := t.Context()
	before, err := engine.SendMessage(ctx, migrationID(), chat.ID, "initial text")
	if err != nil {
		t.Fatal(err)
	}
	file := migrationID()
	if _, _, err = engine.StageAttachment(ctx, chat.ID, file, "document.txt", "text/plain", strings.NewReader("file")); err != nil {
		t.Fatal(err)
	}
	updated, err := engine.UpdatePendingMessage(ctx, migrationID(), before.Message.ID, before.Message.RevisionID,
		&domain.OutgoingComposition{AttachmentIDs: []domain.ID{file}}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = store.ClaimDispatchMessage(ctx, before.Message.ID, before.Message.RevisionID, "", migrationID(), now, false); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("text lane claimed the previous revision", err)
	}
	text, _, err := store.PendingMessagesByAttachments(ctx, now, false, application.PendingMessageCursor{})
	if err != nil || len(text) != 0 {
		t.Fatalf("edit remained in text lane: %+v %v", text, err)
	}
	media, _, err := store.PendingMessagesByAttachments(ctx, now, true, application.PendingMessageCursor{})
	if err != nil || len(media) != 1 || media[0].RevisionID != updated.Message.RevisionID {
		t.Fatalf("edit missing from attachment lane: %+v %v", media, err)
	}
}

type interruptedUploadProtocol struct {
	blockedUploadProtocol
	guardWire, startWire bool
	marker               chan error
}

func (p *interruptedUploadProtocol) RequiresOriginalWireAttempt(domain.Account) bool {
	return p.guardWire
}

func (p *interruptedUploadProtocol) Send(ctx context.Context, _ domain.Account, out application.Outgoing) string {
	var err error
	if p.startWire {
		err = out.RecordWireAttempt(ctx, "https://upload.example.org/started")
	}
	p.marker <- err
	return application.Unknown
}

func TestInterruptedAttachmentDispatchRecoversAtWireBoundary(t *testing.T) {
	for _, test := range []struct {
		name                 string
		guardWire, startWire bool
		wantState            string
	}{
		{name: "upload interrupted", guardWire: true, wantState: "queued"},
		{name: "wire attempt started", guardWire: true, startWire: true, wantState: application.Unknown},
		{name: "adapter without durable marker", wantState: application.Unknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, engine, chat := outboxFixture(t)
			ctx := t.Context()
			protocol := &interruptedUploadProtocol{guardWire: test.guardWire, startWire: test.startWire, marker: make(chan error, 1)}
			engine.SetProtocols(protocol)
			file := migrationID()
			if _, _, err := engine.StageAttachment(ctx, chat.ID, file, "document.txt", "text/plain", strings.NewReader("retained")); err != nil {
				t.Fatal(err)
			}
			when := time.Now().UTC().Add(time.Hour)
			accepted, err := engine.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{AttachmentIDs: []domain.ID{file}}, &when)
			if err != nil {
				t.Fatal(err)
			}
			id := accepted.Message.ID
			clock := func() time.Time { return when.Add(time.Second) }
			ids := func() (domain.ID, error) { return migrationID(), nil }
			engine, err = application.New(store, "local", ids, clock)
			if err != nil {
				t.Fatal(err)
			}
			engine.SetProtocols(protocol)

			// A failure during finalization leaves the exact durable state that
			// recovery sees when the process stops inside this Send call.
			if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER stop_dispatch BEFORE UPDATE OF state ON outbox
				WHEN OLD.state='dispatching' AND NEW.state!='dispatching'
				BEGIN SELECT RAISE(ABORT,'dispatch interrupted'); END;`); err != nil {
				t.Fatal(err)
			}
			run, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = engine.Run(run)
			cancel()
			if err == nil || !strings.Contains(err.Error(), "dispatch interrupted") {
				t.Fatalf("expected interrupted finalization: %v", err)
			}
			select {
			case err = <-protocol.marker:
				if err != nil {
					t.Fatalf("could not mark wire attempt: %v", err)
				}
			default:
				t.Fatal("interruption happened before the adapter was called")
			}
			claimed, err := store.Message(ctx, id)
			if err != nil || claimed.SendState != "dispatching" || !claimed.DispatchStarted {
				t.Fatalf("claim did not survive interruption: %+v %v", claimed, err)
			}
			if _, err = store.db.ExecContext(ctx, "DROP TRIGGER stop_dispatch"); err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, store.dir, migrationID())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			restarted, err := application.New(reopened, "local", ids, clock)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer startOutboundWorker(t, restarted)()
				awaitOutbound(t, func() bool {
					m, err := reopened.Message(ctx, id)
					return err == nil && m.SendState == test.wantState
				})
			}()
			current, err := restarted.Message(ctx, id)
			if err != nil || !current.DispatchStarted || current.ScheduledAt == nil || !current.ScheduledAt.Equal(when) {
				t.Fatalf("recovery lost the claim or schedule: %+v %v", current, err)
			}
			if test.startWire && current.AttachmentWireText != "https://upload.example.org/started" {
				t.Fatal("ambiguous attempt lost the recorded attachment links")
			}
			if _, err = restarted.CancelMessage(ctx, migrationID(), id, current.RevisionID); !errors.Is(err, application.ErrStateConflict) {
				t.Fatalf("recovery reopened cancellation cutoff: %v", err)
			}
			if _, err = restarted.UpdatePendingMessage(ctx, migrationID(), id, current.RevisionID, nil, nil, true); !errors.Is(err, application.ErrStateConflict) {
				t.Fatalf("recovery reopened Send now cutoff: %v", err)
			}
			if err = reopened.RecordOriginalWireAttempt(ctx, id, claimed.RevisionID, "late marker"); !errors.Is(err, application.ErrStateConflict) {
				t.Fatalf("marker from pre-restart claim unexpectedly accepted: %v", err)
			}
			pending, _, err := reopened.PendingMessagesByAttachments(ctx, clock(), true, application.PendingMessageCursor{})
			if err != nil {
				t.Fatal(err)
			}
			if test.wantState != "queued" {
				if len(pending) != 0 || current.TimelineAt == nil {
					t.Fatalf("ambiguous attempt is retryable or hidden: %+v %+v", pending, current)
				}
				return
			}
			if len(pending) != 1 || pending[0].ID != id || current.TimelineAt != nil {
				t.Fatalf("known-unsent attempt did not remain an eligible schedule: %+v %+v", pending, current)
			}

			retry := &blockedUploadProtocol{started: make(chan domain.ID, 1), release: make(chan struct{})}
			close(retry.release)
			restarted.SetProtocols(retry)
			defer startOutboundWorker(t, restarted)()
			awaitOutbound(t, func() bool {
				m, err := reopened.Message(ctx, id)
				return err == nil && m.SendState == application.Submitted
			})
			if got := <-retry.started; got != id {
				t.Fatalf("retry changed message identity: %s", got)
			}
		})
	}
}

type sparseDispatchProtocol struct {
	blockedUploadProtocol
	connected domain.ID
}

func (p *sparseDispatchProtocol) Connection(_ context.Context, a domain.Account) (domain.Connection, error) {
	state := "disconnected"
	if a.ID == p.connected {
		state = "connected"
	}
	return domain.Connection{State: state}, nil
}

func TestOutboxScansPastDisconnectedAccounts(t *testing.T) {
	for _, lane := range []string{"text", "attachments"} {
		t.Run(lane, func(t *testing.T) {
			store, engine, blockedChat := outboxFixture(t)
			ctx := t.Context()
			account, err := engine.Register(ctx, migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "other@localhost", Label: "Other"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			engine, err = application.New(store, "local", func() (domain.ID, error) { return migrationID(), nil }, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			protocol := &sparseDispatchProtocol{connected: account.Account.ID}
			protocol.started, protocol.release = make(chan domain.ID, 1), make(chan struct{})
			close(protocol.release)
			engine.SetProtocols(protocol)
			readyChat, err := engine.OpenConversation(ctx, migrationID(), account.Account.ID, "bob@localhost")
			if err != nil {
				t.Fatal(err)
			}
			composition := func(chat domain.ID) domain.OutgoingComposition {
				if lane == "text" {
					return domain.OutgoingComposition{OriginalText: "queued message"}
				}
				file := migrationID()
				if _, _, err := engine.StageAttachment(ctx, chat, file, "document.txt", "text/plain", strings.NewReader("file")); err != nil {
					t.Fatal(err)
				}
				return domain.OutgoingComposition{AttachmentIDs: []domain.ID{file}}
			}
			blocked := composition(blockedChat.ID)
			var lastBlocked domain.ID
			for range 100 {
				pending, err := engine.SendComposition(ctx, migrationID(), blockedChat.ID, blocked, nil)
				if err != nil {
					t.Fatal(err)
				}
				lastBlocked = pending.Message.ID
			}
			ready, err := engine.SendComposition(ctx, migrationID(), readyChat.Conversation.ID, composition(readyChat.Conversation.ID), nil)
			if err != nil {
				t.Fatal(err)
			}
			if ready.Message.ID <= lastBlocked {
				t.Fatal("fixture must put the ready message past the first page at the same attempt time")
			}
			defer startOutboundWorker(t, engine)()
			awaitOutbound(t, func() bool {
				m, err := engine.Message(ctx, ready.Message.ID)
				return err == nil && m.SendState == application.Submitted
			})
			m, err := engine.Message(ctx, lastBlocked)
			if err != nil || m.SendState != "queued" || m.DispatchStarted {
				t.Fatalf("offline account was unexpectedly claimed: %+v %v", m, err)
			}
		})
	}
}
