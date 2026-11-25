package sqlite

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type protectedOutboxProtocol struct {
	outboundProtocol
	enabled        atomic.Bool
	wireStarted    bool
	activateOnSend bool
	calls          chan domain.ID
}

func (p *protectedOutboxProtocol) RoomEncryption(domain.Account, string) (bool, error) {
	return p.enabled.Load(), nil
}

func (p *protectedOutboxProtocol) InspectRoomOMEMO(context.Context, domain.Account, string) (application.RoomOMEMOState, error) {
	return application.RoomOMEMOState{Enabled: p.enabled.Load()}, nil
}

func (p *protectedOutboxProtocol) SetRoomOMEMO(_ context.Context, _ domain.Account, _ string, enabled bool) error {
	p.enabled.Store(enabled)
	return nil
}

func (*protectedOutboxProtocol) AttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error {
	return nil
}

func (*protectedOutboxProtocol) RequiresOriginalWireAttempt(domain.Account) bool { return true }

func (p *protectedOutboxProtocol) Send(ctx context.Context, _ domain.Account, out application.Outgoing) string {
	select {
	case p.calls <- out.ID:
	default:
	}
	if p.activateOnSend {
		p.enabled.Store(true)
		return application.ProtectionPending
	}
	if p.wireStarted {
		if out.RecordOMEMOWireAttempt == nil || out.RecordOMEMOWireAttempt(ctx, []string{"omemo:" + strings.Repeat("d", 64)}) != nil {
			return application.Failed
		}
	}
	return application.NotSent
}

func protectedOutboxFixture(t *testing.T, clock func() time.Time, wireStarted bool) (*Store, *application.Engine, domain.Conversation, *protectedOutboxProtocol) {
	t.Helper()
	store, _, direct := outboxFixture(t)
	engine, err := application.New(store, "local", func() (domain.ID, error) { return migrationID(), nil }, clock)
	if err != nil {
		t.Fatal(err)
	}
	chat := outboundRoom(t, store, direct.AccountID, "private@conference.localhost")
	protocol := &protectedOutboxProtocol{
		outboundProtocol: outboundProtocol{autoJoined: true, connected: true},
		wireStarted:      wireStarted,
		calls:            make(chan domain.ID, 4),
	}
	engine.SetProtocols(protocol)
	if _, err := engine.JoinRoom(t.Context(), migrationID(), chat.ID, "Tester", ""); err != nil {
		t.Fatal(err)
	}
	return store, engine, chat, protocol
}

func TestOMEMOOutboxReopensOnlyProtectedPreWireClaim(t *testing.T) {
	for _, tc := range []struct {
		name                                                       string
		protected, wireStarted, attachments, activate, cancellable bool
	}{
		{name: "waiting for verified devices", protected: true, cancellable: true},
		{name: "protected wire was reached", protected: true, wireStarted: true},
		{name: "protected upload may have started", protected: true, attachments: true},
		{name: "ordinary delivery retains cutoff"},
		{name: "room activated after claim", activate: true, cancellable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store, engine, chat, protocol := protectedOutboxFixture(t, time.Now, tc.wireStarted)
			protocol.enabled.Store(tc.protected)
			protocol.activateOnSend = tc.activate
			composition := domain.OutgoingComposition{OriginalText: "original send"}
			if tc.attachments {
				id := migrationID()
				if _, _, err := engine.StageAttachment(ctx, chat.ID, id, "file.txt", "text/plain", strings.NewReader("file")); err != nil {
					t.Fatal(err)
				}
				composition.AttachmentIDs = []domain.ID{id}
			}
			result, err := engine.SendComposition(ctx, migrationID(), chat.ID, composition, nil)
			if err != nil {
				t.Fatal(err)
			}
			sent := result.Message
			defer startOutboundWorker(t, engine)()
			var queued domain.Message
			awaitOutbound(t, func() bool {
				m, err := store.Message(ctx, sent.ID)
				if err != nil {
					t.Fatal(err)
				}
				if m.SendState == application.Failed {
					t.Fatal("fixture wire preparation failed")
				}
				queued = m
				return m.SendState == "queued" && m.RevisionID != sent.RevisionID
			})
			if queued.DispatchStarted == tc.cancellable {
				t.Fatalf("unexpected retry cutoff: started=%v cancellable=%v", queued.DispatchStarted, tc.cancellable)
			}
			cancelled, err := engine.CancelMessage(ctx, migrationID(), queued.ID, queued.RevisionID)
			if tc.cancellable {
				if err != nil || cancelled.Message.SendState != "cancelled" {
					t.Fatalf("protected pre-wire claim could not be cancelled: %v %+v", err, cancelled)
				}
			} else if !errors.Is(err, application.ErrStateConflict) {
				t.Fatalf("closed cutoff accepted cancellation: %v", err)
			}
		})
	}
}

func TestOMEMOOutboxDoesNotClaimPlainScheduleAfterActivation(t *testing.T) {
	now := time.Now().UTC()
	var nanos atomic.Int64
	nanos.Store(now.UnixNano())
	store, engine, chat, protocol := protectedOutboxFixture(t, func() time.Time { return time.Unix(0, nanos.Load()).UTC() }, false)
	due := now.Add(time.Hour)
	scheduled := queueBackupFixture(t, engine, chat.ID, "plaintext scheduled first", &due)
	protocol.enabled.Store(true)
	nanos.Store(due.Add(time.Second).UnixNano())
	protected := queueBackupFixture(t, engine, chat.ID, "marker after activation", nil)
	defer startOutboundWorker(t, engine)()

	select {
	case sent := <-protocol.calls:
		if sent != protected.ID {
			t.Fatalf("plaintext scheduled row reached dispatch: %v", sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher did not reach the protected marker")
	}
	m, err := store.Message(t.Context(), scheduled.ID)
	if err != nil || m.RevisionID != scheduled.RevisionID || m.DispatchStarted {
		t.Fatalf("held schedule changed the original cutoff: %+v %v", m, err)
	}
	got, err := engine.CancelMessage(t.Context(), migrationID(), scheduled.ID, scheduled.RevisionID)
	if err != nil || got.Message.SendState != "cancelled" {
		t.Fatalf("held plaintext schedule could not be cancelled: %v %+v", err, got)
	}
}
