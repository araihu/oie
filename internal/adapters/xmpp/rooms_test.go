package xmpp

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"

	"net"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"

	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	"mellium.im/xmpp/stream"
)

type roomHostStub struct {
	hostStub
	changes []domain.RoomSession
}

func (h *roomHostStub) RoomSessionChanged(_ context.Context, s domain.RoomSession) error {
	h.changes = append(h.changes, s)
	return nil
}
func roomFixture(t *testing.T) (*Manager, *entry, *roomSlot, *roomHostStub) {
	t.Helper()
	host := &roomHostStub{}
	m := New(t.TempDir(), nil)
	m.host = host
	a := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}
	c := domain.Conversation{ID: "019a5df0-0000-7000-8000-000000000002", AccountID: a.ID, Address: "room@conference.example.test", Kind: "room"}
	s := &mx.Session{}
	slot := &roomSlot{conversation: c, snapshot: roomSnapshot(c, "joining", "Oie", "", "019a5df0-0000-7000-8000-000000000003"), session: s, requestID: "join-request", joinDeadline: time.Now().Add(time.Second)}
	e := &entry{account: a, session: s, state: domain.Connection{AccountID: a.ID, State: "connected"}, rooms: map[string]*roomSlot{c.Address: slot}, sidScopes: map[string]bool{c.Address: true}}
	m.entries[a.ID] = e
	return m, e, slot, host
}
func feedRoomPresence(t *testing.T, m *Manager, e *entry, raw string) {
	t.Helper()
	var p incomingPresence
	if err := xml.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.captureRoomPresence(t.Context(), e, p); err != nil {
		t.Fatal(err)
	}
}
func TestRoomJoinRequiresScopedSelfPresenceAndInvalidatesLeave(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	base := `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" id="join-request">`
	feedRoomPresence(t, m, e, base+`<x xmlns="http://jabber.org/protocol/muc#user"/></presence>`)
	if slot.snapshot.State != "joining" || len(host.changes) != 0 {
		t.Fatal("non-self presence confirmed join")
	}
	feedRoomPresence(t, m, e, strings.Replace(base, "join-request", "obsolete-request", 1)+`<x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/></x></presence>`)
	if slot.snapshot.State != "joining" {
		t.Fatal("obsolete join presence confirmed current intent")
	}
	feedRoomPresence(t, m, e, `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" type="error" id="obsolete-request"><error><conflict xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></presence>`)
	feedRoomPresence(t, m, e, `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" type="unavailable"><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/><status code="307"/></x></presence>`)
	if slot.snapshot.State != "joining" || len(host.changes) != 0 {
		t.Fatal("retired attempt changed a newer joining intent")
	}
	feedRoomPresence(t, m, e, base+`<x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/></x></presence>`)
	if slot.snapshot.State != "joined" || len(host.changes) != 1 || host.changes[0].IntentRevisionID != slot.snapshot.IntentRevisionID {
		t.Fatal("confirmed session lost binding")
	}
	// A new generation owns this address. An obsolete slot cannot publish into it.
	old := slot
	fresh := *slot
	fresh.snapshot = roomSnapshot(slot.conversation, "not_joined", "Oie", "", "019a5df0-0000-7000-8000-000000000004")
	fresh.leaving = true
	e.rooms[slot.conversation.Address] = &fresh
	m.notifyRoom(t.Context(), e, old, old.snapshot)
	if len(host.changes) != 1 {
		t.Fatal("retired slot published a session")
	}
	feedRoomPresence(t, m, e, base+`<x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/></x></presence>`)
	if fresh.snapshot.State != "not_joined" || len(host.changes) != 1 {
		t.Fatal("late join response undid leave")
	}
}
func TestRoomPermissionFailuresCarryTerminalObservation(t *testing.T) {
	for _, tc := range []struct {
		name, raw, code string
		revoked         bool
	}{
		{"conflict", `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" type="error"><error><conflict xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></presence>`, "nickname_conflict", true},
		{"forbidden", `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" type="error"><error><forbidden xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></presence>`, "forbidden", true},
		{"kicked", `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" type="unavailable"><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/><status code="307"/></x></presence>`, "removed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, e, slot, host := roomFixture(t)
			if tc.name == "kicked" {
				slot.snapshot.State = "joined"
			}
			feedRoomPresence(t, m, e, tc.raw)
			if slot.snapshot.State != "failed" || slot.snapshot.LastErrorCode != tc.code || slot.snapshot.Revoked != tc.revoked || len(host.changes) != 1 {
				t.Fatal("room failure was not truthful", slot.snapshot)
			}
		})
	}
}
func TestRoomOwnEchoHintsCanonicalSourceAndUsesRoomSID(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	slot.snapshot.State = "joined"
	outID := domain.ID("019a5df0-0000-7000-8000-000000000005")
	raw := `<message xmlns="jabber:client" from="room@conference.example.test/Oie" type="groupchat" id="wire-client"><body>my text</body><origin-id xmlns="urn:xmpp:sid:0" id="` + string(outID) + `"/><stanza-id xmlns="urn:xmpp:sid:0" by="room@conference.example.test" id="server-room-id"/></message>`
	decoder := xml.NewDecoder(strings.NewReader(raw))
	token, _ := decoder.Token()
	start := token.(xml.StartElement)
	var sink bytes.Buffer
	if err := m.handle(t.Context(), e, tokens{decoder, xml.NewEncoder(&sink)}, &start); err != nil {
		t.Fatal(err)
	}
	if len(host.received) != 1 {
		t.Fatal("own live echo lost")
	}
	in := host.received[0]
	if !in.IsOwn || in.Kind != "room" || in.CanonicalID != outID || in.Address != slot.conversation.Address || len(in.References) != 1 || in.References[0].Scope != "room:"+slot.conversation.Address || in.References[0].ID != "server-room-id" {
		t.Fatal("self echo lost canonical scope", in)
	}
	// Another occupant cannot nominate an own-message UUID even if it copies origin-id.
	from, _ := jid.Parse(slot.conversation.Address + "/Other")
	err := m.receiveRoomMessage(t.Context(), e, structMessage(from), replyMetadata{Origins: []wireID{{ID: string(outID)}}}, "my text", "", "", fileSharingMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if host.received[1].CanonicalID != "" || host.received[1].IsOwn {
		t.Fatal("foreign occupant forged own echo")
	}
	// A retained room is not membership: groupchat before self confirmation is ignored.
	slot.snapshot.State = "not_joined"
	before := len(host.received)
	_ = m.receiveRoomMessage(t.Context(), e, structMessage(from), replyMetadata{}, "ignored", "", "", fileSharingMetadata{})
	if len(host.received) != before {
		t.Fatal("unjoined room accepted live traffic")
	}
	var _ application.Protocols = m
}

func structMessage(from jid.JID) stanza.Message {
	return stanza.Message{From: from, Type: stanza.GroupChatMessage, ID: "other-wire"}
}

func TestRoomErrorsRequireProviderAuthority(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	slot.snapshot.State = "joined"
	out := domain.ID("019a5df0-0000-7000-8000-000000000005")
	e.ownRoomMessages = map[string]ownRoomMessage{string(out): {address: slot.conversation.Address, body: "sent"}}
	for _, from := range []string{slot.conversation.Address + "/Other", "eve@example.test/phone", "eve@example.test", slot.conversation.Address, "conference.example.test"} {
		decoder := xml.NewDecoder(strings.NewReader(`<message xmlns="jabber:client" from="` + from + `" type="error" id="` + string(out) + `"><error><forbidden xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></message>`))
		token, _ := decoder.Token()
		start := token.(xml.StartElement)
		var sink bytes.Buffer
		if err := m.handle(t.Context(), e, tokens{decoder, xml.NewEncoder(&sink)}, &start); err != nil {
			t.Fatal(err)
		}
	}
	if len(host.rejected) != 2 || host.rejected[0].Address != slot.conversation.Address || host.rejected[0].FromServer || host.rejected[1].Address != slot.conversation.Address || !host.rejected[1].FromServer {
		t.Fatal("occupant or unrelated peer proved room rejection", host.rejected)
	}
}
func TestRoomSendNeverFallsBackToDirectOrOldIntent(t *testing.T) {
	m, e, slot, _ := roomFixture(t)
	left, right := net.Pipe()
	defer func() { _ = left.Close(); _ = right.Close() }()
	e.raw = left
	for _, state := range []string{"joining", "not_joined", "failed", "joined"} {
		slot.snapshot.State = state
		out := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000005", Address: slot.conversation.Address, OriginalText: "must not dispatch", Kind: "room", RoomIntentRevisionID: "019a5df0-0000-7000-8000-000000000006"}
		if got := m.Send(t.Context(), e.account, out); got != application.NotSent {
			t.Fatal("room send escaped current verified membership", state, got)
		}
	}
}

func TestRoomJoinTimeoutAndDisconnectedObservationKeepIntentBinding(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	binding := slot.snapshot.IntentRevisionID
	slot.joinDeadline = time.Now().Add(-time.Second)
	snapshot, err := m.RoomSession(t.Context(), e.account, slot.conversation)
	if err != nil || snapshot.State != "failed" || snapshot.LastErrorCode != "timeout" || snapshot.IntentRevisionID != binding || len(host.changes) != 1 {
		t.Fatal("join timeout lost its intent or notification", snapshot, err)
	}
	_, _ = m.RoomSession(t.Context(), e.account, slot.conversation)
	if len(host.changes) != 1 {
		t.Fatal("timeout replayed its callback")
	}
	slot.snapshot.State = "joined"
	e.session = nil
	snapshot, err = m.RoomSession(t.Context(), e.account, slot.conversation)
	if err != nil || snapshot.State != "not_joined" || snapshot.LastErrorCode != "not_connected" || snapshot.IntentRevisionID != binding {
		t.Fatal("retired connection still proved membership", snapshot, err)
	}
}

type roomPrivacyTestHost struct {
	roomHostStub
	privacy application.RoomReactionPrivacy
}

func (h *roomPrivacyTestHost) SetRoomReactionPrivacy(_ context.Context, _ domain.ID, _ string, privacy application.RoomReactionPrivacy) error {
	h.privacy = privacy
	return nil
}

func TestNonanonymousReactionPrivacyRequiresCurrentPresence(t *testing.T) {
	for _, action := range []string{"unjoined discovery", "leave", "kick"} {
		t.Run(action, func(t *testing.T) {
			m, e, slot, _ := roomFixture(t)
			host := &roomPrivacyTestHost{}
			m.host = host
			room := slot.conversation.Address
			slot.snapshot.State = "joined"
			if action == "unjoined discovery" {
				slot.snapshot.State = "not_joined"
			}
			disco := `<query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="muc_nonanonymous"/><feature var="urn:xmpp:sid:0"/></query>`
			if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, room, disco, 0); err != nil {
				t.Fatal(err)
			}
			if host.privacy.Public != (action != "unjoined discovery") || host.privacy.Mode != application.RoomReactionsNonanonymous {
				t.Fatal("discovery applied the wrong privacy", host.privacy)
			}
			switch action {
			case "leave":
				left, right := net.Pipe()
				t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
				session, err := mx.NewSession(t.Context(), jid.MustParse("example.test"), jid.MustParse("alice@example.test/test"), left, mx.Ready, func(context.Context, *stream.Info, *stream.Info, *mx.Session, any) (mx.SessionState, io.ReadWriter, any, error) {
					return mx.Ready, nil, nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				e.session, e.raw, slot.session = session, left, session
				received := make(chan stanza.Presence, 1)
				go func() {
					_ = right.SetReadDeadline(time.Now().Add(time.Second))
					var p stanza.Presence
					_ = xml.NewDecoder(right).Decode(&p)
					received <- p
				}()
				if _, err := m.LeaveRoom(t.Context(), e.account, slot.conversation, slot.snapshot.IntentRevisionID); err != nil {
					t.Fatal(err)
				}
				if p := <-received; p.Type != stanza.UnavailablePresence {
					t.Fatal("room did not receive an unavailable presence", p)
				}
			case "kick":
				feedRoomPresence(t, m, e, `<presence xmlns="jabber:client" from="`+room+`/Oie" type="unavailable"><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/><status code="307"/></x></presence>`)
			}
			if host.privacy.Public {
				t.Fatal("room without presence can expose peer addresses")
			}
			if action != "unjoined discovery" {
				if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, room, disco, 0); err == nil {
					t.Fatal("discovery started before losing presence restored visibility")
				}
			}
		})
	}
}

func TestRoomConfigurationRemovalPreventsJoinedObservation(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	name := "Pending creation"
	slot.creationName = &name
	slot.configuring = true
	feedRoomPresence(t, m, e, `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" type="unavailable" id="room-event"><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/><status code="307"/></x></presence>`)
	if slot.snapshot.State != "failed" || !slot.snapshot.Revoked || slot.snapshot.LastErrorCode != "removed" || len(host.changes) != 1 {
		t.Fatalf("room removal during configuration was ignored: %+v", slot.snapshot)
	}
}

func TestRoomSlotLimitEvictsOnlyInactiveObservations(t *testing.T) {
	_, e, slot, _ := roomFixture(t)
	for len(e.rooms) < maxRoomSessions {
		key := fmt.Sprintf("old-%d", len(e.rooms))
		e.rooms[key] = &roomSlot{snapshot: domain.RoomSession{State: "failed", UpdatedAt: time.Now()}}
	}
	if !reserveRoomSlot(e, "next") || len(e.rooms) != maxRoomSessions-1 || e.rooms[slot.conversation.Address] != slot {
		t.Fatal("room limit rejected an entry despite inactive slots")
	}
}

func TestRoomPreviewDoesNotProbeUnadvertisedService(t *testing.T) {
	m, e, _, _ := roomFixture(t)
	e.account.Protocol = "xmpp"
	preview, _, err := m.PreviewRoom(t.Context(), e.account, "tracking-token@attacker.example", true)
	if err != nil || preview.State != "unavailable" {
		t.Fatalf("unexpected automatic room lookup result: %+v %v", preview, err)
	}
	if err := m.ValidateRoomNickname(e.account, "name\u200b"); err != application.ErrInvalid {
		t.Fatalf("invalid XMPP nickname accepted: %v", err)
	}
}
