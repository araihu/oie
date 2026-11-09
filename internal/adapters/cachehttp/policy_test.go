package cachehttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConditionalDecoratorValidatesBeforeConsideringClientTag(t *testing.T) {
	for _, tc := range []struct {
		status                 int
		tag, condition, cookie string
		want                   int
		control                string
	}{
		{200, `"one"`, `"other", W/"one"`, "", 304, "private, no-cache"},
		{200, `"one,two"`, `W/"one,two", "three"`, "", 304, "private, no-cache"},
		{206, `"one"`, `*`, "", 304, "private, no-cache"},
		{200, `"one"`, `"one", invalid`, "", 200, "private, no-cache"},
		{200, `"one"`, `"one",`, "", 200, "private, no-cache"},
		{404, `"one"`, `"one"`, "", 404, "no-store"},
		{409, `"one"`, `*`, "", 409, "no-store"},
		{200, "", "*", "", 200, "no-store"},
		{200, `"one"`, `"one"`, "session=secret", 200, "no-store"},
	} {
		validated := false
		handler := PrivateRevalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			validated = true
			w.Header().Set("ETag", tc.tag)
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", "bytes 0-3/20")
			if tc.cookie != "" {
				w.Header().Set("Set-Cookie", tc.cookie)
			}
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, "body")
		}))
		request := httptest.NewRequest("GET", "/", nil)
		request.Header.Set("If-None-Match", tc.condition)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !validated || response.Code != tc.want || response.Header().Get("Cache-Control") != tc.control {
			t.Fatalf("%+v: code=%d headers=%v", tc, response.Code, response.Header())
		}
		if tc.want == 304 && (response.Body.Len() != 0 || response.Header().Get("Content-Length") != "" || response.Header().Get("Content-Range") != "") {
			t.Fatal("304 retained body/range", response)
		}
	}
}
func TestHeadAndStreamingConditionalEndWithoutBody(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		consumed := false
		handler := PrivateRevalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", Digest([]byte("content")))
			w.WriteHeader(200)
			if http.NewResponseController(w).Flush() != nil {
				return
			}
			consumed = true
			_, _ = io.WriteString(w, "content")
		}))
		request := httptest.NewRequest(method, "/", nil)
		request.Header.Set("If-None-Match", Digest([]byte("content")))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if consumed || response.Code != 304 || response.Body.Len() != 0 {
			t.Fatal("conditional stream consumed body", response)
		}
	}
	response := httptest.NewRecorder()
	Revalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", Digest(nil))
		_, _ = io.WriteString(w, "content")
	})).ServeHTTP(response, httptest.NewRequest("HEAD", "/", nil))
	if response.Body.Len() != 0 {
		t.Fatal("HEAD body")
	}
}
func FuzzConditionalTags(f *testing.F) {
	for _, seed := range []string{`W/"abc", "def"`, `*`, `"comma,tag"`, strings.Repeat("x", 8200)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, condition string) { _ = matches(condition, `"abc"`) })
}
