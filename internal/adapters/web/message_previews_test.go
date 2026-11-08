package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestPreviewBatchUsesOnlyVisibleScopedMetadata(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/conversations/"+testChat+"/previews" || len(r.URL.Query()["messageId"]) != 2 {
			t.Errorf("unexpected preview request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		items := []engineapi.MessagePreview{
			{Id: testRevision, AccountId: testAccount, ConversationId: testChat, MessageId: testMessage, Url: "https://example.test/a"},
			{Id: testRevision, AccountId: testAccount, ConversationId: testChat, MessageId: testEngine, Url: "https://example.test/not-visible"},
			{Id: testRevision, AccountId: testEngine, ConversationId: testChat, MessageId: testMessage, Url: "https://example.test/wrong-account"},
			{Id: testRevision, AccountId: testAccount, ConversationId: testChat, MessageId: testMessage, Url: "javascript:alert(1)"},
		}
		_ = json.NewEncoder(w).Encode(engineapi.PreviewPage{Items: items})
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	v := ui.baseView(httptest.NewRequest("GET", "/", nil))
	v.Selected = &engineapi.Conversation{Id: testChat, AccountId: testAccount}
	v.Messages = []messageView{{Message: engineapi.Message{Id: testMessage}}, {Message: engineapi.Message{Id: testRevision}}}
	ui.loadMessagePreviews(context.Background(), &v)
	if calls != 1 || len(v.MessagePreviews[testMessage]) != 1 || len(v.MessagePreviews) != 1 {
		t.Fatal("preview batch leaked unselected metadata or used N+1 requests")
	}
}

func TestPreviewDownloadsRequireCSRFAndCurrentEngine(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/engine" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.5.0", "eventCursor": testEngine + ":0"})
			return
		}
		if r.URL.Path != "/api/v1/previews/"+testRevision+"/download" || r.Method != "POST" {
			t.Fatalf("unexpected API request %s %s", r.Method, r.URL)
		}
		calls++
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(engineapi.MessagePreview{Id: testRevision, AccountId: testAccount, ConversationId: testChat, MessageId: testMessage, Url: "https://example.test/photo.png", Kind: "image", DownloadState: "queued", State: "ready", UpdatedAt: time.Now()})
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	seed := httptest.NewRecorder()
	token := csrfToken(seed, httptest.NewRequest("GET", "/", nil))
	cookie := seed.Result().Cookies()[0]
	post := func(csrf, engine string) *httptest.ResponseRecorder {
		values := url.Values{"csrf": {csrf}, "engineId": {engine}, "previewId": {testRevision}}
		r := httptest.NewRequest("POST", "/ui/previews/download", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	if w := post("forged", testEngine); w.Code != 403 || calls != 0 {
		t.Fatal("forged CSRF reached image ensure")
	}
	if w := post(token, testAccount); w.Code != 409 || calls != 0 {
		t.Fatal("wrong engine reached image ensure")
	}
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest("GET", "/ui/previews/download", nil))
	if w.Code != 405 || calls != 0 {
		t.Fatal("GET had download effects")
	}
	w = post(token, testEngine)
	if w.Code != 202 || calls != 1 || !strings.Contains(w.Body.String(), `"downloadState":"queued"`) {
		t.Fatalf("canonical ensure result was lost: %d %s", w.Code, w.Body.String())
	}
}

func TestMediaProxyVerifiesLocalBytesAndNeverRedirects(t *testing.T) {
	data, hash := avatarPNG(t, 1)
	for _, tc := range []struct {
		name, contentType, location string
		status, want                int
		body                        []byte
	}{
		{name: "retained PNG", contentType: "image/png", status: 200, want: 200, body: data},
		{name: "provider HTML", contentType: "text/html", status: 200, want: 502, body: []byte("<script>alert(1)</script>")},
		{name: "wrong MIME", contentType: "image/jpeg", status: 200, want: 502, body: data},
		{name: "wrong digest", contentType: "image/png", status: 200, want: 502, body: []byte("not image data")},
		{name: "external redirect", location: "https://images.example.test/remote", status: 302, want: 503},
		{name: "uncached original", status: 404, want: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/media/"+hash {
					t.Errorf("unexpected cached media path %s", r.URL)
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Location", tc.location)
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			ui.ServeHTTP(w, httptest.NewRequest("GET", "/ui/media/"+hash, nil))
			if w.Code != tc.want || w.Header().Get("Location") != "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("unsafe cached-media result %d %v", w.Code, w.Header())
			}
			if w.Code == 200 && w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
				t.Fatal("media cache policy changed")
			}
		})
	}
}
