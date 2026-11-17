package xmpp

import (
	"bytes"
	"encoding/xml"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	"strings"
	"testing"
)

func TestRoomReceiptEligibility(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	slot.snapshot.State = "joined"
	slot.ownOccupantID = "own"
	e.occupantScopes = map[string]bool{slot.conversation.Address: true}
	for _, tc := range []struct {
		name, extension, wantReceiptID string
	}{
		{"other occupant", `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="guest"/>`, "trusted-room-id"},
		{"same account on a second resource", `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="own"/>`, ""},
		{"unverified occupant", "", ""},
		{"displayed with a body", `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="guest"/><displayed xmlns="urn:xmpp:chat-markers:0" id="earlier"/>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host.received = nil
			raw := `<message xmlns="jabber:client" from="room@conference.example.test/AnotherNickname" type="groupchat" id="wire"><body>example</body>` +
				`<stanza-id xmlns="urn:xmpp:sid:0" by="room@conference.example.test" id="trusted-room-id"/>` + tc.extension + `</message>`
			decoder := xml.NewDecoder(strings.NewReader(raw))
			token, _ := decoder.Token()
			start := token.(xml.StartElement)
			var sink bytes.Buffer
			if err := m.handle(t.Context(), e, tokens{decoder, xml.NewEncoder(&sink)}, &start); err != nil {
				t.Fatal(err)
			}
			if len(host.received) != 1 || host.received[0].ReadReceiptID != tc.wantReceiptID {
				t.Fatalf("wrong marker eligibility: %+v", host.received)
			}
		})
	}
}

func TestDiscoAdvertisesDisplayedMarkers(t *testing.T) {
	m, e, _, _ := roomFixture(t)
	body, err := contactPushWire(t, m, e, `<iq xmlns="jabber:client" type="get" id="client-disco"><query xmlns="http://jabber.org/protocol/disco#info"/></iq>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `var="urn:xmpp:chat-markers:0"`) {
		t.Fatal("displayed markers missing from disco result", body)
	}
}

func TestUnsubscribeRevokesAudienceBeforeRosterPush(t *testing.T) {
	_, e, slot, _ := roomFixture(t)
	peer := jid.MustParse("peer@example.test")
	e.readReceiptAudience = map[string]bool{peer.String(): true}
	slot.conversation.Kind = "direct"
	slot.conversation.Address = peer.String()
	captureContactRequest(e, stanza.Presence{From: peer, Type: stanza.UnsubscribePresence})
	if _, allowed := e.displayedDestination(slot.conversation); allowed || e.rosterPushGeneration == 0 {
		t.Fatal("an observed unsubscribe left the peer authorized or a snapshot unguarded")
	}
}
