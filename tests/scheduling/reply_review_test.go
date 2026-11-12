package scheduling_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/web/bffapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type replyRaceProtocol struct {
	receiptUploadProtocol
	beforeFreeze atomic.Pointer[func()]
}

func (*replyRaceProtocol) CapabilityEvidence(context.Context, domain.Account) (domain.CapabilityEvidence, error) {
	return domain.CapabilityEvidence{Features: []domain.CapabilityFeature{{
		ID: "send_replies", ProtocolSupport: "supported", ServerSupport: "not_required",
	}}}, nil
}

func (p *replyRaceProtocol) SelectReplyReference(domain.Account, domain.Conversation, domain.Message) (domain.MessageReference, bool) {
	if beforeFreeze := p.beforeFreeze.Swap(nil); beforeFreeze != nil {
		(*beforeFreeze)()
	}
	return domain.MessageReference{}, false
}

func fetchReplyPreparation(t *testing.T, client *http.Client, base string, chat domain.Conversation, sourceID string) bffapi.ReplySource {
	t.Helper()
	query := url.Values{"engineId": {testEngine}, "accountId": {string(chat.AccountID)}, "conversationId": {string(chat.ID)}, "messageId": {sourceID}}
	response, err := client.Get(base + "/ui/reply-source?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var preview bffapi.ReplySource
	if err = json.NewDecoder(response.Body).Decode(&preview); err != nil || response.StatusCode != http.StatusOK || preview.MessageId != sourceID ||
		preview.ExcerptSha256 != domain.ReplyExcerptSHA256(preview.Excerpt) {
		t.Fatalf("reply preview unavailable: %d %+v %v", response.StatusCode, preview, err)
	}
	return preview
}

func TestScheduledReplyReviewBindsSourceAndReplaysOriginalAcceptance(t *testing.T) {
	protocol := &replyRaceProtocol{receiptUploadProtocol: receiptUploadProtocol{limit: 1024}}
	engine, backend, chat := receiptTestEngineWithProtocol(t, protocol)
	ui, client, csrf := liveUI(t, backend)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Second)
	receive := func(id, target, body string, created time.Time) error {
		return engine.Receive(ctx, application.Incoming{
			AccountID: chat.AccountID, Address: chat.Address, ProviderID: id, OriginalText: body, CreatedAt: created,
			Correction: &domain.MessageCorrection{Scope: "xmpp:sender:bob@localhost", Author: "bob@localhost", ID: id, TargetID: target},
		})
	}
	if err := receive("reply-source", "", "first quote", at); err != nil {
		t.Fatal(err)
	}
	items, err := engine.Messages(ctx, chat.ID, "", 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("source unavailable: %+v %v", items, err)
	}
	sourceID := string(items[0].ID)
	getSource := func() bffapi.ReplySource {
		return fetchReplyPreparation(t, client, ui.URL, chat, sourceID)
	}
	first := getSource()
	form := url.Values{
		"engineId": {testEngine}, "accountId": {string(chat.AccountID)}, "conversationId": {string(chat.ID)},
		"operationKey": {newOperationKey()}, "originalText": {"authored reply"}, "replyToMessageId": {sourceID},
		"expectedReplyRevisionId": {first.RevisionId}, "scheduledAt": {at.Add(time.Hour).Format(time.RFC3339)},
	}
	status, accepted := livePost(t, client, ui.URL, "/ui/messages/send", csrf, form)
	if status != http.StatusOK || !accepted.Accepted {
		t.Fatalf("reviewed reply not accepted: %d %+v", status, accepted)
	}
	assertQuote := func(want, revision string) domain.Message {
		t.Helper()
		m, err := engine.Message(ctx, domain.ID(accepted.MessageID))
		if err != nil || m.OutboundReply == nil || m.OutboundReply.Excerpt != want || string(m.OutboundReply.TargetRevisionID) != revision {
			t.Fatalf("incorrect frozen reply: %+v %v", m.OutboundReply, err)
		}
		return m
	}
	scheduled := assertQuote(first.Excerpt, first.RevisionId)
	if err = receive("reply-edit-1", "reply-source", "second quote", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	current := getSource()
	if current.RevisionId == first.RevisionId || current.Excerpt != "second quote" {
		t.Fatalf("correction missing from preview: %+v", current)
	}
	for _, path := range []string{"/ui/messages/send", "/ui/messages/reconcile"} {
		status, replay := livePost(t, client, ui.URL, path, csrf, form)
		if status != http.StatusOK || !replay.Accepted || replay.MessageID != accepted.MessageID {
			t.Fatalf("old acceptance changed: %s %d %+v", path, status, replay)
		}
	}
	key := form.Get("operationKey")
	form.Set("expectedReplyRevisionId", current.RevisionId)
	status, conflict := livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, form)
	if status != http.StatusConflict || conflict.Code != "idempotency_conflict" {
		t.Fatalf("receipt ignored different reviewed revision: %d %+v", status, conflict)
	}
	form.Set("operationKey", newOperationKey())
	form.Set("expectedReplyRevisionId", first.RevisionId)
	status, rejected := livePost(t, client, ui.URL, "/ui/messages/send", csrf, form)
	if status != http.StatusConflict || !rejected.Rejected || rejected.Code != "reply_source_changed" {
		t.Fatalf("changed source accepted in a new send: %d %+v", status, rejected)
	}
	if _, err = engine.Operation(ctx, domain.ID(form.Get("operationKey"))); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("stale review recorded an operation: %v", err)
	}
	form.Set("replaceComposition", "1")
	form.Set("messageId", accepted.MessageID)
	form.Set("expectedRevisionId", string(scheduled.RevisionID))
	form.Set("originalText", "edited reply")
	status, rejected = livePost(t, client, ui.URL, "/ui/scheduling/update", csrf, form)
	if status != http.StatusConflict || !rejected.Rejected || rejected.Code != "reply_source_changed" {
		t.Fatalf("changed source accepted in an edit: %d %+v", status, rejected)
	}
	form.Del("expectedReplyRevisionId")
	status, edit := livePost(t, client, ui.URL, "/ui/scheduling/update", csrf, form)
	if status != http.StatusOK || !edit.Accepted {
		t.Fatalf("edit preserving frozen quote rejected: %d %+v", status, edit)
	}
	scheduled = assertQuote(first.Excerpt, first.RevisionId)
	form.Set("expectedRevisionId", string(scheduled.RevisionID))
	form.Set("expectedReplyRevisionId", current.RevisionId)
	form.Set("operationKey", newOperationKey())
	sameTextCorrection := func() {
		// Preserve the body hash. Only checking the version inside the write
		// transaction can detect this correction after the preparation read.
		if err := receive("reply-edit-2", "reply-edit-1", current.Excerpt, at.Add(2*time.Second)); err != nil {
			t.Error(err)
		}
	}
	protocol.beforeFreeze.Store(&sameTextCorrection)
	status, rejected = livePost(t, client, ui.URL, "/ui/scheduling/update", csrf, form)
	if status != http.StatusConflict || !rejected.Rejected || rejected.Code != "reply_source_changed" {
		t.Fatalf("source changed after edit preparation: %d %+v", status, rejected)
	}
	assertQuote(first.Excerpt, first.RevisionId)
	current = getSource()
	form.Set("expectedReplyRevisionId", current.RevisionId)
	status, edit = livePost(t, client, ui.URL, "/ui/scheduling/update", csrf, form)
	if status != http.StatusOK || !edit.Accepted {
		t.Fatalf("reselected quote rejected: %d %+v", status, edit)
	}
	assertQuote(current.Excerpt, current.RevisionId)
	form.Set("operationKey", key)
	form.Set("originalText", "authored reply")
	form.Set("expectedReplyRevisionId", first.RevisionId)
	status, replay := livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, form)
	if status != http.StatusOK || !replay.Accepted {
		t.Fatalf("edit changed original receipt: %d %+v", status, replay)
	}
}

func TestScheduledReplyRejectsChangedExcerptWithoutNewRevision(t *testing.T) {
	protocol := &replyRaceProtocol{receiptUploadProtocol: receiptUploadProtocol{limit: 1024}}
	engine, backend, chat := receiptTestEngineWithProtocol(t, protocol)
	ui, client, csrf := liveUI(t, backend)
	ctx := t.Context()
	const prefix = "> earlier quote\n\n"
	const body = "authored text of the source"
	incoming := application.Incoming{AccountID: chat.AccountID, Address: chat.Address, ProviderID: "reply-fallback-source", OriginalText: prefix + body}
	if err := engine.Receive(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	items, err := engine.Messages(ctx, chat.ID, "", 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("source unavailable: %+v %v", items, err)
	}
	sourceID := string(items[0].ID)
	first := fetchReplyPreparation(t, client, ui.URL, chat, sourceID)
	if first.Excerpt != prefix+body {
		t.Fatalf("unexpected original excerpt: %+v", first)
	}
	form := url.Values{
		"engineId": {testEngine}, "accountId": {string(chat.AccountID)}, "conversationId": {string(chat.ID)},
		"operationKey": {newOperationKey()}, "originalText": {"new scheduled reply"}, "replyToMessageId": {sourceID},
		"expectedReplyRevisionId": {first.RevisionId}, "expectedReplyExcerptSha256": {first.ExcerptSha256},
		"scheduledAt": {time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)},
	}
	enrich := func() {
		incoming.Reply = &domain.ReplyReference{
			Target: domain.MessageReference{ID: "not retained"}, Fallback: []domain.TextRange{{Start: 0, End: len(prefix)}},
		}
		if err := engine.Receive(ctx, incoming); err != nil {
			t.Error(err)
		}
	}
	protocol.beforeFreeze.Store(&enrich)
	status, rejected := livePost(t, client, ui.URL, "/ui/messages/send", csrf, form)
	if status != http.StatusConflict || !rejected.Rejected || rejected.Code != "reply_source_changed" {
		t.Fatalf("same revision with different quote was accepted after metadata raced the write: %d %+v", status, rejected)
	}
	if _, err = engine.Operation(ctx, domain.ID(form.Get("operationKey"))); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("stale excerpt recorded an operation: %v", err)
	}
	current := fetchReplyPreparation(t, client, ui.URL, chat, sourceID)
	if current.RevisionId != first.RevisionId || current.Excerpt != body || current.ExcerptSha256 == first.ExcerptSha256 {
		t.Fatalf("fixture did not change only derived excerpt: first=%+v current=%+v", first, current)
	}
	form.Set("expectedReplyExcerptSha256", current.ExcerptSha256)
	status, accepted := livePost(t, client, ui.URL, "/ui/messages/send", csrf, form)
	if status != http.StatusOK || !accepted.Accepted {
		t.Fatalf("refreshed quote was rejected: %d %+v", status, accepted)
	}
	status, replay := livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, form)
	if status != http.StatusOK || !replay.Accepted || replay.MessageID != accepted.MessageID {
		t.Fatalf("identical quote proof did not reconcile: %d %+v", status, replay)
	}
	form.Set("expectedReplyExcerptSha256", first.ExcerptSha256)
	for _, path := range []string{"/ui/messages/send", "/ui/messages/reconcile"} {
		status, conflict := livePost(t, client, ui.URL, path, csrf, form)
		if status != http.StatusConflict || conflict.Code != "idempotency_conflict" {
			t.Fatalf("changed quote proof matched receipt: %s %d %+v", path, status, conflict)
		}
	}
}
