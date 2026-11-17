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

type receiptProtocol struct {
	outboundProtocol
	transport string
}

func (p *receiptProtocol) DisplayedMarkerTransport(domain.ID) string { return p.transport }

func (*receiptProtocol) SendDisplayedMarker(context.Context, domain.Conversation, domain.DisplayedMarker) {
}

func TestDisplayedMarkersRespectUnreadGapsAndCascadingPolicy(t *testing.T) {
	_, e, chat := outboxFixture(t)
	protocol := &receiptProtocol{transport: "first"}
	e.SetProtocols(protocol)
	ctx := t.Context()
	at := time.Now().UTC()
	receive := func(wire string, own, historical bool) domain.ID {
		t.Helper()
		at = at.Add(time.Second)
		markerID := wire
		if strings.HasPrefix(wire, "unrequested ") {
			markerID = ""
		}
		if err := e.Receive(ctx, application.Incoming{
			AccountID: chat.AccountID, Address: chat.Address, OriginalText: wire, ProviderID: wire,
			ReadReceiptID: markerID, CreatedAt: at, IsOwn: own, Historical: historical,
		}); err != nil {
			t.Fatal(err)
		}
		messages, err := e.Messages(ctx, chat.ID, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range messages {
			if msg.OriginalText == wire {
				return msg.ID
			}
		}
		t.Fatalf("missing message %q", wire)
		return ""
	}
	mark := func(key domain.ID, ids ...domain.ID) application.ReadResult {
		t.Helper()
		read, err := e.MarkMessagesRead(ctx, key, chat.ID, ids)
		if err != nil {
			t.Fatal(err)
		}
		return read
	}
	first, later := receive("first", false, false), receive("later", false, false)
	if got := mark(migrationID(), later); got.DisplayedMarker != nil || got.State.UnreadCount != 1 {
		t.Fatal("marker crossed an unread message", got)
	}
	key := migrationID()
	frontier := mark(key, first)
	if frontier.DisplayedMarker == nil || frontier.DisplayedMarker.RemoteID != "later" || frontier.State.UnreadCount != 0 {
		t.Fatal("closing the gap did not confirm the already read frontier", frontier)
	}
	if claimed, err := e.ClaimDisplayedMarker(ctx, *frontier.DisplayedMarker); err != nil || !claimed {
		t.Fatal("eligible frontier was not claimed", claimed, err)
	}
	if claimed, err := e.ClaimDisplayedMarker(ctx, *frontier.DisplayedMarker); err != nil || claimed {
		t.Fatal("frontier was claimed twice", claimed, err)
	}
	if got := mark(key, first); !got.Replayed || got.DisplayedMarker != nil {
		t.Fatal("operation replay attempted a marker", got)
	}
	global, err := e.GlobalReadReceipts(ctx)
	if err != nil || !global.Enabled {
		t.Fatal(global, err)
	}
	global.Enabled = false
	global, err = e.SetGlobalReadReceipts(ctx, global)
	if err != nil || global.Enabled {
		t.Fatal(global, err)
	}
	disabled := receive("while disabled", false, false)
	if got := mark(migrationID(), disabled); got.DisplayedMarker != nil || got.State.UnreadCount != 0 {
		t.Fatal("disabled receipt changed local read behavior", got)
	}
	policy, err := e.ConversationReadReceipts(ctx, chat.ID)
	if err != nil || policy.Override != nil || policy.EffectiveEnabled {
		t.Fatal(policy, err)
	}
	policy, err = e.SetConversationReadReceipts(ctx, chat.ID, policy.Revision, new(true))
	if err != nil || !policy.EffectiveEnabled || policy.GlobalEnabled || policy.Override == nil || !*policy.Override {
		t.Fatal(policy, err)
	}
	if got := mark(migrationID(), disabled, receive("own", true, false), receive("archive", false, true)); got.DisplayedMarker != nil {
		t.Fatal("read of an already read, own or historical message attempted a marker", got)
	}
	older, newest := receive("older", false, false), receive("newest", false, false)
	got := mark(migrationID(), older, newest)
	if got.DisplayedMarker == nil || got.DisplayedMarker.RemoteID != "newest" ||
		got.DisplayedMarker.PolicyEpoch != policy.PolicyEpoch {
		t.Fatal("expected coalesced marker at newest message", got)
	}
	global.Enabled = true
	if _, err = e.SetGlobalReadReceipts(ctx, global); err != nil {
		t.Fatal(err)
	}
	if claimed, err := e.ClaimDisplayedMarker(ctx, *got.DisplayedMarker); err != nil || !claimed {
		t.Fatal("global change superseded an explicit override", claimed, err)
	}
	policy, err = e.ConversationReadReceipts(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	at = at.Add(5 * time.Second)
	lateFrontier := mark(migrationID(), receive("next", false, false)).DisplayedMarker
	if lateFrontier == nil {
		t.Fatal("expected a pending frontier")
	}
	at = at.Add(-3 * time.Second)
	late := receive("delivered out of order", false, false)
	if claimed, err := e.ClaimDisplayedMarker(ctx, *lateFrontier); err != nil || claimed {
		t.Fatal("claim crossed a newly arrived unread message", claimed, err)
	}
	afterLate := mark(migrationID(), late).DisplayedMarker
	if afterLate == nil || afterLate.RemoteID != lateFrontier.RemoteID {
		t.Fatal("closing the late gap lost the pending frontier", afterLate)
	}
	inherited, err := e.SetConversationReadReceipts(ctx, chat.ID, policy.Revision, nil)
	if err != nil || inherited.Override != nil || !inherited.EffectiveEnabled {
		t.Fatal(inherited, err)
	}
	if claimed, err := e.ClaimDisplayedMarker(ctx, *afterLate); err != nil || claimed {
		t.Fatal("stale marker survived a policy change", claimed, err)
	}
	if _, err := e.SetConversationReadReceipts(ctx, chat.ID, policy.Revision, new(false)); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale conversation setting was accepted", err)
	}
	at = at.Add(10 * time.Second)
	unrequested := receive("unrequested after policy change", false, false)
	if got := mark(migrationID(), unrequested); got.DisplayedMarker != nil {
		t.Fatal("a new read resurrected a candidate from another policy", got)
	}

	protocol.transport = ""
	if got := mark(migrationID(), receive("read while disconnected", false, false)); got.DisplayedMarker != nil {
		t.Fatal("read while disconnected prepared a marker", got)
	}
	protocol.transport = "second"
	if got := mark(migrationID(), receive("unrequested after reconnect", false, false)); got.DisplayedMarker != nil {
		t.Fatal("reconnect resurrected a disconnected read", got)
	}
	beforeReconnect := mark(migrationID(), receive("read before reconnect", false, false)).DisplayedMarker
	if beforeReconnect == nil || beforeReconnect.Transport != "second" {
		t.Fatal("expected a candidate bound to the live transport", beforeReconnect)
	}
	protocol.transport = "third"
	if got := mark(migrationID(), receive("unrequested in a replacement transport", false, false)); got.DisplayedMarker != nil {
		t.Fatal("a new read in a replacement transport resurrected an old candidate", got)
	}
}
