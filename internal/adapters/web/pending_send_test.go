package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestSubmissionReceiptLookupNeverCreatesMessage(t *testing.T) {
	f := &liveFixture{}
	ui, client, csrf := liveUI(t, f)
	key := newOperationKey()
	original := url.Values{"operationKey": {key}, "accountId": {testAccount}, "conversationId": {testChat}, "originalText": {"Same immutable body 👋"}}
	for n := 0; n < 2; n++ {
		status, result := livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, original)
		if status != 404 || result.Accepted || result.Rejected || f.effectCount() != 0 {
			t.Fatalf("missing receipt dispatched or rejected intent: %d %+v", status, result)
		}
	}
	status, result := livePost(t, client, ui.URL, "/ui/messages/send", csrf, original)
	if status != 200 || !result.Accepted || f.effectCount() != 1 {
		t.Fatalf("send failed: %d %+v", status, result)
	}
	for _, tc := range []struct {
		name, field, value string
		status             int
	}{
		{"matching", "", "", 200},
		{"repeated", "", "", 200},
		{"different body", "originalText", "Different", 409},
		{"foreign account", "accountId", testRevision, 409},
		{"foreign conversation", "conversationId", testRevision, 409},
		{"different reply", "replyToMessageId", testRevision, 409},
		{"invalid key", "operationKey", "invalid", 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := url.Values{}
			for k, v := range original {
				values[k] = append([]string{}, v...)
			}
			if tc.field != "" {
				values.Set(tc.field, tc.value)
			}
			code, receipt := livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, values)
			if code != tc.status || receipt.Accepted != (tc.status == 200) || f.effectCount() != 1 {
				t.Fatalf("receipt mismatch: %d %+v effects=%d", code, receipt, f.effectCount())
			}
			if receipt.Accepted && (receipt.MessageID != testMessage || !validOperationKey(receipt.NextKey)) {
				t.Fatal("receipt lost canonical identity")
			}
		})
	}
	code, _ := livePost(t, client, ui.URL, "/ui/messages/reconcile", "forged", original)
	if code != 403 || f.effectCount() != 1 {
		t.Fatal("receipt bypassed CSRF")
	}
}

func TestExpiredSendReceiptReconcilesOnlyTheExactIntent(t *testing.T) {
	key := newOperationKey()
	fixture := &liveFixture{}
	ui, client, csrf := liveUI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/operations/"+key {
			w.Header().Set("Content-Type", "application/json")
			expired := true
			_ = json.NewEncoder(w).Encode(engineapi.Operation{Id: key, ResourceId: testMessage, Kind: "send_message", Status: "succeeded", AcceptedMessageExpired: &expired})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/conversations/"+testChat+"/messages" {
			if r.Header.Get("X-Oie-Verify-Expired-Send") != "true" || r.Header.Get("Idempotency-Key") != key {
				t.Error("reconciliation must use the read-only receipt check")
			}
			var in engineapi.SendMessage
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.OriginalText == "Same immutable body" {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(engineapi.Problem{Code: "idempotency_conflict", Status: 409})
			}
			return
		}
		fixture.ServeHTTP(w, r)
	}))
	values := url.Values{"operationKey": {key}, "accountId": {testAccount}, "conversationId": {testChat}, "originalText": {"Same immutable body"}}
	status, result := livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, values)
	if status != 200 || !result.Accepted || result.State != "expired" || result.MessageID != testMessage || !validOperationKey(result.NextKey) {
		t.Fatalf("exact expired receipt: %d %+v", status, result)
	}
	values.Set("originalText", "Another body")
	status, result = livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, values)
	if status != 409 || result.Accepted || fixture.effectCount() != 0 {
		t.Fatalf("mismatched intent: %d %+v", status, result)
	}
}
