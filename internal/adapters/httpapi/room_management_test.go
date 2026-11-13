package httpapi_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type managementHTTPProtocol struct {
	*roomHTTPProtocol
	commands atomic.Int32
}

func (p *managementHTTPProtocol) RoomManagement(context.Context, domain.Account, domain.Conversation, string) (domain.RoomManagement, error) {
	return domain.RoomManagement{State: "available", Revision: "room-revision", Actions: []string{"topic"}, Affiliations: []string{}}, nil
}

func (p *managementHTTPProtocol) ManageRoom(_ context.Context, _ domain.Account, _ domain.Conversation, _ domain.ID, cmd domain.RoomManagementCommand, _ domain.Message) string {
	if cmd.ExpectedRevision != "room-revision" {
		return "stale"
	}
	p.commands.Add(1)
	return "submitted"
}

func TestRoomManagementHTTPReplayAndScope(t *testing.T) {
	p := &managementHTTPProtocol{roomHTTPProtocol: &roomHTTPProtocol{protocolStub: &protocolStub{connected: true}, rooms: map[domain.ID]domain.RoomSession{}}}
	e, _, server := newMessagingEngine(t, t.TempDir(), p)
	p.host = e
	ctx := t.Context()
	direct := openTestConversation(t, e, "alice@localhost")
	room, err := e.OpenRoom(ctx, id(), direct.AccountID, "garden@rooms.localhost", "Host", "", false)
	if err != nil {
		t.Fatal(err)
	}
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	account, err := e.Account(ctx, direct.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := e.RoomSession(ctx, room.Conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.JoinRoom(ctx, account, room.Conversation, "Host", session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	roomID := string(room.Conversation.ID)
	info, err := client.GetRoomManagementWithResponse(ctx, roomID, &engineapi.GetRoomManagementParams{})
	if err != nil || info.JSON200 == nil || info.JSON200.Revision == nil {
		t.Fatalf("read: %+v %v", info, err)
	}
	topic := "Reading"
	cmd := engineapi.ManageRoom{Action: "topic", ExpectedRevision: *info.JSON200.Revision, Value: &topic}
	key := &engineapi.ManageRoomParams{IdempotencyKey: string(id())}
	for range 2 {
		result, err := client.ManageRoomWithResponse(ctx, roomID, key, cmd)
		if err != nil || result.JSON200 == nil || result.JSON200.State != "submitted" {
			t.Fatalf("command: %+v %v", result, err)
		}
	}
	topic = "Changed"
	result, err := client.ManageRoomWithResponse(ctx, roomID, key, cmd)
	if err != nil || result.StatusCode() != 409 || p.commands.Load() != 1 {
		t.Fatalf("changed replay: %+v %v, effects=%d", result, err, p.commands.Load())
	}
	result, err = client.ManageRoomWithResponse(ctx, string(direct.ID), &engineapi.ManageRoomParams{IdempotencyKey: string(id())}, cmd)
	if err != nil || result.StatusCode() != 409 || p.commands.Load() != 1 {
		t.Fatalf("foreign room: %+v %v, effects=%d", result, err, p.commands.Load())
	}
	if _, err := e.LeaveRoom(ctx, id(), room.Conversation.ID, session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	result, err = client.ManageRoomWithResponse(ctx, roomID, &engineapi.ManageRoomParams{IdempotencyKey: string(id())}, cmd)
	if err != nil || result.JSON200 == nil || result.JSON200.State != "stale" || p.commands.Load() != 1 {
		t.Fatalf("left room: %+v %v, effects=%d", result, err, p.commands.Load())
	}
}

func TestRoomModerationHidesExistingAndLateOriginals(t *testing.T) {
	e, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	ctx := t.Context()
	direct := openTestConversation(t, e, "alice@localhost")
	address := "garden@rooms.localhost"
	at := time.Now().UTC()
	receive := func(stanzaID string) {
		t.Helper()
		err := e.Receive(ctx, application.Incoming{AccountID: direct.AccountID, Kind: "room", Address: address, SenderName: "Guest",
			CreatedAt: at, ProviderID: stanzaID, OriginalText: "unwelcome content", References: []domain.MessageReference{{Scope: "room:" + address, ID: stanzaID}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	moderate := func(stanzaID string) {
		t.Helper()
		err := e.Receive(ctx, application.Incoming{AccountID: direct.AccountID, Kind: "room", Address: address, Moderation: &application.IncomingRoomModeration{
			StanzaID: stanzaID, MessageModeration: domain.MessageModeration{At: at, Reason: "Room rules"},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	receive("old")
	moderate("old")
	moderate("old")
	moderate("late")
	receive("late")
	rooms, err := e.Conversations(ctx, "", application.ConversationQuery{AccountID: direct.AccountID, Kind: "room", Limit: 10})
	if err != nil || len(rooms) != 1 || rooms[0].LastMessage != "" {
		t.Fatalf("summary leaked moderated content: %+v %v", rooms, err)
	}
	messages, err := e.Messages(ctx, rooms[0].ID, "", 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("timeline: %+v %v", messages, err)
	}
	for _, m := range messages {
		status, body := request(t, server.URL, "GET", "/api/v1/messages/"+string(m.ID), "", "")
		if status != 200 || m.Moderation == nil || strings.Contains(string(body), "unwelcome") || !strings.Contains(string(body), "Room rules") {
			t.Fatalf("moderation projection: %+v, %d %s", m, status, body)
		}
	}
}
