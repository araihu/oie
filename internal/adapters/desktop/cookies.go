//go:build !server

package desktop

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
)

// Native WebKit scheme handlers stream HTTP responses, but do not retain HTTP
// cookies. This store supplies the BFF's HttpOnly cookies inside this process.
// Only the two device preferences are written to disk. The CSRF token rotates
// whenever the desktop process starts.
type nativeCookies struct {
	mu     sync.Mutex
	path   string
	values map[string]string
}

func newNativeCookies(dataDir string) (*nativeCookies, error) {
	dir := filepath.Join(dataDir, "webview")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	result := &nativeCookies{path: filepath.Join(dir, "preferences.json"), values: map[string]string{"oie_csrf": base64.RawURLEncoding.EncodeToString(token[:])}}
	info, err := os.Lstat(result.path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("desktop preferences must be a private regular file: %s", result.path)
	}
	data, err := os.ReadFile(result.path)
	if err != nil {
		return nil, err
	}
	var preferences map[string]string
	if err = json.Unmarshal(data, &preferences); err != nil {
		return nil, fmt.Errorf("read desktop preferences: %w", err)
	}
	if preferences == nil {
		return nil, errors.New("desktop preferences must contain a JSON object")
	}
	for key, value := range preferences {
		if !validDevicePreference(key, value) {
			return nil, fmt.Errorf("invalid desktop preference %q", key)
		}
		result.values[key] = value
	}
	return result, nil
}

func validDevicePreference(key, value string) bool {
	switch key {
	case "oie_lang":
		return value == "en" || value == "pt-BR"
	case "oie_theme":
		return value == "auto" || value == "light" || value == "dark"
	default:
		return false
	}
}

func nativeRequest(r *http.Request) bool {
	// Wails normalizes its scheme request to Go's server request form before
	// invoking the asset handler. Both the native URL and that relative form
	// must still name the one trusted host.
	nativeURL := r.URL.Scheme == "wails" && r.URL.Host == "localhost"
	serverURL := r.URL.Scheme == "" && r.URL.Host == "" && r.Host == "localhost"
	if (!nativeURL && !serverURL) || r.Host != "localhost" || r.URL.User != nil {
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	for _, key := range []string{"Origin", "Referer"} {
		value := r.Header.Get(key)
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "wails" || u.Host != "localhost" || u.User != nil {
			return false
		}
	}
	return true
}

func (c *nativeCookies) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !nativeRequest(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		r = r.Clone(r.Context())
		r.Header.Del("Cookie")
		c.mu.Lock()
		for key, value := range c.values {
			r.AddCookie(&http.Cookie{Name: key, Value: value})
		}
		c.mu.Unlock()
		next.ServeHTTP(&nativeCookieWriter{ResponseWriter: w, store: c}, r)
	})
}

func (c *nativeCookies) capture(header http.Header) error {
	cookies := (&http.Response{Header: header}).Cookies()
	if len(cookies) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	updated := maps.Clone(c.values)
	changed := false
	for _, cookie := range cookies {
		if cookie.Domain != "" || (cookie.Path != "" && cookie.Path != "/") {
			continue
		}
		if cookie.Name != "oie_lang" && cookie.Name != "oie_theme" {
			continue
		}
		if cookie.MaxAge < 0 {
			if _, ok := updated[cookie.Name]; ok {
				delete(updated, cookie.Name)
				changed = true
			}
		} else if validDevicePreference(cookie.Name, cookie.Value) && updated[cookie.Name] != cookie.Value {
			updated[cookie.Name] = cookie.Value
			changed = true
		}
	}
	if !changed {
		return nil
	}
	preferences := make(map[string]string)
	for key, value := range updated {
		if validDevicePreference(key, value) {
			preferences[key] = value
		}
	}
	if err := writePrivateJSON(c.path, preferences); err != nil {
		return err
	}
	c.values = updated
	return nil
}

func writePrivateJSON(path string, value any) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".preferences-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err = file.Chmod(0600); err != nil {
		return err
	}
	if err = json.NewEncoder(file).Encode(value); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

type nativeCookieWriter struct {
	http.ResponseWriter
	store  *nativeCookies
	wrote  bool
	failed bool
}

func (w *nativeCookieWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *nativeCookieWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	// Interim responses do not commit the final cookie headers.
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wrote = true
	if err := w.store.capture(w.Header()); err != nil {
		slog.Error("save desktop preferences", "error", err)
		w.failed = true
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *nativeCookieWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.failed {
		return 0, errors.New("desktop preferences could not be saved")
	}
	return w.ResponseWriter.Write(data)
}
func (w *nativeCookieWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if !w.failed {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
}
