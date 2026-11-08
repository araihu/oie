package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestStarBFFReconcilesHistoricalReceiptConflictAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		name            string
		put, read, want int
		pending         bool
	}{
		{"historical receipt", 200, 200, 200, false}, {"stale conflict", 409, 200, 409, false}, {"unknown current", 200, 503, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/engine" {
					_ = json.NewEncoder(w).Encode(map[string]string{"id": testEngine, "role": "hosted", "eventCursor": testEngine + ":0"})
					return
				}
				if r.URL.Path != "/api/v1/messages/"+testMessage+"/star" {
					t.Fatalf("unexpected request %s", r.URL)
				}
				calls = append(calls, r.Method)
				if r.Method == "PUT" {
					if r.Header.Get("Idempotency-Key") != testRevision {
						t.Fatal("operation key changed")
					}
					var input engineapi.SetMessageStar
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil || !input.Starred || input.ExpectedRevisionId != nil {
						t.Fatal("star precondition/body changed")
					}
					w.WriteHeader(tc.put)
					if tc.put == 409 {
						_ = json.NewEncoder(w).Encode(map[string]any{"code": "state_conflict", "status": 409, "currentStar": engineapi.MessageStar{MessageId: testMessage, Starred: true}})
					} else {
						_ = json.NewEncoder(w).Encode(engineapi.MessageStar{MessageId: testMessage, Starred: true})
					}
					return
				}
				w.WriteHeader(tc.read)
				if tc.read == 200 {
					_ = json.NewEncoder(w).Encode(engineapi.MessageStar{MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, Starred: false})
				}
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			seed := httptest.NewRecorder()
			token := csrfToken(seed, httptest.NewRequest("GET", "/", nil))
			form := url.Values{"engineId": {testEngine}, "csrf": {token}, "messageId": {testMessage}, "operationKey": {testRevision}, "starred": {"true"}, "expectedRevisionId": {""}}
			r := httptest.NewRequest("POST", "/ui/messages/star", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Accept", "application/json")
			r.AddCookie(seed.Result().Cookies()[0])
			w := httptest.NewRecorder()
			ui.ServeHTTP(w, r)
			var result mutationResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.want || result.StarPending != tc.pending || len(calls) != 2 || calls[0] != "PUT" || calls[1] != "GET" {
				t.Fatalf("wrong star reconciliation %d %s calls=%v", w.Code, w.Body.String(), calls)
			}
			if result.Star != nil && result.Star.Starred {
				t.Fatal("displayed historical/stale true snapshot instead of current false")
			}
		})
	}
}
func TestStarBFFCSRFAndMethodGuardBeforeEffects(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/ui/messages/star", nil)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		want := 405
		if method == "POST" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("guard returned %d", w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("untrusted star reached backend")
	}
}
