package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestDisplayedMarkersRespectUnreadGapsAndAccountPreference(t *testing.T) {
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
	prefs, err := e.SetAccountCommunicationPreferences(ctx, chat.AccountID, "", "available",
		application.AccountCommunicationToggles{SendReadReceipts: new(false)})
	if err != nil || prefs.SendReadReceipts {
		t.Fatal(prefs, err)
	}
	disabled := receive("while disabled", false, false)
	if got := mark(migrationID(), disabled); got.DisplayedMarker != nil || got.State.UnreadCount != 0 {
		t.Fatal("disabled receipt changed local read behavior", got)
	}
	prefs, err = e.SetAccountCommunicationPreferences(ctx, chat.AccountID, prefs.RevisionID, "available",
		application.AccountCommunicationToggles{SendReadReceipts: new(true)})
	if err != nil || !prefs.SendReadReceipts {
		t.Fatal(prefs, err)
	}
	if got := mark(migrationID(), disabled, receive("own", true, false), receive("archive", false, true)); got.DisplayedMarker != nil {
		t.Fatal("read of an already read, own or historical message attempted a marker", got)
	}
	older, newest := receive("older", false, false), receive("newest", false, false)
	if got := mark(migrationID(), older, newest); got.DisplayedMarker == nil || got.DisplayedMarker.RemoteID != "newest" ||
		got.DisplayedMarker.PreferencesRevision != prefs.RevisionID {
		t.Fatal("expected coalesced marker at newest message", got)
	}
}
