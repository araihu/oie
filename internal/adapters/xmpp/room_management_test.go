package xmpp

import (
	"encoding/xml"
	"testing"

	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestRoomManagementRejectsReusedNicknameAndChecksPermission(t *testing.T) {
	room, _ := jid.Parse("garden@conference.localhost")
	guest, _ := room.WithResource("Reader")
	owner, _ := room.WithResource("Owner")
	slot := &roomSlot{snapshot: domain.RoomSession{Nickname: "Owner"}}
	observe := func(from jid.JID, role, affiliation string, self, leaving bool) {
		p := incomingPresence{Presence: stanza.Presence{From: from}, MUC: &mucUser{}}
		p.MUC.Item.Role, p.MUC.Item.Affiliation = role, affiliation
		if leaving {
			p.Type = stanza.UnavailablePresence
		}
		observeRoomManagement(slot, p, self)
	}
	observe(owner, "moderator", "owner", true, false)
	observe(guest, "participant", "none", false, false)
	c := domain.Conversation{Address: room.String()}
	a := domain.Account{Address: "owner@localhost"}
	old := slot.management.occupants["Reader"].id
	cmd := domain.RoomManagementCommand{Action: "role", Target: old, Value: "none"}
	wire, code := slot.management.payload(a, c, cmd, domain.Message{})
	if code != "" || wire != roomAdminQuery+`<item nick="Reader" role="none"></item></query>` {
		t.Fatalf("kick: %s %s", code, wire)
	}
	observe(guest, "none", "none", false, true)
	observe(guest, "participant", "none", false, false)
	if _, code := slot.management.payload(a, c, cmd, domain.Message{}); code != "stale" {
		t.Fatalf("reused nickname was actionable: %s", code)
	}
	cmd.Target = slot.management.occupants["Reader"].id
	observe(owner, "participant", "member", true, false)
	if _, code := slot.management.payload(a, c, cmd, domain.Message{}); code != "denied" {
		t.Fatalf("permission change was ignored: %s", code)
	}
}

func TestArchiveModerationTrustAndTombstone(t *testing.T) {
	const room = "garden@conference.localhost"
	const account = "alice@localhost"
	for _, tc := range []struct {
		name, scope, from, payload string
		want, tombstone            bool
	}{
		{"room event", room, room, `<retract xmlns="urn:xmpp:message-retract:1" id="original"><moderated xmlns="urn:xmpp:message-moderate:1"/><reason>Room rules</reason></retract>`, true, false},
		{"occupant spoof", room, room + "/Reader", `<retract xmlns="urn:xmpp:message-retract:1" id="original"><moderated xmlns="urn:xmpp:message-moderate:1"/></retract>`, false, false},
		{"foreign archive", account, room, `<retract xmlns="urn:xmpp:message-retract:1" id="original"><moderated xmlns="urn:xmpp:message-moderate:1"/></retract>`, false, false},
		{"room tombstone", room, room + "/Reader", `<retracted xmlns="urn:xmpp:message-retract:1" stamp="2026-11-13T16:00:01Z"><moderated xmlns="urn:xmpp:message-moderate:1"/></retracted>`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := `<result id="archive-id"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="2026-11-13T16:00:00Z"/><message xmlns="jabber:client" from="` + tc.from + `" type="groupchat">` + tc.payload + `</message></forwarded></result>`
			var result archiveResult
			if err := xml.Unmarshal([]byte(wire), &result); err != nil {
				t.Fatal(err)
			}
			in, ok, err := archivedIncoming(domain.Account{Address: account}, tc.scope, &result, archiveTrust{})
			if err != nil || ok != tc.want || in.ModerationTombstone != tc.tombstone {
				t.Fatalf("trust: %+v %v %v", in, ok, err)
			}
			if tc.tombstone && (in.Moderation.StanzaID != result.ID || in.ArchiveID != result.ID) {
				t.Fatalf("wrong tombstone identity: %+v", in)
			}
		})
	}
}
