package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/web/bffapi"
)

func TestDocumentMarkdownUsesMargoStaticProjection(t *testing.T) {
	source := "# Release notes\n\n**Fixed** and _improved_, with a literal <user> name.\n\n- First\n- Second\n\n> Quote\n\n```go\nfmt.Println(\"hello\")\n```\n\n[Site](https://example.test/docs)\n"
	fragment, err := renderDocumentMarkdown(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1>", "Release notes", "<strong>Fixed</strong>", "<em>improved</em>", "<ul>", "<blockquote>", "<pre>", "<code>", `href="https://example.test/docs"`} {
		if !strings.Contains(fragment, want) {
			t.Errorf("missing semantic rendering %q in %s", want, fragment)
		}
	}
	for _, forbidden := range []string{"<img", "<script", "<style", "<iframe", " id=", " src=", " x-data", " onclick"} {
		if strings.Contains(fragment, forbidden) {
			t.Errorf("active document content %q in %s", forbidden, fragment)
		}
	}
}
func TestDocumentMarkdownAuthoredHTMLCannotLoadOrExecute(t *testing.T) {
	fragment, err := renderDocumentMarkdown(t.Context(), "# Title\n\n<img src=\"https://tracker.test\" onerror=\"bad()\"><script>bad()</script><style>body{display:none}</style>\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"<img", "<script", "<style", "tracker.test", "onerror", "bad()"} {
		if strings.Contains(fragment, forbidden) {
			t.Fatal(fragment)
		}
	}
}
func TestDocumentMarkdownRemoteImagesUsePlainTextFallback(t *testing.T) {
	if _, err := renderDocumentMarkdown(context.Background(), "# Title\n\n![Remote](https://example.test/track.png)"); err == nil {
		t.Fatal("remote image policy should retain the plain-text fallback")
	}
}
func TestDocumentHTMLHostProjectionDropsActiveResourcesAndIDs(t *testing.T) {
	fragment, err := staticDocumentHTML(`<h1 id="workspace" onclick="bad()">Title</h1><p style="color:red" x-data="bad()"><a href="javascript:bad()">Unsafe</a><a href="https://user:secret@example.test">Credentials</a><a href="https://example.test">Safe</a><img src="https://tracker.test" alt="Alt"><script>bad()</script><iframe src="https://tracker.test">Bad</iframe><input autofocus></p>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"javascript:", "secret", "tracker.test", "script", "iframe", "onclick", "x-data", "style=", "id=", "input", "autofocus"} {
		if strings.Contains(fragment, forbidden) {
			t.Errorf("retained %q: %s", forbidden, fragment)
		}
	}
	if !strings.Contains(fragment, `href="https://example.test" target="_blank" rel="noopener noreferrer"`) || !strings.Contains(fragment, "Alt") {
		t.Fatal(fragment)
	}
}
func TestDocumentMarkdownEndpointHasNoEngineReadsAndBoundsInput(t *testing.T) {
	ui, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"source":"# Release"}`, 200},
		{`{"source":""}`, 200},
		{`{}`, 400},
		{`{"source":null}`, 400},
		{`null`, 400},
		{`{"source":"one","url":"https://provider.test"}`, 400},
		{`{"source":"one"} {}`, 400},
		{`{"source":"\u0000"}`, 400},
		{`{"source":"` + strings.Repeat("x", documentMarkdownLimit+1) + `"}`, 413},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/ui/document-markdown", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		ui.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("status %d want %d: %s", response.Code, tc.status, response.Body)
		}
		if tc.status == 200 {
			var value bffapi.MarkdownFragment
			if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(response.Header())
			}
		}
	}
}
