package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func TestRetentionAPISettingsAndInheritance(t *testing.T) {
	server, store := start(t, t.TempDir())
	const global = "/api/v1/engine/retention"
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		got, data := request(t, server.URL, method, path, "", body)
		if got != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, got, want, data)
		}
		return data
	}
	var settings engineapi.GlobalRetention
	if err := json.Unmarshal(call("GET", global, "", http.StatusOK), &settings); err != nil ||
		settings.RoomDays != 180 || settings.DirectDays != 0 || settings.Revision != "0" {
		t.Fatalf("defaults: %+v %v", settings, err)
	}
	const change = `{"expectedRevision":"0","roomDays":0,"directDays":90}`
	call("PUT", global, change, http.StatusOK)
	call("PUT", global, change, http.StatusOK)
	call("PUT", global, `{"expectedRevision":"0","roomDays":90,"directDays":90}`, http.StatusConflict)
	call("PUT", global, `{"expectedRevision":"1","roomDays":-1,"directDays":90}`, http.StatusBadRequest)

	account := register(t, server.URL, string(id()), "retention@example.test")
	chat := domain.Conversation{ID: id(), RevisionID: id(), AccountID: domain.ID(account.Id), PeerID: id(), Address: "peer@example.test", Title: "Peer", Kind: "direct", UpdatedAt: time.Now().UTC()}
	if err := store.RestoreConversation(t.Context(), chat); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/conversations/" + string(chat.ID) + "/retention"
	var rule engineapi.ConversationRetention
	check := func(data []byte, revision string, wantDays *int, effective int) {
		t.Helper()
		err := json.Unmarshal(data, &rule)
		if err != nil || rule.Revision != revision || rule.EffectiveDays != effective || rule.GlobalDays != 90 ||
			(wantDays == nil && rule.Days != nil) || (wantDays != nil && (rule.Days == nil || *rule.Days != *wantDays)) {
			t.Fatalf("conversation policy: %+v %v", rule, err)
		}
	}
	check(call("GET", path, "", http.StatusOK), "0", nil, 90)
	zero := 0
	check(call("PUT", path, `{"expectedRevision":"0","days":0}`, http.StatusOK), "1", &zero, 0)
	check(call("PUT", path, `{"expectedRevision":"1","days":null}`, http.StatusOK), "2", nil, 90)
	call("PUT", path, `{"expectedRevision":"2"}`, http.StatusBadRequest)
	call("GET", "/api/v1/conversations/"+string(id())+"/retention", "", http.StatusNotFound)
}
