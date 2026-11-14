package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestConversationDetailsRejectsForeignEvidence(t *testing.T) {
	for _, field := range []string{"valid", "account", "conversation", "connection", "connection state", "participant scope", "address", "kind"} {
		t.Run(field, func(t *testing.T) {
			d := engineapi.ConversationDetails{ConversationId: testChat, AccountId: testAccount, Address: "room@example.test", Kind: "room", Participants: engineapi.ConversationParticipants{Scope: "occupants"}, AccountConnection: engineapi.Connection{AccountId: testAccount, State: "connected"}}
			switch field {
			case "account":
				d.AccountId = testRevision
			case "conversation":
				d.ConversationId = testRevision
			case "connection state":
				d.AccountConnection.State = ""
			case "participant scope":
				d.Participants.Scope = "peer"
			case "connection":
				d.AccountConnection.AccountId = testRevision
			case "address":
				d.Address = "other@example.test"
			case "kind":
				d.Kind = "direct"
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/conversations/"+testChat+"/details" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(d)
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			v := view{Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount, Address: "room@example.test", Kind: "room"}}
			ui.loadConversationDetails(context.Background(), &v)
			if (v.ConversationInfo != nil) != (field == "valid") {
				t.Fatal("foreign conversation details accepted")
			}
		})
	}
}
func TestRoomParticipantsRenderReadOnlySnapshot(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	v := view{
		Text:     catalogs["en"],
		InfoView: "participants",
		Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount, Kind: "room", Title: "Room", Address: "room@example.test"},
		InfoParticipants: &engineapi.RoomParticipants{State: "available", Participants: []engineapi.RoomSessionParticipant{
			{Nickname: "Visitor", Role: "visitor", Affiliation: "none"},
			{Nickname: "<script>Owner</script>", Role: "moderator", Affiliation: "owner", AvatarHash: &hash},
		}},
	}
	var out bytes.Buffer
	if err := chatInfoContent(v).Render(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, "/ui/avatars/"+hash) || !strings.Contains(html, "&lt;script&gt;Owner&lt;/script&gt;") ||
		!strings.Contains(html, `id="info-room-participants-search"`) || strings.Index(html, ">Owners<") >= strings.Index(html, ">Visitors<") {
		t.Fatalf("read-only roster: %s", html)
	}
	for _, forbidden := range []string{"<script>", "<form", "data-room-participant-open", "room@example.test"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("unexpected %q in read-only roster", forbidden)
		}
	}
}
