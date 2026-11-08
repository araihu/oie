package httpapi_test

import (
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"testing"
	"time"
)

func TestReplyContextContractAndOriginalPreservation(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	ref := domain.MessageReference{Scope: "sender:bob@localhost", ID: "parent", Author: "bob@localhost"}
	original := "original 🍰"
	receive := func(body, provider string, refs []domain.MessageReference, reply *domain.ReplyReference) {
		t.Helper()
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: body, ProviderID: provider, SenderName: "Bob", Historical: true, References: refs, Reply: reply}); err != nil {
			t.Fatal(err)
		}
	}
	receive(original, "parent", []domain.MessageReference{ref}, nil)
	fallback := "> https://example.com/quoted.png 🍰\n"
	raw := fallback + "actual answer"
	receive(raw, "answer", nil, &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: len([]rune(fallback))}}})
	client, _ := engineapi.NewClientWithResponses(server.URL)
	page, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 2 {
		t.Fatal(page, err)
	}
	var answer, parent engineapi.Message
	for _, m := range page.JSON200.Items {
		if m.OriginalText == raw {
			answer = m
		} else {
			parent = m
		}
	}
	if answer.Reply == nil || !answer.Reply.Available || answer.Reply.MessageId == nil || *answer.Reply.MessageId != parent.Id || answer.Reply.BodyText != "actual answer" || answer.Reply.Excerpt != original || answer.OriginalText != raw {
		t.Fatal("reply projection lost original body or target", answer)
	}
	single, err := client.GetMessageWithResponse(t.Context(), answer.Id)
	if err != nil || single.JSON200 == nil || single.JSON200.Reply == nil || single.JSON200.OriginalText != raw {
		t.Fatal("single projection", err)
	}
	limit := 1
	window, err := client.GetMessageContextWithResponse(t.Context(), string(c.ID), parent.Id, &engineapi.GetMessageContextParams{Limit: &limit})
	if err != nil || window.JSON200 == nil || len(window.JSON200.Items) != 1 || window.JSON200.TargetId != parent.Id || window.JSON200.HasOlder || !window.JSON200.HasNewer {
		t.Fatal("context contract", window, err)
	}
	for _, n := range []int{0, 101} {
		res, err := client.GetMessageContextWithResponse(t.Context(), string(c.ID), parent.Id, &engineapi.GetMessageContextParams{Limit: &n})
		if err != nil || res.StatusCode() != 400 {
			t.Fatal("invalid limit accepted", n, err)
		}
	}
	missing, err := client.GetMessageContextWithResponse(t.Context(), string(id()), parent.Id, nil)
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal("foreign conversation context", err)
	}
	missing, err = client.GetMessageContextWithResponse(t.Context(), string(c.ID), string(id()), nil)
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal("missing original context", err)
	}
	previews, err := client.ListConversationPreviewsWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || previews.JSON200 == nil || len(previews.JSON200.Items) != 0 {
		t.Fatal("quoted fallback URL produced duplicate media", err)
	}
	events, err := e.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Message != nil && string(event.Message.ID) == answer.Id {
			found = true
			if event.Message.Reply == nil || !event.Message.Reply.Available {
				t.Fatal("event omitted reply projection")
			}
		}
	}
	if !found {
		t.Fatal("missing reply event")
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("read operations queued send", err)
	}
}
