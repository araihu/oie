package application

import (
	"fmt"
	"strings"
	"testing"
)

func TestSharedReferencesClassifyWithoutLosingSources(t *testing.T) {
	text := "Caption https://example.test/Imagem%20São.JPG?q=1 https://example.test/page https://example.test/relatório.pdf https://example.test/voice.opus https://example.test/clip.mp4 https://example.test/page https://user:secret@example.test/private.png"
	refs := SharedReferences(text)
	if len(refs) != 5 {
		t.Fatal(refs)
	}
	for i, kind := range []string{"image", "link", "document", "audio", "video"} {
		if refs[i].Kind != kind {
			t.Fatal(refs[i])
		}
	}
	if refs[0].FileName != "Imagem São.JPG" {
		t.Fatal(refs[0])
	}
	var many strings.Builder
	for i := range 40 {
		fmt.Fprintf(&many, "https://example.test/%d.png ", i)
	}
	if len(SharedReferences(many.String())) != 32 {
		t.Fatal("unbounded message catalog")
	}
}
func TestSharedCursorRejectsMalformedAndNonUUIDReferences(t *testing.T) {
	for _, value := range []string{"garbage", "eyJtcyI6MSwiaWQiOiJub3QtYS11dWlkIn0", strings.Repeat("a", 513)} {
		if _, err := ParseSharedContentCursor(value); err == nil {
			t.Fatal("invalid cursor", value)
		}
	}
}
