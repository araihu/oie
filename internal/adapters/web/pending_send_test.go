package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestOfflineSendDistinguishesReadFailuresFromAttemptedWrites(t *testing.T) {
	conversation := "/api/v1/conversations/" + testChat
	for _, tc := range []struct {
		name, method, path, action string
		status                     int
		drop, offline, wantWaiting bool
	}{
		{"engine unavailable", http.MethodGet, "/api/v1/engine", "send", 503, false, true, true},
		{"engine unauthorized", http.MethodGet, "/api/v1/engine", "send", 401, false, true, false},
		{"receipt unavailable", http.MethodGet, "receipt", "send", 503, false, true, true},
		{"receipt transport lost", http.MethodGet, "receipt", "send", 0, true, true, true},
		{"receipt forbidden", http.MethodGet, "receipt", "send", 403, false, true, false},
		{"receipt unavailable on retry", http.MethodGet, "receipt", "send", 503, false, false, false},
		{"receipt unavailable on reconcile", http.MethodGet, "receipt", "reconcile", 503, false, true, false},
		{"conversation unavailable", http.MethodGet, conversation, "send", 503, false, true, true},
		{"conversation missing", http.MethodGet, conversation, "send", 404, false, true, false},
		{"write unavailable", http.MethodPost, conversation + "/messages", "send", 503, false, true, false},
		{"write response lost", http.MethodPost, conversation + "/messages", "send", 0, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := newOperationKey()
			path := tc.path
			if path == "receipt" {
				path = "/api/v1/operations/" + key
			}
			var fail atomic.Bool
			var writes atomic.Int32
			fixture := &liveFixture{}
			ui, client, csrf := liveUI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					writes.Add(1)
				}
				if fail.Load() && r.Method == tc.method && r.URL.Path == path {
					if tc.drop {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(tc.status)
					// An upstream body cannot forge the BFF's pre-dispatch evidence.
					_ = json.NewEncoder(w).Encode(map[string]any{"code": "engine_unavailable", "status": tc.status})
					return
				}
				fixture.ServeHTTP(w, r)
			}))
			fail.Store(true)
			values := url.Values{"operationKey": {key}, "accountId": {testAccount}, "conversationId": {testChat}, "originalText": {"same body"}}
			if tc.offline {
				values.Set("offlineCapable", "1")
			}
			status, result := livePost(t, client, ui.URL, "/ui/messages/"+tc.action, csrf, values)
			wantWrites := int32(0)
			if tc.method == http.MethodPost {
				wantWrites = 1
			}
			waiting := result.Code == "engine_unavailable"
			// net/http can retry a lost POST that carries an idempotency key.
			writesMatch := writes.Load() == wantWrites || tc.drop && wantWrites == 1 && writes.Load() > 1
			if waiting != tc.wantWaiting || waiting && (status != http.StatusServiceUnavailable || result.Rejected) ||
				result.Accepted || !writesMatch {
				t.Fatalf("response %d %+v, POSTs %d, want waiting=%t and POSTs=%d", status, result, writes.Load(), tc.wantWaiting, wantWrites)
			}
		})
	}
}

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

func TestMaskedSendReceiptReconcilesOnlyTheExactIntent(t *testing.T) {
	t.Run("expired", func(t *testing.T) { testMaskedSendReceipt(t, true) })
	t.Run("withdrawn", func(t *testing.T) { testMaskedSendReceipt(t, false) })
}

func testMaskedSendReceipt(t *testing.T, expired bool) {
	t.Helper()
	key := newOperationKey()
	fixture := &liveFixture{}
	ui, client, csrf := liveUI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/operations/"+key {
			w.Header().Set("Content-Type", "application/json")
			receipt := engineapi.Operation{Id: key, ResourceId: testMessage, Kind: "send_message", Status: "succeeded"}
			if expired {
				receipt.AcceptedMessageExpired = &expired
			} else {
				receipt.AcceptedMessage = &engineapi.Message{Id: testMessage, IsOwn: true, SendState: "queued", AccountId: testAccount, ConversationId: testChat, Retraction: &engineapi.MessageRetraction{}}
			}
			_ = json.NewEncoder(w).Encode(receipt)
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
	expectedState := "queued"
	if expired {
		expectedState = "expired"
	}
	if status != 200 || !result.Accepted || result.State != expectedState || result.MessageID != testMessage || !validOperationKey(result.NextKey) {
		t.Fatalf("exact masked receipt: %d %+v", status, result)
	}
	values.Set("originalText", "Another body")
	status, result = livePost(t, client, ui.URL, "/ui/messages/reconcile", csrf, values)
	if status != 409 || result.Accepted || fixture.effectCount() != 0 {
		t.Fatalf("mismatched intent: %d %+v", status, result)
	}
}
