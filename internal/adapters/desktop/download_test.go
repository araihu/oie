//go:build !server

package desktop

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stagedDownloadAddress = "/ui/scheduling/file?engineId=019a5df0-0000-7000-8000-000000000102" +
	"&conversationId=019a5df0-0000-7000-8000-000000000103&attachmentId=019a5df0-0000-7000-8000-000000000104&download=1"

func TestNativeDownloadValidatesSources(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, address := range []string{
		stagedDownloadAddress,
		"/ui/media/" + hash + "?download=photo.jpg",
		"/ui/shared-content/019a5df0-0000-7000-8000-000000000101/content?engineId=019a5df0-0000-7000-8000-000000000102&download=1",
	} {
		if _, err := nativeDownloadURL(address); err != nil {
			t.Fatal(address, err)
		}
	}
	for _, address := range []string{
		strings.Replace(stagedDownloadAddress, "&download=1", "", 1),
		strings.Replace(stagedDownloadAddress, "download=1", "download=0", 1),
		strings.Replace(stagedDownloadAddress, "conversationId=019a5df0-0000-7000-8000-000000000103", "conversationId=bad", 1),
		stagedDownloadAddress + "&engineId=019a5df0-0000-7000-8000-000000000102",
		stagedDownloadAddress + "&extra=1",
		"https://outside.invalid/file", "//outside.invalid/ui/media/" + hash + "?download=image", "/api/v1/engine", "/ui/media/../private?download=x",
		"/ui/media/" + hash, "/ui/media/" + hash + "?download=x&token=secret", "/ui/media/" + hash + "?download=x&download=y", "/ui/media/" + hash + "?download=x#fragment",
		"/ui/shared-content/not-an-id/content?download=1", "/ui/shared-content/019a5df0-0000-7000-8000-000000000101/content?download=1",
	} {
		if _, err := nativeDownloadURL(address); err == nil {
			t.Fatal("accepted untrusted download", address)
		}
	}
}

func TestNativeDownloadStreamsExactBytesAndKeepsExistingFileOnFailure(t *testing.T) {
	address := "/ui/media/" + strings.Repeat("a", 64) + "?download=photo.jpg"
	dir := t.TempDir()
	target := filepath.Join(dir, "selected.jpg")
	content := strings.Repeat("original bytes", 100000)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RequestURI() != address || r.URL.Scheme != "wails" || r.Host != "localhost" {
			t.Error("did not reuse expected BFF request")
		}
		w.Header().Set("Content-Disposition", `attachment; filename="photo.jpg"`)
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal("native save cannot receive streamed BFF content", err)
		}
		_, _ = w.Write([]byte(content))
	})
	if err := saveNativeDownload(context.Background(), handler, address, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != content {
		t.Fatal("saved original bytes differ", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("saved file is not private", err)
	}
	for _, status := range []int{302, 403, 404, 500} {
		failed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("error page"))
		})
		if err := saveNativeDownload(context.Background(), failed, address, target); err == nil {
			t.Fatal("saved failed HTTP response", status)
		}
		unchanged, _ := os.ReadFile(target)
		if string(unchanged) != content {
			t.Fatal("failed download overwrote existing file")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary download remains", entries, err)
	}
}

func TestNativeDownloadCancellationAndDestinationErrors(t *testing.T) {
	address := "/ui/media/" + strings.Repeat("b", 64) + "?download=file"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; _, _ = w.Write([]byte("file")) })
	target := filepath.Join(t.TempDir(), "result")
	if err := saveNativeDownload(ctx, handler, address, target); !errors.Is(err, context.Canceled) || called {
		t.Fatal(err, called)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled download wrote destination")
	}
	if err := saveNativeDownload(context.Background(), handler, address, filepath.Join(target, "missing", "file")); err == nil {
		t.Fatal("unwritable destination accepted")
	}
}

func TestNativeFilenameCannotEscapeChosenDirectory(t *testing.T) {
	for input, want := range map[string]string{"../../photo.jpg": "photo.jpg", `..\..\photo.jpg`: "photo.jpg", "\x00\n": "download", "..": "download", "": "download"} {
		if got := nativeFilename(input); got != want {
			t.Fatal(input, got, want)
		}
	}
	if got := nativeFilename(strings.Repeat("á", 240) + ".txt"); len(got) > 220 {
		t.Fatal("filename exceeds native limit")
	}
}

func TestNativeSaveActionHandlesSelectionCancelAndErrors(t *testing.T) {
	address := "/ui/media/" + strings.Repeat("c", 64) + "?download=photo.jpg"
	writes := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes++
		w.Header().Set("Content-Disposition", `attachment; filename="photo.jpg"`)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("original"))
	})
	choose := func(name string) (string, error) {
		if name != "photo.jpg" {
			t.Error(name)
		}
		return "", nil
	}
	if saved, err := nativeSaveAction(context.Background(), handler, address, "../photo.jpg", choose); saved || err != nil || writes != 0 {
		t.Fatal(saved, err, writes)
	}
	selected := filepath.Join(t.TempDir(), "chosen.jpg")
	choose = func(string) (string, error) { return selected, nil }
	if saved, err := nativeSaveAction(context.Background(), handler, address, "photo.jpg", choose); !saved || err != nil || writes != 1 {
		t.Fatal(saved, err, writes)
	}
	bytes, err := os.ReadFile(selected)
	if err != nil || string(bytes) != "original" {
		t.Fatal(err, string(bytes))
	}
	choose = func(string) (string, error) { return "", errors.New("dialog failed") }
	if saved, err := nativeSaveAction(context.Background(), handler, address, "photo.jpg", choose); saved || err == nil || writes != 1 {
		t.Fatal(saved, err, writes)
	}
	choose = func(string) (string, error) { t.Error("chooser opened for rejected address"); return "", nil }
	if saved, err := nativeSaveAction(context.Background(), handler, "https://outside.invalid/file", "photo.jpg", choose); saved || err == nil || writes != 1 {
		t.Fatal(saved, err, writes)
	}
}

func TestNativeDownloadRejectsTruncatedAndUnexpectedSuccessResponses(t *testing.T) {
	address := "/ui/media/" + strings.Repeat("d", 64) + "?download=file"
	target := filepath.Join(t.TempDir(), "chosen")
	if err := os.WriteFile(target, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, headers := range []http.Header{
		{"Content-Disposition": {`attachment; filename="file"`}, "Content-Type": {"application/octet-stream"}, "Content-Length": {"10"}},
		{"Content-Type": {"text/html"}},
		{"Content-Disposition": {`inline; filename="error.html"`}, "Content-Type": {"text/html"}},
		{"Content-Disposition": {`attachment; filename="file"`}},
	} {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for key, values := range headers {
				w.Header()[key] = values
			}
			_, _ = w.Write([]byte("123"))
		})
		if err := saveNativeDownload(context.Background(), handler, address, target); err == nil {
			t.Fatal("accepted incomplete or unexpected 200", headers)
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "previous" {
			t.Fatal("incomplete response changed destination", err)
		}
	}
}

func TestNativeStagedDownloadSupportsOlderEngineWithoutFilename(t *testing.T) {
	target := filepath.Join(t.TempDir(), "draft.txt")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != stagedDownloadAddress {
			t.Errorf("unexpected download request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("retained attachment"))
	})
	if err := saveNativeDownload(t.Context(), handler, stagedDownloadAddress, target); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "retained attachment" {
		t.Fatalf("saved attachment = %q, %v", got, err)
	}
}
