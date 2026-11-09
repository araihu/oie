package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type mentionHTTPProtocol struct {
	*roomHTTPProtocol
	scope domain.ID
	reads int
	items []domain.MentionCandidate
}

func (p *mentionHTTPProtocol) MentionCandidates(_ context.Context, a domain.Account, c domain.Conversation) (domain.MentionCandidates, error) {
	p.reads++
	out := domain.MentionCandidates{ConversationID: c.ID, AccountID: a.ID, State: "known", Items: []domain.MentionCandidate{}}
	if c.ID == p.scope {
		out.Items = append(out.Items, p.items...)
	}
	return out, nil
}
func TestRoomMentionHTTPScopesRangesAndImmutableReplay(t *testing.T) {
	p := &mentionHTTPProtocol{roomHTTPProtocol: &roomHTTPProtocol{protocolStub: &protocolStub{connected: true}, rooms: map[domain.ID]domain.RoomSession{}}}
	e, _, server := newMessagingEngine(t, t.TempDir(), p)
	p.host = e
	direct := openTestConversation(t, e, "alice@localhost")
	address := "room@rooms.localhost"
	if err := e.Receive(t.Context(), application.Incoming{AccountID: direct.AccountID, Address: address, Kind: "room", SenderName: "Speaker", ProviderID: string(id()), OriginalText: "Room source", Historical: true, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	chats, err := e.Conversations(t.Context(), "", application.ConversationQuery{AccountID: direct.AccountID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var room domain.Conversation
	for _, c := range chats {
		if c.Address == address {
			room = c
		}
	}
	p.scope = room.ID
	p.items = []domain.MentionCandidate{{ID: "opaque-juliet", DisplayName: "Juliet", InsertText: "@Juliet", ReferenceMode: "structured", URI: "xmpp:private-jid@localhost"}}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	choices, err := client.GetMentionCandidatesWithResponse(t.Context(), string(room.ID))
	if err != nil || choices.JSON200 == nil || len(choices.JSON200.Items) != 1 || choices.JSON200.Items[0].InsertText != "@Juliet" {
		t.Fatal(choices, err)
	}
	data, _ := json.Marshal(choices.JSON200)
	if strings.Contains(string(data), "private-jid") {
		t.Fatal("candidate endpoint leaked protocol URI", string(data))
	}
	if p.joins != 0 {
		t.Fatal("candidate read joined room")
	}
	if _, err := e.JoinRoom(t.Context(), id(), room.ID, "Alice", ""); err != nil {
		t.Fatal(err)
	}
	selections := []engineapi.MentionSelection{{ParticipantId: "opaque-juliet", Begin: 2, End: 9}}
	body := engineapi.SendMessage{OriginalText: "😀 @Juliet", Mentions: &selections}
	key := string(id())
	sent, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: key}, body)
	if err != nil || sent.JSON202 == nil || sent.JSON202.Mentions == nil || len(*sent.JSON202.Mentions) != 1 || (*sent.JSON202.Mentions)[0].ReferenceMode != "structured" {
		t.Fatal(sent, err)
	}
	data, _ = json.Marshal(sent.JSON202)
	if strings.Contains(string(data), "private-jid") {
		t.Fatal("message response leaked protocol URI", string(data))
	}
	p.items = nil
	reads := p.reads
	replay, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: key}, body)
	if err != nil || replay.JSON200 == nil || p.reads != reads {
		t.Fatal("replay read changed candidates", replay, err)
	}
	changed := []engineapi.MentionSelection{{ParticipantId: "other-room-user", Begin: 2, End: 9}}
	body.Mentions = &changed
	conflict, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: key}, body)
	if err != nil || conflict.StatusCode() != 409 {
		t.Fatal("changed recipient reused key", conflict, err)
	}
	invalid, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: string(id())}, body)
	if err != nil || invalid.StatusCode() != 400 {
		t.Fatal("foreign candidate accepted", invalid, err)
	}
	selections[0].Begin = -1
	body.Mentions = &selections
	invalid, err = client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: string(id())}, body)
	if err != nil || invalid.StatusCode() != 400 {
		t.Fatal("invalid codepoint range accepted", invalid, err)
	}
	directSend, err := client.SendMessageWithResponse(t.Context(), string(direct.ID), &engineapi.SendMessageParams{IdempotencyKey: string(id())}, engineapi.SendMessage{OriginalText: "😀 @Juliet", Mentions: &changed})
	if err != nil || directSend.StatusCode() != 400 {
		t.Fatal("room mention accepted in direct chat", directSend, err)
	}
	status, _ := request(t, server.URL, http.MethodPost, "/api/v1/conversations/"+string(room.ID)+"/messages", string(id()), `{"originalText":"@Juliet","mentions":[{"participantId":"opaque-juliet","begin":0,"end":7,"uri":"xmpp:forged@localhost"}]}`)
	if status != 400 {
		t.Fatal("client protocol URI accepted", status)
	}
}
