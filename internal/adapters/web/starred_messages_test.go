package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web/appicons"
)

func TestFavoritesWorkspaceShowsStarredMessagesAcrossConversations(t *testing.T) {
	const roomID = "019a5df0-0000-7000-8000-000000000011"
	const directID = "019a5df0-0000-7000-8000-000000000012"
	const imageURL = "https://images.example.test/favorite.png"
	const imageHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const otherAccount = "019a5df0-0000-7000-8000-000000000013"
	fixture := &liveFixture{}
	starred := func(chat engineapi.Conversation, messageID, text string) engineapi.StarredMessage {
		revision := testRevision
		return engineapi.StarredMessage{
			Conversation: chat,
			Message: engineapi.Message{
				Id: messageID, RevisionId: revision, AccountId: chat.AccountId, ConversationId: chat.Id,
				OriginalText: text, Sender: engineapi.Sender{DisplayName: "Alice"},
			},
			Star: engineapi.MessageStar{
				MessageId: messageID, AccountId: chat.AccountId, ConversationId: chat.Id,
				RevisionId: &revision, Starred: true,
			},
		}
	}
	room := starred(engineapi.Conversation{Id: roomID, AccountId: testAccount, Title: "Archived room", Kind: "room", Archived: true}, testMessage, "**Starred in the room**\n"+imageURL)
	direct := starred(engineapi.Conversation{Id: testChat, AccountId: otherAccount, Title: "Direct conversation", Kind: "direct"}, directID, "Starred in a direct conversation")
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/conversations/"+roomID+"/previews" {
			if ids := r.URL.Query()["messageId"]; len(ids) != 1 || ids[0] != testMessage {
				t.Errorf("unexpected preview scope: %s", r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			hash := imageHash
			_ = json.NewEncoder(w).Encode(engineapi.PreviewPage{Items: []engineapi.MessagePreview{
				{Id: testRevision, RevisionId: testRevision, MessageId: testMessage, AccountId: testAccount, ConversationId: roomID, Kind: "image", State: "ready", DownloadState: "ready", Url: imageURL, ImageHash: &hash, FileHash: &hash},
				{Id: otherAccount, MessageId: testMessage, AccountId: otherAccount, ConversationId: roomID, Kind: "link", Url: "https://example.test/foreign-preview"},
			}})
			return
		}
		if r.URL.Path != "/api/v1/starred-messages" {
			if r.URL.Path == "/api/v1/conversations" && r.URL.Query().Get("favoritesOnly") == "true" {
				t.Error("workspace requested favorite conversations")
			}
			fixture.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("conversationId") != "" || q.Get("limit") != "50" {
			t.Errorf("unexpected star scope: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		page := engineapi.StarredMessagePage{Items: []engineapi.StarredMessage{}}
		switch {
		case q.Get("cursor") == "stale":
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(engineapi.Problem{Status: 409})
			return
		case q.Get("q") == "Archived room":
			page.Items = []engineapi.StarredMessage{room}
			page.NextCursor = &room.Message.Id
		case q.Get("q") != "":
		case q.Get("cursor") == directID:
			page.Items = []engineapi.StarredMessage{room}
		case q.Get("accountId") == testAccount:
			page.Items = []engineapi.StarredMessage{room}
			page.NextCursor = &room.Message.Id
		case q.Get("accountId") == "":
			page.Items = []engineapi.StarredMessage{room, direct}
			page.NextCursor = &direct.Message.Id
		}
		_ = json.NewEncoder(w).Encode(page)
	})
	ui, client, _ := liveUI(t, backend)
	for _, tc := range []struct {
		path       string
		want, omit []string
	}{
		{"/?section=favorites&filter=favorites", []string{"<strong>Starred in the room</strong>", "/ui/media/" + imageHash, "data-preview-expand", "Archived room", "Starred in a direct conversation", "Direct conversation", "favoritesCursor=" + directID, "chat=" + roomID, "message=" + testMessage, appicons.SpriteURL + "#" + string(appicons.IconHeroiconsNavigation16SolidArrowTurnRightDown)}, []string{`id="conversation-list"`, `href="` + imageURL + `"`, "foreign-preview"}},
		{"/?section=favorites&account=" + testAccount, []string{"Starred in the room", "favoritesCursor=" + testMessage}, []string{"Starred in a direct conversation"}},
		{"/?section=favorites&favoritesCursor=" + directID, []string{"Starred in the room", "Show newest items"}, []string{"Starred in a direct conversation"}},
		{"/?section=favorites&account=" + otherAccount + "&lang=pt-BR", []string{"Nenhuma mensagem favorita"}, []string{"Starred in the room", "Starred in a direct conversation"}},
		{"/?section=favorites&favoritesQuery=Archived+room&lang=en", []string{`type="search"`, `aria-label="Search starred messages"`, `data-search-query="Archived room"`, "favoritesQuery=Archived+room", "favoritesCursor=" + testMessage}, []string{"Starred in a direct conversation"}},
		{"/?section=favorites&favoritesQuery=missing&lang=en", []string{"No messages found"}, []string{"No starred messages", "Starred in the room"}},
		{"/?section=favorites&favoritesCursor=stale&lang=en", []string{"Show newest items"}, []string{"Starred in the room"}},
		{"/ui/refresh?section=favorites", []string{`id="favorites-results"`, "Starred in the room", "/ui/media/" + imageHash}, []string{`id="oie-app"`}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			res, err := client.Get(ui.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != 200 {
				t.Fatalf("unexpected status: %d", res.StatusCode)
			}
			html := string(raw)
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, omit := range tc.omit {
				if strings.Contains(html, omit) {
					t.Errorf("unexpected %q", omit)
				}
			}
		})
	}
}
