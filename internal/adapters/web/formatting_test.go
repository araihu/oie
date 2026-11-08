package web

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/araihu/margo"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

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
