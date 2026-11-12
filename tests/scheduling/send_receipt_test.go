package scheduling_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type receiptUploadProtocol struct{ limit int64 }

func (receiptUploadProtocol) NormalizeAddress(_, address string) (string, error) {
	return address, nil
}

func (receiptUploadProtocol) Connection(_ context.Context, account domain.Account) (domain.Connection, error) {
	return domain.Connection{AccountID: account.ID, State: "not_connected", UpdatedAt: time.Now().UTC()}, nil
}

func (receiptUploadProtocol) Configure(context.Context, domain.Account, application.ConnectionInput) (domain.Connection, error) {
	return domain.Connection{}, application.ErrProtocolUnavailable
}

func (receiptUploadProtocol) Disconnect(context.Context, domain.Account) (domain.Connection, error) {
	return domain.Connection{}, application.ErrProtocolUnavailable
}

func (receiptUploadProtocol) Send(context.Context, domain.Account, application.Outgoing) string {
	return application.NotSent
}

func (p receiptUploadProtocol) AttachmentAvailability(_ context.Context, _ domain.Account, files []domain.StagedAttachment) error {
	for _, file := range files {
		if file.Size > p.limit {
			return application.ErrAttachmentTooLarge
		}
	}
	return nil
}

func receiptTestEngine(t *testing.T, limit int64) (*application.Engine, http.Handler, domain.Conversation) {
	t.Helper()
	return receiptTestEngineWithProtocol(t, receiptUploadProtocol{limit: limit})
}

func receiptTestEngineWithProtocol(t *testing.T, protocol application.Protocols) (*application.Engine, http.Handler, domain.Conversation) {
	t.Helper()
	ctx := t.Context()
	store, err := sqlite.Open(ctx, t.TempDir(), domain.ID(testEngine))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine, err := application.New(store, "local", func() (domain.ID, error) { return domain.ID(newOperationKey()), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetProtocols(protocol)
	account, err := engine.Register(ctx, domain.ID(newOperationKey()), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := engine.OpenConversation(ctx, domain.ID(newOperationKey()), account.Account.ID, "bob@localhost")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	return engine, backend, conversation.Conversation
}

func receiptStage(t *testing.T, engine *application.Engine, conversation domain.ID, name, content string) domain.ID {
	t.Helper()
	id := domain.ID(newOperationKey())
	if _, _, err := engine.StageAttachment(t.Context(), conversation, id, name, "application/octet-stream", strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	return id
}

func receiptAttachmentIDs(t *testing.T, ids ...domain.ID) string {
	t.Helper()
	encoded, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestScheduledSendAndReconcileUseOriginalAcceptance(t *testing.T) {
	engine, backend, conversation := receiptTestEngine(t, 1024)
	var writes atomic.Int32
	var legacy atomic.Bool
	ui, client, csrf := liveUI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/conversations/"+string(conversation.ID)+"/messages" {
			writes.Add(1)
		}
		if legacy.Load() && strings.HasPrefix(r.URL.Path, "/api/v1/operations/") {
			r.URL.RawQuery = ""
		}
		backend.ServeHTTP(w, r)
	}))
	first := receiptStage(t, engine, conversation.ID, "one.txt", "one")
	second := receiptStage(t, engine, conversation.ID, "two.txt", "two")
	files := receiptAttachmentIDs(t, first, second)
	key := newOperationKey()
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	values := url.Values{
		"operationKey": {key}, "accountId": {string(conversation.AccountID)}, "conversationId": {string(conversation.ID)},
		"originalText": {"Original composition"}, "scheduledAt": {at.Format(time.RFC3339)}, "attachmentIds": {files},
	}
	status, accepted := livePost(t, client, ui.URL, "/ui/messages/send", csrf, values)
	if status != http.StatusOK || !accepted.Accepted || writes.Load() != 1 {
		t.Fatalf("initial acceptance: %d %+v", status, accepted)
	}
	messageID := domain.ID(accepted.MessageID)
	initial, err := engine.Message(t.Context(), messageID)
	if err != nil {
		t.Fatal(err)
	}
	registrationKey := newOperationKey()
	if _, err = engine.Register(t.Context(), domain.ID(registrationKey), application.RegisterInput{Protocol: "xmpp", Address: "other@localhost", Label: "Other"}); err != nil {
		t.Fatal(err)
	}
	operation := func(id, query string) engineapi.Operation {
		t.Helper()
		w := httptest.NewRecorder()
		backend.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/operations/"+id+query, nil))
		var result engineapi.Operation
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK {
			t.Fatalf("get receipt: %d %s (%v)", w.Code, w.Body, err)
		}
		return result
	}
	if operation(key, "").AcceptedMessage != nil || operation(registrationKey, "?includeAcceptedMessage=true").AcceptedMessage != nil {
		t.Fatal("receipt changed the default or returned a message for another operation kind")
	}
	changed := domain.OutgoingComposition{OriginalText: "Edited composition", AttachmentIDs: []domain.ID{second, first}}
	later := at.Add(time.Hour)
	edited, err := engine.UpdatePendingMessage(t.Context(), domain.ID(newOperationKey()), messageID, initial.RevisionID, &changed, &later, false)
	if err != nil {
		t.Fatal(err)
	}
	receipt := operation(key, "?includeAcceptedMessage=true").AcceptedMessage
	if receipt == nil || receipt.RevisionId != string(initial.RevisionID) || receipt.ScheduledAt == nil || !receipt.ScheduledAt.Equal(at) || receipt.OriginalText != initial.OriginalText {
		t.Fatalf("original acceptance changed: %+v", receipt)
	}
	assertReceipt := func(path string, form url.Values, wantStatus int) {
		t.Helper()
		status, result := livePost(t, client, ui.URL, path, csrf, maps.Clone(form))
		if status != wantStatus || result.Accepted != (wantStatus == http.StatusOK) ||
			(wantStatus == http.StatusOK && result.MessageID != accepted.MessageID) ||
			(wantStatus == http.StatusConflict && result.Code != "idempotency_conflict") {
			t.Fatalf("%s with %v: %d %+v", path, form, status, result)
		}
	}
	for _, path := range []string{"/ui/messages/send", "/ui/messages/reconcile"} {
		original := maps.Clone(values)
		original.Set("scheduledAt", at.In(time.FixedZone("test offset", 2*60*60)).Format(time.RFC3339))
		assertReceipt(path, original, http.StatusOK)
		matchesEdit := maps.Clone(values)
		matchesEdit.Set("originalText", changed.OriginalText)
		matchesEdit.Set("scheduledAt", later.Format(time.RFC3339))
		matchesEdit.Set("attachmentIds", receiptAttachmentIDs(t, second, first))
		assertReceipt(path, matchesEdit, http.StatusConflict)
		changedOrder := maps.Clone(values)
		changedOrder.Set("attachmentIds", receiptAttachmentIDs(t, second, first))
		assertReceipt(path, changedOrder, http.StatusConflict)
	}
	if _, err := engine.UpdatePendingMessage(t.Context(), domain.ID(newOperationKey()), messageID, edited.Message.RevisionID, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	matchesSendNow := maps.Clone(values)
	matchesSendNow.Set("originalText", changed.OriginalText)
	matchesSendNow.Set("attachmentIds", receiptAttachmentIDs(t, second, first))
	matchesSendNow.Del("scheduledAt")
	for _, path := range []string{"/ui/messages/send", "/ui/messages/reconcile"} {
		assertReceipt(path, values, http.StatusOK)
		assertReceipt(path, matchesSendNow, http.StatusConflict)
	}
	if writes.Load() != 1 {
		t.Fatalf("snapshot lookup emitted %d send requests", writes.Load())
	}
	legacy.Store(true)
	assertReceipt("/ui/messages/reconcile", values, http.StatusServiceUnavailable)
	if writes.Load() != 1 {
		t.Fatal("legacy reconciliation attempted a send")
	}
	assertReceipt("/ui/messages/send", values, http.StatusOK)
	if writes.Load() != 2 {
		t.Fatal("explicit legacy retry did not verify the original operation")
	}
}

func TestAttachmentLimitRejectionIsDefinitiveForSendAndScheduledEdit(t *testing.T) {
	engine, backend, conversation := receiptTestEngine(t, 8)
	ui, client, csrf := liveUI(t, backend)
	allowed := receiptStage(t, engine, conversation.ID, "small.txt", "ok")
	oversize := receiptStage(t, engine, conversation.ID, "large.txt", "too large")
	at := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	values := url.Values{
		"operationKey": {newOperationKey()}, "accountId": {string(conversation.AccountID)}, "conversationId": {string(conversation.ID)},
		"originalText": {"Keep this composition"}, "scheduledAt": {at}, "attachmentIds": {receiptAttachmentIDs(t, oversize)},
	}
	assertRefused := func(path, lang, message string) {
		t.Helper()
		request := path + "?lang=" + lang
		status, result := livePost(t, client, ui.URL, request, csrf, values)
		if status != http.StatusRequestEntityTooLarge || !result.Rejected || result.Accepted ||
			result.Code != "media_too_large" || result.Message != message {
			t.Fatalf("%s: %d %+v", request, status, result)
		}
	}
	assertRefused("/ui/messages/send", "en", "An attachment exceeds the provider’s upload limit. Remove it or choose a smaller file.")
	values.Set("attachmentIds", receiptAttachmentIDs(t, allowed))
	status, accepted := livePost(t, client, ui.URL, "/ui/messages/send", csrf, values)
	if status != http.StatusOK || !accepted.Accepted {
		t.Fatalf("replacement attachment was not accepted: %d %+v", status, accepted)
	}
	message, err := engine.Message(t.Context(), domain.ID(accepted.MessageID))
	if err != nil {
		t.Fatal(err)
	}
	values.Set("operationKey", newOperationKey())
	values.Set("messageId", accepted.MessageID)
	values.Set("expectedRevisionId", string(message.RevisionID))
	values.Set("replaceComposition", "1")
	values.Set("attachmentIds", receiptAttachmentIDs(t, oversize))
	assertRefused("/ui/scheduling/update", "pt-BR", "Um anexo excede o limite de envio do provedor. Remova-o ou escolha um arquivo menor.")
	values.Set("attachmentIds", receiptAttachmentIDs(t, allowed))
	status, updated := livePost(t, client, ui.URL, "/ui/scheduling/update", csrf, values)
	if status != http.StatusOK || !updated.Accepted || updated.MessageID != accepted.MessageID {
		t.Fatalf("edited replacement attachment was not accepted: %d %+v", status, updated)
	}
}
