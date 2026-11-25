package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type protectedRoomArchiveHost struct {
	*testArchiveHost
	received []application.Incoming
}

func (h *protectedRoomArchiveHost) Receive(ctx context.Context, in application.Incoming) error {
	h.received = append(h.received, in)
	if in.Moderation != nil || in.Retraction != nil {
		return nil
	}
	return h.testArchiveHost.Receive(ctx, in)
}

func (h *protectedRoomArchiveHost) RetainedOMEMO(_ context.Context, in application.Incoming, _ string) (bool, error) {
	for _, saved := range h.received {
		if saved.Encrypted && saved.ProviderID == in.ProviderID {
			return true, nil
		}
	}
	return false, nil
}

func TestRoomOMEMOArchiveReopensAndContinuesPastVerificationGap(t *testing.T) {
	ctx := t.Context()
	cutoff := time.Now().UTC()
	caught, held := cutoff.Add(-40*time.Minute), cutoff.Add(-30*time.Minute)
	newer, notice := cutoff.Add(-20*time.Minute), cutoff.Add(-10*time.Minute)
	const alice = "alice@example.test"
	const bob = "bob@example.test"
	const eve = "eve@example.test"
	aliceDevice := newRoomArchiveTestDevice(t)
	bobDevice := newRoomArchiveTestDevice(t)
	eveDevice := newRoomArchiveTestDevice(t)
	otherDevice := newRoomArchiveTestDevice(t)
	heldWire := roomArchiveTestEnvelope(t, bobDevice, alice, aliceDevice, "held encrypted text")
	knownWire := roomArchiveTestEnvelope(t, eveDevice, alice, aliceDevice, "recoverable encrypted text")
	otherWire := roomArchiveTestEnvelope(t, eveDevice, alice, otherDevice, "another device only")
	verifyRoomArchiveTestSender(t, aliceDevice, eve, eveDevice)
	const control = `<retract xmlns="urn:xmpp:message-retract:1" id="retained-room-id"><moderated xmlns="urn:xmpp:message-moderate:1"/></retract>`
	m, e, base := archiveFixture(t, func(iq testArchiveIQ, n int) string {
		switch n {
		case 0:
			if iq.field("start") == "" || iq.Query.Set.Before != "" {
				t.Error("completed plaintext coverage was reused for encrypted recovery")
			}
			return protectedRoomArchivePage(iq, false, []protectedRoomArchiveRecord{
				{held, "awaiting-bob", "/Bob", bob, heldWire},
				{newer, "other-device", "/Eve", eve, otherWire},
				{notice, "room-notice", "", "", control},
			})
		case 1:
			if iq.Query.Set.Before != "awaiting-bob" {
				t.Error("normal traversal failed to advance past the unavailable key")
			}
			return protectedRoomArchivePage(iq, true, []protectedRoomArchiveRecord{{caught, "recoverable", "/Eve", eve, knownWire}})
		case 2:
			return iq.page(true) // No archive older than the bootstrap window.
		case 3, 4:
			if iq.field("start") != held.Format(time.RFC3339Nano) || iq.field("end") != held.Format(time.RFC3339Nano) {
				t.Error("retry window included a message for another device")
			}
			return protectedRoomArchivePage(iq, true, []protectedRoomArchiveRecord{{held, "awaiting-bob", "/Bob", bob, heldWire}})
		default:
			t.Error("unexpected archive request")
			return iq.page(true)
		}
	})
	host := &protectedRoomArchiveHost{testArchiveHost: base}
	m.host = host
	e.omemoDevice = aliceDevice
	if err := aliceDevice.SetRoomEncryption(base.room.Address, true); err != nil {
		t.Fatal(err)
	}
	slot := e.rooms[base.room.Address]
	slot.session = e.session
	slot.snapshot.State = "joined"
	slot.management.self.affiliation = "member"
	e.roomConfigurations = map[string]roomConfiguration{base.room.Address: {omemo: true}}
	e.archiveCapabilities = map[string]roomArchiveCapability{base.room.Address: {mam: true, textSearch: true}}
	base.archive.RoomArchiveProgress = domain.RoomArchiveProgress{
		InitialComplete: true, RecentComplete: true, OlderComplete: true, CaughtUpThrough: cutoff,
	}
	turn := func() (domain.RoomArchiveResult, error) {
		return m.roomArchiveTurn(ctx, ctx, host, e, e.session, base.room, domain.RoomArchiveRequest{}, archiveAutomatic, cutoff)
	}
	for range 3 {
		if result, err := turn(); err != nil || result.Outcome != "stored" || !result.Archive.Protected {
			t.Fatalf("protected archive did not advance: %+v %v", result, err)
		}
	}
	if len(host.received) != 2 || host.received[0].Moderation == nil || host.received[0].Moderation.StanzaID != "retained-room-id" ||
		host.received[1].OriginalText != "recoverable encrypted text" {
		t.Fatalf("mixed archive did not retain withdrawal and decryptable history: %+v", host.received)
	}
	if result, err := turn(); !errors.Is(err, errOMEMOHistoryIncomplete) || result.Archive.DeferredOMEMO == nil {
		t.Fatalf("remaining gap was not retained: %+v %v", result, err)
	}
	verifyRoomArchiveTestSender(t, aliceDevice, bob, bobDevice)
	if result, err := turn(); err != nil || result.Archive.DeferredOMEMO != nil || len(host.received) != 3 ||
		host.received[2].OriginalText != "held encrypted text" {
		t.Fatalf("verification did not fill just the unresolved gap: %+v %v %+v", result, err, host.received)
	}
}

type protectedRoomArchiveRecord struct {
	stamp                            time.Time
	id, fromSuffix, realSender, body string
}

func protectedRoomArchivePage(iq testArchiveIQ, complete bool, records []protectedRoomArchiveRecord) string {
	var wire strings.Builder
	room := iq.To.String()
	for _, record := range records {
		fmt.Fprintf(&wire, `<message xmlns="jabber:client" from="%s"><result xmlns="urn:xmpp:mam:2" id="%s" queryid="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s%s" id="%s" type="groupchat"><body>Untrusted fallback</body>`,
			room, record.id, iq.Query.ID, record.stamp.Format(time.RFC3339Nano), room, record.fromSuffix, record.id)
		if record.realSender != "" {
			fmt.Fprintf(&wire, `<x xmlns="http://jabber.org/protocol/muc#user"><item affiliation="member" jid="%s"/></x>`, record.realSender)
		}
		wire.WriteString(record.body + "</message></forwarded></result></message>")
	}
	fmt.Fprintf(&wire, `<iq xmlns="jabber:client" from="%s" type="result" id="%s"><fin xmlns="urn:xmpp:mam:2" complete="%t"><set xmlns="http://jabber.org/protocol/rsm"><first>%s</first><last>%s</last></set></fin></iq>`,
		room, iq.ID, complete, records[0].id, records[len(records)-1].id)
	return wire.String()
}

func newRoomArchiveTestDevice(t *testing.T) *omemo.Device {
	t.Helper()
	device, err := omemo.Open(filepath.Join(t.TempDir(), "device.json"))
	if err != nil {
		t.Fatal(err)
	}
	return device
}

func verifyRoomArchiveTestSender(t *testing.T, recipient *omemo.Device, senderAddress string, sender *omemo.Device) {
	t.Helper()
	id, fingerprint, err := sender.LocalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := recipient.Verify(senderAddress, id, fingerprint); err != nil {
		t.Fatal(err)
	}
}

func roomArchiveTestEnvelope(t *testing.T, sender *omemo.Device, address string, recipient *omemo.Device, body string) string {
	t.Helper()
	verifyRoomArchiveTestSender(t, sender, address, recipient)
	id, bundle, err := recipient.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := sender.Encrypt(t.Context(), []omemo.Target{{JID: address, DeviceID: id, Bundle: bundle}}, body)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := xml.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(wire)
}
