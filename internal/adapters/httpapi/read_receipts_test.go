package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func TestReadReceiptSettingsOverrideAndInherit(t *testing.T) {
	server, store := start(t, t.TempDir())
	const global = "/api/v1/engine/read-receipts"
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, data := request(t, server.URL, method, path, "", body)
		if status != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, status, want, data)
		}
		return data
	}
	owner := register(t, server.URL, string(id()), "owner@example.test")
	paths := make([]string, 0, 2)
	for _, kind := range []string{"direct", "room"} {
		c := domain.Conversation{
			ID: id(), RevisionID: id(), AccountID: domain.ID(owner.Id), PeerID: id(),
			Address: kind + "@example.test", Title: kind, Kind: kind, UpdatedAt: time.Now().UTC(),
		}
		if err := store.RestoreConversation(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, "/api/v1/conversations/"+string(c.ID)+"/read-receipts")
	}
	check := func(body []byte, revision string, globalEnabled, effectiveEnabled bool, override *bool) {
		t.Helper()
		var settings engineapi.ConversationReadReceipts
		if err := json.Unmarshal(body, &settings); err != nil || settings.Revision != revision ||
			settings.GlobalEnabled != globalEnabled || settings.EffectiveEnabled != effectiveEnabled ||
			(override == nil && settings.Override != nil) || (override != nil && (settings.Override == nil || *settings.Override != *override)) {
			t.Fatalf("conversation settings: %+v (%s), %v", settings, body, err)
		}
	}
	for _, p := range paths {
		check(call("GET", p, "", http.StatusOK), "0:0", true, true, nil)
	}
	call("PUT", global, `{"expectedRevision":"0","enabled":false}`, http.StatusOK)
	call("PUT", global, `{"expectedRevision":"0","enabled":true}`, http.StatusConflict)
	check(call("GET", paths[0], "", http.StatusOK), "1:0", false, false, nil)
	call("PUT", paths[0], `{"expectedRevision":"0:0","override":true}`, http.StatusConflict)
	check(call("PUT", paths[0], `{"expectedRevision":"1:0","override":true}`, http.StatusOK), "1:1", false, true, new(true))
	check(call("GET", paths[1], "", http.StatusOK), "1:0", false, false, nil)

	call("PUT", global, `{"expectedRevision":"1","enabled":true}`, http.StatusOK)
	check(call("PUT", paths[1], `{"expectedRevision":"2:0","override":false}`, http.StatusOK), "2:1", true, false, new(false))
	check(call("GET", paths[0], "", http.StatusOK), "2:1", true, true, new(true))
	call("PUT", paths[1], `{"expectedRevision":"2:1"}`, http.StatusBadRequest)
	check(call("PUT", paths[1], `{"expectedRevision":"2:1","override":null}`, http.StatusOK), "2:2", true, true, nil)
	call("GET", "/api/v1/conversations/"+string(id())+"/read-receipts", "", http.StatusNotFound)
}
