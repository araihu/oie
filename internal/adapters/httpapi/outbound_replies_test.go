package httpapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func TestOutgoingReplyContractScopesAndFreezesAcceptance(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	source := domain.Message{ID: id(), RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob 😀"}, OriginalText: "Original café\nSecond line", CreatedAt: time.Now().UTC(), SendState: "received", Historical: true, ProviderID: string(id())}
	if err := store.ReceiveMessage(t.Context(), c, source, id()); err != nil {
		t.Fatal(err)
	}
	other := openTestConversation(t, e, "other@localhost")
	foreign := source
	foreign.ID = id()
	foreign.RevisionID = id()
	foreign.AccountID = other.AccountID
	foreign.ConversationID = other.ID
	foreign.ProviderID = string(id())
	if err := store.ReceiveMessage(t.Context(), other, foreign, id()); err != nil {
		t.Fatal(err)
	}
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	authored := "Resposta 👋\r\nKeep exact text"
	target, key := string(source.ID), string(id())
	body := engineapi.SendMessage{OriginalText: authored, ReplyToMessageId: &target}
	first, err := client.SendMessageWithResponse(t.Context(), string(c.ID), &engineapi.SendMessageParams{IdempotencyKey: key}, body)
	if err != nil || first.JSON202 == nil {
		t.Fatal("reply not accepted", first, err)
	}
	accepted := first.JSON202
	if accepted.ReplyToMessageId == nil || *accepted.ReplyToMessageId != target || accepted.Reply == nil || accepted.Reply.BodyText != authored || !accepted.Reply.Available || accepted.Reply.MessageId == nil || *accepted.Reply.MessageId != target || !strings.Contains(accepted.OriginalText, "> Original café\n> Second line\n") || !strings.HasSuffix(accepted.OriginalText, authored) {
		t.Fatal("reply source/fallback/authored body lost", accepted)
	}
	replay, err := client.SendMessageWithResponse(t.Context(), string(c.ID), &engineapi.SendMessageParams{IdempotencyKey: key}, body)
	if err != nil || replay.JSON200 == nil || replay.JSON200.Id != accepted.Id || replay.JSON200.OriginalText != accepted.OriginalText || replay.JSON200.Reply == nil || replay.JSON200.Reply.BodyText != authored || replay.JSON200.ReplyToMessageId == nil || *replay.JSON200.ReplyToMessageId != target {
		t.Fatal("replay lost frozen reply intent", replay, err)
	}
	different := string(foreign.ID)
	body.ReplyToMessageId = &different
	conflict, err := client.SendMessageWithResponse(t.Context(), string(c.ID), &engineapi.SendMessageParams{IdempotencyKey: key}, body)
	if err != nil || conflict.StatusCode() != 409 || conflict.ApplicationproblemJSON409 == nil || conflict.ApplicationproblemJSON409.Code != "idempotency_conflict" {
		t.Fatal("changed reply target reused accepted key", conflict, err)
	}
	rejected, err := client.SendMessageWithResponse(t.Context(), string(c.ID), &engineapi.SendMessageParams{IdempotencyKey: string(id())}, body)
	if err != nil || rejected.StatusCode() != 400 {
		t.Fatal("foreign reply target accepted", rejected, err)
	}
	read, err := client.GetMessageWithResponse(t.Context(), accepted.Id)
	if err != nil || read.JSON200 == nil || read.JSON200.Reply == nil || read.JSON200.Reply.BodyText != authored || read.JSON200.ReplyToMessageId == nil || *read.JSON200.ReplyToMessageId != target {
		t.Fatal("retained reply lost immutable intent", read, err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 1 || pending[0].ID != domain.ID(accepted.Id) {
		t.Fatal("reply replay or rejection queued another send", err)
	}
}
