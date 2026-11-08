package httpapi_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type roomHTTPProtocol struct {
	*protocolStub
	roomMu        sync.Mutex
	rooms         map[domain.ID]domain.RoomSession
	host          *application.Engine
	joins, leaves int
	lastKind      string
}

func (p *roomHTTPProtocol) RoomSession(_ context.Context, a domain.Account, c domain.Conversation) (domain.RoomSession, error) {
	p.roomMu.Lock()
	defer p.roomMu.Unlock()
	if s, ok := p.rooms[c.ID]; ok {
		return s, nil
	}
	return domain.RoomSession{AccountID: a.ID, ConversationID: c.ID, State: "not_joined", UpdatedAt: time.Now().UTC()}, nil
}
func (p *roomHTTPProtocol) JoinRoom(ctx context.Context, a domain.Account, c domain.Conversation, nickname string, revision domain.ID) (domain.RoomSession, error) {
	if nickname == "" {
		nickname = "default-nickname"
	}
	s := domain.RoomSession{AccountID: a.ID, ConversationID: c.ID, IntentRevisionID: revision, State: "joined", Nickname: nickname, UpdatedAt: time.Now().UTC()}
	p.roomMu.Lock()
	p.rooms[c.ID] = s
	p.joins++
	p.roomMu.Unlock()
	return s, p.host.RoomSessionChanged(ctx, s)
}
func (p *roomHTTPProtocol) LeaveRoom(ctx context.Context, a domain.Account, c domain.Conversation, revision domain.ID) (domain.RoomSession, error) {
	s := domain.RoomSession{AccountID: a.ID, ConversationID: c.ID, IntentRevisionID: revision, State: "not_joined", UpdatedAt: time.Now().UTC()}
	p.roomMu.Lock()
	p.rooms[c.ID] = s
	p.leaves++
	p.roomMu.Unlock()
	return s, p.host.RoomSessionChanged(ctx, s)
}
func (p *roomHTTPProtocol) Send(ctx context.Context, a domain.Account, out application.Outgoing) string {
	p.roomMu.Lock()
	p.lastKind = out.Kind
	p.roomMu.Unlock()
	return p.protocolStub.Send(ctx, a, out)
}

func TestRoomSessionHTTPRequiresExplicitIntentAndKeepsReplyReplay(t *testing.T) {
	protocol := &roomHTTPProtocol{protocolStub: &protocolStub{connected: true, outcome: application.Submitted}, rooms: map[domain.ID]domain.RoomSession{}}
	e, store, server := newMessagingEngine(t, t.TempDir(), protocol)
	protocol.host = e
	direct := openTestConversation(t, e, "alice@localhost")
	address := "room@rooms.localhost"
	if err := e.Receive(t.Context(), application.Incoming{AccountID: direct.AccountID, Address: address, Kind: "room", SenderName: "Speaker 😀", ProviderID: string(id()), OriginalText: "Retained room original", Historical: true, CreatedAt: time.Now().UTC()}); err != nil {
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
	if room.Kind != "room" {
		t.Fatal("room fixture absent")
	}
	messages, err := e.Messages(t.Context(), room.ID, "", 10)
	if err != nil || len(messages) != 1 {
		t.Fatal(err)
	}
	target := string(messages[0].ID)
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	before, err := client.GetRoomSessionWithResponse(t.Context(), string(room.ID))
	if err != nil || before.JSON200 == nil || before.JSON200.Active || before.JSON200.State != "not_joined" || before.JSON200.IntentRevisionId != nil {
		t.Fatal("room read invented membership", before, err)
	}
	reply := engineapi.SendMessage{OriginalText: "Room response", ReplyToMessageId: &target}
	blocked, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: string(id())}, reply)
	if err != nil || blocked.StatusCode() != 409 || blocked.ApplicationproblemJSON409 == nil || blocked.ApplicationproblemJSON409.CurrentRoomSession == nil || blocked.ApplicationproblemJSON409.CurrentRoomSession.Active {
		t.Fatal("unrequested room allowed send", blocked, err)
	}
	joinKey := string(id())
	joinBody := engineapi.JoinRoom{}
	joined, err := client.JoinRoomWithResponse(t.Context(), string(room.ID), &engineapi.JoinRoomParams{IdempotencyKey: joinKey}, joinBody)
	if err != nil || joined.JSON200 == nil || !joined.JSON200.Active || joined.JSON200.IntentRevisionId == nil || joined.JSON200.IntentId == nil {
		t.Fatal("explicit join intent not durable", joined, err)
	}
	protocol.roomMu.Lock()
	joins := protocol.joins
	protocol.roomMu.Unlock()
	if joins != 0 {
		t.Fatal("HTTP read/acceptance synchronously joined room")
	}
	sendKey := string(id())
	accepted, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: sendKey}, reply)
	if err != nil || accepted.JSON202 == nil || accepted.JSON202.Reply == nil || accepted.JSON202.Reply.BodyText != reply.OriginalText {
		t.Fatal("active room reply not queued", accepted, err)
	}
	stale, err := client.LeaveRoomWithResponse(t.Context(), string(room.ID), &engineapi.LeaveRoomParams{IdempotencyKey: string(id())}, engineapi.LeaveRoom{})
	if err != nil || stale.StatusCode() != 409 || stale.ApplicationproblemJSON409 == nil || stale.ApplicationproblemJSON409.CurrentRoomSession == nil {
		t.Fatal("stale leave revision accepted", stale, err)
	}
	left, err := client.LeaveRoomWithResponse(t.Context(), string(room.ID), &engineapi.LeaveRoomParams{IdempotencyKey: string(id())}, engineapi.LeaveRoom{ExpectedRevisionId: joined.JSON200.IntentRevisionId})
	if err != nil || left.JSON200 == nil || left.JSON200.Active || left.JSON200.IntentRevisionId == nil || *left.JSON200.IntentRevisionId == *joined.JSON200.IntentRevisionId {
		t.Fatal("leave did not supersede join", left, err)
	}
	replayJoin, err := client.JoinRoomWithResponse(t.Context(), string(room.ID), &engineapi.JoinRoomParams{IdempotencyKey: joinKey}, joinBody)
	if err != nil || replayJoin.JSON200 == nil || replayJoin.JSON200.Active {
		t.Fatal("old join replay reactivated leave", replayJoin, err)
	}
	replaySend, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: sendKey}, reply)
	if err != nil || replaySend.JSON200 == nil || replaySend.JSON200.Id != accepted.JSON202.Id || replaySend.JSON200.ReplyToMessageId == nil || *replaySend.JSON200.ReplyToMessageId != target {
		t.Fatal("accepted reply replay failed after leave", replaySend, err)
	}
	stop := runWorker(t, e)
	defer stop()
	time.Sleep(650 * time.Millisecond)
	protocol.mu.Lock()
	sent := protocol.count
	protocol.mu.Unlock()
	if sent != 0 {
		t.Fatal("inactive room dispatched queued reply")
	}
	current, err := e.Message(t.Context(), domain.ID(accepted.JSON202.Id))
	if err != nil || current.SendState != "queued" {
		t.Fatal("left room lost pending send", current, err)
	}
	if _, err = e.CancelMessage(t.Context(), id(), current.ID, current.RevisionID); err != nil {
		t.Fatal("pending room reply cannot cancel", err)
	}
	again, err := client.JoinRoomWithResponse(t.Context(), string(room.ID), &engineapi.JoinRoomParams{IdempotencyKey: string(id())}, engineapi.JoinRoom{ExpectedRevisionId: left.JSON200.IntentRevisionId})
	if err != nil || again.JSON200 == nil || !again.JSON200.Active {
		t.Fatal("new explicit join failed", again, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		session, err := client.GetRoomSessionWithResponse(t.Context(), string(room.ID))
		if err != nil || session.JSON200 == nil {
			t.Fatal(err)
		}
		if session.JSON200.State == "joined" {
			if session.JSON200.Nickname == nil || *session.JSON200.Nickname != "default-nickname" {
				t.Fatal("adapter nickname absent")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("self presence never observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	final, err := client.SendMessageWithResponse(t.Context(), string(room.ID), &engineapi.SendMessageParams{IdempotencyKey: string(id())}, reply)
	if err != nil || final.JSON202 == nil {
		t.Fatal("joined room reply rejected", final, err)
	}
	for {
		protocol.mu.Lock()
		sent = protocol.count
		protocol.mu.Unlock()
		if sent == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("joined room reply not dispatched")
		}
		time.Sleep(10 * time.Millisecond)
	}
	protocol.roomMu.Lock()
	kind := protocol.lastKind
	protocol.roomMu.Unlock()
	if kind != "room" {
		t.Fatal("room sent through direct-chat adapter mode", kind)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("unexpected extra room send", err)
	}
}
