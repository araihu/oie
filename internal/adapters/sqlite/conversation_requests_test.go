package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestDirectRequestRequiresDecisionAndPreservesArchive(t *testing.T) {
	s, e, chat := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	incoming := application.Incoming{CheckDirectHistory: true, DirectArchiveID: "first-archive-id", AccountID: chat.AccountID, Address: chat.Address, ProviderID: "first", OriginalText: "hidden request body"}
	if err := e.Receive(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	checking, err := e.PendingDirectHistoryChecks(ctx, chat.AccountID)
	if err != nil || len(checking) != 1 || checking[0].ID != chat.ID || checking[0].DirectHistoryCheck.ArchiveID != incoming.DirectArchiveID {
		t.Fatal("an empty local conversation must check the archive", checking, err)
	}
	check := *checking[0].DirectHistoryCheck
	assertHidden := func() {
		t.Helper()
		messages, err := e.Messages(ctx, chat.ID, "", 10)
		if err != nil || len(messages) != 0 {
			t.Fatal("pending timeline exposed content", messages, err)
		}
		for _, archived := range []bool{false, true} {
			listed, err := e.Conversations(ctx, "", application.ConversationQuery{Limit: 10, Archived: archived, IncludeLatestMessagePageCount: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, conversation := range listed {
				if conversation.ID == chat.ID && conversation.LatestMessagePageCount != nil {
					t.Fatal("pending conversation exposed its message count")
				}
			}
		}
		results, err := e.SearchMessages(ctx, domain.MessageSearchQuery{Search: incoming.OriginalText, Limit: 10})
		if err != nil || len(results.Items) != 0 {
			t.Fatal("pending content appeared in search", results, err)
		}
		events, err := s.Events(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Message != nil && event.Message.ConversationID == chat.ID {
				t.Fatal("pending content appeared in a message event")
			}
		}
	}
	assertHidden()
	if _, err = e.SendMessage(ctx, migrationID(), chat.ID, "forged reply"); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("reply before acceptance", err)
	}
	if err = e.ResolveDirectHistoryCheck(ctx, chat.ID, check, false); err != nil {
		t.Fatal(err)
	}
	pending, err := e.Conversation(ctx, chat.ID)
	if err != nil || pending.Request != domain.MessageRequest || pending.DirectHistoryCheck != nil {
		t.Fatal("no history must leave an actionable request", pending, err)
	}
	archived, err := e.UpdateConversationPreferences(ctx, migrationID(), chat.ID, preferenceInput(pending, preferenceBool(true), nil))
	if err != nil || !archived.Conversation.Archived || archived.Conversation.Request != domain.MessageRequest {
		t.Fatal("archive must preserve the pending request", archived, err)
	}
	incoming.ProviderID = "second"
	if err = e.Receive(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	if err = e.ResolveDirectHistoryCheck(ctx, chat.ID, check, true); err != nil {
		t.Fatal(err)
	}
	assertHidden()
	current, err := e.Conversation(ctx, chat.ID)
	if err != nil || !current.Archived || current.Request != domain.MessageRequest {
		t.Fatal("arrival or late lookup reopened an archived request", current, err)
	}
	accept := application.ConversationPreferencesInput{AccountID: current.AccountID, ExpectedRevisionID: archived.Conversation.RevisionID, AcceptRequest: true}
	if _, err = e.UpdateConversationPreferences(ctx, migrationID(), chat.ID, accept); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale tab accepted a newer revision", err)
	}
	accept.ExpectedRevisionID = current.RevisionID
	key := migrationID()
	result, err := e.UpdateConversationPreferences(ctx, key, chat.ID, accept)
	if err != nil || result.Conversation.Request != "" || result.Conversation.Archived {
		t.Fatal("accept from archive", result, err)
	}
	replay, err := e.UpdateConversationPreferences(ctx, key, chat.ID, accept)
	if err != nil || !replay.Replayed || replay.Conversation.RevisionID != result.Conversation.RevisionID {
		t.Fatal("accept replay", replay, err)
	}
	messages, err := e.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(messages) != 2 {
		t.Fatal("accept must reveal both retained messages", messages, err)
	}
}

func TestDirectRequestUsesExistingHistoryAndVerifiedPriorResult(t *testing.T) {
	_, e, chat := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	if err := e.Receive(ctx, application.Incoming{AccountID: chat.AccountID, Address: chat.Address, Historical: true, OriginalText: "retained conversation", CreatedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, peer := range []string{chat.Address, "new-peer@example.test"} {
		if err := e.Receive(ctx, application.Incoming{CheckDirectHistory: true, AccountID: chat.AccountID, Address: peer, OriginalText: "new arrival", ProviderID: "arrival"}); err != nil {
			t.Fatal(err)
		}
	}
	checks, err := e.PendingDirectHistoryChecks(ctx, chat.AccountID)
	if err != nil || len(checks) != 1 || checks[0].Address != "new-peer@example.test" {
		t.Fatal("only the peer without local history may query MAM", checks, err)
	}
	fresh := checks[0]
	if err = e.ResolveDirectHistoryCheck(ctx, fresh.ID, *fresh.DirectHistoryCheck, true); err != nil {
		t.Fatal(err)
	}
	current, err := e.Conversation(ctx, fresh.ID)
	if err != nil || current.Request != "" || current.DirectHistoryCheck != nil {
		t.Fatal("verified prior archive history should open the conversation", current, err)
	}
}
