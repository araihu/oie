package web

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func omemoAttachmentHref(message string, index int) string {
	return "/ui/omemo-attachments/" + message + "/" + strconv.Itoa(index)
}

func (s *Server) omemoAttachment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id, rawIndex, found := strings.Cut(strings.TrimPrefix(r.URL.Path, "/ui/omemo-attachments/"), "/")
	index, err := strconv.Atoi(rawIndex)
	if !found || !validOperationKey(id) || err != nil || index < 0 || index >= 16 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := s.archiveClient.DownloadOmemoAttachment(ctx, id, index)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		w.WriteHeader(res.StatusCode)
		return
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Content-Security-Policy", "X-Content-Type-Options"} {
		w.Header().Set(name, res.Header.Get(name))
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	control := http.NewResponseController(w)
	_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
	defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, res.Body)
}
