package previewfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
)

func mediaCode(err error) string {
	var f application.MediaFailure
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}
func TestMediaReaderValidatesRangesAndProviderFallback(t *testing.T) {
	data := []byte("0123456789abcdefghijklmnopqrstuv")
	var seen atomic.Int32
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("compressed range request")
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("ETag", `"fixture-v1"`)
		if r.URL.Path == "/ignore" {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		http.ServeContent(w, r, "clip.mp4", time.Unix(1000, 0), bytes.NewReader(data))
	})
	for _, tt := range []struct {
		name      string
		r         *application.ByteRange
		condition application.MediaCondition
		partial   bool
		expected  string
		path      string
	}{
		{name: "full", expected: string(data)},
		{name: "bounded", r: &application.ByteRange{Start: 3, End: 7}, partial: true, expected: "34567"},
		{name: "open", r: &application.ByteRange{Start: 25, End: -1}, partial: true, expected: string(data[25:])},
		{name: "suffix", r: &application.ByteRange{Start: 4, End: -1, Suffix: true}, partial: true, expected: string(data[len(data)-4:])},
		{name: "if-range-match", r: &application.ByteRange{Start: 3, End: 7}, condition: application.MediaCondition{ETag: `"fixture-v1"`}, partial: true, expected: "34567"},
		{name: "if-range-changed", r: &application.ByteRange{Start: 3, End: 7}, condition: application.MediaCondition{ETag: `"other"`}, expected: string(data)},
		{name: "range-ignored", r: &application.ByteRange{Start: 3, End: 7}, expected: string(data), path: "/ignore"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := f.Open(t.Context(), "http://public.example"+tt.path, application.MediaReadRequest{Range: tt.r, Condition: tt.condition})
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(result.Body)
			_ = result.Body.Close()
			if err != nil || string(body) != tt.expected || result.Partial != tt.partial || result.Total != int64(len(data)) {
				t.Fatal(string(body), result, err)
			}
			if len(f.mediaActive) != 0 {
				t.Fatal("reader slot leaked")
			}
		})
	}
	_, err := f.Open(t.Context(), "http://public.example/range", application.MediaReadRequest{Range: &application.ByteRange{Start: 999, End: -1}})
	var failure application.MediaFailure
	if !errors.As(err, &failure) || failure.Code != "range_not_satisfiable" || failure.Total != int64(len(data)) {
		t.Fatal(err)
	}
	before := seen.Load()
	if _, err = f.Open(t.Context(), "http://internal.example/private", application.MediaReadRequest{}); mediaCode(err) != "media_blocked" || seen.Load() != before {
		t.Fatal("private destination reached provider", err)
	}
}

func TestMediaReaderRejectsInvalidResponseWithoutConsumingOriginal(t *testing.T) {
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		switch r.URL.Path {
		case "/large":
			w.Header().Set("Content-Length", fmt.Sprint(application.MaxMediaSource+1))
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Length", "10")
		case "/encoded":
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", "10")
		case "/bad-range":
			w.Header().Set("Content-Range", "bytes 8-10/9")
			w.WriteHeader(206)
		case "/wrong-range":
			w.Header().Set("Content-Range", "bytes 5-7/20")
			w.Header().Set("Content-Length", "3")
			w.WriteHeader(206)
		case "/unknown":
			w.(http.Flusher).Flush()
		case "/redirect":
			http.Redirect(w, r, "http://internal.example/private", http.StatusFound)
		}
	})
	for _, tt := range []struct{ path, code string }{{"/large", "media_too_large"}, {"/html", "media_unsupported"}, {"/encoded", "media_fetch_failed"}, {"/bad-range", "media_fetch_failed"}, {"/wrong-range", "media_fetch_failed"}, {"/unknown", "media_fetch_failed"}, {"/redirect", "media_blocked"}} {
		t.Run(tt.path, func(t *testing.T) {
			_, err := f.Open(t.Context(), "http://public.example"+tt.path, application.MediaReadRequest{Range: &application.ByteRange{Start: 0, End: 2}})
			if mediaCode(err) != tt.code {
				t.Fatal(err)
			}
			if len(f.mediaActive) != 0 {
				t.Fatal("failed read retained slot")
			}
		})
	}
}

func TestMediaReaderCancelConcurrencyAndPrivacy(t *testing.T) {
	cancelled := make(chan struct{}, 8)
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			t.Error("provider credential or referrer leaked")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://public.example/stream", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "video/webm")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "x")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		cancelled <- struct{}{}
	})
	streams := []application.MediaRead{}
	for i := range 4 {
		value, err := f.Open(t.Context(), fmt.Sprintf("http://public.example:%d/stream", 10000+i), application.MediaReadRequest{})
		if err != nil {
			t.Fatal(err)
		}
		streams = append(streams, value)
	}
	if _, err := f.Open(t.Context(), "http://public.example/stream", application.MediaReadRequest{}); mediaCode(err) != "media_busy" {
		t.Fatal("unbounded concurrent reads", err)
	}
	for _, stream := range streams {
		_ = stream.Body.Close()
		_ = stream.Body.Close()
	}
	if len(f.mediaActive) != 0 {
		t.Fatal("concurrency slots not released")
	}
	ctx, cancel := context.WithCancel(t.Context())
	result, err := f.Open(ctx, "http://public.example/redirect?private-token=never-a-referrer", application.MediaReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = io.ReadAll(result.Body)
	_ = result.Body.Close()
	if err == nil {
		t.Fatal("cancelled stream completed")
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for range 5 {
		select {
		case <-cancelled:
		case <-deadline.C:
			t.Fatal("upstream requests not cancelled")
		}
	}
}
func TestMediaReaderTruncationHasNoSyntheticContent(t *testing.T) {
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/ogg")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	})
	result, err := f.Open(t.Context(), "http://public.example/clip", application.MediaReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(result.Body)
	_ = result.Body.Close()
	if err == nil || string(body) != "short" || strings.Contains(string(body), "problem") {
		t.Fatal("truncated original changed", string(body), err)
	}
}
