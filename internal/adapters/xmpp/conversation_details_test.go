package xmpp

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestRoomDetailsSeparateTopicCountAndVisibleOccupants(t *testing.T) {
	var raw disco.Info
	xmlInfo := `<query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text" name="Room &amp; friends"/><feature var="muc_membersonly"/><feature var="muc_persistent"/><x xmlns="jabber:x:data" type="result"><field var="FORM_TYPE" type="hidden"><value>http://jabber.org/protocol/muc#roominfo</value></field><field var="muc#roominfo_description"><value>Read &lt;this&gt;</value></field><field var="muc#roominfo_subject"><value>Current topic 🍰</value></field><field var="muc#roominfo_occupants"><value>37</value></field></x></query>`
	if err := xml.Unmarshal([]byte(xmlInfo), &raw); err != nil {
		t.Fatal(err)
	}
	info := parseRoomInfo(raw)
	if info.Name != "Room & friends" || info.Topic != "Current topic 🍰" || info.Description != "Read <this>" || info.OccupantCount == nil || *info.OccupantCount != 37 || info.RoomSession != "not_joined" {
		t.Fatal(info)
	}
	participants := parseOccupants("room@conference.localhost", `<query xmlns="http://jabber.org/protocol/disco#items"><item jid="room@conference.localhost/nick"/><item jid="other@conference.localhost/stranger"/><item jid="room@conference.localhost/nick"/><item jid="room@conference.localhost/person@elsewhere" name="Display name"/></query>`)
	if participants.Scope != "occupants" || participants.State != "partial" || len(participants.Items) != 2 || participants.Items[1].DisplayName != "Display name" {
		t.Fatal(participants)
	}
	for _, p := range participants.Items {
		if p.Source != "discovery" || p.Presence.State != "unknown" || !p.Presence.ObservedAt.IsZero() {
			t.Fatal("discovery invented presence", p)
		}
	}
	for _, value := range []string{"-1", "NaN", "2147483648"} {
		var bad disco.Info
		_ = xml.Unmarshal([]byte(strings.Replace(xmlInfo, ">37<", ">"+value+"<", 1)), &bad)
		if parseRoomInfo(bad).OccupantCount != nil {
			t.Fatal("invalid count became occupancy", value)
		}
	}
}
func TestOccupantListsAreBoundedAndNeverPersonalIdentities(t *testing.T) {
	var text strings.Builder
	text.WriteString(`<query xmlns="http://jabber.org/protocol/disco#items">`)
	for i := range 205 {
		fmt.Fprintf(&text, `<item jid="room@conference.localhost/nick%d"/>`, i)
	}
	text.WriteString(`</query>`)
	p := parseOccupants("room@conference.localhost", text.String())
	if len(p.Items) != 200 || !p.Truncated || p.State != "partial" {
		t.Fatal(len(p.Items), p.State)
	}
	if got := parseOccupants("room@conference.localhost", `<query xmlns="other"><item jid="room@conference.localhost/nick"/></query>`); got.State != "unavailable" || len(got.Items) != 0 {
		t.Fatal("foreign namespace accepted")
	}
}
func TestDetailCacheDeduplicatesAndClearsOnDisconnect(t *testing.T) {
	a := domain.Account{ID: "owner"}
	c := domain.Conversation{ID: "conversation", AccountID: a.ID, Kind: "room", Address: "room@conference.localhost"}
	e := &entry{session: new(mx.Session), state: domain.Connection{State: "connected"}, detailsWake: make(chan struct{}, 1)}
	manager := New(t.TempDir(), nil)
	manager.entries[a.ID] = e
	for range 10 {
		got, err := manager.ConversationInfo(t.Context(), a, c)
		if err != nil || got.State != "pending" {
			t.Fatal(got, err)
		}
	}
	if len(e.detailsWake) != 1 || len(e.details) != 1 {
		t.Fatal("reads queued repeated protocol work")
	}
	slot := e.details[c.ID]
	slot.info = parseRoomInfo(disco.Info{})
	slot.info.Name = "Cached room"
	slot.info.Participants.Items = []domain.ConversationParticipant{{ID: "opaque", DisplayName: "Nickname"}}
	snapshot, _ := manager.ConversationInfo(t.Context(), a, c)
	snapshot.Participants.Items[0].DisplayName = "changed"
	if slot.info.Participants.Items[0].DisplayName != "Nickname" {
		t.Fatal("snapshot aliases cache")
	}
	// A reconnect clears data via publish; disconnected getters must also hide any old snapshot.
	e.state.State = "not_connected"
	off, _ := manager.ConversationInfo(t.Context(), a, c)
	if off.State != "not_connected" || off.Name != "" || len(off.Participants.Items) != 0 {
		t.Fatal("stale metadata leaked", off)
	}
}
func TestObservedPeerPresenceNeverGuessesOfflineOrOccupantIdentity(t *testing.T) {
	e := &entry{state: domain.Connection{State: "connected"}, avatarTargets: map[string]string{"bob@localhost": "direct"}}
	from, _ := jid.Parse("bob@localhost/phone")
	capturePeerPresence(e, incomingPresence{Presence: stanza.Presence{From: from}, Show: "away", Status: "Lunch"})
	got := peerPresence(e, "bob@localhost")
	if got.State != "away" || got.StatusText != "Lunch" || got.ObservedAt.IsZero() {
		t.Fatal(got)
	}
	laptop, _ := jid.Parse("bob@localhost/laptop")
	capturePeerPresence(e, incomingPresence{Presence: stanza.Presence{From: laptop}, Show: "dnd", Priority: "10"})
	capturePeerPresence(e, incomingPresence{Presence: stanza.Presence{From: from, Type: stanza.UnavailablePresence}})
	if got = peerPresence(e, "bob@localhost"); got.State != "busy" {
		t.Fatal("one device hid another", got)
	}
	capturePeerPresence(e, incomingPresence{Presence: stanza.Presence{From: laptop, Type: stanza.UnavailablePresence}})
	if got = peerPresence(e, "bob@localhost"); got.State != "unavailable" {
		t.Fatal(got)
	}
	malformed, _ := jid.Parse("bob@localhost/malformed")
	capturePeerPresence(e, incomingPresence{Presence: stanza.Presence{From: malformed}, Priority: strings.Repeat("9", 100)})
	if peerPresence(e, "bob@localhost").State != "unavailable" {
		t.Fatal("invalid priority invented available presence")
	}
	stranger, _ := jid.Parse("stranger@localhost/device")
	capturePeerPresence(e, incomingPresence{Presence: stanza.Presence{From: stranger}})
	if peerPresence(e, "stranger@localhost").State != "unknown" {
		t.Fatal("unsolicited presence trusted")
	}
	room, _ := jid.Parse("room@conference.localhost/name@localhost")
	capturePeerPresence(e, incomingPresence{Presence: stanza.Presence{From: room}, MUC: &struct{}{}})
	if peerPresence(e, room.Bare().String()).State != "unknown" {
		t.Fatal("occupant nick became a person")
	}
	if !got.ObservedAt.Before(time.Now().Add(time.Second)) {
		t.Fatal("non-observed timestamp")
	}
}

func TestPreviousDetailsWorkerCannotConsumeCurrentSessionQueue(t *testing.T) {
	current, previous := new(mx.Session), new(mx.Session)
	oldWake, newWake := make(chan struct{}, 1), make(chan struct{}, 1)
	oldWake <- struct{}{}
	newWake <- struct{}{}
	slot := &detailSlot{queued: true}
	e := &entry{session: current, state: domain.Connection{State: "connected"}, detailsWake: newWake, details: map[domain.ID]*detailSlot{"current": slot}}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	manager := New(t.TempDir(), nil)
	manager.detailsLoop(ctx, e, previous, oldWake)
	if !slot.queued || len(newWake) != 1 {
		t.Fatal("old worker consumed current session request")
	}
}
