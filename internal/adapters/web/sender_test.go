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

func TestSenderInformationScopeAndRoomPrivacy(t *testing.T) {
	const engine = "019a5df0-0000-7000-8000-000000000001"
	const message = "019a5df0-0000-7000-8000-000000000099"
	for _, variant := range []string{"room", "direct", "invalid", "engine", "message", "message account", "message conversation", "conversation", "conversation account", "account", "missing", "unavailable"} {
		t.Run(variant, func(t *testing.T) {
			m := engineapi.Message{Id: message, AccountId: testAccount, ConversationId: testChat, Sender: engineapi.Sender{Id: testChat, DisplayName: "<Alice>@example.test"}}
			c := engineapi.Conversation{Id: testChat, AccountId: testAccount, Kind: "room", Address: "room@example.test", Title: "Example room"}
			a := engineapi.Account{Id: testAccount, Address: "owner@example.test", Label: "My account"}
			switch variant {
			case "direct":
				c.Kind = "direct"
				c.Address = "peer@example.test"
			case "message":
				m.Id = testChat
			case "message account":
				m.AccountId = testRevision
			case "message conversation":
				m.ConversationId = testRevision
			case "conversation":
				c.Id = testRevision
			case "conversation account":
				c.AccountId = testRevision
			case "account":
				a.Id = testRevision
			}
			paths := []string{}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Method != "GET" {
					t.Errorf("unexpected mutation: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				send := func(value any) { _ = json.NewEncoder(w).Encode(value) }
				switch r.URL.Path {
				case "/api/v1/engine":
					id := engine
					if variant == "engine" {
						id = testRevision
					}
					send(map[string]string{"id": id})
				case "/api/v1/messages/" + message:
					if variant == "missing" {
						w.WriteHeader(404)
						send(map[string]any{"status": 404, "code": "not_found", "detail": "Missing message"})
						return
					}
					if variant == "unavailable" {
						w.WriteHeader(503)
						return
					}
					send(m)
				case "/api/v1/conversations/" + testChat:
					send(c)
				case "/api/v1/accounts/" + testAccount:
					send(a)
				case "/api/v1/accounts/" + testAccount + "/avatars":
					send(map[string]any{"items": []any{}})
				case "/api/v1/conversations/" + testChat + "/details":
					send(engineapi.ConversationDetails{ConversationId: testChat, AccountId: testAccount, Kind: "direct", Address: c.Address, AccountConnection: engineapi.Connection{AccountId: testAccount, State: "connected"}, Participants: engineapi.ConversationParticipants{Scope: "peer"}, PeerPresence: &engineapi.PresenceObservation{State: "away"}})
				default:
					t.Errorf("unexpected request: %s", r.URL)
					w.WriteHeader(404)
				}
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			query := url.Values{"engineId": {engine}, "accountId": {testAccount}, "conversationId": {testChat}, "messageId": {message}, "lang": {"en"}}
			if variant == "invalid" {
				query.Set("messageId", "bad")
			}
			out := httptest.NewRecorder()
			ui.ServeHTTP(out, httptest.NewRequest("GET", "/ui/sender?"+query.Encode(), nil))
			want := 404
			switch variant {
			case "room", "direct":
				want = 200
			case "invalid":
				want = 400
			case "engine":
				want = 409
			case "unavailable":
				want = 503
			}
			if out.Code != want {
				t.Fatalf("status %d, want %d", out.Code, want)
			}
			html := out.Body.String()
			if out.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private sender fragment became cacheable")
			}
			if want != 200 {
				if strings.Contains(html, "Example room") || strings.Contains(html, "My account") {
					t.Fatal("foreign scope disclosed sender data")
				}
				return
			}
			if strings.Contains(html, "<Alice>") || !strings.Contains(html, "&lt;Alice&gt;") {
				t.Fatal("sender name was not escaped")
			}
			if !strings.Contains(html, "data-sender-message=\""+message+"\"") || !strings.Contains(html, "My account") {
				t.Fatal("missing scoped sender context")
			}
			if variant == "room" {
				if strings.Contains(html, "data-sender-address") || strings.Contains(html, "data-presence-state") || strings.Contains(html, "/ui/avatars/") {
					t.Fatal("room nickname gained a personal profile")
				}
				if len(paths) != 4 {
					t.Fatalf("room view made unnecessary identity/presence requests: %v", paths)
				}
			} else if !strings.Contains(html, "peer@example.test") || !strings.Contains(html, "data-presence-state=\"away\"") {
				t.Fatal("authorized direct peer details missing")
			}
		})
	}
}
