package httpapi_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type reactionHTTPProtocol struct{ protocolStub }

func (p *reactionHTTPProtocol) PrepareReaction(_ context.Context, a domain.Account, _ domain.Conversation, m domain.Message) (domain.OutgoingReaction, error) {
	return domain.OutgoingReaction{
		TargetMessageID: m.ID, Actor: a.Address, SessionID: "http-reactions",
		Reference: domain.MessageReference{Scope: "xmpp:sender:" + a.Address, ID: m.ProviderID, Author: a.Address}, MaxEmojis: 8,
	}, nil
}

func TestReactionHTTPSetClearReceiptsAndSourceIsolation(t *testing.T) {
	ctx := t.Context()
	p := &reactionHTTPProtocol{protocolStub: protocolStub{connected: true, outcome: application.Submitted}}
	e, store, server := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	source, err := e.SendMessage(ctx, id(), c.ID, "Source text stays unchanged")
	if err != nil {
		t.Fatal(err)
	}
	stop := runWorker(t, e)
	defer stop()
	original := waitMessage(t, e, source.Message.ID, application.Submitted)
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	beforeConversation, _ := e.Conversation(ctx, c.ID)
	_, beforeReads, _ := store.Snapshot(ctx)
	empty, err := client.GetMessageReactionsWithResponse(ctx, string(original.ID))
	if err != nil || empty.JSON200 == nil || !empty.JSON200.Eligible || empty.JSON200.OwnRevisionId != nil || len(empty.JSON200.Items) != 0 || len(empty.JSON200.OwnEmojis) != 0 {
		t.Fatalf("empty read-only reaction snapshot: %#v %v", empty, err)
	}
	if empty.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("reaction snapshot became publicly cacheable")
	}
	_, afterReads, _ := store.Snapshot(ctx)
	if beforeReads != afterReads {
		t.Fatal("reading reactions allocated state")
	}
	invalid, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: string(id())}, engineapi.SetMessageReactions{Emojis: []string{"ordinary text"}})
	if err != nil || invalid.StatusCode() != 400 {
		t.Fatal("non-emoji reaction accepted", invalid, err)
	}
	duplicate, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: string(id())}, engineapi.SetMessageReactions{Emojis: []string{"👍", "👍"}})
	if err != nil || duplicate.StatusCode() != 400 {
		t.Fatal("duplicate reaction selection accepted", duplicate, err)
	}
	key := string(id())
	body := engineapi.SetMessageReactions{ExpectedOwnRevisionId: nil, Emojis: []string{"👍", "❤️"}}
	accepted, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: key}, body)
	if err != nil || accepted.JSON202 == nil || accepted.JSON202.Id != key {
		t.Fatalf("reaction command acceptance: %#v %v", accepted, err)
	}
	replayed, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: key}, body)
	if err != nil || replayed.JSON200 == nil || replayed.JSON200.Id != key {
		t.Fatal("immutable command replay lost", replayed, err)
	}
	different := body
	different.Emojis = []string{"👋"}
	conflict, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: key}, different)
	if err != nil || conflict.StatusCode() != 409 {
		t.Fatal("same operation accepted different reaction set", conflict, err)
	}
	waitCommand := func(key, state string) engineapi.MessageReactionCommand {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			receipt, err := client.GetMessageReactionCommandWithResponse(ctx, key)
			if err == nil && receipt.JSON200 != nil && string(receipt.JSON200.State) == state {
				return *receipt.JSON200
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("reaction command did not reach", state)
		return engineapi.MessageReactionCommand{}
	}
	waitCommand(key, application.Submitted)
	current, err := client.GetMessageReactionsWithResponse(ctx, string(original.ID))
	if err != nil || current.JSON200 == nil || current.JSON200.OwnRevisionId == nil || len(current.JSON200.Items) != 2 || len(current.JSON200.OwnEmojis) != 2 {
		t.Fatal("submitted own set missing", current, err)
	}
	for _, item := range current.JSON200.Items {
		if !item.Own || item.Count != 1 {
			t.Fatal("own reaction count wrong", item)
		}
	}
	stale, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: string(id())}, body)
	if err != nil || stale.StatusCode() != 409 {
		t.Fatal("stale own revision was accepted", stale, err)
	}
	batch, err := client.ListConversationReactionsWithResponse(ctx, string(c.ID), &engineapi.ListConversationReactionsParams{MessageId: []string{string(original.ID)}})
	if err != nil || batch.JSON200 == nil || len(batch.JSON200.Items) != 1 || batch.JSON200.Items[0].MessageId != string(original.ID) {
		t.Fatal("bounded conversation reactions unavailable", batch, err)
	}
	other, err := e.OpenConversation(ctx, id(), c.AccountID, "carol@localhost")
	if err != nil {
		t.Fatal(err)
	}
	wrongScope, err := client.ListConversationReactionsWithResponse(ctx, string(other.Conversation.ID), &engineapi.ListConversationReactionsParams{MessageId: []string{string(original.ID)}})
	if err != nil || wrongScope.StatusCode() != 404 {
		t.Fatal("batch leaked another conversation", wrongScope, err)
	}
	clearKey := string(id())
	clearBody := engineapi.SetMessageReactions{ExpectedOwnRevisionId: current.JSON200.OwnRevisionId, Emojis: []string{}}
	cleared, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: clearKey}, clearBody)
	if err != nil || cleared.JSON202 == nil {
		t.Fatal("empty replacement not accepted", cleared, err)
	}
	waitCommand(clearKey, application.Submitted)
	clearedSnapshot, err := client.GetMessageReactionsWithResponse(ctx, string(original.ID))
	if err != nil || clearedSnapshot.JSON200 == nil || len(clearedSnapshot.JSON200.Items) != 0 || len(clearedSnapshot.JSON200.OwnEmojis) != 0 || clearedSnapshot.JSON200.OwnRevisionId == nil {
		t.Fatal("removal lost its revision or retained reactions", clearedSnapshot, err)
	}
	unchanged, err := e.Message(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(unchanged, original) {
		t.Fatal("reaction changed source content/revisions/outbox", unchanged, original, err)
	}
	afterConversation, _ := e.Conversation(ctx, c.ID)
	if !reflect.DeepEqual(afterConversation, beforeConversation) {
		t.Fatal("reaction changed conversation activity or unread state")
	}
	page, err := e.Messages(ctx, c.ID, "", 100)
	if err != nil || len(page) != 1 {
		t.Fatal("reaction created a timeline message", page, err)
	}
	p.mu.Lock()
	p.outcome = application.Unknown
	beforeWrites := p.count
	p.mu.Unlock()
	unknownKey := string(id())
	unknown, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: unknownKey}, engineapi.SetMessageReactions{ExpectedOwnRevisionId: clearedSnapshot.JSON200.OwnRevisionId, Emojis: []string{"👋"}})
	if err != nil || unknown.JSON202 == nil {
		t.Fatal("uncertain command not accepted", unknown, err)
	}
	waitCommand(unknownKey, application.Unknown)
	unknownReplay, err := client.SetMessageReactionsWithResponse(ctx, string(original.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: unknownKey}, engineapi.SetMessageReactions{ExpectedOwnRevisionId: clearedSnapshot.JSON200.OwnRevisionId, Emojis: []string{"👋"}})
	if err != nil || unknownReplay.JSON200 == nil || unknownReplay.JSON200.State != "unknown" {
		t.Fatal("unknown receipt was replaced with a new dispatch", unknownReplay, err)
	}
	unknownSnapshot, _ := client.GetMessageReactionsWithResponse(ctx, string(original.ID))
	if unknownSnapshot.JSON200 == nil || unknownSnapshot.JSON200.PendingCommand == nil || unknownSnapshot.JSON200.Eligible || len(unknownSnapshot.JSON200.OwnEmojis) != 0 {
		t.Fatal("uncertain transmission presented as submitted own set", unknownSnapshot)
	}
	time.Sleep(100 * time.Millisecond)
	p.mu.Lock()
	afterWrites := p.count
	p.mu.Unlock()
	if afterWrites != beforeWrites+1 {
		t.Fatal("uncertain reaction was retransmitted", beforeWrites, afterWrites)
	}
}
