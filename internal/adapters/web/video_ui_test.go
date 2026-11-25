package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestNativeVideoMarkupRequiresExplicitSameOriginPlayback(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "pt-BR"} {
		t.Run(locale, func(t *testing.T) {
			item := engineapi.SharedContentItem{Id: testMessage, Kind: "video", Url: "https://files.example.test/clip.webm", FileName: "<script>clip.webm", Message: engineapi.Message{Id: testRevision, AccountId: testAccount, ConversationId: testChat}}
			v := view{EngineID: testEngine, Locale: locale, Text: catalogs[locale]}
			var out bytes.Buffer
			if err := videoCard(v, item).Render(context.Background(), &out); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			native := regexp.MustCompile(`<video\b[^>]*>`).FindString(html)
			if native == "" || !strings.Contains(native, `preload="none"`) || !strings.Contains(native, "controls") || !strings.Contains(native, "playsinline") || strings.Contains(native, "src=") || strings.Contains(native, "autoplay") || strings.Contains(html, "<source") || strings.Contains(native, "poster=") {
				t.Fatalf("eager or non-native video markup: %s", html)
			}
			for _, want := range []string{`data-video-item="` + testMessage + `"`, `data-video-engine="` + testEngine + `"`, `data-video-account="` + testAccount + `"`, `data-video-conversation="` + testChat + `"`, `data-video-message="` + testRevision + `"`, `data-video-source="/ui/shared-content/` + testMessage + `/content?engineId=` + testEngine + `"`, catalogs[locale].VideoPlay, "&lt;script&gt;clip.webm"} {
				if !strings.Contains(html, want) {
					t.Errorf("missing scoped/localized video content %q", want)
				}
			}
			for _, control := range []string{"data-video-thumbnail-load", "data-video-download", "data-video-retry"} {
				if regexp.MustCompile(`\s` + control + `[\s=>]`).MatchString(html) {
					t.Errorf("redundant inline control %s", control)
				}
			}
			if strings.Contains(html, "<script>") {
				t.Fatal("file metadata interpreted as markup")
			}
		})
	}
}

func TestVideoDecoderDistribution(t *testing.T) {
	index, err := os.ReadFile("libmedia/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Revision string
		SHA256   map[string]string
	}
	if err := json.Unmarshal(index, &manifest); err != nil {
		t.Fatal(err)
	}
	root := "libmedia/" + manifest.Revision[:8]
	assets, err := fs.Sub(videoDecoderAssets, root)
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(content)) != manifest.SHA256[path] {
			t.Errorf("asset checksum mismatch: %s", path)
		}
		delete(manifest.SHA256, path)
		return nil
	})
	if err != nil || len(manifest.SHA256) != 0 {
		t.Fatalf("incomplete decoder distribution: %v, missing %v", err, manifest.SHA256)
	}
}
