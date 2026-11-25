package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type schedulingProtocol struct {
	*roomHTTPProtocol
	sent chan application.Outgoing
}

func (p *schedulingProtocol) AttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error {
	return nil
}

func (p *schedulingProtocol) Send(ctx context.Context, account domain.Account, out application.Outgoing) string {
	p.sent <- out
	return p.roomHTTPProtocol.Send(ctx, account, out)
}

func TestSchedulingAcrossRoomsAndAttachmentDispatchAfterRestart(t *testing.T) {
	ctx, dir := t.Context(), t.TempDir()
	protocol := &schedulingProtocol{roomHTTPProtocol: &roomHTTPProtocol{protocolStub: &protocolStub{
		connected: true, outcome: application.Submitted, release: make(chan struct{}),
	}, rooms: make(map[domain.ID]domain.RoomSession)}, sent: make(chan application.Outgoing, 2)}
	e, store, server := newMessagingEngine(t, dir, protocol)
	protocol.host = e
	direct := openTestConversation(t, e, "alice@localhost")
	account := direct.AccountID
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	rooms := make(map[string]domain.ID)
	for _, address := range []string{"one@rooms.localhost", "two@rooms.localhost"} {
		err := e.Receive(ctx, application.Incoming{AccountID: account, Address: address, Kind: "room", SenderName: "Speaker", ProviderID: string(id()), OriginalText: "Room history", Historical: true, CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
	}
	conversations, err := e.Conversations(ctx, "", application.ConversationQuery{AccountID: account, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conversations {
		if c.Kind != "room" {
			continue
		}
		rooms[c.Address] = c.ID
		joined, err := client.JoinRoomWithResponse(ctx, string(c.ID), &engineapi.JoinRoomParams{IdempotencyKey: string(id())}, engineapi.JoinRoom{})
		if err != nil || joined.JSON200 == nil || !joined.JSON200.Active {
			t.Fatalf("join intent: %+v %v", joined, err)
		}
	}
	room, other := string(rooms["one@rooms.localhost"]), string(rooms["two@rooms.localhost"])
	fileID := string(id())
	attachmentForm := func(content string, extra bool) (*bytes.Buffer, string) {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, err := form.CreateFormFile("file", "reference.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(file, content); err != nil {
			t.Fatal(err)
		}
		if extra {
			if err = form.WriteField("extra", "unexpected"); err != nil {
				t.Fatal(err)
			}
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		return &body, form.FormDataContentType()
	}
	stage := func(conversation, content string, extra bool, id string) *engineapi.StageMessageAttachmentResponse {
		t.Helper()
		body, contentType := attachmentForm(content, extra)
		res, err := client.StageMessageAttachmentWithBodyWithResponse(ctx, conversation, id, contentType, body)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// Older engines capped the staged file independently of XMPP discovery.
	large := stage(room, strings.Repeat("x", (20<<20)+1), false, string(id()))
	if large.JSON201 == nil || large.JSON201.Size != (20<<20)+1 {
		t.Fatalf("large staged attachment: HTTP %d", large.StatusCode())
	}
	// Exceed the default request limit to exercise the multipart exception.
	content := strings.Repeat("persistent file bytes", 1<<16)
	staged := stage(room, content, false, fileID)
	if staged.JSON201 == nil || staged.JSON201.Size != int64(len(content)) {
		t.Fatalf("stage: %s", staged.Body)
	}
	if stage(room, content, false, fileID).JSON200 == nil || stage(other, content, false, fileID).StatusCode() != 409 ||
		stage(room, "different", false, fileID).StatusCode() != 409 || stage(room, content, true, string(id())).StatusCode() != 400 {
		t.Fatal("invalid multipart or staging replay accepted")
	}
	truncated, contentType := attachmentForm(content, false)
	truncated.Truncate(truncated.Len() - len("--\r\n"))
	truncatedID := string(id())
	rejected, err := client.StageMessageAttachmentWithBodyWithResponse(ctx, room, truncatedID, contentType, truncated)
	if err != nil || rejected.StatusCode() != 400 {
		t.Fatalf("truncated multipart was not rejected: %+v, %v", rejected, err)
	}
	missing, err := client.GetStagedMessageAttachmentWithResponse(ctx, room, truncatedID, nil)
	if err != nil || missing.StatusCode() != 404 {
		t.Fatalf("truncated multipart left a stored attachment: %+v, %v", missing, err)
	}
	at := time.Now().Add(time.Hour).UTC()
	keys := []string{string(id()), string(id())}
	files := []string{fileID}
	accept := func(conversation, key string, at time.Time, files *[]string) *engineapi.SendMessageResponse {
		t.Helper()
		res, err := client.SendMessageWithResponse(ctx, conversation, &engineapi.SendMessageParams{IdempotencyKey: key}, engineapi.SendMessage{
			OriginalText: "Studio https://example.com/studio", AttachmentIds: files, ScheduledAt: &at,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if accept(other, string(id()), at, &files).StatusCode() != 404 {
		t.Fatal("foreign attachment accepted")
	}
	first, second := accept(room, keys[0], at, &files).JSON202, accept(other, keys[1], at.Add(time.Hour), nil).JSON202
	if first == nil || second == nil || first.ScheduledAt == nil || !first.ScheduledAt.Equal(at) || first.Attachments == nil {
		t.Fatal("scheduled acceptance did not preserve composition")
	}
	limit := 1
	page, err := client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{Limit: &limit})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Conversation.Id != room || page.JSON200.NextCursor == nil || page.JSON200.Destinations != nil {
		t.Fatalf("first scheduled page: %+v %v", page, err)
	}
	cursor := page.JSON200.NextCursor
	page, err = client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{Cursor: cursor})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Message.Id != second.Id {
		t.Fatalf("next scheduled page: %+v %v", page, err)
	}
	includeDestinations := true
	page, err = client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{ConversationId: &room, Limit: &limit, IncludeDestinations: &includeDestinations})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Message.Id != first.Id {
		t.Fatalf("scoped scheduled page: %+v %v", page, err)
	}
	if page.JSON200.Destinations == nil || len(*page.JSON200.Destinations) != 2 {
		t.Fatalf("expected both pending destinations outside the page filter: %+v", page.JSON200.Destinations)
	}
	destinations := map[string]bool{}
	for _, option := range *page.JSON200.Destinations {
		if option.Title == "" || option.AccountId == "" {
			t.Fatalf("incomplete scheduled destination: %+v", option)
		}
		destinations[option.Id] = true
	}
	if !destinations[room] || !destinations[other] {
		t.Fatalf("missing pending destination: %+v", destinations)
	}
	selected := []string{room, other}
	page, err = client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{ConversationIds: &selected, Limit: &limit})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Message.Id != first.Id || page.JSON200.NextCursor == nil {
		t.Fatalf("multi-conversation first page: %+v %v", page, err)
	}
	selected[0], selected[1] = selected[1], selected[0]
	selectionCursor := page.JSON200.NextCursor
	page, err = client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{ConversationIds: &selected, Cursor: selectionCursor})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Message.Id != second.Id {
		t.Fatalf("reordered selection continuation: %+v %v", page, err)
	}
	page, err = client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{ConversationId: &room, Cursor: selectionCursor})
	if err != nil || page.StatusCode() != 400 {
		t.Fatalf("cursor accepted with another filter: %+v %v", page, err)
	}
	subset := []string{room, string(direct.ID)}
	page, err = client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{ConversationIds: &subset})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Message.Id != first.Id {
		t.Fatalf("multi-conversation subset: %+v %v", page, err)
	}
	scheduled, err := store.PendingMessages(ctx, time.Now())
	if err != nil || len(scheduled) != 0 {
		t.Fatalf("future message eligible: %+v %v", scheduled, err)
	}
	updateKey := &engineapi.UpdatePendingMessageParams{IdempotencyKey: string(id())}
	later := at.Add(30 * time.Minute)
	edit := engineapi.UpdatePendingMessage{ExpectedRevisionId: first.RevisionId, ScheduledAt: &later,
		Composition: &engineapi.OutgoingComposition{OriginalText: "", AttachmentIds: &files}}
	update, err := client.UpdatePendingMessageWithResponse(ctx, first.Id, updateKey, edit)
	if err != nil || update.JSON200 == nil || update.JSON200.Composition == nil ||
		update.JSON200.Composition.OriginalText != "" || !update.JSON200.ScheduledAt.Equal(later) {
		t.Fatalf("composition and schedule update: %+v %v", update, err)
	}
	revision := update.JSON200.RevisionId
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	e, _, server = newMessagingEngine(t, dir, protocol)
	protocol.host = e
	client, err = engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	download, err := client.GetStagedMessageAttachmentWithResponse(ctx, room, fileID, nil)
	if err != nil || download.StatusCode() != 200 || string(download.Body) != content {
		t.Fatalf("staged bytes not durable: %+v %v", download, err)
	}
	kind, params, err := mime.ParseMediaType(download.HTTPResponse.Header.Get("Content-Disposition"))
	if err != nil || kind != "attachment" || params["filename"] != staged.JSON201.FileName {
		t.Fatalf("staged attachment disposition: %q, %v", download.HTTPResponse.Header.Get("Content-Disposition"), err)
	}
	partial, err := client.GetStagedMessageAttachmentWithResponse(ctx, room, fileID, &engineapi.GetStagedMessageAttachmentParams{Range: new("bytes=0-1")})
	if err != nil || partial.StatusCode() != 206 || string(partial.Body) != content[:2] ||
		partial.HTTPResponse.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-1/%d", len(content)) {
		t.Fatalf("staged byte range: %+v %v", partial, err)
	}
	if replay := accept(room, keys[0], at, &files); replay.JSON200 == nil || replay.JSON200.Id != first.Id || !replay.JSON200.ScheduledAt.Equal(at) {
		t.Fatal("acceptance snapshot not replayed after edit and restart")
	}
	sendNowKey := &engineapi.SendScheduledMessageNowParams{IdempotencyKey: string(id())}
	sendNow := engineapi.SendScheduledMessageNow{ExpectedRevisionId: revision}
	becameEligible, err := client.SendScheduledMessageNowWithResponse(ctx, first.Id, sendNowKey, sendNow)
	if err != nil || becameEligible.JSON200 == nil || becameEligible.JSON200.Id != first.Id || becameEligible.JSON200.ScheduledAt != nil {
		t.Fatalf("send now did not preserve the same intent: %+v %v", becameEligible, err)
	}
	page, err = client.ListScheduledMessagesWithResponse(ctx, &engineapi.ListScheduledMessagesParams{})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Message.Id != second.Id {
		t.Fatal("send now did not remove exactly one scheduled item")
	}
	stop := runWorker(t, e)
	defer stop()
	var outgoing application.Outgoing
	select {
	case outgoing = <-protocol.sent:
	case <-time.After(5 * time.Second):
		t.Fatal("eligible send did not reach the provider")
	}
	if outgoing.Kind != "room" || outgoing.Address != "one@rooms.localhost" || len(outgoing.Attachments) != 1 {
		t.Fatalf("dispatch lost scope or composition: %+v", outgoing)
	}
	reader, err := outgoing.Attachments[0].Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != content {
		t.Fatal("provider could not read durable file bytes")
	}
	current, err := client.GetMessageWithResponse(ctx, first.Id)
	if err != nil || current.JSON200 == nil || current.JSON200.SendState != "dispatching" || current.JSON200.DispatchStarted == nil || !*current.JSON200.DispatchStarted {
		t.Fatalf("dispatch claim not observable: %+v %v", current, err)
	}
	blocked, err := client.UpdatePendingMessageWithResponse(ctx, first.Id, &engineapi.UpdatePendingMessageParams{IdempotencyKey: string(id())},
		engineapi.UpdatePendingMessage{ExpectedRevisionId: current.JSON200.RevisionId, ScheduledAt: &later})
	if err != nil || blocked.StatusCode() != 409 {
		t.Fatalf("edit after claim: %+v %v", blocked, err)
	}
	replayed, err := client.SendScheduledMessageNowWithResponse(ctx, first.Id, sendNowKey, sendNow)
	if err != nil || replayed.JSON200 == nil || replayed.JSON200.RevisionId != becameEligible.JSON200.RevisionId {
		t.Fatal("send now retry lost original accepted result")
	}
	close(protocol.release)
	waitMessage(t, e, domain.ID(first.Id), "submitted")
	select {
	case <-protocol.sent:
		t.Fatal("an additional logical send reached the provider")
	default:
	}
}
