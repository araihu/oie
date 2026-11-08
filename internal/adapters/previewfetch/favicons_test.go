package previewfetch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSiteIconFallbackRemainsBoundedAndServerSide(t *testing.T) {
	icon := []byte{137, 80, 78, 71, 13, 10, 26, 10, 1, 2, 3}
	var icons atomic.Int32
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<title>Article</title><link rel="icon" href="/icon.png">`)
		case "/icon.png":
			icons.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(icon)
		case "/unsafe":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<title>Safe text</title><link rel="icon" href="http://internal.example/private">`)
		case "/oversized":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<title>Safe text</title><link rel="icon" href="/large.png">`)
		case "/large.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = fmt.Fprint(w, strings.Repeat("x", (64<<10)+1))
		}
	})
	metadata, err := f.Link(t.Context(), "http://public.example/page")
	if err != nil || metadata.Title != "Article" || !bytes.Equal(metadata.Favicon, icon) || len(metadata.Image) != 0 {
		t.Fatal(metadata, err)
	}
	metadata.Favicon[0] = 0
	cached, err := f.Link(t.Context(), "http://public.example/page")
	if err != nil || cached.Favicon[0] != 137 || icons.Load() != 1 {
		t.Fatal("cache alias or repeated icon request")
	}
	f.InvalidateLink("http://public.example/page")
	if _, err = f.Link(t.Context(), "http://public.example/page"); err != nil || icons.Load() != 2 {
		t.Fatal("explicit refresh reused metadata cache", err)
	}
	for _, page := range []string{"unsafe", "oversized"} {
		metadata, err = f.Link(t.Context(), "http://public.example/"+page)
		if err != nil || metadata.Title != "Safe text" || len(metadata.Favicon) != 0 {
			t.Fatal("icon failure destroyed usable metadata", metadata, err)
		}
	}
}

func TestPNGInsideICOIsExtractedWithoutServingICO(t *testing.T) {
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 1, 2, 3}
	ico := make([]byte, 22+len(png))
	binary.LittleEndian.PutUint16(ico[2:4], 1)
	binary.LittleEndian.PutUint16(ico[4:6], 1)
	binary.LittleEndian.PutUint32(ico[14:18], uint32(len(png)))
	binary.LittleEndian.PutUint32(ico[18:22], 22)
	copy(ico[22:], png)
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(ico)
	})
	if got := f.siteIcon(t.Context(), "http://public.example/icon.ico"); !bytes.Equal(got, png) {
		t.Fatal("embedded PNG not extracted")
	}
}

func TestInvalidationDoesNotCacheAnOlderInflightResponse(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path != "/page" {
			return
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			_, _ = fmt.Fprint(w, "<title>Old</title>")
			return
		}
		_, _ = fmt.Fprint(w, "<title>Fresh</title>")
	})
	done := make(chan error, 1)
	go func() { _, err := f.Link(t.Context(), "http://public.example/page"); done <- err }()
	<-entered
	f.InvalidateLink("http://public.example/page")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	metadata, err := f.Link(t.Context(), "http://public.example/page")
	if err != nil || metadata.Title != "Fresh" || calls.Load() != 2 {
		t.Fatal("stale in-flight response repopulated cache", metadata, err)
	}
}
