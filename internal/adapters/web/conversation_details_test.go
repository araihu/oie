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
func TestRoomParticipantIdentityAndPresenceRemainScoped(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	name := "alice@example.test"
	status := "<script>bad()</script>"
	v := view{Text: catalogs["en"], Selected: &engineapi.Conversation{Id: testChat, AccountId: testAccount, Kind: "room", Title: "Room", Address: "room@example.test"}, Avatars: map[avatarIdentity]string{{Account: testAccount, Address: name}: strings.Repeat("a", 64)}, ConversationInfo: &engineapi.ConversationDetails{AccountConnection: engineapi.Connection{AccountId: testAccount, State: "not_connected"}, Participants: engineapi.ConversationParticipants{State: "partial", Scope: "occupants", Truncated: true, Items: []engineapi.ConversationParticipant{{Id: "room/nick", DisplayName: name, Source: "discovery", Presence: engineapi.PresenceObservation{State: "unknown", StatusText: &status}}}}}}
	var out bytes.Buffer
	if err := detailsParticipants(v).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, "<img") || strings.Contains(html, "/ui/avatars/") || strings.Contains(html, "<script>") {
		t.Fatal("unverified occupant gained a photo or executable status text")
	}
	if !strings.Contains(html, "Presence unknown") || !strings.Contains(html, "Public room listing") || !strings.Contains(html, "data-participants-truncated") {
		t.Fatal("participant observation lost its limited scope")
	}
	if v.presenceLabel("") != v.Text.PresenceUnknown || v.presenceLabel("unknown") == v.presenceLabel("unavailable") {
		t.Fatal("missing presence became unavailable")
	}
	if v.detailsAccountView().connectionLabel(testAccount) != v.Text.NotConnected {
		t.Fatal("own account connection was not authoritative")
	}
	before := v.detailsVersion("participants")
	identity := v.detailsVersion("identity")
	v.ConversationInfo.Participants.Items[0].Presence.State = "away"
	if v.detailsVersion("participants") == before || v.detailsVersion("identity") != identity {
		t.Fatal("section versions lost scoped invalidation")
	}
}
