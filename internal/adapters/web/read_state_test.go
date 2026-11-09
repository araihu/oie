package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestReadProxyProtectsEngineAccountAndCSRF(t *testing.T) {
	var effects atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(engineapi.Engine{Id: testEngine})
		case "/api/v1/conversations/" + testChat:
			_ = json.NewEncoder(w).Encode(engineapi.Conversation{Id: testChat, AccountId: testAccount})
		case "/api/v1/conversations/" + testChat + "/read-state":
			if r.Method == "POST" {
				effects.Add(1)
				if r.Header.Get("Idempotency-Key") != testRevision || r.Header.Get("Cookie") != "" {
					t.Error("browser credentials or operation identity forwarded incorrectly")
				}
			}
			_ = json.NewEncoder(w).Encode(engineapi.ConversationReadState{ConversationId: testChat, AccountId: testAccount, UnreadCount: 0})
		default:
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct {
		name, method, engine, account, body string
		csrf                                bool
		want, changes                       int
	}{
		{name: "safe GET", method: "GET", engine: testEngine, account: testAccount, want: 200},
		{name: "missing CSRF", method: "POST", engine: testEngine, account: testAccount, body: `{"messageIds":["` + testMessage + `"]}`, want: 403},
		{name: "changed engine", method: "POST", engine: testRevision, account: testAccount, body: `{"messageIds":["` + testMessage + `"]}`, csrf: true, want: 409},
		{name: "changed account", method: "POST", engine: testEngine, account: testRevision, body: `{"messageIds":["` + testMessage + `"]}`, csrf: true, want: 409},
		{name: "invalid UUID", method: "POST", engine: testEngine, account: testAccount, body: `{"messageIds":["bad"]}`, csrf: true, want: 400},
		{name: "empty batch", method: "POST", engine: testEngine, account: testAccount, body: `{"messageIds":[]}`, csrf: true, want: 400},
		{name: "trailing data", method: "POST", engine: testEngine, account: testAccount, body: `{"messageIds":["` + testMessage + `"]}{}`, csrf: true, want: 400},
		{name: "valid POST", method: "POST", engine: testEngine, account: testAccount, body: `{"messageIds":["` + testMessage + `"]}`, csrf: true, want: 200, changes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := effects.Load()
			request := httptest.NewRequest(tc.method, "/api/v1/conversations/"+testChat+"/read-state", strings.NewReader(tc.body))
			request.Header.Set("X-Oie-Engine-ID", tc.engine)
			request.Header.Set("X-Oie-Account-ID", tc.account)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", testRevision)
			if tc.csrf {
				request.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
				request.Header.Set("X-CSRF-Token", token)
			}
			response := httptest.NewRecorder()
			ui.ServeHTTP(response, request)
			if response.Code != tc.want || effects.Load()-before != int32(tc.changes) {
				t.Fatal(response.Code, response.Body.String(), effects.Load()-before)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("read state was cacheable")
			}
			if tc.want == 200 && !validOperationKey(response.Header().Get("X-Oie-Next-Operation-Key")) {
				t.Fatal("missing UUIDv7 next intent")
			}
		})
	}
}
