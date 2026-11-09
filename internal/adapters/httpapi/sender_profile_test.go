package httpapi_test

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"testing"
)

type profileProtocol struct {
	*protocolStub
	name, state string
	calls       []string
}

func (p *profileProtocol) PublishedProfile(_ context.Context, a domain.Account, address string) (domain.SenderProfile, error) {
	p.calls = append(p.calls, address)
	return domain.SenderProfile{FullName: p.name, PersonalAddress: "forged@other.test", State: p.state}, nil
}
func TestMessageSenderProfileUsesRetainedIdentityOnly(t *testing.T) {
	stub := &protocolStub{}
	e, store, server := newMessagingEngine(t, t.TempDir(), stub)
	c := openTestConversation(t, e, "alice@localhost")
	p := &profileProtocol{protocolStub: stub, name: "Alice Example", state: "known"}
	e.SetProtocols(p)
	client, _ := engineapi.NewClientWithResponses(server.URL)
	for _, tc := range []struct{ kind, address, author, want string }{{"direct", "alice@localhost", "", "alice@localhost"}, {"room", "room@conference.localhost", "", ""}, {"room", "room@conference.localhost", "author@localhost", "author@localhost"}} {
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Kind: tc.kind, Address: tc.address, SenderName: "Same nickname", SenderAddress: tc.author, ProviderID: string(id()), OriginalText: "retained text"}); err != nil {
			t.Fatal(err)
		}
		chats, err := e.Conversations(t.Context(), "", application.ConversationQuery{AccountID: c.AccountID, Kind: tc.kind, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var message domain.Message
		for _, chat := range chats {
			if chat.Address == tc.address {
				items, err := e.Messages(t.Context(), chat.ID, "", 1)
				if err != nil || len(items) < 1 {
					t.Fatal(items, err)
				}
				message = items[0]
			}
		}
		_, before, _ := store.Snapshot(t.Context())
		calls := len(p.calls)
		response, err := client.GetMessageSenderProfileWithResponse(t.Context(), string(message.ID))
		if err != nil || response.JSON200 == nil {
			t.Fatal(response, err)
		}
		got := response.JSON200
		if got.AccountId != string(c.AccountID) || got.ConversationId != string(message.ConversationID) || got.MessageId != string(message.ID) {
			t.Fatal("scope mismatch")
		}
		if tc.want == "" {
			if got.PersonalAddress != nil || got.FullName != nil || got.State != "not_disclosed" || len(p.calls) != calls {
				t.Fatal("nickname became a personal identity", got)
			}
		} else if got.PersonalAddress == nil || *got.PersonalAddress != tc.want || got.FullName == nil || *got.FullName != p.name || p.calls[len(p.calls)-1] != tc.want {
			t.Fatal("canonical address or published name lost", got)
		}
		_, after, _ := store.Snapshot(t.Context())
		if before != after || stub.count != 0 {
			t.Fatal("profile read changed state or sent a message")
		}
	}
	missing, err := client.GetMessageSenderProfileWithResponse(t.Context(), string(id()))
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal(missing, err)
	}
}
