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

func TestMentionCandidatePreparationIsReadOnlyAndScoped(t *testing.T) {
	mode := "correct"
	reads := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("mention preparation mutated engine", r.Method)
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted"})
		case "/api/v1/conversations/" + testChat:
			owner := testAccount
			if mode == "wrong-owner" {
				owner = testRevision
			}
			kind := "room"
			if mode == "direct" {
				kind = "direct"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testChat, "accountId": owner, "kind": kind})
		case "/api/v1/conversations/" + testChat + "/mention-candidates":
			reads++
			owner := testAccount
			if mode == "wrong-result" {
				owner = testRevision
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"accountId": owner, "conversationId": testChat, "state": "partial", "truncated": true, "items": []map[string]string{{"id": "opaque-room-occupant", "displayName": "<Ana> 🐻", "insertText": "@ana", "referenceMode": "text"}}})
		default:
			t.Error("unexpected lookup", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	server, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}}
	for _, tc := range []struct {
		mode   string
		status int
	}{{"correct", 200}, {"wrong-owner", 404}, {"direct", 404}, {"wrong-result", 503}, {"wrong-engine", 409}} {
		t.Run(tc.mode, func(t *testing.T) {
			mode = tc.mode
			q := query.Encode()
			if mode == "wrong-engine" {
				q = strings.ReplaceAll(q, testEngine, testRevision)
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, httptest.NewRequest("GET", "/ui/mentions?"+q, nil))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("candidate roster cached")
			}
			if w.Code == 200 {
				var result struct {
					EngineID string `json:"engineId"`
					engineapi.MentionCandidates
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.EngineID != testEngine || result.AccountId != testAccount || !result.Truncated || len(result.Items) != 1 || result.Items[0].DisplayName != "<Ana> 🐻" {
					t.Fatal("lost candidate scope or readable data", w.Body.String())
				}
			}
		})
	}
	if reads != 2 {
		t.Fatal("queried candidates before proving ownership", reads)
	}
}

func TestMentionSubmissionRejectsProtocolIdentifiersAndInvalidSpans(t *testing.T) {
	body := "😀 @ana @bob"
	for _, raw := range []string{`[{"participantId":"a","begin":2,"end":6,"uri":"xmpp:forged@example.org"}]`, `[{"participantId":"a","begin":-1,"end":6}]`, `[{"participantId":"a","begin":2,"end":99}]`, `[{"participantId":"a","begin":2,"end":6},{"participantId":"b","begin":5,"end":8}]`, `[] true`, `[{"participantId":"","begin":2,"end":6}]`} {
		if _, err := parseMentionSelections(raw, body); err == nil {
			t.Fatal("accepted invalid selection", raw)
		}
	}
	selections, err := parseMentionSelections(`[{"participantId":"a","begin":2,"end":6},{"participantId":"b","begin":7,"end":11}]`, body)
	if err != nil || len(selections) != 2 {
		t.Fatal("valid Unicode selections rejected", selections, err)
	}
}

func TestMentionReceiptMustMatchFrozenSelections(t *testing.T) {
	chosen := []engineapi.MentionSelection{{ParticipantId: "room-occupant", Begin: 2, End: 6}}
	actual := []engineapi.MessageMention{{ParticipantId: "room-occupant", Begin: 2, End: 6, ReferenceMode: "text"}}
	message := engineapi.Message{AccountId: testAccount, ConversationId: testChat, OriginalText: "😀 @ana", Mentions: &actual}
	if !matchingSend(message, testChat, testAccount, "😀 @ana", "", chosen) {
		t.Fatal("matching receipt rejected")
	}
	for _, changed := range [][]engineapi.MentionSelection{nil, {{ParticipantId: "another-room", Begin: 2, End: 6}}, {{ParticipantId: "room-occupant", Begin: 3, End: 6}}} {
		if matchingSend(message, testChat, testAccount, "😀 @ana", "", changed) {
			t.Fatal("changed selections matched accepted receipt", changed)
		}
	}
}
