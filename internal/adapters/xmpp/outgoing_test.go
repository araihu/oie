package xmpp

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestOutgoingRepliesPreserveLegacyBodyAndUnicodeFallback(t *testing.T) {
	a := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}
	prefix := "> Bob 🧑🏽‍💻 & café\n> texto original\n"
	authored := "Ótima ideia <&> 😀"
	body := prefix + authored
	for _, kind := range []string{"direct", "room"} {
		t.Run(kind, func(t *testing.T) {
			address, scope, author := "bob@example.test", "sender:bob@example.test", "bob@example.test/phone"
			if kind == "room" {
				address = "room@conference.example.test"
				scope = "room:" + address
				author = address + "/Bob"
			}
			out := application.Outgoing{ID: domain.ID("019a5df0-0000-7000-8000-000000000002"), Address: address, Kind: kind, OriginalText: body, Reply: &domain.ReplyReference{Target: domain.MessageReference{Scope: scope, ID: "server-issued-original", Author: author}, Fallback: []domain.TextRange{{Start: 0, End: utf8.RuneCountInString(prefix)}}}}
			payload, err := buildOutgoing(a, out)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := xml.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			// The client stream supplies this inherited namespace on the wire.
			raw = bytes.Replace(raw, []byte("<message"), []byte(`<message xmlns="jabber:client"`), 1)
			var received archiveMessage
			if err = xml.Unmarshal(raw, &received); err != nil {
				t.Fatal(err)
			}
			if received.Body != body || received.Type != payload.Type || len(received.Replies) != 1 || received.Replies[0].ID != "server-issued-original" || received.Replies[0].To != author || len(received.Fallback) != 1 || received.Fallback[0].For != replyNS {
				t.Fatal("legacy/supporting wire mismatch", string(raw))
			}
			if received.Fallback[0].Bodies[0].End == nil || *received.Fallback[0].Bodies[0].End != strconv.Itoa(payload.Fallback.Bodies[0].End) {
				t.Fatal("wrong Unicode offset")
			}
			start, end := payload.Fallback.Bodies[0].Start, payload.Fallback.Bodies[0].End
			text, _ := domain.ReplyBody(received.Body, []domain.TextRange{{Start: start, End: end}})
			if text != authored {
				t.Fatal("supporting client did not recover authored text", text)
			}
		})
	}
}

func TestReplySelectionNeverUsesRoomClientMessageID(t *testing.T) {
	a := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}
	c := domain.Conversation{ID: "019a5df0-0000-7000-8000-000000000002", AccountID: a.ID, Address: "room@conference.example.test", Kind: "room"}
	target := domain.Message{AccountID: a.ID, ConversationID: c.ID}
	m := New(t.TempDir(), nil)
	valid := domain.MessageReference{Scope: "room:" + c.Address, ID: "room-issued", Author: c.Address + "/Bob"}
	for _, tc := range []struct {
		name string
		refs []domain.MessageReference
		want bool
	}{
		{"only client id", []domain.MessageReference{{Scope: "sender:bob@example.test", ID: "client-id", Author: "bob@example.test"}}, false},
		{"other room", []domain.MessageReference{{Scope: "room:other@conference.example.test", ID: "room-issued"}}, false},
		{"wrong author authority", []domain.MessageReference{{Scope: valid.Scope, ID: valid.ID, Author: "other@conference.example.test/Bob"}}, false},
		{"ambiguous room ids", []domain.MessageReference{valid, {Scope: valid.Scope, ID: "another", Author: valid.Author}}, false},
		{"valid retained authority", []domain.MessageReference{valid}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target.References = tc.refs
			ref, ok := m.SelectReplyReference(a, c, target)
			if ok != tc.want || ok && ref != valid {
				t.Fatal("incorrect frozen reply reference", ref, ok)
			}
		})
	}
	target.References = []domain.MessageReference{valid}
	target.ConversationID = a.ID
	if _, ok := m.SelectReplyReference(a, c, target); ok {
		t.Fatal("cross-conversation target accepted")
	}
}
func TestInvalidFrozenReplyFallsBackToUnmodifiedQuote(t *testing.T) {
	a := domain.Account{Address: "alice@example.test"}
	out := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000001", Address: "room@conference.example.test", Kind: "room", OriginalText: "> Original\nResposta", Reply: &domain.ReplyReference{Target: domain.MessageReference{Scope: "sender:bob@example.test", ID: "sender-must-not-be-room-target"}, Fallback: []domain.TextRange{{Start: 0, End: 999}}}}
	payload, err := buildOutgoing(a, out)
	if err != nil || payload.Body != out.OriginalText || payload.Reply != nil || payload.Fallback != nil {
		t.Fatal("invalid target changed legacy body or emitted bad metadata")
	}
	features := map[string]bool{}
	for _, feature := range clientInfo().Features {
		features[feature.Var] = true
	}
	if !features[replyNS] || !features[fallbackNS] || !features["http://jabber.org/protocol/muc"] {
		t.Fatal("client reply discovery missing")
	}
}
