package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestReadStateHTTPIsScopedDurableAndDoesNotSend(t *testing.T) {
	dir := t.TempDir()
	protocol := &protocolStub{}
	e, store, server := newMessagingEngine(t, dir, protocol)
	c := openTestConversation(t, e, "alice@localhost")
	for _, provider := range []string{"one", "two", "three"} {
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: provider, ProviderID: provider}); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := e.Messages(t.Context(), c.ID, "", 50)
	if err != nil || len(messages) != 3 {
		t.Fatal(err)
	}
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := client.GetConversationReadStateWithResponse(t.Context(), string(c.ID))
	if err != nil || initial.JSON200 == nil || initial.JSON200.UnreadCount != 3 || initial.JSON200.RevisionId != nil {
		t.Fatal(initial, err)
	}
	key := string(id())
	params := &engineapi.MarkConversationMessagesReadParams{IdempotencyKey: key}
	body := engineapi.MarkMessagesRead{MessageIds: []string{string(messages[0].ID)}}
	marked, err := client.MarkConversationMessagesReadWithResponse(t.Context(), string(c.ID), params, body)
	if err != nil || marked.JSON200 == nil || marked.JSON200.UnreadCount != 2 || marked.JSON200.RevisionId == nil {
		t.Fatal(marked, err)
	}
	repeat, err := client.MarkConversationMessagesReadWithResponse(t.Context(), string(c.ID), params, body)
	if err != nil || repeat.JSON200 == nil || *repeat.JSON200.RevisionId != *marked.JSON200.RevisionId {
		t.Fatal("receipt replay", err)
	}
	for _, raw := range []string{`{"messageIds":[]}`, `{"messageIds":["invalid"]}`, `{"messageIds":["` + string(messages[0].ID) + `"],"other":true}`} {
		req, _ := http.NewRequest("POST", server.URL+"/api/v1/conversations/"+string(c.ID)+"/read-state", strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", string(id()))
		response, callErr := http.DefaultClient.Do(req)
		if callErr != nil {
			t.Fatal(callErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal("invalid read accepted", response.StatusCode)
		}
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("read queued protocol work", err)
	}
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, restarted := newMessagingEngine(t, dir, protocol)
	next, _ := engineapi.NewClientWithResponses(restarted.URL)
	state, err := next.GetConversationReadStateWithResponse(t.Context(), string(c.ID))
	if err != nil || state.JSON200 == nil || state.JSON200.UnreadCount != 2 || state.JSON200.RevisionId == nil || *state.JSON200.RevisionId != *marked.JSON200.RevisionId {
		t.Fatal("read state did not survive restart", state, err)
	}
}

func TestReadLatestHTTPRejectsStaleTimelineAndClearsEarlierPages(t *testing.T) {
	e, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	ctx := t.Context()
	c := openTestConversation(t, e, "alice@localhost")
	other := openTestConversation(t, e, "bob@localhost")
	at := time.Now().UTC().Add(-time.Hour)
	receive := func(chat domain.Conversation, position int) {
		t.Helper()
		if err := e.Receive(ctx, application.Incoming{AccountID: chat.AccountID, Address: chat.Address, OriginalText: "incoming", ProviderID: string(id()), CreatedAt: at.Add(time.Duration(position) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	for position := range 105 {
		receive(c, position)
	}
	receive(other, 0)
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.GetConversationReadStateWithResponse(ctx, string(c.ID))
	if err != nil || state.JSON200 == nil || state.JSON200.CanMarkLatestRead == nil || !*state.JSON200.CanMarkLatestRead {
		t.Fatal("missing latest-read capability", state, err)
	}
	snapshot, err := e.Conversation(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := e.Messages(ctx, c.ID, "", 50)
	if err != nil || len(messages) < 2 {
		t.Fatal(messages, err)
	}
	match := `"` + string(snapshot.RevisionID) + `"`
	params := &engineapi.MarkConversationMessagesReadParams{IdempotencyKey: string(id()), IfMatch: &match}
	mark := func(message domain.ID, status int) *engineapi.MarkConversationMessagesReadResponse {
		t.Helper()
		body := engineapi.MarkMessagesRead{MessageIds: []string{string(message)}}
		response, err := client.MarkConversationMessagesReadWithResponse(ctx, string(c.ID), params, body)
		if err != nil || response.StatusCode() != status {
			t.Fatalf("mark latest returned %+v, %v, want %d", response, err, status)
		}
		return response
	}
	mark(messages[1].ID, http.StatusPreconditionFailed)
	receive(c, -1)
	mark(messages[0].ID, http.StatusPreconditionFailed)
	current, err := e.Conversation(ctx, c.ID)
	if err != nil || current.UnreadCount != 106 {
		t.Fatal("stale read changed the count", current.UnreadCount, err)
	}
	match = `"` + string(current.RevisionID) + `"`
	marked := mark(messages[0].ID, http.StatusOK)
	after, err := e.Conversation(ctx, c.ID)
	if err != nil || marked.JSON200.UnreadCount != 0 || after.UnreadCount != 0 || after.ActivitySequence != current.ActivitySequence {
		t.Fatal("latest read did not clear earlier messages or changed activity", after, err)
	}
	receive(c, -2)
	replay := mark(messages[0].ID, http.StatusOK)
	if *replay.JSON200.RevisionId != *marked.JSON200.RevisionId {
		t.Fatal("latest read did not replay the original receipt")
	}
	for _, chat := range []domain.Conversation{c, other} {
		after, err := e.Conversation(ctx, chat.ID)
		if err != nil || after.UnreadCount != 1 {
			t.Fatal("later arrival or another conversation was cleared", after, err)
		}
	}
}
