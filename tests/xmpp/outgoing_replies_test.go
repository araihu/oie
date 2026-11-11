package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type sentReplyWire struct {
	Mentions []struct {
		Type  string `xml:"type,attr"`
		URI   string `xml:"uri,attr"`
		Begin int    `xml:"begin,attr"`
		End   int    `xml:"end,attr"`
	} `xml:"urn:xmpp:reference:0 reference"`
	stanza.Message
	Body   string `xml:"jabber:client body"`
	Origin struct {
		ID string `xml:"id,attr"`
	} `xml:"urn:xmpp:sid:0 origin-id"`
	Stable []struct {
		ID string `xml:"id,attr"`
		By string `xml:"by,attr"`
	} `xml:"urn:xmpp:sid:0 stanza-id"`
	Reply *struct {
		ID string `xml:"id,attr"`
		To string `xml:"to,attr"`
	} `xml:"urn:xmpp:reply:0 reply"`
	Fallback *struct {
		For  string `xml:"for,attr"`
		Body struct {
			Start int `xml:"start,attr"`
			End   int `xml:"end,attr"`
		} `xml:"urn:xmpp:fallback:0 body"`
	} `xml:"urn:xmpp:fallback:0 fallback"`
}

func stanzaText(text string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(text))
	return out.String()
}
func TestExplicitRoomJoinReplyWireReconnectAndLeave(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("development Prosody required")
	}
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("fixture CA")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Second)
	defer cancel()
	seed, raw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = raw.Close() }()
	room, _ := jid.Parse("reply-" + string(id()) + "@conference.localhost")
	owner, _ := room.WithResource("Fixture Bob")
	joined := make(chan struct{}, 1)
	wire := make(chan sentReplyWire, 32)
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			decoder := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens))
			switch start.Name.Local {
			case "presence":
				var p stanza.Presence
				if decoder.Decode(&p) == nil && p.From.Equal(owner) && p.Type == "" {
					select {
					case joined <- struct{}{}:
					default:
					}
				}
			case "message":
				var m sentReplyWire
				if decoder.Decode(&m) == nil && (m.From.Bare().Equal(room) || m.From.Bare().String() == "alice@localhost") && m.Body != "" {
					select {
					case wire <- m:
					default:
					}
				}
			}
			return nil
		}))
	}()
	if err = seed.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
	}
	if err = seed.Send(ctx, stanza.Presence{To: owner}.Wrap(xml.NewDecoder(strings.NewReader(`<x xmlns="http://jabber.org/protocol/muc"/>`)))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("fixture room owner did not join")
	}
	configuration := `<query xmlns="http://jabber.org/protocol/muc#owner"><x xmlns="jabber:x:data" type="submit"><field var="FORM_TYPE" type="hidden"><value>http://jabber.org/protocol/muc#roomconfig</value></field><field var="muc#roomconfig_roomname"><value>Reply integration fixture</value></field><field var="muc#roomconfig_publicroom"><value>1</value></field><field var="muc#roomconfig_whois"><value>anyone</value></field><field var="muc#roomconfig_membersonly"><value>0</value></field><field var="muc#roomconfig_persistentroom"><value>0</value></field></x></query>`
	response, err := seed.SendIQ(ctx, stanza.IQ{To: room, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(configuration))))
	if err != nil {
		t.Fatal(err)
	}
	var configured stanza.IQ
	err = xml.NewTokenDecoder(response).Decode(&configured)
	_ = response.Close()
	if err != nil || configured.Type != stanza.ResultIQ {
		t.Fatal("fixture room setup", err)
	}
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "hosted", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	registered, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Reply room fixture"})
	if err != nil {
		t.Fatal(err)
	}
	connect := func() {
		t.Helper()
		if _, err := engine.ConfigureConnection(ctx, registered.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool {
			s, err := engine.Connection(ctx, registered.Account.ID)
			return err == nil && s.State == "connected"
		})
	}
	connect()
	awaitWire := func(body string) sentReplyWire {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case m := <-wire:
				if m.Body == body {
					return m
				}
			case <-deadline.C:
				t.Fatal("room wire did not receive expected reply")
				return sentReplyWire{}
			}
		}
	}
	// A legacy peer advertising no reply feature still receives the readable
	// quote, while supporting peers can remove the exact marked fallback.
	directOriginal := "Direct original café 🧑🏽‍💻 " + string(id())
	directOrigin := "direct-origin-" + string(id())
	recipient, _ := jid.Parse("alice@localhost")
	if err = seed.Send(ctx, stanza.Message{To: recipient, Type: stanza.ChatMessage, ID: "direct-client-" + string(id())}.Wrap(xml.NewDecoder(strings.NewReader(`<body>`+stanzaText(directOriginal)+`</body><origin-id xmlns="urn:xmpp:sid:0" id="`+directOrigin+`"/>`)))); err != nil {
		t.Fatal(err)
	}
	var directTarget domain.Message
	var directChat domain.Conversation
	eventually(t, func() bool {
		rows, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: registered.Account.ID, Kind: "direct", Limit: 100})
		if err != nil {
			return false
		}
		for _, c := range rows {
			if c.Address != "bob@localhost" {
				continue
			}
			messages, err := engine.Messages(ctx, c.ID, "", 100)
			if err != nil {
				return false
			}
			for _, m := range messages {
				if m.OriginalText == directOriginal {
					directChat = c
					directTarget = m
					return true
				}
			}
		}
		return false
	})
	directAuthored := "Direct reply without peer feature gate 😀"
	directSent, err := engine.SendMessageWithReply(ctx, id(), directChat.ID, directAuthored, directTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	directWire := awaitWire(directSent.Message.OriginalText)
	if directWire.Type != stanza.ChatMessage || directWire.Reply == nil || directWire.Reply.ID != directOrigin || directWire.Reply.To != "bob@localhost" || directWire.Fallback == nil || !strings.Contains(directWire.Body, directOriginal) {
		t.Fatal("direct legacy-compatible reply wire", directWire)
	}
	if authored, _ := domain.ReplyBody(directWire.Body, []domain.TextRange{{Start: directWire.Fallback.Body.Start, End: directWire.Fallback.Body.End}}); authored != directAuthored {
		t.Fatal("direct fallback changed authored text", authored)
	}
	if err = engine.RestoreConversation(ctx, registered.Account.ID, room.String(), "room", "Reply fixture"); err != nil {
		t.Fatal(err)
	}
	rows, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: registered.Account.ID, Kind: "room", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var conversation domain.Conversation
	for _, c := range rows {
		if c.Address == room.String() {
			conversation = c
			break
		}
	}
	if conversation.ID == "" {
		t.Fatal("fixture room absent")
	}
	initial, err := engine.RoomSession(ctx, conversation.ID)
	if err != nil || initial.Active || initial.State != "not_joined" {
		t.Fatal("retained room caused implicit membership", initial, err)
	}
	// A conflicting nickname is an explicit failure, not a joined session.
	if _, err = engine.JoinRoom(ctx, id(), conversation.ID, "Fixture Bob", initial.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	var session domain.RoomSession
	eventually(t, func() bool {
		session, err = engine.RoomSession(ctx, conversation.ID)
		return err == nil && session.State == "failed"
	})
	if session.LastErrorCode != "nickname_conflict" {
		t.Fatal("nickname conflict lost its condition", session)
	}
	if _, err = engine.JoinRoom(ctx, id(), conversation.ID, "Oie reply", session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		session, err = engine.RoomSession(ctx, conversation.ID)
		return err == nil && session.Active && session.State == "joined"
	})

	var receivedMentions []domain.ID
	for _, body := range []string{"Oie reply, hello!", "Hello @OIE REPLY!"} {
		payload := "<body>" + stanzaText(body) + "</body>"
		if err = seed.Send(ctx, stanza.Message{To: room, Type: stanza.GroupChatMessage, ID: string(id())}.Wrap(xml.NewDecoder(strings.NewReader(payload)))); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool {
			items, err := engine.Messages(ctx, conversation.ID, "", 100)
			if err != nil {
				return false
			}
			for _, item := range items {
				if item.OriginalText == body && item.MentionsYou {
					receivedMentions = append(receivedMentions, item.ID)
					return true
				}
			}
			return false
		})
	}
	chat, err := engine.Conversation(ctx, conversation.ID)
	if err != nil || !chat.UnreadMention {
		t.Fatal("incoming nickname mention did not mark the room", chat, err)
	}
	if _, err = engine.MarkMessagesRead(ctx, id(), conversation.ID, receivedMentions); err != nil {
		t.Fatal(err)
	}
	chat, err = engine.Conversation(ctx, conversation.ID)
	if err != nil || chat.UnreadMention {
		t.Fatal("read mentions kept the room marked", chat, err)
	}

	// The room explicitly discloses real JIDs. Verify an actual XEP-0372 stanza
	// against Prosody rather than inferring support from a discovery flag alone.
	var mention domain.MentionCandidate
	eventually(t, func() bool {
		candidates, err := engine.MentionCandidates(ctx, conversation.ID)
		if err != nil {
			return false
		}
		for _, candidate := range candidates.Items {
			if candidate.InsertText == "@Fixture Bob" && candidate.ReferenceMode == "structured" {
				mention = candidate
				return true
			}
		}
		return false
	})
	nickname := strings.TrimPrefix(mention.InsertText, "@")
	for _, body := range []struct{ before, after string }{{"", ", hello!"}, {"😀 hello ", "!"}} {
		mentionBody := body.before + nickname + body.after
		begin := utf8.RuneCountInString(body.before)
		end := begin + utf8.RuneCountInString(nickname)
		chosen := []domain.MentionSelection{{ParticipantID: mention.ID, Begin: begin, End: end}}
		acceptedMention, err := engine.SendMessageWithMentions(ctx, id(), conversation.ID, mentionBody, "", chosen)
		if err != nil || len(acceptedMention.Message.Mentions) != 1 {
			t.Fatal("mention acceptance", acceptedMention, err)
		}
		mentionWire := awaitWire(mentionBody)
		if len(mentionWire.Mentions) != 1 || mentionWire.Mentions[0].Type != "mention" || mentionWire.Mentions[0].URI != "xmpp:bob@localhost" || mentionWire.Mentions[0].Begin != begin || mentionWire.Mentions[0].End != end {
			t.Fatal("XEP-0372 mention was not preserved by the room", mentionWire)
		}
	}
	original := "Original 🧑🏽‍💻 & café\nsecond line"
	clientID := "owner-client-" + string(id())
	payload := `<body>` + stanzaText(original) + `</body><origin-id xmlns="urn:xmpp:sid:0" id="` + clientID + `"/>`
	if err = seed.Send(ctx, stanza.Message{To: room, Type: stanza.GroupChatMessage, ID: clientID}.Wrap(xml.NewDecoder(strings.NewReader(payload)))); err != nil {
		t.Fatal(err)
	}
	var target domain.Message
	eventually(t, func() bool {
		items, err := engine.Messages(ctx, conversation.ID, "", 100)
		if err != nil {
			return false
		}
		for _, m := range items {
			if m.OriginalText == original {
				target = m
				return true
			}
		}
		return false
	})
	fullTarget, err := engine.Message(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	target = fullTarget
	var targetID string
	for _, ref := range target.References {
		if ref.Scope == "room:"+room.String() {
			targetID = ref.ID
		}
	}
	if targetID == "" || targetID == clientID {
		t.Fatal("room-issued reference was not recovered", target.References)
	}

	verifyReply := func(sent domain.Message, authored string) {
		t.Helper()
		packet := awaitWire(sent.OriginalText)
		if packet.Type != stanza.GroupChatMessage || packet.Reply == nil || packet.Reply.ID != targetID || packet.Reply.To != owner.String() || packet.Origin.ID != string(sent.ID) || packet.Fallback == nil || packet.Fallback.For != "urn:xmpp:reply:0" {
			t.Fatal("wrong room reply metadata", packet)
		}
		text, quote := domain.ReplyBody(packet.Body, []domain.TextRange{{Start: packet.Fallback.Body.Start, End: packet.Fallback.Body.End}})
		if text != authored || !strings.Contains(quote, "café") || packet.Fallback.Body.Start != 0 || packet.Fallback.Body.End != utf8.RuneCountInString(strings.TrimSuffix(packet.Body, authored)) {
			t.Fatal("legacy/supporting receiver body mismatch", text, quote)
		}
		eventually(t, func() bool {
			messages, err := engine.Messages(ctx, conversation.ID, "", 100)
			if err != nil {
				return false
			}
			count := 0
			for _, m := range messages {
				if m.OriginalText == sent.OriginalText {
					count++
					if m.ID != sent.ID {
						return false
					}
				}
			}
			return count == 1
		})
	}
	authored := "Resposta com <&> 😀"
	sent, err := engine.SendMessageWithReply(ctx, id(), conversation.ID, authored, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	verifyReply(sent.Message, authored)
	// The frozen target/body/ranges survive a disconnected queue and rejoin.
	if _, err = engine.Disconnect(ctx, registered.Account.ID); err != nil {
		t.Fatal(err)
	}
	authored = "queued after disconnect 🍰"
	queued, err := engine.SendMessageWithReply(ctx, id(), conversation.ID, authored, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Message.SendState != "queued" {
		t.Fatal("offline reply was dispatched")
	}
	connect()
	eventually(t, func() bool {
		s, err := engine.RoomSession(ctx, conversation.ID)
		return err == nil && s.State == "joined"
	})
	verifyReply(queued.Message, authored)
	// Received corrections use the sender's client ID, not the room-assigned
	// reply target. Discovery after reconnect must establish occupant authority.
	response, err = seed.SendIQ(ctx, stanza.IQ{To: room, Type: stanza.GetIQ}.Wrap(xml.NewDecoder(strings.NewReader(`<query xmlns="http://jabber.org/protocol/disco#info"/>`))))
	if err != nil {
		t.Fatal(err)
	}
	var discovered struct {
		stanza.IQ
		Info disco.Info `xml:"http://jabber.org/protocol/disco#info query"`
	}
	err = xml.NewTokenDecoder(response).Decode(&discovered)
	_ = response.Close()
	if err != nil || discovered.Type != stanza.ResultIQ {
		t.Fatal("correction fixture room discovery", err)
	}
	occupantSupport := false
	for _, feature := range discovered.Info.Features {
		occupantSupport = occupantSupport || feature.Var == "urn:xmpp:occupant-id:0"
	}
	corrected := original + "\nCorrected room source"
	payload = `<body>` + stanzaText(corrected) + `</body><replace xmlns="urn:xmpp:message-correct:0" id="` + clientID + `"/>`
	if err = seed.Send(ctx, stanza.Message{To: room, Type: stanza.GroupChatMessage, ID: "room-edit-" + string(id())}.Wrap(xml.NewDecoder(strings.NewReader(payload)))); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		messages, err := engine.Messages(ctx, conversation.ID, "", 100)
		if err != nil {
			return false
		}
		matches := []domain.Message{}
		for _, message := range messages {
			if message.OriginalText == original || message.OriginalText == corrected {
				matches = append(matches, message)
			}
		}
		if !occupantSupport {
			return len(matches) == 2
		}
		return len(matches) == 1 && matches[0].ID == target.ID && matches[0].Edited && matches[0].OriginalText == corrected && matches[0].RevisionID != target.RevisionID
	})
	t.Logf("received room correction verified with XEP-0421 authority: %t", occupantSupport)
	session, err = engine.RoomSession(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.LeaveRoom(ctx, id(), conversation.ID, session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		s, err := engine.RoomSession(ctx, conversation.ID)
		return err == nil && !s.Active && s.State == "not_joined"
	})
	if _, err = engine.SendMessageWithReply(ctx, id(), conversation.ID, "must not send after leave", target.ID); err == nil {
		t.Fatal("left room accepted a send")
	}
	if _, err = engine.Disconnect(ctx, registered.Account.ID); err != nil {
		t.Fatal(err)
	}
	connect()
	time.Sleep(750 * time.Millisecond)
	session, err = engine.RoomSession(ctx, conversation.ID)
	if err != nil || session.Active || session.State == "joined" {
		t.Fatal("explicit leave was undone by reconnect", session, err)
	}
	// Rejoin explicitly, then the owner's removal must stop future automatic joins.
	if _, err = engine.JoinRoom(ctx, id(), conversation.ID, "Oie reply", session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		s, err := engine.RoomSession(ctx, conversation.ID)
		return err == nil && s.State == "joined"
	})
	removal := `<query xmlns="http://jabber.org/protocol/muc#admin"><item nick="Oie reply" role="none"><reason>isolated integration fixture</reason></item></query>`
	response, err = seed.SendIQ(ctx, stanza.IQ{To: room, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(removal))))
	if err != nil {
		t.Fatal(err)
	}
	var removed stanza.IQ
	err = xml.NewTokenDecoder(response).Decode(&removed)
	_ = response.Close()
	if err != nil || removed.Type != stanza.ResultIQ {
		t.Fatal("fixture removal", err)
	}
	eventually(t, func() bool {
		session, err = engine.RoomSession(ctx, conversation.ID)
		return err == nil && !session.Active
	})
	if session.LastErrorCode != "removed" {
		t.Fatal("room removal not exposed", session)
	}
	if _, err = engine.Disconnect(ctx, registered.Account.ID); err != nil {
		t.Fatal(err)
	}
	connect()
	time.Sleep(750 * time.Millisecond)
	session, err = engine.RoomSession(ctx, conversation.ID)
	if err != nil || session.Active || session.State == "joined" {
		t.Fatal("revoked membership was automatically rejoined", session, err)
	}
}
