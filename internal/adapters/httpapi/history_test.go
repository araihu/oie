package httpapi_test

import (
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"testing"
	"time"
)

func TestHistoryContractRetainsTextWithoutUnreadOrSendEffects(t *testing.T) {
	engine, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, engine, "alice@localhost")
	at := time.Date(2019, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := engine.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: "old text", ProviderID: "old-provider", ArchiveScope: "alice@localhost", ArchiveID: "old-archive", CreatedAt: at, Historical: true}); err != nil {
		t.Fatal(err)
	}
	if err := engine.HistoryChanged(t.Context(), domain.HistoryStatus{AccountID: c.AccountID, State: "complete"}); err != nil {
		t.Fatal(err)
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	h, err := client.GetAccountHistoryWithResponse(t.Context(), string(c.AccountID))
	if err != nil || h.JSON200 == nil || h.JSON200.State != "complete" || h.JSON200.RetainedMessages != 1 {
		t.Fatalf("history API: %+v %v", h, err)
	}
	messages, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || messages.JSON200 == nil || len(messages.JSON200.Items) != 1 {
		t.Fatal("retained API message absent")
	}
	m := messages.JSON200.Items[0]
	if m.Historical == nil || !*m.Historical || !m.CreatedAt.Equal(at) {
		t.Fatal("archive API lacks historical/original-time evidence")
	}
	got, _ := engine.Conversation(t.Context(), c.ID)
	if got.UnreadCount != 0 {
		t.Fatal("archive marked unread")
	}
	pending, _ := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if len(pending) != 0 {
		t.Fatal("archive generated sends")
	}
	events, err := store.Events(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == "account.history_changed" && e.History != nil && e.History.RetainedMessages == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("history recovery event missing")
	}
	missing, err := client.GetAccountHistoryWithResponse(t.Context(), string(id()))
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal("unknown account history was not404")
	}
}
