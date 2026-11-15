package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// The upstream engine emits an SSE heartbeat every 15 seconds when idle.
const engineEventIdleTimeout = 35 * time.Second

// events preserves structured engine event IDs. It has no total client timeout;
// upstream response headers, downstream writes, and cancellation remain bounded.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("cursor")
	}
	// A reconnect still feeds replayed events to the UI, but only events
	// after this snapshot can alert. The marker has no SSE ID of its own.
	snapshot, snapshotErr := s.client.GetEngineWithResponse(r.Context())
	liveCursor := ""
	if snapshotErr == nil && snapshot.JSON200 != nil {
		liveCursor = snapshot.JSON200.EventCursor
	}
	if cursor == "" {
		if liveCursor == "" {
			w.WriteHeader(503)
			return
		}
		cursor = liveCursor
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	response, err := s.streamClient.StreamEvents(ctx, &engineapi.StreamEventsParams{LastEventID: &cursor})
	if err != nil {
		w.WriteHeader(503)
		return
	}
	defer func() { _ = response.Body.Close() }()
	write := func(text string, flush bool) bool {
		control := http.NewResponseController(w)
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
		if _, err := fmt.Fprint(w, text); err != nil {
			return false
		}
		return !flush || control.Flush() == nil
	}
	if response.StatusCode == http.StatusConflict {
		snapshot, err := s.client.GetEngineWithResponse(r.Context())
		if err != nil || snapshot.JSON200 == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		setEventStreamHeaders(w)
		data, _ := json.Marshal(map[string]string{"engineId": snapshot.JSON200.Id, "cursor": snapshot.JSON200.EventCursor})
		write(fmt.Sprintf("event: oie.reset\ndata: %s\n\n", data), true)
		return
	}
	if response.StatusCode != http.StatusOK {
		w.WriteHeader(response.StatusCode)
		return
	}
	setEventStreamHeaders(w)
	data, _ := json.Marshal(liveCursor)
	if !write(fmt.Sprintf(": connected\n\nevent: oie.live\ndata: %s\n\n", data), true) {
		return
	}
	idle := time.AfterFunc(engineEventIdleTimeout, cancel)
	defer idle.Stop()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == ": heartbeat" {
			line = "event: oie.heartbeat\ndata: {}"
		}
		if line == "" {
			idle.Reset(engineEventIdleTimeout)
		}
		if !write(line+"\n", line == "") {
			return
		}
	}
}

func setEventStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
}
