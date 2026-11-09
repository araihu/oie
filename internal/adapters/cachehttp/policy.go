// Package cachehttp decorates HTTP representations with a shared cache policy.
// It owns no bodies, timers or process cache; resource authorization and content
// validation remain in the wrapped handler, before it sets an entity tag.
package cachehttp

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
)

type Policy struct{ Control string }

var Revalidate = Policy{Control: "no-cache"}
var PrivateRevalidate = Policy{Control: "private, no-cache"}
var PrivateImmutable = Policy{Control: "private, max-age=31536000, immutable"}

func Digest(body []byte) string { return fmt.Sprintf(`"%x"`, sha256.Sum256(body)) }
func (p Policy) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &conditionalWriter{ResponseWriter: w, request: r, policy: p}
		next.ServeHTTP(writer, r)
		if writer.status == 0 {
			writer.WriteHeader(http.StatusOK)
		}
	})
}

type conditionalWriter struct {
	http.ResponseWriter
	request *http.Request
	policy  Policy
	status  int
}

func (w *conditionalWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	tag := w.Header().Get("ETag")
	if (w.request.Method == http.MethodGet || w.request.Method == http.MethodHead) && (status == 200 || status == 206 || status == 304) && validTag(tag) && len(w.Header().Values("Set-Cookie")) == 0 {
		w.Header().Set("Cache-Control", w.policy.Control)
		if status != 304 && matches(w.request.Header.Get("If-None-Match"), tag) {
			status = 304
		}
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	if status == 304 {
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Range")
		w.Header().Del("Content-Encoding")
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *conditionalWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.status == 304 {
		return 0, http.ErrBodyNotAllowed
	}
	if w.request.Method == http.MethodHead {
		return len(body), nil
	}
	return w.ResponseWriter.Write(body)
}
func (w *conditionalWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *conditionalWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.status == 304 {
		return http.ErrBodyNotAllowed
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func validTag(tag string) bool {
	tag = strings.TrimPrefix(tag, "W/")
	if len(tag) < 2 || len(tag) > 1024 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, c := range []byte(tag[1 : len(tag)-1]) {
		if c < 0x21 || c == 0x22 || c == 0x7f {
			return false
		}
	}
	return true
}
func matches(condition, tag string) bool {
	if condition == "" || len(condition) > 8192 {
		return false
	}
	if strings.TrimSpace(condition) == "*" {
		return true
	}
	tag = strings.TrimPrefix(tag, "W/")
	matched := false
	for rest := condition; rest != ""; {
		rest = strings.TrimLeft(rest, " \t")
		start := 0
		if strings.HasPrefix(rest, "W/") {
			start = 2
		}
		if len(rest) <= start || rest[start] != '"' {
			return false
		}
		end := strings.IndexByte(rest[start+1:], '"')
		if end < 0 {
			return false
		}
		end += start + 2
		candidate := rest[:end]
		if !validTag(candidate) {
			return false
		}
		if strings.TrimPrefix(candidate, "W/") == tag {
			matched = true
		}
		rest = strings.TrimLeft(rest[end:], " \t")
		if rest == "" {
			break
		}
		if rest[0] != ',' {
			return false
		}
		rest = rest[1:]
		if strings.TrimSpace(rest) == "" {
			return false
		}
	}
	return matched
}
