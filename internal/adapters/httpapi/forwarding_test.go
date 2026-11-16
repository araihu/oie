package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type forwardingMedia struct{}

const forwardingAudioURL = "https://example.test/voice-123"

func (forwardingMedia) Open(context.Context, string, application.MediaReadRequest) (application.MediaRead, error) {
	return application.MediaRead{}, application.ErrInvalid
}

func (forwardingMedia) OpenForwardAttachment(_ context.Context, url string) (string, io.ReadCloser, error) {
	switch url {
	case "https://example.test/missing.pdf":
		return "", nil, application.ErrNotFound
	case forwardingAudioURL:
		return "audio/mpeg", io.NopCloser(strings.NewReader("full audio bytes")), nil
	}
	return "application/pdf", io.NopCloser(strings.NewReader("full file bytes")), nil
}

func TestForwardMessageCreatesScopedOutboxCopies(t *testing.T) {
	ctx := t.Context()
	protocol := &schedulingProtocol{roomHTTPProtocol: &roomHTTPProtocol{protocolStub: &protocolStub{}, rooms: make(map[domain.ID]domain.RoomSession)}}
	e, _, server := newMessagingEngine(t, t.TempDir(), protocol)
	e.SetMediaReader(forwardingMedia{})
	source := openTestConversation(t, e, "alice@localhost")
	other := openTestConversation(t, e, "carol@localhost")
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.OpenRoom(ctx, id(), source.AccountID, "one@rooms.localhost", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.OpenRoom(ctx, id(), other.AccountID, "two@rooms.localhost", "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	stagedID := id()
	if _, _, err = e.StageAttachment(ctx, source.ID, stagedID, "saved.txt", "text/plain", strings.NewReader("saved file bytes")); err != nil {
		t.Fatal(err)
	}
	original, err := e.SendComposition(ctx, id(), source.ID, domain.OutgoingComposition{OriginalText: "**Copy this**", AttachmentIDs: []domain.ID{stagedID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	post := func(message domain.Message, key, destination, revision domain.ID, status int) *engineapi.Message {
		t.Helper()
		result, err := client.ForwardMessageWithResponse(ctx, string(message.ID), &engineapi.ForwardMessageParams{IdempotencyKey: string(key)},
			engineapi.ForwardMessage{ExpectedRevisionId: string(revision), ConversationId: string(destination)})
		if err != nil || result.StatusCode() != status {
			t.Fatalf("forward status %d: %+v %v", status, result, err)
		}
		if result.JSON202 != nil {
			return result.JSON202
		}
		return result.JSON200
	}
	key := id()
	copied := post(original.Message, key, first.Conversation.ID, original.Message.RevisionID, 202)
	repeated := post(original.Message, key, first.Conversation.ID, original.Message.RevisionID, 200)
	post(original.Message, key, second.Conversation.ID, original.Message.RevisionID, 409)
	crossAccount := post(original.Message, id(), second.Conversation.ID, original.Message.RevisionID, 202)
	post(original.Message, id(), second.Conversation.ID, id(), 409)
	direct := post(original.Message, id(), other.ID, original.Message.RevisionID, 202)
	if direct == nil || direct.ConversationId != string(other.ID) || direct.AccountId != string(other.AccountID) {
		t.Fatalf("direct destination lost scope: %+v", direct)
	}
	if copied == nil || repeated == nil || crossAccount == nil || copied.Id != repeated.Id || copied.Id == crossAccount.Id || crossAccount.AccountId != string(other.AccountID) {
		t.Fatalf("copy or replay scope was lost: %+v %+v %+v", copied, repeated, crossAccount)
	}
	for _, copy := range []*engineapi.Message{copied, crossAccount, direct} {
		if copy.Id == string(original.Message.ID) || copy.OriginalText != "**Copy this**" || copy.Reply != nil || copy.Mentions != nil || copy.SendState != "queued" || copy.Attachments == nil || len(*copy.Attachments) != 1 || (*copy.Attachments)[0].Id == string(stagedID) {
			t.Fatalf("invalid copied composition: %+v", copy)
		}
		file, content, err := e.OpenStagedAttachment(ctx, domain.ID(copy.ConversationId), domain.ID((*copy.Attachments)[0].Id))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(content)
		_ = content.Close()
		if err != nil || string(body) != "saved file bytes" || file.FileName != "saved.txt" {
			t.Fatalf("attachment was not copied: %+v %s %v", file, body, err)
		}
	}
	received := func(in application.Incoming) domain.Message {
		t.Helper()
		in.AccountID = source.AccountID
		in.Address = source.Address
		in.SenderName = "Speaker"
		in.ProviderID = string(id())
		in.CreatedAt = time.Now().UTC()
		if err := e.Receive(ctx, in); err != nil {
			t.Fatal(err)
		}
		messages, err := e.Messages(ctx, source.ID, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range messages {
			if message.OriginalText == in.OriginalText {
				return message
			}
		}
		t.Fatal("received message missing")
		return domain.Message{}
	}
	withURL := received(application.Incoming{OriginalText: "Report: [full report](https://example.test/report.pdf) and https://example.test/page"})
	remote := post(withURL, id(), first.Conversation.ID, withURL.RevisionID, 202)
	if remote == nil || remote.OriginalText != "Report: full report and https://example.test/page" || remote.Attachments == nil || len(*remote.Attachments) != 1 || (*remote.Attachments)[0].FileName != "report.pdf" {
		t.Fatalf("remote attachment was not replaced by a new upload: %+v", remote)
	}
	voice := received(application.Incoming{
		OriginalText:   "Meeting recording",
		AudioURLs:      []string{forwardingAudioURL},
		FileReferences: []domain.SharedReference{{URL: forwardingAudioURL, Kind: "audio", FileName: "meeting recording.mp3"}},
	})
	audio := post(voice, id(), other.ID, voice.RevisionID, 202)
	if audio == nil || audio.OriginalText != voice.OriginalText || audio.ConversationId != string(other.ID) || audio.AccountId != string(other.AccountID) || audio.Attachments == nil || len(*audio.Attachments) != 1 {
		t.Fatalf("structured audio was not copied with its caption to the direct destination: %+v", audio)
	}
	file, content, err := e.OpenStagedAttachment(ctx, other.ID, domain.ID((*audio.Attachments)[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content)
	_ = content.Close()
	if err != nil || string(body) != "full audio bytes" || file.FileName != "meeting recording.mp3" || file.MediaType != "audio/mpeg" {
		t.Fatalf("structured audio copy lost its name, media type or contents: %+v %q %v", file, body, err)
	}
	missing := received(application.Incoming{OriginalText: "https://example.test/missing.pdf"})
	post(missing, id(), first.Conversation.ID, missing.RevisionID, 404)
	_, err = e.LeaveRoom(ctx, id(), second.Conversation.ID, roomIntentRevision(t, e, second.Conversation.ID))
	if err != nil {
		t.Fatal(err)
	}
	post(original.Message, id(), second.Conversation.ID, original.Message.RevisionID, 409)
	for _, room := range []struct {
		id   domain.ID
		want int
	}{{first.Conversation.ID, 2}, {second.Conversation.ID, 1}} {
		items, err := e.Messages(ctx, room.id, "", 10)
		if err != nil || len(items) != room.want {
			t.Fatalf("unexpected effects in room %s: %+v %v", room.id, items, err)
		}
	}
}

func roomIntentRevision(t *testing.T, e *application.Engine, room domain.ID) domain.ID {
	t.Helper()
	session, err := e.RoomSession(t.Context(), room)
	if err != nil {
		t.Fatal(err)
	}
	return session.IntentRevisionID
}

type blockingForwardMedia struct {
	forwardingMedia
	started chan struct{}
	finish  chan struct{}
}

func (m blockingForwardMedia) OpenForwardAttachment(ctx context.Context, url string) (string, io.ReadCloser, error) {
	m.started <- struct{}{}
	select {
	case <-m.finish:
		return m.forwardingMedia.OpenForwardAttachment(ctx, url)
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
}

func TestForwardRejectsSourceCancelledDuringAttachmentTransfer(t *testing.T) {
	ctx := t.Context()
	protocol := &schedulingProtocol{roomHTTPProtocol: &roomHTTPProtocol{protocolStub: &protocolStub{}, rooms: make(map[domain.ID]domain.RoomSession)}}
	e, _, server := newMessagingEngine(t, t.TempDir(), protocol)
	source := openTestConversation(t, e, "alice@localhost")
	first, err := e.OpenRoom(ctx, id(), source.AccountID, "one@rooms.localhost", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.OpenRoom(ctx, id(), source.AccountID, "two@rooms.localhost", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	e.SetMediaReader(forwardingMedia{})
	original, err := e.SendComposition(ctx, id(), source.ID, domain.OutgoingComposition{OriginalText: "https://example.test/report.pdf"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	acceptedKey, racingKey := id(), id()
	send := func(key, destination domain.ID) (*engineapi.ForwardMessageResponse, error) {
		return client.ForwardMessageWithResponse(ctx, string(original.Message.ID),
			&engineapi.ForwardMessageParams{IdempotencyKey: string(key)},
			engineapi.ForwardMessage{ExpectedRevisionId: string(original.Message.RevisionID), ConversationId: string(destination)})
	}
	accepted, err := send(acceptedKey, first.Conversation.ID)
	if err != nil || accepted.JSON202 == nil {
		t.Fatalf("first forward: %+v %v", accepted, err)
	}
	media := blockingForwardMedia{started: make(chan struct{}, 1), finish: make(chan struct{})}
	e.SetMediaReader(media)
	type outcome struct {
		response *engineapi.ForwardMessageResponse
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := send(racingKey, second.Conversation.ID)
		done <- outcome{response, err}
	}()
	select {
	case <-media.started:
	case result := <-done:
		t.Fatalf("copy returned before staging its attachment: %+v", result)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelled, err := e.CancelMessage(ctx, id(), original.Message.ID, original.Message.RevisionID)
	if err != nil || cancelled.Message.SendState != "cancelled" {
		t.Fatalf("source cancellation: %+v %v", cancelled, err)
	}
	close(media.finish)
	result := <-done
	if result.err != nil || result.response.StatusCode() != http.StatusConflict {
		t.Fatalf("forward after source cancellation: %+v %v", result.response, result.err)
	}
	conflict := result.response.ApplicationproblemJSON409
	if conflict == nil || conflict.CurrentMessage == nil || conflict.CurrentMessage.RevisionId != string(cancelled.Message.RevisionID) {
		t.Fatalf("conflict must identify the changed source: %+v", conflict)
	}
	messages, err := e.Messages(ctx, second.Conversation.ID, "", 10)
	if err != nil || len(messages) != 0 {
		t.Fatalf("unexpected messages in the second room: %+v %v", messages, err)
	}
	replayed, err := send(acceptedKey, first.Conversation.ID)
	if err != nil || replayed.JSON200 == nil || replayed.JSON200.Id != accepted.JSON202.Id {
		t.Fatalf("previous acceptance after source cancellation: %+v %v", replayed, err)
	}
}
