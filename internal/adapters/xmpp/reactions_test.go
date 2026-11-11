package xmpp

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	"mellium.im/xmpp/stream"
)

func feedReaction(t *testing.T, m *Manager, e *entry, raw string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := token.(xml.StartElement)
	var sink bytes.Buffer
	if err := m.handle(t.Context(), e, tokens{decoder, xml.NewEncoder(&sink)}, &start); err != nil {
		t.Fatal(err)
	}
}

func TestReactionControlsReplaceWholeSetAndDoNotCreateTextMessages(t *testing.T) {
	host := &hostStub{}
	m := New(t.TempDir(), nil)
	m.host = host
	e := &entry{account: domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}}
	for _, payload := range []string{`<reaction>👋</reaction><reaction>🧑🏽‍💻</reaction><reaction>👋</reaction>`, ``} {
		feedReaction(t, m, e, `<message xmlns="jabber:client" type="chat" from="bob@example.test/phone" to="alice@example.test/oie" id="event"><body>must not become text</body><reactions xmlns="urn:xmpp:reactions:0" id="source">`+payload+`</reactions></message>`)
	}
	if len(host.received) != 2 {
		t.Fatal("reaction controls not received", host.received)
	}
	for i, expected := range [][]string{{"👋", "🧑🏽‍💻"}, {}} {
		in := host.received[i]
		r := in.Reaction
		if r == nil || in.AccountID != e.account.ID || in.OriginalText != "" || in.ProviderID != "" || r.Address != "bob@example.test" || r.Actor != "bob@example.test" || r.IsOwn || r.Kind != "direct" || r.Target.ID != "source" || r.Target.Scope != "" || !reflect.DeepEqual(r.Emojis, expected) {
			t.Fatal("reaction control mixed with text or actor/target scope", in)
		}
	}
	for _, payload := range []string{
		`<reactions xmlns="urn:xmpp:reactions:0" id="one"/><reactions xmlns="urn:xmpp:reactions:0" id="two"/>`,
		`<reactions xmlns="urn:xmpp:reactions:0"/>`,
		`<reactions xmlns="urn:xmpp:reactions:0" id="source">junk</reactions>`,
		`<reactions xmlns="urn:xmpp:reactions:0" id="source"><other/></reactions>`,
		`<reactions xmlns="urn:xmpp:reactions:0" id="source"><reaction><nested/></reaction></reactions>`,
		`<reactions xmlns="urn:xmpp:reactions:0" id="source"><reaction/></reactions>`,
	} {
		feedReaction(t, m, e, `<message xmlns="jabber:client" type="chat" from="bob@example.test/phone" id="event"><body>must not become text</body>`+payload+`</message>`)
	}
	if len(host.received) != 2 {
		t.Fatal("malformed controls fell through to ordinary text")
	}
	feedReaction(t, m, e, `<message xmlns="jabber:client" type="chat" from="bob@example.test/phone" to="another@example.test" id="event"><reactions xmlns="urn:xmpp:reactions:0" id="source"/></message>`)
	feedReaction(t, m, e, `<message xmlns="jabber:client" type="chat" from="bob@example.test/phone"><reactions xmlns="urn:xmpp:reactions:0" id="source"/></message>`)
	if len(host.received) != 2 {
		t.Fatal("foreign recipient or unstable event accepted")
	}
}

func TestDirectArchivedReactionsCarryStableDelayAndOwnIdentity(t *testing.T) {
	a := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}
	var r archiveResult
	r.ID = "archive-event"
	r.Forwarded.Delay.Stamp = "2026-11-08T12:30:00Z"
	for _, from := range []string{"bob@example.test/phone", "alice@example.test/laptop"} {
		var msg archiveMessage
		to := a.Address + "/oie"
		if strings.HasPrefix(from, a.Address) {
			to = "bob@example.test/phone"
		}
		if err := xml.Unmarshal([]byte(`<message xmlns="jabber:client" from="`+from+`" to="`+to+`" type="chat"><reactions xmlns="urn:xmpp:reactions:0" id="original"><reaction>❤️</reaction></reactions></message>`), &msg); err != nil {
			t.Fatal(err)
		}
		r.Forwarded.Message = &msg
		in, ok, err := archivedIncoming(a, a.Address, &r, archiveTrust{})
		if err != nil || !ok || in.AccountID != a.ID || in.Reaction == nil || !in.Reaction.Historical || in.Reaction.ArchiveID != r.ID || in.Reaction.ArchiveScope != a.Address || in.Reaction.CreatedAt.Format(time.RFC3339) != r.Forwarded.Delay.Stamp || in.Reaction.Address != "bob@example.test" || !strings.HasPrefix(in.Reaction.ProviderID, "archive-reaction:") || in.Reaction.IsOwn != strings.HasPrefix(from, a.Address) {
			t.Fatal("archive reaction lost identity/delay", in, ok, err)
		}
	}
	r.Forwarded.Message.Type = stanza.GroupChatMessage
	if _, ok, err := archivedIncoming(a, "room@conference.example.test", &r, archiveTrust{SIDVerified: true, OccupantVerified: true}); err != nil || ok {
		t.Fatal("archived room nickname/occupant metadata cannot establish reaction actor")
	}
}

func TestRoomReactionArchiveIdentityIsIndependentOfClientID(t *testing.T) {
	a := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}
	room := "room@conference.example.test"
	var r archiveResult
	r.Forwarded.Delay.Stamp = "2026-11-08T12:30:00Z"
	trust := archiveTrust{SIDVerified: true, OccupantVerified: true, Semianonymous: true, OwnReactionActor: roomReactionActor(a.ID, room, "self")}
	raw := `<message xmlns="jabber:client" from="` + room + `/Bob" type="groupchat" id="reused"><occupant-id xmlns="urn:xmpp:occupant-id:0" id="peer"/><reactions xmlns="urn:xmpp:reactions:0" id="original"/></message>`
	var providerIDs []string
	for i, raw := range []string{raw, raw, strings.Replace(raw, ` id="reused"`, "", 1)} {
		var msg archiveMessage
		if err := xml.Unmarshal([]byte(raw), &msg); err != nil {
			t.Fatal(err)
		}
		r.Forwarded.Message = &msg
		r.ID = []string{"archive-a", "archive-b", "archive-c"}[i]
		in, ok, err := archivedReaction(a, room, &r, trust)
		if err != nil || !ok || in.CommandEchoID != "" || slices.Contains(providerIDs, in.ProviderID) {
			t.Fatal("a client ID interfered with the trusted archive event identity", in, ok, err)
		}
		providerIDs = append(providerIDs, in.ProviderID)
	}
}

func reactionRoomFixture(t *testing.T) (*Manager, *entry, *roomSlot, *roomHostStub) {
	t.Helper()
	m, e, slot, host := roomFixture(t)
	e.account.Protocol = "xmpp"
	e.correctionSession = "reaction-session"
	slot.snapshot.State = "joined"
	raw, peer := net.Pipe()
	t.Cleanup(func() { _ = raw.Close(); _ = peer.Close() })
	e.raw = raw
	e.details = map[domain.ID]*detailSlot{slot.conversation.ID: {conversation: slot.conversation, info: domain.ConversationInfo{State: "known", CheckedAt: time.Now().UTC(), Features: []string{"non_anonymous"}}}}
	slot.mentionOccupants = map[string]observedMention{slot.conversation.Address + "/Oie": {present: true, uri: "xmpp:alice@example.test"}, slot.conversation.Address + "/Bob": {present: true, uri: "xmpp:bob@example.test"}}
	return m, e, slot, host
}

func TestLiveRoomReactionRequiresCurrentVerifiedRealActorAndStableTargetScope(t *testing.T) {
	m, e, slot, host := reactionRoomFixture(t)
	raw := `<message xmlns="jabber:client" from="room@conference.example.test/Bob" type="groupchat" id="event"><stanza-id xmlns="urn:xmpp:sid:0" by="room@conference.example.test" id="room-event"/><reactions xmlns="urn:xmpp:reactions:0" id="room-source"><reaction>👍</reaction></reactions></message>`
	feedReaction(t, m, e, raw)
	if len(host.received) != 1 || host.received[0].Reaction == nil || host.received[0].Reaction.Actor != "bob@example.test" || host.received[0].Reaction.ActorName != "Bob" || host.received[0].Reaction.Target.Scope != "room:"+slot.conversation.Address {
		t.Fatal("live room identity/reference invalid", host.received)
	}
	feedReaction(t, m, e, strings.Replace(raw, "<reactions", `<delay xmlns="urn:xmpp:delay" stamp="2026-11-08T12:00:00Z" from="room@conference.example.test"/><reactions`, 1))
	delete(slot.mentionOccupants, slot.conversation.Address+"/Bob")
	feedReaction(t, m, e, raw)
	slot.mentionOccupants[slot.conversation.Address+"/Bob"] = observedMention{present: true, uri: "xmpp:bob@example.test"}
	e.details[slot.conversation.ID].info.Features = []string{"anonymous"}
	feedReaction(t, m, e, raw)
	e.details[slot.conversation.ID].info.Features = []string{"non_anonymous"}
	e.sidScopes[slot.conversation.Address] = false
	feedReaction(t, m, e, raw)
	if len(host.received) != 1 {
		t.Fatal("delayed, unknown, anonymous or unstable room reaction associated")
	}
}

func TestSemianonymousRoomReactionsUseVerifiedOccupantIDs(t *testing.T) {
	m, e, slot, host := reactionRoomFixture(t)
	c := slot.conversation
	slot.snapshot.State = "joining"
	e.details[c.ID].info.Features = []string{"anonymous"}
	discoXML := `<query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="muc_semianonymous"/><feature var="urn:xmpp:occupant-id:0"/><feature var="urn:xmpp:sid:0"/></query>`
	if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, c.Address, discoXML, 0); err != nil {
		t.Fatal(err)
	}
	target := reactionTarget(e, c)
	if _, err := m.PrepareReaction(t.Context(), e.account, c, target); err == nil {
		t.Fatal("accepted before confirmed own occupant identity")
	}
	presence := `<presence xmlns="jabber:client" from="room@conference.example.test/Oie" id="join-request"><occupant-id xmlns="urn:xmpp:occupant-id:0" id="own-id"/><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/></x></presence>`
	feedReaction(t, m, e, presence)
	plan, err := m.PrepareReaction(t.Context(), e.account, c, target)
	if err != nil || !domain.IsRoomReactionActor(plan.RoomActor) || plan.ActorName != "Oie" || plan.Reference != target.References[0] {
		t.Fatal("verified own occupant could not prepare a reaction", plan, err)
	}
	out := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000012", Kind: c.Kind, Address: c.Address, Reaction: &plan, ReactionEmojis: []string{"👍"}, RoomIntentRevisionID: plan.RoomIntentRevisionID}
	payload, err := buildOutgoing(e.account, out)
	if err != nil || !validFrozenReaction(e, e.account, out) {
		t.Fatal("valid semianonymous reaction rejected", err)
	}
	wire, err := xml.Marshal(payload)
	if err != nil || bytes.Contains(wire, []byte(e.account.Address)) || bytes.Contains(wire, []byte("own-id")) || bytes.Contains(wire, []byte("<body")) {
		t.Fatal("reaction sent private identity or message body", string(wire), err)
	}
	reaction := func(nickname, event, occupant string) {
		feedReaction(t, m, e, `<message xmlns="jabber:client" from="`+c.Address+"/"+nickname+`" type="groupchat" id="reused-client-id"><stanza-id xmlns="urn:xmpp:sid:0" by="`+c.Address+`" id="`+event+`"/>`+occupant+`<reactions xmlns="urn:xmpp:reactions:0" id="room-assigned-original"><reaction>👍</reaction></reactions></message>`)
	}
	peer := `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="peer-id"/>`
	reaction("Bob", "add", peer)
	reaction("Robert", "rename", peer)
	reaction("Oie", "echo", `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="own-id"/>`)
	reaction("Bob", "new-occupant", `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="other-id"/>`)
	reaction("Bob", "missing", "")
	reaction("Bob", "duplicate", peer+peer)
	if len(host.received) != 4 {
		t.Fatal("missing or ambiguous occupant identity accepted", host.received)
	}
	first, renamed, own, replacement := host.received[0].Reaction, host.received[1].Reaction, host.received[2].Reaction, host.received[3].Reaction
	if !domain.IsRoomReactionActor(first.Actor) || first.Actor != renamed.Actor || first.ProviderID == renamed.ProviderID || first.CommandEchoID != "" || own.CommandEchoID != "reused-client-id" || renamed.ActorName != "Robert" ||
		replacement.Actor == first.Actor || first.IsOwn || !own.IsOwn || own.Actor != plan.RoomActor {
		t.Fatal("occupant aliases were not scoped by verified identity", first, renamed, own, replacement)
	}
	if first.Actor == roomReactionActor(e.account.ID, "other@conference.example.test", "peer-id") {
		t.Fatal("occupant identifier linked across rooms")
	}
	slot.ownOccupantID = "replacement-id"
	if validFrozenReaction(e, e.account, out) {
		t.Fatal("frozen command rebound to a different occupant")
	}
	if _, err = m.decodeDiscoInfo(t.Context(), e, e.session, c.Address, strings.Replace(discoXML, `<feature var="urn:xmpp:occupant-id:0"/>`, "", 1), currentRoomConfiguration(e, c.Address).revision); err != nil {
		t.Fatal(err)
	}
	reaction("Bob", "unverified", peer)
	if _, err = m.PrepareReaction(t.Context(), e.account, c, target); err == nil || len(host.received) != 4 {
		t.Fatal("occupant IDs accepted without a matching room advertisement")
	}
}

func TestRoomConfigurationInvalidatesReactionTrust(t *testing.T) {
	m, e, slot, host := reactionRoomFixture(t)
	room := slot.conversation.Address
	slot.ownOccupantID = "own-id"
	disco := `<query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="muc_semianonymous"/><feature var="urn:xmpp:occupant-id:0"/><feature var="urn:xmpp:sid:0"/></query>`
	if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, room, disco, 0); err != nil {
		t.Fatal(err)
	}
	inFlight := &archiveQuery{Scope: room}
	e.archives = map[string]*archiveQuery{"before-change": inFlight}
	notification := `<message xmlns="jabber:client" from="` + room + `" type="groupchat"><x xmlns="http://jabber.org/protocol/muc#user"><status code="104"/></x></message>`
	feedReaction(t, m, e, strings.Replace(notification, room, room+"/Bob", 1))
	if currentRoomConfiguration(e, room).pending {
		t.Fatal("participant changed the room configuration")
	}
	feedReaction(t, m, e, notification)
	if !currentRoomConfiguration(e, room).pending || inFlight.Err == nil {
		t.Fatal("room configuration failed to revoke cached and in-flight proof")
	}
	if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, room, disco, 0); err == nil {
		t.Fatal("accepted a discovery result from before the room change")
	}
	feedReaction(t, m, e, `<message xmlns="jabber:client" from="`+room+`/Bob" type="groupchat" id="client"><occupant-id xmlns="urn:xmpp:occupant-id:0" id="own-id"/><reactions xmlns="urn:xmpp:reactions:0" id="source"><reaction>👍</reaction></reactions></message>`)
	if len(host.received) != 0 {
		t.Fatal("accepted an occupant ID using revoked proof")
	}
	fresh := strings.Replace(disco, `<feature var="urn:xmpp:occupant-id:0"/>`, "", 1)
	if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, room, fresh, 1); err != nil {
		t.Fatal(err)
	}
	if currentRoomConfiguration(e, room).pending || e.occupantScopes[room] {
		t.Fatal("failed to apply the fresh room capabilities")
	}
}

func reactionTarget(e *entry, c domain.Conversation) domain.Message {
	ref := domain.MessageReference{Scope: "sender:bob@example.test", ID: "direct-original", Author: "bob@example.test"}
	if c.Kind == "room" {
		ref = domain.MessageReference{Scope: "room:" + c.Address, ID: "room-assigned-original", Author: c.Address + "/Bob"}
	}
	return domain.Message{ID: "019a5df0-0000-7000-8000-000000000010", RevisionID: "019a5df0-0000-7000-8000-000000000011", AccountID: e.account.ID, ConversationID: c.ID, References: []domain.MessageReference{ref}}
}

func TestOutgoingReactionsUseCompleteSetAndNeverCarryMessageBody(t *testing.T) {
	for _, kind := range []string{"direct", "room"} {
		t.Run(kind, func(t *testing.T) {
			m, e, slot, _ := reactionRoomFixture(t)
			c := slot.conversation
			if kind == "direct" {
				c.Kind, c.Address = "direct", "bob@example.test"
			}
			target := reactionTarget(e, c)
			plan, err := m.PrepareReaction(t.Context(), e.account, c, target)
			if err != nil {
				t.Fatal(err)
			}
			for _, emojis := range [][]string{{"👋", "🧑🏽‍💻"}, {}} {
				out := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000012", Kind: c.Kind, Address: c.Address, Reaction: &plan, ReactionEmojis: emojis, RoomIntentRevisionID: plan.RoomIntentRevisionID}
				payload, err := buildOutgoing(e.account, out)
				if err != nil || !validFrozenReaction(e, e.account, out) {
					t.Fatal("valid reaction rejected", err)
				}
				wire, err := xml.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(wire, []byte("<body")) || !bytes.Contains(wire, []byte(`xmlns="urn:xmpp:hints"`)) || payload.Reactions.ID != target.References[0].ID || len(payload.Reactions.Items) != len(emojis) {
					t.Fatal("reaction set became text or lost source", string(wire))
				}
				out.OriginalText = "not allowed"
				if _, err = buildOutgoing(e.account, out); err == nil {
					t.Fatal("reaction plus message body accepted")
				}
			}
			out := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000012", Kind: c.Kind, Address: c.Address, Reaction: &plan, ReactionEmojis: []string{"👋"}, RoomIntentRevisionID: plan.RoomIntentRevisionID}
			e.correctionSession = "different-connection"
			if validFrozenReaction(e, e.account, out) {
				t.Fatal("frozen sender silently rebound after reconnect")
			}
		})
	}
}

func TestReactionSendWritesBodylessWireWithoutChangingOriginalSendTracking(t *testing.T) {
	for _, kind := range []string{"direct", "room"} {
		t.Run(kind, func(t *testing.T) {
			m, e, slot, _ := reactionRoomFixture(t)
			c := slot.conversation
			if kind == "direct" {
				c.Kind, c.Address = "direct", "bob@example.test"
			}
			left, right := net.Pipe()
			t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
			s, err := mx.NewSession(t.Context(), jid.MustParse("example.test"), jid.MustParse("alice@example.test/test"), left, mx.Ready, func(context.Context, *stream.Info, *stream.Info, *mx.Session, any) (mx.SessionState, io.ReadWriter, any, error) {
				return mx.Ready, nil, nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			e.raw, e.session, slot.session = left, s, s
			plan, err := m.PrepareReaction(t.Context(), e.account, c, reactionTarget(e, c))
			if err != nil {
				t.Fatal(err)
			}
			command := domain.ID("019a5df0-0000-7000-8000-000000000012")
			received := make(chan archiveMessage, 1)
			go func() {
				var wire archiveMessage
				_ = right.SetReadDeadline(time.Now().Add(3 * time.Second))
				if err := xml.NewDecoder(right).Decode(&wire); err != nil {
					t.Error(err)
				}
				received <- wire
			}()
			out := application.Outgoing{ID: command, Kind: c.Kind, Address: c.Address, Reaction: &plan, ReactionEmojis: []string{"👋"}, RoomIntentRevisionID: plan.RoomIntentRevisionID}
			if outcome := m.Send(t.Context(), e.account, out); outcome != application.Submitted {
				t.Fatal("local wire write did not complete", outcome)
			}
			wire := <-received
			if wire.ID != string(command) || wire.Body != "" || len(wire.Reactions) != 1 || wire.Reactions[0].ID != plan.Reference.ID || len(e.ownRoomMessages) != 0 || len(e.correctionLatest) != 0 || len(e.correctionSources) != 0 || len(e.reactionCommands) != 1 {
				t.Fatal("reaction altered original message identity/tracking", wire, e)
			}
		})
	}
}

func TestRestrictionFormsAreBoundedAndRecheckedBeforeDispatch(t *testing.T) {
	parse := func(fields string) disco.Info {
		var info disco.Info
		if err := xml.Unmarshal([]byte(`<query xmlns="http://jabber.org/protocol/disco#info"><x xmlns="jabber:x:data" type="result"><field var="FORM_TYPE" type="hidden"><value>urn:xmpp:reactions:0:restrictions</value></field>`+fields+`</x></query>`), &info); err != nil {
			t.Fatal(err)
		}
		return info
	}
	info := parse(`<field var="max_reactions_per_user"><value>1</value></field><field var="allowlist"><value>❤️</value><value>💜</value></field>`)
	limit := restrictions(info)
	if limit.max != 1 || !reflect.DeepEqual(limit.allowed, []string{"❤️", "💜"}) || withinReactionLimit([]string{"👋"}, limit) || withinReactionLimit([]string{"❤️", "💜"}, limit) || !withinReactionLimit(nil, limit) {
		t.Fatal("restrictions not applied", limit)
	}
	for _, bad := range []string{`<field var="max_reactions_per_user"><value>-1</value></field>`, `<field var="max_reactions_per_user"><value>1</value><value>2</value></field>`, `<field var="max_reactions_per_user"><value>many</value></field>`, `<field var="FORM_TYPE" type="hidden"><value>urn:xmpp:reactions:0:restrictions</value></field>`, `<field var="max_reactions_per_user"><value>1</value></field><field var="max_reactions_per_user"><value>2</value></field>`} {
		if limit := restrictions(parse(bad)); limit.allowed == nil || len(limit.allowed) != 0 || limit.max != 0 {
			t.Fatal("invalid restrictions did not fail closed", limit)
		}
	}
	m, e, slot, _ := reactionRoomFixture(t)
	c := slot.conversation
	plan, err := m.PrepareReaction(t.Context(), e.account, c, reactionTarget(e, c))
	if err != nil {
		t.Fatal(err)
	}
	before := e.session
	recordReactionRestrictions(e, e.session, c.Address, info)
	out := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000012", Kind: c.Kind, Address: c.Address, Reaction: &plan, ReactionEmojis: []string{"👋"}, RoomIntentRevisionID: plan.RoomIntentRevisionID}
	if validFrozenReaction(e, e.account, out) {
		t.Fatal("newly observed restriction bypassed by old preparation")
	}
	out.ReactionEmojis = []string{"❤️"}
	if !validFrozenReaction(e, e.account, out) {
		t.Fatal("allowed reaction refused")
	}
	e.details[c.ID].info.Features = []string{"anonymous"}
	if validFrozenReaction(e, e.account, out) {
		t.Fatal("room identity change bypassed dispatch guard")
	}
	e.session = nil
	recordReactionRestrictions(e, before, c.Address, disco.Info{})
	if currentReactionLimit(e, c.Address).max != 1 {
		t.Fatal("obsolete discovery changed current restrictions")
	}
}

func TestReactionErrorAcceptsOnlyTrackedPeerOrRoomAuthority(t *testing.T) {
	m, e, slot, host := reactionRoomFixture(t)
	id := "019a5df0-0000-7000-8000-000000000012"
	e.reactionCommands = map[string]reactionDestination{id: {address: "bob@example.test", kind: "direct"}}
	from, _ := jid.Parse("bob@example.test/phone")
	if handled, err := m.rejectReaction(t.Context(), e, stanza.Message{From: from, ID: id, Type: stanza.ErrorMessage}); err != nil || !handled || len(host.rejected) != 1 || host.rejected[0].FromParticipant || host.rejected[0].Address != "bob@example.test" {
		t.Fatal("legitimate full-resource peer rejection lost", host.rejected, err)
	}
	e.reactionCommands[id] = reactionDestination{address: slot.conversation.Address, kind: "room"}
	for _, address := range []string{slot.conversation.Address + "/Eve", "eve@example.test/phone"} {
		from, _ = jid.Parse(address)
		_, _ = m.rejectReaction(t.Context(), e, stanza.Message{From: from, ID: id})
	}
	if len(host.rejected) != 1 {
		t.Fatal("participant forged room rejection")
	}
	from, _ = jid.Parse(slot.conversation.Address)
	_, _ = m.rejectReaction(t.Context(), e, stanza.Message{From: from, ID: id})
	if len(host.rejected) != 2 || host.rejected[1].Address != slot.conversation.Address {
		t.Fatal("room authority rejection lost")
	}
	features := []string{}
	for _, feature := range clientInfo().Features {
		features = append(features, feature.Var)
	}
	if !slices.Contains(features, reactionsNS) {
		t.Fatal("client does not advertise implemented reactions")
	}
}

func TestRoomReactionsRecoverAfterEnablingOccupantIDs(t *testing.T) {
	for _, presenceFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("presence before discovery %t", presenceFirst), func(t *testing.T) {
			m, e, slot, host := reactionRoomFixture(t)
			c := slot.conversation
			slot.snapshot.State = "joining"
			disco := `<query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="muc_semianonymous"/><feature var="urn:xmpp:sid:0"/></query>`
			if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, c.Address, disco, 0); err != nil {
				t.Fatal(err)
			}
			presence := `<presence xmlns="jabber:client" from="` + c.Address + `/Oie" id="join-request"><occupant-id xmlns="urn:xmpp:occupant-id:0" id="unverified"/><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/></x></presence>`
			feedReaction(t, m, e, presence)
			if slot.ownOccupantID != "" || slot.snapshot.State != "joined" {
				t.Fatal("unadvertised occupant identity was retained")
			}
			feedReaction(t, m, e, `<message xmlns="jabber:client" from="`+c.Address+`" type="groupchat"><x xmlns="http://jabber.org/protocol/muc#user"><status code="104"/></x></message>`)
			fresh := strings.Replace(disco, "</query>", `<feature var="urn:xmpp:occupant-id:0"/></query>`, 1)
			self := strings.Replace(presence, "unverified", "self", 1)
			if presenceFirst {
				feedReaction(t, m, e, self)
				if slot.ownOccupantID != "" {
					t.Fatal("self-presence was used before discovery proved support")
				}
			}
			if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, c.Address, fresh, currentRoomConfiguration(e, c.Address).revision); err != nil {
				t.Fatal(err)
			}
			if !presenceFirst {
				feedReaction(t, m, e, self)
			}
			plan, err := m.PrepareReaction(t.Context(), e.account, c, reactionTarget(e, c))
			if err != nil || plan.RoomActor != roomReactionActor(e.account.ID, c.Address, "self") || pendingRoomReactionReplay(e, e.session, c.Address, true) == nil {
				t.Fatal("fresh self-presence did not restore reactions and archive recovery", plan, err)
			}
			peer := `<message xmlns="jabber:client" from="` + c.Address + `/Bob" id="reused" type="groupchat"><occupant-id xmlns="urn:xmpp:occupant-id:0" id="bob"/><reactions xmlns="urn:xmpp:reactions:0" id="room-assigned-original"><reaction>👍</reaction></reactions></message>`
			feedReaction(t, m, e, peer)
			feedReaction(t, m, e, strings.Replace(peer, "<reaction>👍</reaction>", "", 1))
			if len(host.received) != 2 || host.received[0].Reaction.ProviderID == host.received[1].Reaction.ProviderID ||
				host.received[0].Reaction.Actor != host.received[1].Reaction.Actor || len(host.received[1].Reaction.Emojis) != 0 {
				t.Fatal("valid live reaction sets without a room event SID were lost or conflated", host.received)
			}
		})
	}
}
