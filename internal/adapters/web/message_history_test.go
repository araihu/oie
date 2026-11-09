package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func historyBoundaryPage(count int, text string) (engineapi.MessageEditHistoryPage, engineapi.Message) {
	source := engineapi.Message{Id: testMessage, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat}
	page := engineapi.MessageEditHistoryPage{RequestedMessageId: testMessage, MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, Snapshot: strings.Repeat("a", 64)}
	for i := range count {
		revision := fmt.Sprintf("019a5df0-0000-7000-8000-%012d", i+500)
		page.Items = append(page.Items, engineapi.MessageEditHistoryItem{RevisionId: revision, SourceMessageId: testMessage, CreatedAt: time.Date(2026, 11, 9, 12, count-i, 0, 0, time.UTC), OriginalText: text, Sender: engineapi.Sender{Id: testAccount}, IsCurrent: i == 0, IsOriginal: i == count-1, ParentRevisionIds: []string{}})
		if i == 0 {
			page.CurrentRevisionId = revision
		}
	}
	return page, source
}

func TestMessageHistoryBodyAndMetadataBoundsAreIndependent(t *testing.T) {
	body := strings.Repeat("a", messageHistoryBodyBytes)
	for _, count := range []int{9, messageHistoryPageSize} {
		page, source := historyBoundaryPage(count, body)
		if !validHistoryPage(page, source, testMessage, testAccount, testChat) {
			t.Fatalf("%d retained sources of exactly 1 MiB rejected", count)
		}
	}
	page, source := historyBoundaryPage(messageHistoryPageSize, body)
	page.Items[0].OriginalText += "a"
	if validHistoryPage(page, source, testMessage, testAccount, testChat) {
		t.Fatal("source exceeding the engine's 1 MiB body bound accepted")
	}
	page.Items[0].OriginalText = body
	// Metadata cannot use unused text capacity. UUIDs and parent references also
	// consume this budget, independently of the retained source bytes.
	uuidBytes := messageHistoryPageSize * 3 * len(testMessage)
	page.Items[0].Sender.DisplayName = strings.Repeat("n", messageHistoryMetadataBytes-uuidBytes)
	if !validHistoryPage(page, source, testMessage, testAccount, testChat) {
		t.Fatal("metadata at its independent bound rejected")
	}
	page.Items[0].Sender.DisplayName += "n"
	if validHistoryPage(page, source, testMessage, testAccount, testChat) {
		t.Fatal("metadata over its independent bound accepted")
	}
	page.Items[0].Sender.DisplayName = ""
	page.Items[0].OriginalText = string([]byte{0xff})
	if validHistoryPage(page, source, testMessage, testAccount, testChat) {
		t.Fatal("invalid UTF-8 retained source accepted")
	}
	page, source = historyBoundaryPage(messageHistoryPageSize+1, "a")
	if validHistoryPage(page, source, testMessage, testAccount, testChat) {
		t.Fatal("oversized history page accepted")
	}
}

func TestMessageHistoryHandlerRendersFullPageOfMaximumRetainedSources(t *testing.T) {
	page, source := historyBoundaryPage(messageHistoryPageSize, strings.Repeat("a", messageHistoryBodyBytes))
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("effectful history read: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.24.0", "eventCursor": testEngine + ":0"})
		case "/api/v1/messages/" + testMessage:
			_ = json.NewEncoder(w).Encode(source)
		case "/api/v1/messages/" + testMessage + "/edit-history":
			if r.URL.Query().Get("limit") != "20" {
				t.Errorf("history page bound changed: %s", r.URL.Query().Get("limit"))
			}
			_ = json.NewEncoder(w).Encode(page)
		default:
			t.Errorf("history performed unrelated read: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "messageId": {testMessage}, "lang": {"en"}}
	r := httptest.NewRequest(http.MethodGet, "/ui/message-history?"+q.Encode(), nil)
	r.Header.Set("X-Oie-Fragment", "message-history")
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("full valid page became unavailable: status %d, %s", w.Code, w.Body.String())
	}
	if got := strings.Count(w.Body.String(), "data-history-revision="); got != messageHistoryPageSize {
		t.Fatalf("rendered %d of %d retained versions", got, messageHistoryPageSize)
	}
}
