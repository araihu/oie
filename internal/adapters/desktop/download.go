//go:build !server

package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Native save actions reuse the BFF's download endpoints. No remote URL or
// caller-selected destination path reaches the native service binding.
func nativeDownloadURL(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return nil, errors.New("invalid native download address")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	media := len(parts) == 3 && parts[0] == "ui" && parts[1] == "media" && lowercaseHex(parts[2], 64)
	shared := len(parts) == 4 && parts[0] == "ui" && parts[1] == "shared-content" && parts[3] == "content" && validUUID(parts[2])
	if !media && !shared {
		return nil, errors.New("unsupported native download address")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || query.Get("download") == "" || len(query["download"]) != 1 {
		return nil, errors.New("invalid native download query")
	}
	for key, values := range query {
		if len(values) != 1 || (key != "download" && key != "engineId") {
			return nil, errors.New("unsupported native download query")
		}
	}
	if shared && (query.Get("download") != "1" || !validUUID(query.Get("engineId"))) {
		return nil, errors.New("invalid shared download query")
	}
	if media && query.Has("engineId") {
		return nil, errors.New("unexpected media engine selector")
	}
	return u, nil
}
func lowercaseHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	return lowercaseHex(strings.ReplaceAll(value, "-", ""), 32)
}
func nativeFilename(value string) string {
	value = filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || value == "/" {
		return "download"
	}
	// Leave room for the temporary name and common file-system limits.
	for len(value) > 220 {
		value = string([]rune(value)[:len([]rune(value))-1])
	}
	return value
}

func nativeSaveAction(ctx context.Context, handler http.Handler, address, name string, choose func(string) (string, error)) (bool, error) {
	if _, err := nativeDownloadURL(address); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	target, err := choose(nativeFilename(name))
	if err != nil || target == "" {
		return false, err
	}
	err = saveNativeDownload(ctx, handler, address, target)
	return err == nil, err
}

func saveNativeDownload(ctx context.Context, handler http.Handler, address, target string) (err error) {
	u, err := nativeDownloadURL(address)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "wails://localhost"+u.RequestURI(), nil)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".oie-download-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	writer := &downloadWriter{header: make(http.Header), file: file, ctx: ctx}
	handler.ServeHTTP(writer, request)
	if writer.status != http.StatusOK {
		return fmt.Errorf("download failed with HTTP status %d", writer.status)
	}
	if writer.err != nil {
		return writer.err
	}
	disposition, params, headerErr := mime.ParseMediaType(writer.header.Get("Content-Disposition"))
	if headerErr != nil || disposition != "attachment" || params["filename"] == "" {
		return errors.New("download response is not an attachment")
	}
	if contentType, _, err := mime.ParseMediaType(writer.header.Get("Content-Type")); err != nil || contentType == "" {
		return errors.New("download response has no valid content type")
	}
	if length := writer.header.Get("Content-Length"); length != "" {
		expected, err := strconv.ParseInt(length, 10, 64)
		if err != nil || expected < 0 || expected != writer.written {
			return errors.New("download response is incomplete")
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}

type downloadWriter struct {
	header  http.Header
	file    io.Writer
	ctx     context.Context
	status  int
	written int64
	err     error
}

func (w *downloadWriter) Header() http.Header { return w.header }
func (w *downloadWriter) Flush() {
	// BFF media streams flush after headers and each chunk. File writes already
	// reach the descriptor directly; the final successful save syncs it once.
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
}
func (w *downloadWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		return
	}
	if w.status == 0 {
		w.status = status
	}
}
func (w *downloadWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.status != 200 {
		return len(data), nil
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	n, err := w.file.Write(data)
	w.written += int64(n)
	if err != nil {
		w.err = err
	}
	return n, err
}
