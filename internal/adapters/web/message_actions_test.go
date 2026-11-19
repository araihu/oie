package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestMessageMenuReadsMediaConcurrentlyAndPrefersVideo(t *testing.T) {
	const url = "https://files.example.test/clip.webm"
	fixture := &liveFixture{state: "submitted", acceptedBody: url}
	message := fixture.message()
	previewStarted, videoStarted := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/context"):
			_ = json.NewEncoder(w).Encode(engineapi.MessageContext{TargetId: message.Id, Items: []engineapi.Message{message}})
		case strings.HasSuffix(r.URL.Path, "/previews"):
			close(previewStarted)
			select {
			case <-videoStarted:
			case <-r.Context().Done():
				return
			}
			image := engineapi.MessagePreview{Id: testEngine, AccountId: testAccount, ConversationId: testChat, MessageId: message.Id, Kind: "image", Url: url}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []engineapi.MessagePreview{image}})
		case strings.HasSuffix(r.URL.Path, "/shared-content") && r.URL.Query().Get("type") == "video":
			close(videoStarted)
			select {
			case <-previewStarted:
			case <-r.Context().Done():
				return
			}
			video := engineapi.SharedContentItem{Id: testRevision, Kind: "video", Url: url, Message: message}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []engineapi.SharedContentItem{video}})
		default:
			fixture.ServeHTTP(w, r)
		}
	}))
	defer backend.Close()
	s, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/ui/message-menu?engineId="+testEngine+"&accountId="+testAccount+"&conversationId="+testChat+"&messageId="+testMessage, nil)
	w := httptest.NewRecorder()
	s.serveMessageMenu(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "open-video-"+testRevision) || strings.Contains(body, "open-preview-"+testEngine) {
		t.Fatalf("expected the video action alone for the shared URL, got %d %s", w.Code, body)
	}
}
