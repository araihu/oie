package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestReplyReceiptUsesFrozenTargetAndAuthoredText(t *testing.T) {
	target := testMessage
	m := engineapi.Message{AccountId: testAccount, ConversationId: testChat, OriginalText: "> frozen fallback\n\nauthored", ReplyToMessageId: &target, Reply: &engineapi.MessageReply{BodyText: "authored"}}
	if !matchingSend(m, testChat, testAccount, "authored", target) {
		t.Fatal("outbound fallback receipt was not accepted")
	}
	for _, tc := range []struct{ account, conversation, body, target string }{{testAccount, testChat, "authored", ""}, {testAccount, testChat, "changed", target}, {testAccount, testChat, "authored", testRevision}, {testRevision, testChat, "authored", target}, {testAccount, testRevision, "authored", target}} {
		if matchingSend(m, tc.conversation, tc.account, tc.body, tc.target) {
			t.Fatalf("mismatched receipt accepted: %+v", tc)
		}
	}
	m.Reply = nil
	if matchingSend(m, testChat, testAccount, "authored", target) {
		t.Fatal("missing authored-text proof accepted")
	}
}
func TestReplyPreparationBindsCanonicalSourceAndBoundsPlainExcerpt(t *testing.T) {
	sourceAccount := testAccount
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted"})
		case "/api/v1/conversations/" + testChat:
			_ = json.NewEncoder(w).Encode(engineapi.Conversation{Id: testChat, AccountId: testAccount})
		case "/api/v1/conversations/" + testChat + "/messages/" + testMessage + "/context":
			if r.URL.Query().Get("limit") != "1" {
				t.Error("preparation loaded unnecessary history")
			}
			_ = json.NewEncoder(w).Encode(engineapi.MessageContext{TargetId: testMessage, Items: []engineapi.Message{{Id: testMessage, RevisionId: testRevision, AccountId: sourceAccount, ConversationId: testChat, Sender: engineapi.Sender{DisplayName: "<sender>"}, OriginalText: "compatibility fallback", Reply: &engineapi.MessageReply{BodyText: strings.Repeat("😀", 520)}}}})
		default:
			t.Errorf("unexpected source request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "messageId": {testMessage}}
	get := func() (int, replyPreparation) {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", "/ui/reply-source?"+query.Encode(), nil))
		var result replyPreparation
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("quote metadata became shared cacheable")
		}
		return w.Code, result
	}
	status, result := get()
	digest := sha256.Sum256([]byte(result.Excerpt))
	if status != 200 || result.MessageID != testMessage || result.RevisionID != testRevision || result.SenderName != "<sender>" ||
		result.Excerpt != strings.Repeat("😀", 512) || result.ExcerptSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("invalid preparation: %d %+v", status, result)
	}
	sourceAccount = testRevision
	status, _ = get()
	if status != 503 {
		t.Fatal("foreign account source leaked")
	}
	query.Set("engineId", testRevision)
	status, _ = get()
	if status != 409 {
		t.Fatal("engine identity not bound")
	}
}

func TestUnknownSendReceiptCannotBecomeRejectedOrRepeat(t *testing.T) {
	writes := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"code":"unavailable"}`))
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, _ := loadCatalogs()
	form := url.Values{"operationKey": {newOperationKey()}, "accountId": {testAccount}, "conversationId": {testChat}, "originalText": {"authored"}, "replyToMessageId": {testMessage}}
	request := httptest.NewRequest("POST", "/ui/messages/send", nil)
	request.Form = form
	status, result := ui.send(context.Background(), request, catalogs["en"])
	if status != 503 || result.Rejected || result.Accepted || writes != 0 {
		t.Fatalf("uncertain receipt became rejection/retry: %d %+v writes=%d", status, result, writes)
	}
}
func TestRoomCASPassesExplicitNullAndNeverPrechecksReplay(t *testing.T) {
	calls := 0
	key := newOperationKey()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/conversations/" + testChat:
			_ = json.NewEncoder(w).Encode(engineapi.Conversation{Id: testChat, AccountId: testAccount, Kind: "room"})
		case "/api/v1/conversations/" + testChat + "/room-session":
			calls++
			if r.Header.Get("Idempotency-Key") != key {
				t.Error("operation key changed")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			value, present := body["expectedRevisionId"]
			if !present || value != nil {
				t.Fatal("initial CAS must explicitly serialize null")
			}
			_ = json.NewEncoder(w).Encode(engineapi.RoomSession{ConversationId: testChat, AccountId: testAccount, State: "joining", Active: true})
		default:
			t.Errorf("unexpected room precheck %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, _ := loadCatalogs()
	r := httptest.NewRequest("POST", "/ui/rooms/join", nil)
	r.Form = url.Values{"operationKey": {key}, "accountId": {testAccount}, "conversationId": {testChat}, "expectedRevisionId": {""}}
	for range 2 {
		status, result := ui.changeRoom(context.Background(), r, catalogs["en"], false)
		if status != 200 || result.RoomSession == nil || result.RoomSession.State != "joining" {
			t.Fatalf("join receipt failed: %d %+v", status, result)
		}
	}
	if calls != 2 {
		t.Fatal("BFF bypassed authoritative replay")
	}
}

func TestUncertainRoomJoinReceipt(t *testing.T) {
	key := newOperationKey()
	cases := []struct {
		name                     string
		account, operation, room string
		kind                     engineapi.OperationKind
		outcome                  engineapi.OperationStatus
		readStatus, wantStatus   int
	}{
		{"accepted", testAccount, key, testChat, "join_room", "succeeded", 200, 200},
		{"foreign owner", testMessage, key, testChat, "join_room", "succeeded", 0, 409},
		{"different operation", testAccount, testMessage, testChat, "join_room", "succeeded", 200, 409},
		{"different room", testAccount, key, testMessage, "join_room", "succeeded", 200, 409},
		{"different command", testAccount, key, testChat, "leave_room", "succeeded", 200, 409},
		{"unknown outcome", testAccount, key, testChat, "join_room", "pending", 200, 409},
		{"absent receipt", testAccount, key, testChat, "join_room", "", 404, 422},
		{"unavailable receipt", testAccount, key, testChat, "join_room", "", 503, 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					t.Errorf("receipt check issued %s %s", r.Method, r.URL.Path)
					w.WriteHeader(405)
					return
				}
				switch r.URL.Path {
				case "/api/v1/conversations/" + testChat:
					_ = json.NewEncoder(w).Encode(engineapi.Conversation{Id: testChat, AccountId: tc.account, Kind: "room"})
				case "/api/v1/operations/" + key:
					if tc.readStatus == 0 {
						t.Error("read receipt before checking room ownership")
						w.WriteHeader(403)
						return
					}
					w.WriteHeader(tc.readStatus)
					if tc.readStatus == 200 {
						_ = json.NewEncoder(w).Encode(engineapi.Operation{Id: tc.operation, Kind: tc.kind, ResourceId: tc.room, Status: tc.outcome})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]int{"status": tc.readStatus})
					}
				default:
					t.Errorf("unexpected read %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/ui/rooms/join", nil)
			req.Form = url.Values{"operationKey": {key}, "accountId": {testAccount}, "conversationId": {testChat}, "expectedRevisionId": {""}, "checkJoinReceipt": {"1"}}
			status, result := ui.changeRoom(t.Context(), req, copy{}, false)
			if status != tc.wantStatus || result.OK != (status == 200) || result.Rejected != (status == 409) {
				t.Fatalf("got %d, ok=%v, rejected=%v", status, result.OK, result.Rejected)
			}
			if status == 422 && result.Code != "room_password_requested" {
				t.Fatalf("missing password prompt on absent receipt: %s", result.Code)
			}
		})
	}
}
