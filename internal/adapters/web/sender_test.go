package web

import (
	"bytes"
	"context"
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
				case "/api/v1/messages/" + message + "/sender-profile":
					w.WriteHeader(404)
					send(map[string]any{"status": 404, "code": "not_found"})
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
				if len(paths) != 5 {
					t.Fatalf("room view made unnecessary identity/presence requests: %v", paths)
				}
			} else if !strings.Contains(html, "peer@example.test") || !strings.Contains(html, "data-presence-state=\"away\"") {
				t.Fatal("authorized direct peer details missing")
			}
		})
	}
}

func TestSenderPublishedProfileProjection(t *testing.T) {
	const message = "019a5df0-0000-7000-8000-000000000099"
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"room known", "direct known", "same nickname", "same address", "foreign account", "foreign conversation", "foreign message", "invalid address", "resource address", "unsupported state", "not disclosed", "name controls", "oversize name", "old engine", "direct foreign address"} {
		t.Run(variant, func(t *testing.T) {
			address, name := "alice@example.test", "Alice <Published> Name"
			p := engineapi.MessageSenderProfile{AccountId: testAccount, ConversationId: testChat, MessageId: message, State: "known", FullName: &name, PersonalAddress: &address}
			source := messageView{Message: engineapi.Message{Id: message, AccountId: testAccount, ConversationId: testChat, Sender: engineapi.Sender{DisplayName: "Alice nick"}}}
			result := &senderInfoView{Message: source}
			switch variant {
			case "direct known":
				result.Address = address
			case "same nickname":
				source.Sender.DisplayName = name
				result.Message = source
			case "same address":
				name = address
			case "foreign account":
				p.AccountId = testRevision
			case "foreign conversation":
				p.ConversationId = testRevision
			case "foreign message":
				p.MessageId = testRevision
			case "invalid address":
				address = "alice@example.test<script>"
			case "resource address":
				address = "alice@example.test/resource"
			case "unsupported state":
				p.State = "unexpected"
			case "not disclosed":
				p.State = "not_disclosed"
			case "name controls":
				name = "Alice\nNew line"
			case "oversize name":
				name = strings.Repeat("x", 301)
			case "direct foreign address":
				result.Address = "bob@example.test"
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/messages/"+message+"/sender-profile" {
					t.Errorf("unexpected profile request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if variant == "old engine" {
					w.WriteHeader(404)
					_ = json.NewEncoder(w).Encode(map[string]any{"status": 404})
					return
				}
				_ = json.NewEncoder(w).Encode(p)
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			ui.loadSenderProfile(context.Background(), result)
			accepted := variant == "room known" || variant == "direct known" || variant == "same nickname" || variant == "same address" || variant == "name controls" || variant == "oversize name"
			if accepted && result.Address != address {
				t.Fatal("scoped personal address missing")
			}
			if !accepted && result.FullName != "" {
				t.Fatal("foreign or unavailable profile gained a name")
			}
			if !accepted && variant != "direct foreign address" && result.Address != "" {
				t.Fatal("foreign or undisclosed profile gained an address")
			}
			if variant == "direct foreign address" && result.Address != "bob@example.test" {
				t.Fatal("profile changed canonical direct peer")
			}
			validName := accepted && variant != "name controls" && variant != "oversize name"
			if (result.FullName != "") != validName {
				t.Fatal("invalid published name accepted or valid name lost")
			}
			v := view{Text: catalogs["en"], SenderInfo: result, Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount, Kind: "room", Address: "room@example.test", Title: "Room"}, Accounts: []engineapi.Account{{Id: testAccount, Address: "owner@example.test"}}, Avatars: map[avatarIdentity]string{}}
			var out bytes.Buffer
			if err := senderContent(v).Render(context.Background(), &out); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			if strings.Contains(html, "<Published>") {
				t.Fatal("published name was not escaped")
			}
			if variant == "same nickname" && strings.Contains(html, "data-sender-nickname") {
				t.Fatal("name repeated as nickname")
			}
			if variant == "same address" && strings.Contains(html, "data-sender-address") {
				t.Fatal("name repeated as address")
			}
			if variant == "room known" && (!strings.Contains(html, "data-sender-nickname") || !strings.Contains(html, "data-sender-address") || !strings.Contains(html, "Alice &lt;Published&gt; Name")) {
				t.Fatal("full name, nickname and address missing")
			}
		})
	}
}
