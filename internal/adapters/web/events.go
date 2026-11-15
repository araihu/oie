package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

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
	response, err := s.streamClient.StreamEvents(r.Context(), &engineapi.StreamEventsParams{LastEventID: &cursor})
	if err != nil {
		w.WriteHeader(503)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == 409 {
		snapshot, err := s.client.GetEngineWithResponse(r.Context())
		if err != nil || snapshot.JSON200 == nil {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		data, _ := json.Marshal(map[string]string{"engineId": snapshot.JSON200.Id, "cursor": snapshot.JSON200.EventCursor})
		_, _ = fmt.Fprintf(w, "event: oie.reset\ndata: %s\n\n", data)
		_ = http.NewResponseController(w).Flush()
		return
	}
	if response.StatusCode != 200 {
		w.WriteHeader(response.StatusCode)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
	data, _ := json.Marshal(liveCursor)
	if _, err = fmt.Fprintf(w, ": connected\n\nevent: oie.live\ndata: %s\n\n", data); err != nil {
		return
	}
	if control.Flush() != nil {
		return
	}
	defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err = fmt.Fprintln(w, scanner.Text()); err != nil {
			return
		}
		if scanner.Text() == "" {
			if control.Flush() != nil {
				return
			}
		}
	}
}
