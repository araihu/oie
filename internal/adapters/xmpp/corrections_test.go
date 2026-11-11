package xmpp

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/disco/info"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func correctionWire(t *testing.T, raw string) (stanza.Message, correctionMetadata) {
	t.Helper()
	var message struct {
		stanza.Message
		correctionMetadata
	}
	if err := xml.Unmarshal([]byte(raw), &message); err != nil {
		t.Fatal(err)
	}
	return message.Message, message.correctionMetadata
}
func TestCorrectionUsesClientIDAndRegistersOriginalSource(t *testing.T) {
	from, _ := jid.Parse("bob@example.test/phone")
	original := decodeCorrectionMetadata(correctionMetadata{}, stanza.Message{From: from, ID: "client-original"}, false, false)
	if original == nil || original.ID != "client-original" || original.TargetID != "" || original.Author != "bob@example.test" {
		t.Fatal("original client source lost", original)
	}
	raw := `<message xmlns="jabber:client" from="bob@example.test/tablet" id="client-edit"><origin-id xmlns="urn:xmpp:sid:0" id="origin-is-not-client-id"/><replace xmlns="urn:xmpp:message-correct:0" id="client-original"/></message>`
	msg, meta := correctionWire(t, raw)
	edit := decodeCorrectionMetadata(meta, msg, false, false)
	if edit == nil || edit.ID != "client-edit" || edit.TargetID != original.ID || edit.Author != original.Author || edit.Scope != original.Scope {
		t.Fatal("direct correction lost sender/client identity", edit)
	}
	for _, bad := range []string{`<replace xmlns="wrong" id="client-original"/>`, `<replace xmlns="urn:xmpp:message-correct:0" id=""/>`, `<replace xmlns="urn:xmpp:message-correct:0" id="client-edit"/>`, `<replace xmlns="urn:xmpp:message-correct:0" id="client-original">extra</replace>`, `<replace xmlns="urn:xmpp:message-correct:0" id="client-original"><extra/></replace>`, `<replace xmlns="urn:xmpp:message-correct:0" id="a"/><replace xmlns="urn:xmpp:message-correct:0" id="b"/>`} {
		message, metadata := correctionWire(t, `<message xmlns="jabber:client" from="bob@example.test/phone" id="client-edit">`+bad+`</message>`)
		got := decodeCorrectionMetadata(metadata, message, false, false)
		if got == nil || got.TargetID != "" {
			t.Fatal("malformed correction applied or lost its independent source", bad, got)
		}
	}
	for _, message := range []stanza.Message{{From: from}, {From: from, ID: strings.Repeat("a", 4097)}, {ID: "client-edit"}} {
		if got := decodeCorrectionMetadata(correctionMetadata{Replacements: []wireID{{ID: "client-original"}}}, message, false, false); got != nil {
			t.Fatal("missing sender/client identity acquired correction authority", got)
		}
	}
}
func TestRoomCorrectionRequiresVerifiedOccupantAndFullSender(t *testing.T) {
	from, _ := jid.Parse("room@conference.example.test/Bob")
	base := correctionMetadata{Occupants: []wireID{{ID: "server-occupant"}}}
	original := decodeCorrectionMetadata(base, stanza.Message{From: from, ID: "client-original"}, true, true)
	if original == nil || original.Author != from.String() || original.ID != "client-original" || original.TargetID != "" || strings.Contains(original.Scope, "server-occupant") {
		t.Fatal("room proof missing or exposed", original)
	}
	meta := base
	meta.Replacements = []wireID{{ID: "client-original"}}
	corrected := decodeCorrectionMetadata(meta, stanza.Message{From: from, ID: "client-edit"}, true, true)
	if corrected == nil || corrected.Scope != original.Scope || corrected.Author != original.Author || corrected.TargetID != original.ID {
		t.Fatal("valid room edit lost target", corrected)
	}
	for _, tc := range []struct {
		name    string
		from    string
		meta    correctionMetadata
		trusted bool
		wantNil bool
	}{
		{"not advertised", from.String(), meta, false, true}, {"missing occupant", from.String(), correctionMetadata{}, true, true},
		{"duplicate occupant", from.String(), correctionMetadata{Occupants: []wireID{{ID: "server-occupant"}, {ID: "server-occupant"}}}, true, true},
		{"oversized occupant", from.String(), correctionMetadata{Occupants: []wireID{{ID: strings.Repeat("a", 129)}}}, true, true},
		{"nested occupant", from.String(), correctionMetadata{Occupants: []wireID{{ID: "server-occupant", Children: []struct{}{{}}}}}, true, true},
		{"nick reused", from.String(), correctionMetadata{Occupants: []wireID{{ID: "different-actor"}}}, true, false},
		{"nick changed", "room@conference.example.test/Other", meta, true, false},
		{"other room", "other@conference.example.test/Bob", meta, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender, _ := jid.Parse(tc.from)
			got := decodeCorrectionMetadata(tc.meta, stanza.Message{From: sender, ID: "client-edit"}, true, tc.trusted)
			if tc.wantNil {
				if got != nil {
					t.Fatal("untrusted occupant proof accepted", got)
				}
			} else if got == nil || got.Scope == original.Scope && got.Author == original.Author {
				t.Fatal("different actor acquired original authority", got)
			}
		})
	}
}
func TestOccupantEvidenceIsBoundToDiscoveredRoomAndCurrentSession(t *testing.T) {
	current, old := &mx.Session{}, &mx.Session{}
	e := &entry{account: domain.Account{Address: "alice@example.test"}, session: current, state: domain.Connection{State: "connected"}}
	supported := disco.Info{Identity: []info.Identity{{Category: "conference", Type: "text"}}, Features: []info.Feature{{Var: occupantIDNS}}}
	room := "room@conference.example.test"
	recordDiscoInfo(e, old, room, 0, supported)
	if e.occupantScopes[room] {
		t.Fatal("old session advertised current authority")
	}
	recordDiscoInfo(e, current, room, 0, supported)
	if !e.occupantScopes[room] {
		t.Fatal("room support not retained")
	}
	recordDiscoInfo(e, current, room, 0, disco.Info{Identity: supported.Identity})
	if e.occupantScopes[room] {
		t.Fatal("removed feature remained trusted")
	}
	recordDiscoInfo(e, current, room, 0, disco.Info{Features: supported.Features})
	if e.occupantScopes[room] {
		t.Fatal("client feature advertised room authority")
	}
}
func TestArchiveCorrectionKeepsSourceIDSeparateFromRoomSID(t *testing.T) {
	raw := `<result xmlns="urn:xmpp:mam:2" id="archive-edit"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="2026-11-08T14:08:29Z"/><message xmlns="jabber:client" from="room@conference.example.test/Bob" type="groupchat" id="client-edit"><body>Corrected text</body><replace xmlns="urn:xmpp:message-correct:0" id="client-original"/><occupant-id xmlns="urn:xmpp:occupant-id:0" id="opaque-actor"/><stanza-id xmlns="urn:xmpp:sid:0" by="room@conference.example.test" id="room-issued-edit"/></message></forwarded></result>`
	var result archiveResult
	if err := xml.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	account := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}
	for _, trusted := range []bool{false, true} {
		in, ok, err := archivedIncoming(account, "room@conference.example.test", &result, archiveTrust{SIDVerified: true, OccupantVerified: trusted})
		if err != nil || !ok {
			t.Fatal(err)
		}
		if trusted {
			if in.Correction == nil || in.Correction.ID != "client-edit" || in.Correction.TargetID != "client-original" || len(in.References) != 1 || in.References[0].ID != "room-issued-edit" {
				t.Fatal("archive source namespaces conflated", in)
			}
		} else if in.Correction != nil {
			t.Fatal("forged occupant metadata applied without discovery")
		}
	}
}

func TestLiveRoomCorrectionCarriesVerifiedActorSource(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	slot.snapshot.State = "joined"
	e.occupantScopes = map[string]bool{slot.conversation.Address: true}
	raw := `<message xmlns="jabber:client" from="room@conference.example.test/Bob" type="groupchat" id="client-edit"><body>Corrected café 🧑🏽‍💻</body><replace xmlns="urn:xmpp:message-correct:0" id="client-original"/><occupant-id xmlns="urn:xmpp:occupant-id:0" id="opaque-actor"/><stanza-id xmlns="urn:xmpp:sid:0" by="room@conference.example.test" id="room-issued-edit"/></message>`
	for _, advertised := range []bool{true, false} {
		e.occupantScopes[slot.conversation.Address] = advertised
		decoder := xml.NewDecoder(strings.NewReader(raw))
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		start := token.(xml.StartElement)
		var sink bytes.Buffer
		if err = m.handle(t.Context(), e, tokens{decoder, xml.NewEncoder(&sink)}, &start); err != nil {
			t.Fatal(err)
		}
		in := host.received[len(host.received)-1]
		if in.Correction != nil != advertised || advertised && (in.Correction.ID != "client-edit" || in.Correction.TargetID != "client-original" || in.Correction.Author != "room@conference.example.test/Bob") {
			t.Fatal("live correction lost its advertised sender authority", advertised, in)
		}
		if len(in.References) != 1 || in.References[0].ID != "room-issued-edit" {
			t.Fatal("live correction mixed client source ID with reply target ID", in.References)
		}
	}
}

func TestClientAdvertisesReceivedCorrections(t *testing.T) {
	for _, feature := range clientInfo().Features {
		if feature.Var == correctionNS {
			return
		}
	}
	t.Fatal("received correction support missing from client discovery")
}
