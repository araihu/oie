package previewfetch

import (
	"github.com/araihu/oie/internal/application"
	"net/url"
	"testing"
)

func TestCodebergMarkdownSourceResolvesRawFileWithoutChangingCatalogURL(t *testing.T) {
	for _, tc := range []struct{ source, format, want string }{
		{"https://codeberg.org/tusky/Tusky/src/branch/develop/CHANGELOG.md", "md", "https://codeberg.org/tusky/Tusky/raw/branch/develop/CHANGELOG.md"},
		{"https://codeberg.org/org/repo/src/tag/v1/notes.md?view=1", "md", "https://codeberg.org/org/repo/raw/tag/v1/notes.md?view=1"},
		{"https://codeberg.org/org/repo/src/commit/abcd/notes.md", "md", "https://codeberg.org/org/repo/raw/commit/abcd/notes.md"},
		{"https://codeberg.org/org/repo/raw/branch/main/notes.md", "md", ""},
		{"https://codeberg.org/org/repo/src/branch/main/clip.mp4", "video", ""},
		{"https://example.test/org/repo/src/branch/main/notes.md", "md", ""},
		{"http://codeberg.org/org/repo/src/branch/main/notes.md", "md", ""},
		{"https://codeberg.org:8443/org/repo/src/branch/main/notes.md", "md", ""},
		{"https://codeberg.org/org/repo/src/branch/main%2Fother/notes.md", "md", ""},
		{"https://codeberg.org/org/repo/src/issues/12/notes.md", "md", ""},
	} {
		t.Run(tc.source+tc.format, func(t *testing.T) {
			source, err := url.Parse(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			resolved := documentDownloadURL(source, application.MediaFormat(tc.format))
			want := tc.want
			if want == "" {
				want = tc.source
			}
			if resolved.String() != want || source.String() != tc.source {
				t.Fatalf("source %s resolved %s want %s", source, resolved, want)
			}
		})
	}
}
