package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	_, liveCursor, ok := s.eventSnapshot(r.Context())
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if cursor == "" {
		cursor = liveCursor
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	response, err := s.streamClient.StreamEvents(ctx, &engineapi.StreamEventsParams{LastEventID: &cursor})
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
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
		engineID, cursor, ok := s.eventSnapshot(r.Context())
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		setEventStreamHeaders(w)
		data, _ := json.Marshal(map[string]string{"engineId": engineID, "cursor": cursor})
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

// A missing or inconsistent snapshot cannot establish the boundary between
// replayed events and events that can produce notifications.
func (s *Server) eventSnapshot(ctx context.Context) (engineID, cursor string, ok bool) {
	snapshot, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || snapshot.JSON200 == nil {
		return "", "", false
	}
	engineID, cursor = snapshot.JSON200.Id, snapshot.JSON200.EventCursor
	id, digits, valid := strings.Cut(cursor, ":")
	seq, err := strconv.ParseInt(digits, 10, 64)
	return engineID, cursor, valid && id != "" && id == engineID && err == nil && seq >= 0 && strconv.FormatInt(seq, 10) == digits
}

func setEventStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
}
