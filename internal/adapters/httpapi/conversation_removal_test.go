package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestDirectRemovalHidesUntilExplicitOpenOrNewLiveMessage(t *testing.T) {
	ctx := t.Context()
	e, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	other := openTestConversation(t, e, "separate@localhost")
	inbound := application.Incoming{
		AccountID: c.AccountID, Address: c.Address, ProviderID: "first-live", OriginalText: "First live message",
	}
	if err := e.Receive(ctx, inbound); err != nil {
		t.Fatal(err)
	}
	current, err := e.Conversation(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := e.UpdateConversationPreferences(ctx, id(), c.ID, application.ConversationPreferencesInput{
		AccountID: c.AccountID, ExpectedRevisionID: current.RevisionID, Archived: new(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	remove := func(route string, key, account, revision domain.ID, want int) {
		t.Helper()
		body := fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q}`, account, revision)
		status, data := request(t, server.URL, http.MethodPost, "/api/v1/conversations/"+string(c.ID)+route, string(key), body)
		if status != want {
			t.Fatalf("%s returned %d, want %d (%s)", route, status, want, data)
		}
	}
	assertListed := func(account, conversation domain.ID, archived, want bool) {
		t.Helper()
		list, err := e.Conversations(ctx, "", application.ConversationQuery{AccountID: account, Archived: archived, Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range list {
			if row.ID == conversation {
				if !want {
					t.Fatalf("removed conversation %s is listed (archived=%t)", conversation, archived)
				}
				return
			}
		}
		if want {
			t.Fatalf("conversation %s is missing (archived=%t)", conversation, archived)
		}
	}
	const route = "/remove-direct"
	revision := archived.Conversation.RevisionID
	remove(route, id(), other.AccountID, revision, http.StatusNotFound)
	remove("/remove-room", id(), c.AccountID, revision, http.StatusNotFound)
	remove(route, id(), c.AccountID, current.RevisionID, http.StatusConflict)
	assertListed(c.AccountID, c.ID, true, true)
	key := id()
	remove(route, key, c.AccountID, revision, http.StatusNoContent)
	assertListed(c.AccountID, c.ID, false, false)
	assertListed(c.AccountID, c.ID, true, false)
	assertListed(other.AccountID, other.ID, false, true)
	status, data := request(t, server.URL, http.MethodPost, "/api/v1/conversations/"+string(c.ID)+"/messages", string(id()), `{"originalText":"Sent from a stale tab"}`)
	if status != http.StatusConflict {
		t.Fatalf("removed conversation accepted a new send: %d (%s)", status, data)
	}

	if err := e.Receive(ctx, inbound); err != nil {
		t.Fatal(err)
	}
	inbound.Historical, inbound.ProviderID, inbound.OriginalText = true, "archive-only", "Recovered history"
	if err := e.Receive(ctx, inbound); err != nil {
		t.Fatal(err)
	}
	assertListed(c.AccountID, c.ID, false, false)
	reopened, err := e.OpenConversation(ctx, id(), c.AccountID, c.Address)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Conversation.ID != c.ID || reopened.Conversation.RemovedFromChats || reopened.Conversation.Archived {
		t.Fatalf("explicit open did not restore the same conversation: %+v", reopened.Conversation)
	}
	remove(route, key, c.AccountID, revision, http.StatusNoContent)
	assertListed(c.AccountID, c.ID, false, true)

	secondKey, secondRevision := id(), reopened.Conversation.RevisionID
	remove(route, secondKey, c.AccountID, secondRevision, http.StatusNoContent)
	inbound.Historical, inbound.ProviderID, inbound.OriginalText = false, "new-live", "New incoming message"
	if err := e.Receive(ctx, inbound); err != nil {
		t.Fatal(err)
	}
	remove(route, secondKey, c.AccountID, secondRevision, http.StatusNoContent)
	assertListed(c.AccountID, c.ID, false, true)
	messages, err := e.Messages(ctx, c.ID, "", 50)
	if err != nil || len(messages) != 3 {
		t.Fatalf("want three retained messages after restoring the chat, got %d: %v", len(messages), err)
	}
}
