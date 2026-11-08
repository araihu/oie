package xmpp

import (
	"encoding/xml"
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
)

func TestReplyWireNamespacesIDsAndUnicodeFallback(t *testing.T) {
	body := "> Bob 🧑🏽 wrote & said:\n> cake\nÓtima ideia!"
	end := utf8.RuneCountInString("> Bob 🧑🏽 wrote & said:\n> cake\n")
	raw := fmt.Sprintf(`<message xmlns="jabber:client" from="bob@example.test/phone" to="alice@example.test" id="wire-id" type="chat"><body>&gt; Bob 🧑🏽 wrote &amp; said:
&gt; cake
Ótima ideia!</body><origin-id xmlns="urn:xmpp:sid:0" id="origin-id"/><reply xmlns="urn:xmpp:reply:0" id="original" to="alice@example.test/laptop"/><fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0"><body start="0" end="%d"/></fallback></message>`, end)
	var msg archiveMessage
	if err := xml.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	refs, reply := decodeReplyMetadata(msg.replyMetadata, msg.Body, msg.From, "alice@example.test", "bob@example.test", false, false)
	if len(refs) != 1 || refs[0].ID != "origin-id" || refs[0].Scope != "sender:bob@example.test" || reply == nil || reply.Target.Scope != "sender:alice@example.test" {
		t.Fatal(refs, reply)
	}
	if msg.Body != body {
		t.Fatal("XML decoded body changed unexpectedly")
	}
	text, quote := domain.ReplyBody(msg.Body, reply.Fallback)
	if text != "Ótima ideia!" || quote != "Bob 🧑🏽 wrote & said:\ncake" {
		t.Fatal(text, quote)
	}
	for _, extra := range []string{
		`<reply xmlns="wrong" id="original"/>`,
		`<reply xmlns="urn:xmpp:reply:0" id=""/>`,
		`<reply xmlns="urn:xmpp:reply:0" id="original" to="foreign@example.test"/>`,
		`<reply xmlns="urn:xmpp:reply:0" id="a"/><reply xmlns="urn:xmpp:reply:0" id="b"/>`,
	} {
		var m archiveMessage
		if err := xml.Unmarshal([]byte(`<message xmlns="jabber:client" from="bob@example.test"><body>quote</body>`+extra+`</message>`), &m); err != nil {
			t.Fatal(err)
		}
		_, r := decodeReplyMetadata(m.replyMetadata, m.Body, m.From, "alice@example.test", "bob@example.test", false, false)
		if r != nil {
			t.Fatal("invalid or foreign reference accepted", extra)
		}
	}
}

func TestRoomRepliesUseDiscoveredRoomIDsNeverSenderIDs(t *testing.T) {
	from, _ := jid.Parse("room@conference.example.test/Bob")
	base := replyMetadata{Replies: []wireReply{{ID: "room-original", To: from.String()}}}
	for _, tc := range []struct {
		name    string
		ids     []wireID
		trusted bool
		want    int
	}{
		{"no room ID", nil, true, 0},
		{"wrong authority", []wireID{{ID: "room-source", By: "other@conference.example.test"}}, true, 0},
		{"resource authority", []wireID{{ID: "room-source", By: from.String()}}, true, 0},
		{"not discovered", []wireID{{ID: "room-source", By: from.Bare().String()}}, false, 0},
		{"duplicate authority", []wireID{{ID: "a", By: from.Bare().String()}, {ID: "b", By: from.Bare().String()}}, true, 0},
		{"valid", []wireID{{ID: "room-source", By: from.Bare().String()}}, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := base
			meta.Stable = tc.ids
			meta.Origins = []wireID{{ID: "sender-must-not-be-used"}}
			refs, r := decodeReplyMetadata(meta, "body", from, "alice@example.test", from.Bare().String(), true, tc.trusted)
			if len(refs) != tc.want {
				t.Fatal(refs)
			}
			if !tc.trusted && r != nil {
				t.Fatal("undiscovered room reply trusted")
			}
			if len(refs) > 0 && (refs[0].ID != "room-source" || refs[0].Scope != "room:"+from.Bare().String() || r.Target.Author != from.String()) {
				t.Fatal(refs, r)
			}
		})
	}
}

func TestFallbackBoundsNeverHideUnmarkedText(t *testing.T) {
	for _, body := range []string{`start="0" end="999"`, `start="-1" end="3"`, `start="4" end="1"`, `start="0"`, `end="3"`, `start="bad" end="3"`} {
		var meta replyMetadata
		raw := `<message><reply xmlns="urn:xmpp:reply:0" id="a"/><fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0"><body ` + body + `/></fallback></message>`
		if err := xml.Unmarshal([]byte(raw), &meta); err != nil {
			t.Fatal(err)
		}
		from, _ := jid.Parse("bob@example.test")
		_, r := decodeReplyMetadata(meta, "keep body", from, "alice@example.test", "bob@example.test", false, false)
		if r == nil {
			t.Fatal("reply reference lost")
		}
		text, _ := domain.ReplyBody("keep body", r.Fallback)
		if text != "keep body" {
			t.Fatal("invalid fallback hid text", body)
		}
	}
}
