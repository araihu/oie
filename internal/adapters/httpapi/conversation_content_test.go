package httpapi_test

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func TestConversationContentReadsKeepAccountAndConversationScope(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	selected := openTestConversation(t, e, "alice@localhost")
	other, err := e.OpenConversation(t.Context(), id(), selected.AccountID, "carol@localhost")
	if err != nil {
		t.Fatal(err)
	}
	foreign := openTestConversation(t, e, "work@localhost")
	var selectedMessage domain.ID
	for _, c := range []domain.Conversation{selected, other.Conversation, foreign} {
		m := domain.Message{ID: id(), RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob"}, OriginalText: "https://example.test/shared.png", CreatedAt: time.Now().UTC(), SendState: "received", Historical: true, ProviderID: string(id())}
		if err = store.ReceiveMessage(t.Context(), c, m, id()); err != nil {
			t.Fatal(err)
		}
		if _, err = e.SetMessageStar(t.Context(), id(), m.ID, "", true); err != nil {
			t.Fatal(err)
		}
		if c.ID == selected.ID {
			selectedMessage = m.ID
		}
	}
	stop := runWorker(t, e)
	defer stop()
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	account, conversation := string(selected.AccountID), string(selected.ID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		page, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{AccountId: &account, ConversationId: &conversation})
		if err != nil || page.JSON200 == nil {
			t.Fatal("scoped media read", err)
		}
		if page.JSON200.Indexing.State == "complete" {
			if len(page.JSON200.Items) != 1 || page.JSON200.Items[0].Message.Id != string(selectedMessage) || page.JSON200.Indexing.TotalMessages != 1 {
				t.Fatal("media leaked an account or another conversation", page.JSON200)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scoped catalog did not finish indexing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stars, err := client.ListStarredMessagesWithResponse(t.Context(), &engineapi.ListStarredMessagesParams{AccountId: &account, ConversationId: &conversation})
	if err != nil || stars.JSON200 == nil || len(stars.JSON200.Items) != 1 || stars.JSON200.Items[0].Message.Id != string(selectedMessage) || stars.JSON200.Items[0].Conversation.Id != conversation || stars.JSON200.Items[0].Star.AccountId != account {
		t.Fatal("starred messages lost the selected source scope", err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("content reads queued a send", err)
	}
}
