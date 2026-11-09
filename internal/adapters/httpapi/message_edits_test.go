package httpapi_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type correctionHTTPProtocol struct {
	protocolStub
	sendsMu sync.Mutex
	sends   []application.Outgoing
}

func (p *correctionHTTPProtocol) PrepareCorrection(_ context.Context, a domain.Account, _ domain.Conversation, m domain.Message) (domain.OutgoingCorrection, error) {
	return domain.OutgoingCorrection{TargetMessageID: m.ID, OriginalWireID: m.ProviderID, Scope: "xmpp:correct:sender:" + a.Address, Author: a.Address, SessionID: "http-fixture-session"}, nil
}

func (p *correctionHTTPProtocol) Send(ctx context.Context, a domain.Account, out application.Outgoing) string {
	p.sendsMu.Lock()
	p.sends = append(p.sends, out)
	p.sendsMu.Unlock()
	return p.protocolStub.Send(ctx, a, out)
}

func TestSentMessageCorrectionHTTPPreservesOriginalAndReconcilesCommands(t *testing.T) {
	p := &correctionHTTPProtocol{protocolStub: protocolStub{connected: true, outcome: application.Submitted}}
	e, store, server := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	client, _ := engineapi.NewClientWithResponses(server.URL)
	ctx := t.Context()
	empty, err := client.GetConversationEditTargetWithResponse(ctx, string(c.ID))
	if err != nil || empty.StatusCode() != 404 {
		t.Fatal("empty conversation must not invent an edit target", empty, err)
	}
	first, err := e.SendMessage(ctx, id(), c.ID, "First own original")
	if err != nil {
		t.Fatal(err)
	}
	stop := runWorker(t, e)
	defer stop()
	waitMessage(t, e, first.Message.ID, application.Submitted)
	last, err := e.SendMessage(ctx, id(), c.ID, "Latest **original** café 😀")
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, e, last.Message.ID, application.Submitted)
	_, sequence, _ := store.Snapshot(ctx)
	target, err := client.GetConversationEditTargetWithResponse(ctx, string(c.ID))
	if err != nil || target.JSON200 == nil || !target.JSON200.Eligible || target.JSON200.MessageId != string(last.Message.ID) || target.JSON200.OriginalText != last.Message.OriginalText {
		t.Fatal("latest own target missing", target, err)
	}
	if target.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private eligibility became cacheable")
	}
	older, err := client.GetMessageEditTargetWithResponse(ctx, string(first.Message.ID))
	if err != nil || older.JSON200 == nil || older.JSON200.Eligible || older.JSON200.Reason == nil || *older.JSON200.Reason != "not_latest" {
		t.Fatal("old message became eligible", older, err)
	}
	_, afterReads, _ := store.Snapshot(ctx)
	if sequence != afterReads {
		t.Fatal("eligibility query mutated durable state")
	}
	originalRevision := target.JSON200.CurrentRevisionId
	collision, err := client.EditMessageWithResponse(ctx, string(last.Message.ID), &engineapi.EditMessageParams{IdempotencyKey: string(first.Message.ID)}, engineapi.EditMessage{ExpectedRevisionId: originalRevision, OriginalText: "Must not reuse another message identity"})
	if err != nil || collision.StatusCode() != 409 {
		t.Fatal("existing source identity accepted as a correction command", collision, err)
	}
	bad, err := client.EditMessageWithResponse(ctx, string(last.Message.ID), &engineapi.EditMessageParams{IdempotencyKey: string(id())}, engineapi.EditMessage{ExpectedRevisionId: string(id()), OriginalText: "stale edit"})
	if err != nil || bad.StatusCode() != 409 {
		t.Fatal("stale content precondition accepted", bad, err)
	}
	key := string(id())
	body := engineapi.EditMessage{ExpectedRevisionId: originalRevision, OriginalText: "Corrected **source** café 😀"}
	accepted, err := client.EditMessageWithResponse(ctx, string(last.Message.ID), &engineapi.EditMessageParams{IdempotencyKey: key}, body)
	if err != nil || accepted.JSON202 == nil || accepted.JSON202.Id != key || accepted.JSON202.MessageId != string(last.Message.ID) || accepted.JSON202.ExpectedRevisionId != originalRevision {
		t.Fatal("correction was not durably accepted", accepted, err)
	}
	replay, err := client.EditMessageWithResponse(ctx, string(last.Message.ID), &engineapi.EditMessageParams{IdempotencyKey: key}, body)
	if err != nil || replay.JSON200 == nil || replay.JSON200.Id != key || replay.JSON200.OriginalText != body.OriginalText {
		t.Fatal("same operation failed to reconcile", replay, err)
	}
	different := body
	different.OriginalText = "Different frozen request"
	conflict, err := client.EditMessageWithResponse(ctx, string(last.Message.ID), &engineapi.EditMessageParams{IdempotencyKey: key}, different)
	if err != nil || conflict.StatusCode() != 409 {
		t.Fatal("operation key reused with different content", conflict, err)
	}
	waitEdit := func(commandID, state string) engineapi.MessageEditCommand {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			got, err := client.GetMessageEditWithResponse(ctx, commandID)
			if err == nil && got.JSON200 != nil && string(got.JSON200.State) == state {
				return *got.JSON200
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("correction command did not reach", state)
		return engineapi.MessageEditCommand{}
	}
	command := waitEdit(key, application.Submitted)
	if command.AccountId != string(c.AccountID) || command.ConversationId != string(c.ID) || command.OriginalText != body.OriginalText {
		t.Fatal("receipt lost scope or exact body")
	}
	current, err := client.GetMessageWithResponse(ctx, string(last.Message.ID))
	if err != nil || current.JSON200 == nil || current.JSON200.OriginalText != body.OriginalText || current.JSON200.Edited == nil || !*current.JSON200.Edited || !current.JSON200.CreatedAt.Equal(last.Message.CreatedAt) || current.JSON200.SendState != "submitted" {
		t.Fatal("correction replaced original send identity or chronology", current, err)
	}
	history, err := client.GetMessageEditHistoryWithResponse(ctx, string(last.Message.ID), nil)
	if err != nil || history.JSON200 == nil || len(history.JSON200.Items) != 2 {
		t.Fatal("correction content history missing", history, err)
	}
	seenOriginal, seenCurrent := false, false
	for _, v := range history.JSON200.Items {
		seenOriginal = seenOriginal || v.IsOriginal && v.OriginalText == last.Message.OriginalText
		seenCurrent = seenCurrent || v.IsCurrent && v.OriginalText == body.OriginalText
	}
	if !seenOriginal || !seenCurrent {
		t.Fatal("original/current content flags lost")
	}
	page, err := e.Messages(ctx, c.ID, "", 100)
	if err != nil || len(page) != 2 {
		t.Fatal("correction created a second timeline bubble", len(page), err)
	}
	p.sendsMu.Lock()
	sent := append([]application.Outgoing{}, p.sends...)
	p.sendsMu.Unlock()
	if len(sent) != 3 || sent[2].ID != domain.ID(key) || sent[2].Correction == nil || sent[2].Correction.OriginalWireID != string(last.Message.ID) || sent[2].Correction.TargetMessageID != last.Message.ID {
		t.Fatal("correction failed to use a distinct wire command and original reference")
	}
	// A possible socket write never becomes another blind correction attempt.
	p.mu.Lock()
	p.outcome = application.Unknown
	p.mu.Unlock()
	latest, err := client.GetMessageEditTargetWithResponse(ctx, string(last.Message.ID))
	if err != nil || latest.JSON200 == nil || !latest.JSON200.Eligible {
		t.Fatal("submitted correction did not leave original eligible", latest, err)
	}
	unknownKey := string(id())
	unknown, err := client.EditMessageWithResponse(ctx, string(last.Message.ID), &engineapi.EditMessageParams{IdempotencyKey: unknownKey}, engineapi.EditMessage{ExpectedRevisionId: latest.JSON200.CurrentRevisionId, OriginalText: "Unconfirmed correction"})
	if err != nil || unknown.JSON202 == nil {
		t.Fatal(unknown, err)
	}
	waitEdit(unknownKey, application.Unknown)
	time.Sleep(650 * time.Millisecond)
	p.sendsMu.Lock()
	count := len(p.sends)
	p.sendsMu.Unlock()
	if count != 4 {
		t.Fatal("ambiguous correction was automatically retried", count)
	}
	unchanged, err := client.GetMessageEditHistoryWithResponse(ctx, string(last.Message.ID), nil)
	if err != nil || unchanged.JSON200 == nil || !reflect.DeepEqual(history.JSON200.Items, unchanged.JSON200.Items) {
		t.Fatal("unknown correction was presented as confirmed content", unchanged, err)
	}
}
