package web

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/araihu/margo"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"golang.org/x/net/html"
)

func TestChatQuotesRequireAnExplicitPrefixOnEveryLine(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		textByDepth  map[int]string
	}{
		{"emoji reply", "> quoted source\n🙌🙌😀", map[int]string{0: "🙌🙌😀", 1: "quoted source"}},
		{"CRLF", "> quoted source\r\n🙌🙌😀\r\n", map[int]string{0: "🙌🙌😀", 1: "quoted source"}},
		{"explicit nesting", ">> inner\n> outer\nOwn **response**", map[int]string{0: "Own response", 1: "outer", 2: "inner"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			component := formattedMessage(context.Background(), tc.source)
			if component == nil {
				t.Fatal("chat formatting failed")
			}
			var output bytes.Buffer
			if err := component.Render(context.Background(), &output); err != nil {
				t.Fatal(err)
			}
			root, err := html.Parse(strings.NewReader(output.String()))
			if err != nil {
				t.Fatal(err)
			}
			texts := map[int]string{}
			var walk func(*html.Node, int)
			walk = func(node *html.Node, depth int) {
				if node.Type == html.ElementNode && node.Data == "blockquote" {
					depth++
				}
				if node.Type == html.TextNode {
					texts[depth] += node.Data
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					walk(child, depth)
				}
			}
			walk(root, 0)
			for depth, expected := range tc.textByDepth {
				if strings.TrimSpace(texts[depth]) != expected {
					t.Errorf("quote depth %d: got %q want %q", depth, texts[depth], expected)
				}
			}
			var copied string
			if err := json.Unmarshal([]byte(originalMessageJSON(tc.source)), &copied); err != nil || copied != tc.source {
				t.Fatal("quote presentation changed the original copy source", err)
			}
		})
	}
}

func TestMessageFormattingKeepsOriginalAndSafeChatProjection(t *testing.T) {
	original := ">> kDE: O documentário\n>> **bold**\n> Lembrando...\n\n![image](https://example.test/image.png) [bad](javascript:alert%281%29)\n\n<script>alert(1)</script>\n"
	m := messageView{Message: engineapi.Message{OriginalText: original}, Formatted: formattedMessage(context.Background(), original)}
	var out bytes.Buffer
	if err := messageBody(view{}, m).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"<blockquote><blockquote>", "<strong>bold</strong>", "&lt;script&gt;", "oie-original-message"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, unsafe := range []string{"<img", `href="javascript:`, "<script>alert"} {
		if strings.Contains(got, unsafe) {
			t.Errorf("unsafe message content: %s", unsafe)
		}
	}
	var copy string
	if err := json.Unmarshal([]byte(originalMessageJSON(original)), &copy); err != nil || copy != original {
		t.Fatal("copy original lost content")
	}
	for _, value := range []string{"line\r\n**bold**\n", `</script><svg onload="bad()">`, "quotes '\" 😀"} {
		var decoded string
		if err := json.Unmarshal([]byte(originalMessageJSON(value)), &decoded); err != nil || decoded != value {
			t.Fatalf("original source changed: %q %v", decoded, err)
		}
	}
}
func TestMessageFormattingFallsBackReadablyOnResourceFailure(t *testing.T) {
	original := strings.Repeat("x", margo.MaxChatBytes+1)
	if formattedMessage(context.Background(), original) != nil {
		t.Fatal("resource limit accepted")
	}
	var out bytes.Buffer
	if err := messageBody(view{}, messageView{Message: engineapi.Message{OriginalText: "**original**"}}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `class="oie-plain-fallback">**original**`) {
		t.Fatal("readable fallback missing")
	}
}

func TestImagePresentationOnlySuppressesRepresentedStandaloneLines(t *testing.T) {
	image := "https://example.test/image.png"
	previews := []engineapi.MessagePreview{{Kind: "image", Url: image}}
	cases := []struct{ source, want string }{
		{image, ""}, {"  " + image + "\n", ""}, {"Caption\n" + image + "\n", "Caption\n"},
		{"before " + image + " after", "before " + image + " after"},
		{"`" + image + "`", "`" + image + "`"},
		{"`code\n" + image + "\ncode`", "`code\n" + image + "\ncode`"},
		{"```text\n" + image + "\n```", "```text\n" + image + "\n```"},
		{"~~~\n" + image + "\n~~~", "~~~\n" + image + "\n~~~"},
		{"- ```\n  " + image + "\n  ```", "- ```\n  " + image + "\n  ```"},
		{"    " + image, "    " + image}, {"> " + image, "> " + image},
		{"https://example.test/article\n" + image, "https://example.test/article\n"},
	}
	for _, tc := range cases {
		if got := imagePresentation(tc.source, previews); got != tc.want {
			t.Errorf("source %q: got %q want %q", tc.source, got, tc.want)
		}
	}
	if got := imagePresentation(image, []engineapi.MessagePreview{{Kind: "link", Url: image}}); got != image {
		t.Fatal("article link text suppressed")
	}
}
