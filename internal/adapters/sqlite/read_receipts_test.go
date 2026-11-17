package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestDisplayedMarkersRespectUnreadGapsAndCascadingPolicy(t *testing.T) {
	_, e, chat := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	at := time.Now().UTC()
	receive := func(wire string, own, historical bool) domain.ID {
		t.Helper()
		at = at.Add(time.Second)
		if err := e.Receive(ctx, application.Incoming{
			AccountID: chat.AccountID, Address: chat.Address, OriginalText: wire, ProviderID: wire,
			ReadReceiptID: wire, CreatedAt: at, IsOwn: own, Historical: historical,
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
	if got := mark(key, first); got.DisplayedMarker == nil || got.DisplayedMarker.RemoteID != "first" || got.State.UnreadCount != 0 {
		t.Fatal("expected a marker for first", got)
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
		got.DisplayedMarker.PolicyRevision != policy.Revision {
		t.Fatal("expected coalesced marker at newest message", got)
	}
	if current, err := e.IsCurrentDisplayedMarker(ctx, *got.DisplayedMarker); err != nil || !current {
		t.Fatal("expected eligible queued marker", current, err)
	}
	inherited, err := e.SetConversationReadReceipts(ctx, chat.ID, policy.Revision, nil)
	if err != nil || inherited.Override != nil || inherited.EffectiveEnabled {
		t.Fatal(inherited, err)
	}
	if current, err := e.IsCurrentDisplayedMarker(ctx, *got.DisplayedMarker); err != nil || current {
		t.Fatal("stale marker survived a policy change", current, err)
	}
	if _, err := e.SetConversationReadReceipts(ctx, chat.ID, policy.Revision, new(false)); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale conversation setting was accepted", err)
	}
}
