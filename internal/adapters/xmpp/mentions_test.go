package xmpp

import (
	"encoding/xml"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestMentionCandidatesPreserveAnonymousRoomPrivacyAndDepartures(t *testing.T) {
	m, e, slot, _ := roomFixture(t)
	slot.snapshot.State = "joined"
	address := slot.conversation.Address
	occupants := parseOccupants(address, `<query xmlns="http://jabber.org/protocol/disco#items"><item jid="`+address+`/Juliet" name="Room alias"/></query>`)
	info := domain.ConversationInfo{State: "known", Features: []string{"anonymous"}, Participants: occupants}
	e.details = map[domain.ID]*detailSlot{slot.conversation.ID: {conversation: slot.conversation, info: info}}
	read := func() domain.MentionCandidates {
		t.Helper()
		got, err := m.MentionCandidates(t.Context(), e.account, slot.conversation)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := read()
	if len(got.Items) != 1 || got.Items[0].InsertText != "@Juliet" || got.Items[0].DisplayName != "Room alias" || got.Items[0].ReferenceMode != "text" {
		t.Fatal(got)
	}
	var presence incomingPresence
	if err := xml.Unmarshal([]byte(`<presence xmlns="jabber:client" from="`+address+`/Juliet"><x xmlns="http://jabber.org/protocol/muc#user"><item jid="juliet@example.test/device"/></x></presence>`), &presence); err != nil {
		t.Fatal(err)
	}
	captureRoomMentionPresence(e, presence)
	got = read()
	if got.Items[0].URI != "" || got.Items[0].ReferenceMode != "text" {
		t.Fatal("moderator observation leaked anonymous JID", got)
	}
	e.details[slot.conversation.ID].info.Features = []string{"non_anonymous", "anonymous"}
	got = read()
	if got.Items[0].ReferenceMode != "text" || got.Items[0].URI != "" {
		t.Fatal("contradictory privacy evidence disclosed target", got)
	}
	e.details[slot.conversation.ID].info.Features = []string{"non_anonymous"}
	got = read()
	if got.Items[0].URI != "xmpp:juliet@example.test" || got.Items[0].ReferenceMode != "structured" {
		t.Fatal(got)
	}
	presence.Type = stanza.UnavailablePresence
	captureRoomMentionPresence(e, presence)
	if got = read(); len(got.Items) != 0 {
		t.Fatal("departed occupant restored from stale discovery", got)
	}
	presence.Type = ""
	presence.From, _ = jid.Parse(address + "/Other")
	presence.MUC = nil
	captureRoomMentionPresence(e, presence)
	if got = read(); len(got.Items) != 0 {
		t.Fatal("non-MUC traffic became candidate", got)
	}
	e.state.State = "disconnected"
	if got = read(); got.State != "not_connected" || len(got.Items) != 0 {
		t.Fatal(got)
	}
}
func TestOutgoingMentionsUnicodeRangesAndReadableFallback(t *testing.T) {
	account := domain.Account{Address: "alice@example.test"}
	body := "> Original 😀\n\nOlá 😀 @Juliet e @Romeo"
	begin := utf8.RuneCountInString("> Original 😀\n\nOlá 😀 ")
	out := application.Outgoing{ID: "outgoing", Kind: "room", Address: "room@rooms.example.test", OriginalText: body, Mentions: []domain.MessageMention{
		{MentionSelection: domain.MentionSelection{ParticipantID: "opaque", Begin: begin, End: begin + 7}, ReferenceMode: "structured", URI: "xmpp:juliet@example.test"},
		{MentionSelection: domain.MentionSelection{ParticipantID: "opaque2", Begin: begin + 10, End: begin + 16}, ReferenceMode: "text"},
	}}
	payload, err := buildOutgoing(account, out)
	if err != nil {
		t.Fatal(err)
	}
	data, err := xml.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if payload.Body != body || len(payload.Mentions) != 1 || payload.Mentions[0].Begin != begin || !strings.Contains(text, `type="mention"`) || !strings.Contains(text, `uri="xmpp:juliet@example.test"`) {
		t.Fatal(text)
	}
	for _, bad := range []string{"xmpp:room@rooms.example.test", "xmpp:room@rooms.example.test/Juliet", "https://example.test", "xmpp:juliet@example.test?message"} {
		out.Mentions[0].URI = bad
		if _, err := buildOutgoing(account, out); err == nil {
			t.Fatal("invalid mention target accepted", bad)
		}
	}
}
