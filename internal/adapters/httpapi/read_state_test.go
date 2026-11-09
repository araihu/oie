package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
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
