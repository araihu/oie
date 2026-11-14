package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func TestMessageSourceContextKeepsWindowAndMetadataScoped(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	chat := openTestConversation(t, e, "source@localhost")
	other, err := e.OpenConversation(t.Context(), id(), chat.AccountID, "other@localhost")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC()
	for n := 1; n <= 25; n++ {
		m := domain.Message{
			ID: timelineHTTPID(n), RevisionID: id(), AccountID: chat.AccountID, ConversationID: chat.ID,
			OriginalText: fmt.Sprintf("source %d", n), ProviderID: fmt.Sprint(n),
			CreatedAt: at.Add(time.Duration(n) * time.Second), SendState: "received", Historical: true,
		}
		if err := store.ReceiveMessage(t.Context(), chat, m, id()); err != nil {
			t.Fatal(err)
		}
	}
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	target := string(timelineHTTPID(13))
	response, err := client.GetMessageSourceContextWithResponse(t.Context(), string(chat.ID), target)
	if err != nil || response.JSON200 == nil {
		t.Fatal(response, err)
	}
	page := response.JSON200
	if page.Conversation.Id != string(chat.ID) || page.Context.TargetId != target ||
		len(page.Context.Items) != domain.SourceContextPageSize || !page.Context.HasOlder || !page.Context.HasNewer {
		t.Fatalf("unexpected source context: %+v", page)
	}
	if page.Stars == nil || len(*page.Stars) != len(page.Context.Items) {
		t.Fatal("source stars missing or unbounded")
	}
	for _, star := range *page.Stars {
		if star.AccountId != string(chat.AccountID) || star.ConversationId != string(chat.ID) {
			t.Fatalf("star outside source scope: %+v", star)
		}
	}
	foreign, err := client.GetMessageSourceContextWithResponse(t.Context(), string(other.Conversation.ID), target)
	if err != nil || foreign.StatusCode() != http.StatusNotFound {
		t.Fatal(foreign, err)
	}
}
