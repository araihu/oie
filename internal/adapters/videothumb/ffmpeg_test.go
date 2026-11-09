package videothumb

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
)

type measuredReader struct {
	io.Reader
	read *int64
}

func (r measuredReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	*r.read += int64(n)
	return n, err
}

func TestRealFFmpegExtractsCachedSizeFrameFromLocalFixture(t *testing.T) {
	decoder := New("ffmpeg")
	if !decoder.Available() {
		t.Skip("FFmpeg binary not installed")
	}
	input, err := os.ReadFile("../../../tests/browser/fixtures/tiny-video.webm")
	if err != nil {
		t.Fatal(err)
	}
	var read int64
	calls := 0
	output, err := decoder.Thumbnail(t.Context(), func(_ context.Context, request application.MediaReadRequest) (application.MediaRead, error) {
		calls++
		start, end, ok := request.Range.Resolve(int64(len(input)))
		if !ok {
			t.Fatal("invalid bounded request", request)
		}
		return application.MediaRead{Body: io.NopCloser(measuredReader{Reader: bytes.NewReader(input[start : end+1]), read: &read}), MediaType: "video/webm", Partial: true, Offset: start, Length: end - start + 1, Total: int64(len(input)), ETag: `"fixture"`}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(output))
	if err != nil || format != "png" || config.Width != 64 || config.Height != 48 {
		t.Fatal(config, format, err)
	}
	if calls != 1 || read != int64(len(input)) || len(output) > OutputLimit {
		t.Fatal("unbounded source/frame", calls, read, len(output))
	}
}

func TestThumbnailSamplesHaveCombinedByteBudgetAndValidator(t *testing.T) {
	for _, variant := range []string{"range", "ignored suffix", "no validator", "changed validator"} {
		t.Run(variant, func(t *testing.T) {
			decoder := New("false")
			if !decoder.Available() {
				t.Skip("POSIX false command unavailable")
			}
			total := int64(20 << 20)
			var read int64
			calls := 0
			_, err := decoder.Thumbnail(t.Context(), func(_ context.Context, request application.MediaReadRequest) (application.MediaRead, error) {
				calls++
				start, end, ok := request.Range.Resolve(total)
				if !ok {
					t.Fatal(request)
				}
				etag := `"source"`
				if variant == "no validator" {
					etag = ""
				}
				if calls == 2 && variant == "changed validator" {
					etag = `"changed"`
				}
				if calls == 2 && request.Condition.ETag != `"source"` {
					t.Fatal("missing If-Range identity", request.Condition)
				}
				partial := calls != 2 || variant != "ignored suffix"
				return application.MediaRead{Body: io.NopCloser(measuredReader{Reader: io.LimitReader(zeroReader{}, end-start+1), read: &read}), MediaType: "video/mp4", Partial: partial, Offset: start, Length: end - start + 1, Total: total, ETag: etag}, nil
			})
			if err == nil {
				t.Fatal("invalid sample decoded")
			}
			expectedCalls := 2
			if variant == "no validator" {
				expectedCalls = 1
			}
			expectedRead := PrefixLimit
			if variant == "range" {
				expectedRead += SuffixLimit
			}
			if calls != expectedCalls || read != expectedRead {
				t.Fatal("sample budget", calls, read, expectedCalls, expectedRead)
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestMissingDecoderDoesNotReadProvider(t *testing.T) {
	decoder := New(filepath.Join(t.TempDir(), "missing"))
	if decoder.Available() {
		t.Fatal("nonexistent decoder available")
	}
	_, err := decoder.Thumbnail(t.Context(), func(context.Context, application.MediaReadRequest) (application.MediaRead, error) {
		t.Fatal("provider contacted")
		return application.MediaRead{}, nil
	})
	if err == nil {
		t.Fatal("missing capability ignored")
	}
}

func TestCanceledExtractionRemovesItsTemporarySample(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	command := filepath.Join(directory, "decoder")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexec sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	decoder := New(command)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := decoder.Thumbnail(ctx, func(context.Context, application.MediaReadRequest) (application.MediaRead, error) {
		return application.MediaRead{Body: io.NopCloser(bytes.NewReader(make([]byte, 64))), MediaType: "video/webm", Total: 64, Length: 64}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	samples, err := filepath.Glob(filepath.Join(directory, "oie-video-sample-*"))
	if err != nil || len(samples) != 0 {
		t.Fatal("temporary source retained", samples, err)
	}
}
