package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

const videoProxyPath = "/ui/shared-content/" + testRevision + "/content"
const videoEnginePath = "/api/v1/shared-content/" + testRevision + "/content"

func TestVideoProxyForwardsRangesAndOnlySelectedHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, rangeHeader, ifRange, contentRange, body string
		status                                         int
	}{
		{name: "full", status: 200, body: "0123456789"},
		{name: "partial", status: 206, rangeHeader: "bytes=3-6", ifRange: `"fixture-v1"`, contentRange: "bytes 3-6/10", body: "3456"},
		{name: "unsatisfiable", status: 416, rangeHeader: "bytes=100-", contentRange: "bytes */10", body: `{"type":"urn:oie:problem:invalid_range","title":"Invalid range","status":416,"code":"invalid_range"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != videoEnginePath || r.URL.RawQuery != "" || r.Header.Get("Range") != tc.rangeHeader || r.Header.Get("If-Range") != tc.ifRange {
					t.Errorf("wrong engine stream request %s %s Range=%q If-Range=%q", r.Method, r.URL, r.Header.Get("Range"), r.Header.Get("If-Range"))
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("browser credentials leaked to the engine stream request")
				}
				w.Header().Set("Content-Type", "video/webm")
				if tc.status == 416 {
					w.Header().Set("Content-Type", "application/problem+json")
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("ETag", `"fixture-v1"`)
				w.Header().Set("Last-Modified", "Sun, 08 Nov 2026 12:00:00 GMT")
				w.Header().Set("Content-Disposition", `inline; filename="fixture.webm"`)
				if tc.contentRange != "" {
					w.Header().Set("Content-Range", tc.contentRange)
				}
				w.Header().Set("Set-Cookie", "upstream=private")
				w.Header().Set("Location", "https://private-provider.invalid/video")
				w.Header().Set("X-Provider-Secret", "not-for-the-browser")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, videoProxyPath, nil)
			r.Header.Set("Range", tc.rangeHeader)
			r.Header.Set("If-Range", tc.ifRange)
			r.Header.Set("Cookie", "browser=private")
			r.Header.Set("Authorization", "Bearer browser-only")
			w := httptest.NewRecorder()
			ui.ServeHTTP(w, r)
			bodyValid := w.Body.String() == tc.body
			if tc.status == 416 {
				bodyValid = json.Valid(w.Body.Bytes())
			}
			if w.Code != tc.status || !bodyValid || calls.Load() != 1 {
				t.Fatalf("stream result status=%d body=%q calls=%d", w.Code, w.Body.String(), calls.Load())
			}
			if w.Header().Get("Content-Range") != tc.contentRange || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("range or browser policy headers lost", w.Header())
			}
			for _, name := range []string{"Set-Cookie", "Location", "X-Provider-Secret"} {
				if w.Header().Get(name) != "" {
					t.Fatalf("forwarded private upstream header %s", name)
				}
			}
			if tc.status != 416 {
				if w.Header().Get("Content-Length") != strconv.Itoa(len(tc.body)) || w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("ETag") != `"fixture-v1"` || w.Header().Get("Last-Modified") == "" {
					t.Fatal("playback headers lost", w.Header())
				}
				kind, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
				if err != nil || kind != "inline" || params["filename"] != "fixture.webm" {
					t.Fatal("video disposition lost", w.Header())
				}
			}
		})
	}
}

func TestVideoProxyAcceptsOnlyCurrentOptionalEngineGuard(t *testing.T) {
	var snapshots, streams atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/engine":
			snapshots.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.14.0", "eventCursor": testEngine + ":0"})
		case videoEnginePath:
			streams.Add(1)
			if r.URL.RawQuery != "" {
				t.Error("browser query data reached the engine", r.URL)
			}
			w.Header().Set("Content-Type", "video/webm")
			_, _ = io.WriteString(w, "video bytes")
		default:
			t.Errorf("unexpected engine path %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query                    string
		status, snapshot, stream int
	}{
		{"?engineId=" + testEngine, 200, 1, 1},
		{"?engineId=" + testAccount, 409, 1, 0},
		{"?engineId=invalid", 400, 0, 0},
		{"?engineId=" + testEngine + "&engineId=" + testAccount, 400, 0, 0},
		{"?url=https%3A%2F%2Fprivate-provider.invalid%2Fvideo", 400, 0, 0},
	} {
		beforeSnapshots, beforeStreams := snapshots.Load(), streams.Load()
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest(http.MethodGet, videoProxyPath+tc.query, nil))
		if w.Code != tc.status || snapshots.Load()-beforeSnapshots != int32(tc.snapshot) || streams.Load()-beforeStreams != int32(tc.stream) {
			t.Fatalf("wrong engine guard behavior %s status=%d snapshots=%d streams=%d", tc.query, w.Code, snapshots.Load()-beforeSnapshots, streams.Load()-beforeStreams)
		}
	}
}

func TestVideoProxyDoesNotFollowRedirectsOrServeNonVideo(t *testing.T) {
	var outside atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		outside.Add(1)
		w.Header().Set("Content-Type", "video/webm")
		_, _ = io.WriteString(w, "should never be retrieved")
	}))
	defer external.Close()
	for _, tc := range []struct {
		name, mime string
		status     int
	}{
		{name: "provider redirect", status: 302, mime: "video/webm"},
		{name: "HTML response", status: 200, mime: "text/html"},
		{name: "JSON response", status: 200, mime: "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.mime)
				w.Header().Set("Location", external.URL)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "untrusted content")
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			ui.ServeHTTP(w, httptest.NewRequest(http.MethodGet, videoProxyPath, nil))
			if w.Code != 502 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "untrusted content") || outside.Load() != 0 {
				t.Fatalf("unsafe stream result %d %v external=%d", w.Code, w.Header(), outside.Load())
			}
		})
	}
}

func TestVideoProxyRejectsMethodsAndInvalidIDsBeforeEngine(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodHead, videoProxyPath, 405},
		{http.MethodPost, videoProxyPath, 405},
		{http.MethodGet, "/ui/shared-content/not-a-uuid/content", 404},
		{http.MethodGet, videoProxyPath + "/extra", 404},
	} {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || calls.Load() != 0 {
			t.Fatalf("invalid request reached engine: %s %s status=%d calls=%d", tc.method, tc.path, w.Code, calls.Load())
		}
		if tc.method == http.MethodHead && (w.Body.Len() != 0 || w.Header().Get("Allow") != "GET") {
			t.Fatal("HEAD must advertise GET without a response body", w.Header())
		}
	}
}

func TestVideoProxyStreamsBeforeCompletionAndCancelsEngine(t *testing.T) {
	prefix := bytes.Repeat([]byte("video-prefix"), 8192)
	release, canceled := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/webm")
		w.Header().Set("Content-Length", strconv.Itoa(len(prefix)*2))
		_, _ = w.Write(prefix)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
			_, _ = w.Write(prefix)
		}
	}))
	defer backend.Close()
	defer close(release)
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(ui)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+videoProxyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("video proxy buffered the blocked upstream response", err)
	}
	chunk := make([]byte, 1024)
	_, err = io.ReadFull(res.Body, chunk)
	_ = res.Body.Close()
	if err != nil || !bytes.Equal(chunk, prefix[:len(chunk)]) || res.StatusCode != 200 {
		t.Fatal("video bytes were not delivered incrementally", err, res.StatusCode)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation left the engine stream running")
	}
}

func TestVideoProxyBoundsProblemResponses(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(503)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 503, "code": "unavailable", "detail": strings.Repeat("upstream-detail ", 2048)})
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest(http.MethodGet, videoProxyPath, nil))
	if w.Code != 502 || w.Body.Len() != 0 {
		t.Fatalf("invalid upstream Problem was not rejected without leaking body bytes: %d, %d bytes", w.Code, w.Body.Len())
	}
}

func TestVisibleVideoReferencesRequireExactScopeAndSourceSnapshot(t *testing.T) {
	const sourceURL = "https://files.example.test/video.webm"
	for _, tc := range []struct {
		name   string
		mutate func(*engineapi.SharedContentItem, *engineapi.Message)
		want   int
	}{
		{name: "visible source", want: 1},
		{name: "foreign account", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) { item.Message.AccountId = testEngine }},
		{name: "foreign conversation", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) {
			item.Message.ConversationId = testEngine
		}},
		{name: "unselected message", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) { item.Message.Id = testEngine }},
		{name: "non-video reference", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) { item.Kind = "image" }},
		{name: "unsafe URL", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) { item.Url = "javascript:alert(1)" }},
		{name: "stale revision", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) { item.Message.RevisionId = testEngine }},
		{name: "stale body", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) {
			item.Message.OriginalText = "Old source " + sourceURL
		}},
		{name: "unrelated URL", mutate: func(item *engineapi.SharedContentItem, _ *engineapi.Message) {
			item.Url = "https://files.example.test/different.webm"
		}},
		{name: "URL prefix is not a source token", mutate: func(item *engineapi.SharedContentItem, message *engineapi.Message) {
			message.OriginalText = sourceURL + ".bad"
			item.Message.OriginalText = message.OriginalText
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := engineapi.Message{Id: testMessage, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat, OriginalText: "Watch " + sourceURL}
			item := engineapi.SharedContentItem{Id: testRevision, RevisionId: testRevision, Kind: "video", Url: sourceURL, Message: message}
			if tc.mutate != nil {
				tc.mutate(&item, &message)
			}
			calls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				if r.URL.Path != "/api/v1/shared-content" || q.Get("type") != "video" || q.Get("accountId") != testAccount || q.Get("conversationId") != testChat || len(q["messageId"]) != 1 || q.Get("messageId") != testMessage {
					t.Error("video metadata escaped selected message scope", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []engineapi.SharedContentItem{item}, "indexing": map[string]any{"state": "complete", "indexedMessages": 1, "totalMessages": 1}})
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			v := ui.baseView(httptest.NewRequest(http.MethodGet, "/", nil))
			v.Selected = &engineapi.Conversation{Id: testChat, AccountId: testAccount}
			v.Messages = []messageView{{Message: message}}
			ui.loadMessageVideos(t.Context(), &v)
			if calls != 1 || len(v.MessageVideos[testMessage]) != tc.want {
				t.Fatal("unbound video metadata reached the visible message", calls, v.MessageVideos)
			}
		})
	}
}
