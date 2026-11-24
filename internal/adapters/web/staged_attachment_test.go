package web

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestStagingStopsReadingStalledHTTPBody(t *testing.T) {
	finished := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const idle = 100 * time.Millisecond
		timer := time.AfterFunc(idle, func() {})
		defer timer.Stop()
		_, err := io.Copy(io.Discard, stageUploadReader{r.Body, http.NewResponseController(w), timer, idle})
		finished <- err
	}))
	defer server.Close()
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	go func() {
		res, err := server.Client().Post(server.URL, "application/octet-stream", reader)
		if err == nil {
			_ = res.Body.Close()
		}
	}()
	if _, err := writer.Write([]byte("begin")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("stalled upload read returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stalled upload was still reading")
	}
}

func TestGenericStagedVideoTypes(t *testing.T) {
	for suffix, want := range map[string]string{"m4v": "video/mp4", "MOV": "video/quicktime", "mkv": "video/x-matroska", "avi": "video/x-msvideo", "html": ""} {
		header := http.Header{}
		header.Set("X-Oie-Attachment-Type", "application/octet-stream")
		header.Set("Content-Disposition", "attachment; filename=clip."+suffix)
		if got := stagedVideoType(header); got != want {
			t.Errorf("%s = %q, want %q", suffix, got, want)
		}
	}
}
