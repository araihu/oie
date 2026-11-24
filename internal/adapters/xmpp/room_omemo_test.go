package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestRoomOMEMOSendRequiresDevice(t *testing.T) {
	const room = "private@rooms.example.org"
	account := domain.Account{ID: "test-account", Address: "owner@example.org"}
	s := &mx.Session{}
	raw, peer := net.Pipe()
	defer raw.Close()
	defer peer.Close()
	e := &entry{session: s, raw: raw, state: domain.Connection{State: "connected"},
		rooms: map[string]*roomSlot{room: {session: s, snapshot: domain.RoomSession{State: "joined"}}}}
	m := &Manager{omemoDir: t.TempDir(), entries: map[domain.ID]*entry{account.ID: e}}
	out := application.Outgoing{ID: "queued", Kind: "room", Address: room, DirectEncryption: "omemo", OriginalText: "protected"}
	out.RecordWireAttempt = func(context.Context, string, ...domain.WireMentionProof) error {
		t.Error("an encrypted message without a device reached the wire attempt")
		return errors.New("stop before encoding")
	}
	if outcome := m.Send(t.Context(), account, out); outcome != application.NotSent {
		t.Fatal(outcome)
	}
}

func TestRoomOMEMOUsesAuthenticatedOccupantAndFailsClosed(t *testing.T) {
	const room = "private@rooms.example.org"
	const sender = "member@example.org"
	account := domain.Account{Address: "owner@example.org"}
	session := &mx.Session{}
	slot := &roomSlot{session: session, snapshot: domain.RoomSession{State: "joined", Nickname: "owner"},
		management: roomManagement{self: roomOccupant{affiliation: "owner"},
			occupants: map[string]roomOccupant{"member": {address: sender, affiliation: "member"}}}}
	e := &entry{account: account, session: session, state: domain.Connection{State: "connected"},
		rooms:              map[string]*roomSlot{room: slot},
		roomConfigurations: map[string]roomConfiguration{room: {omemo: true}}}
	device, err := omemo.Open(filepath.Join(t.TempDir(), "recipient"))
	if err != nil {
		t.Fatal(err)
	}
	e.omemoDevice = device
	if err := device.SetRoomEncryption(room, true); err != nil {
		t.Fatal(err)
	}
	donor, err := omemo.Open(filepath.Join(t.TempDir(), "sender"))
	if err != nil {
		t.Fatal(err)
	}
	id, bundle, err := device.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	fp, err := bundle.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := donor.Verify(account.Address, id, fp); err != nil {
		t.Fatal(err)
	}
	sid, fp, err := donor.LocalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Verify(sender, sid, fp); err != nil {
		t.Fatal(err)
	}
	env, err := donor.Encrypt(t.Context(), []omemo.Target{{JID: account.Address, DeviceID: id, Bundle: bundle}}, "room ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	host := &hostStub{}
	m := &Manager{host: host}
	msg := stanza.Message{From: jid.MustParse(room + "/member"), Type: stanza.GroupChatMessage}
	for _, forged := range []jid.JID{jid.MustParse("other@rooms.example.org/member"), jid.MustParse(room + "/unknown")} {
		bad := msg
		bad.From = forged
		if err := m.receiveRoomOMEMO(t.Context(), e, bad, replyMetadata{}, env, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	e.roomConfigurations[room] = roomConfiguration{pending: true}
	if err := m.receiveRoomOMEMO(t.Context(), e, msg, replyMetadata{}, env, "", ""); err != nil || len(host.received) != 0 {
		t.Fatal("accepted an unconfirmed room sender or configuration", err)
	}
	e.roomConfigurations[room] = roomConfiguration{omemo: true}
	if err := m.receiveRoomOMEMO(t.Context(), e, msg, replyMetadata{}, env, "", ""); err != nil {
		t.Fatal(err)
	}
	if len(host.received) != 1 || host.received[0].Address != room || host.received[0].SenderAddress != sender ||
		host.received[0].OriginalText != "room ciphertext" || !host.received[0].Encrypted {
		t.Fatal(host.received)
	}
}

func TestRoomOMEMOArchiveAuthenticatesOriginalAffiliation(t *testing.T) {
	const room = "private@rooms.example.org"
	makeRecord := func(affiliation string) *archiveResult {
		t.Helper()
		var r archiveResult
		err := xml.Unmarshal([]byte(`<result xmlns="urn:xmpp:mam:2" id="archive"><forwarded xmlns="urn:xmpp:forward:0"><message xmlns="jabber:client" from="`+room+`/member" type="groupchat"><encrypted xmlns="eu.siacs.conversations.axolotl"><header sid="1"/><payload>aa</payload></encrypted><x xmlns="http://jabber.org/protocol/muc#user"><item affiliation="`+affiliation+`" jid="member@example.org"/></x></message></forwarded></result>`), &r)
		if err != nil {
			t.Fatal(err)
		}
		return &r
	}
	for _, affiliation := range []string{"member", "none"} {
		record, ok := archiveRoomOMEMO(domain.Account{Address: "owner@example.org"}, &archiveQuery{Scope: room}, makeRecord(affiliation), time.Now())
		if !ok || (record.sender != "") != (affiliation == "member") {
			t.Fatalf("wrong sender evidence for %q: %+v", affiliation, record.in)
		}
	}
}
