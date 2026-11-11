package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEngineEditorAppliesToQueriesEventsAndMedia(t *testing.T) {
	const capability = "launch-scoped-secret"
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Oie-Host-Capability") != capability {
			http.Error(w, "missing capability", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ": ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/api/v1/shared-content/item/content":
			w.Header().Set("Content-Range", "bytes 0-1/4")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "ab")
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer engine.Close()
	s, err := New(engine.URL, WithEngineRequestEditor(func(_ context.Context, r *http.Request) error {
		r.Header.Set("X-Oie-Host-Capability", capability)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	response, err := s.client.GetEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	_ = response.Body.Close()
	stream, err := s.streamClient.StreamEvents(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != http.StatusOK {
		t.Fatal(stream.StatusCode)
	}
	prefix := make([]byte, 9)
	if _, err := io.ReadFull(stream.Body, prefix); err != nil || string(prefix) != ": ready\n\n" {
		t.Fatalf("stream was buffered or rejected: %q %v", prefix, err)
	}
	cancel()
	_ = stream.Body.Close()
	media, err := s.streamClient.GetSharedContentMedia(t.Context(), "item", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = media.Body.Close() }()
	if media.StatusCode != http.StatusPartialContent {
		t.Fatal(media.StatusCode)
	}
	if media.Header.Get("X-Oie-Host-Capability") != "" {
		t.Fatal("capability exposed in response")
	}
}
