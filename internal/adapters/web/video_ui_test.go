package web

import (
	"bytes"
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
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
			if native == "" || !strings.Contains(native, `preload="none"`) || !strings.Contains(native, "controls") || !strings.Contains(native, "playsinline") || strings.Contains(native, "src=") || strings.Contains(native, "autoplay") || strings.Contains(html, "<source") || strings.Contains(html, "poster=") {
				t.Fatalf("eager or non-native video markup: %s", html)
			}
			for _, want := range []string{`data-video-item="` + testMessage + `"`, `data-video-engine="` + testEngine + `"`, `data-video-account="` + testAccount + `"`, `data-video-conversation="` + testChat + `"`, `data-video-message="` + testRevision + `"`, `data-video-source="/ui/shared-content/` + testMessage + `/content?engineId=` + testEngine + `"`, catalogs[locale].VideoPlay, catalogs[locale].VideoRetry, "&lt;script&gt;clip.webm"} {
				if !strings.Contains(html, want) {
					t.Errorf("missing scoped/localized video content %q", want)
				}
			}
			if strings.Contains(html, "<script>") {
				t.Fatal("file metadata interpreted as markup")
			}
		})
	}
}
