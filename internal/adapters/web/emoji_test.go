package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"unicode/utf8"
)

func TestEmojiCatalogsPreserveLocalizedUnicodeSequences(t *testing.T) {
	var baseline []string
	for _, locale := range []string{"en", "pt-BR"} {
		data, err := emojiFiles.ReadFile("emoji/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Locale string            `json:"locale"`
			Items  []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		if catalog.Locale != locale || len(catalog.Items) < 3900 {
			t.Fatal("incomplete localized emoji catalog", locale, len(catalog.Items))
		}
		seen := map[string]bool{}
		for index, raw := range catalog.Items {
			var item []json.RawMessage
			if err := json.Unmarshal(raw, &item); err != nil || len(item) != 4 {
				t.Fatal("invalid emoji record", locale, index)
			}
			var glyph, name, keywords string
			var group int
			for i, field := range []*string{&glyph, &name, &keywords} {
				if err := json.Unmarshal(item[i], field); err != nil || *field == "" || !utf8.ValidString(*field) {
					t.Fatal("missing localized emoji metadata", locale, index, i)
				}
			}
			if err := json.Unmarshal(item[3], &group); err != nil || group < 0 || group > 8 || seen[glyph] {
				t.Fatal("invalid emoji identity or category", locale, index)
			}
			seen[glyph] = true
			if locale == "en" {
				baseline = append(baseline, glyph)
			} else if index >= len(baseline) || baseline[index] != glyph {
				t.Fatal("localization altered the Unicode sequence", index)
			}
		}
		for _, required := range []string{"❤️", "👍🏽", "👩🏽‍💻", "🇧🇷"} {
			if !seen[required] {
				t.Fatal("missing sequence", locale, required)
			}
		}
	}
}

func TestEmojiAssetsAreLocalAndDoNotAcceptArbitraryPaths(t *testing.T) {
	for _, path := range []string{"/ui/emoji/en.json", "/ui/emoji/pt-BR.json", "/ui/emoji/LICENSE.txt", "/ui/emoji-picker.js", "/ui/emoji-picker.css"} {
		response := httptest.NewRecorder()
		if !serveEmojiAsset(response, httptest.NewRequest("GET", path, nil)) || response.Code != 200 || response.Body.Len() == 0 || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing local emoji asset", path)
		}
	}
	if serveEmojiAsset(httptest.NewRecorder(), httptest.NewRequest("GET", "/ui/emoji/../../locales/en.json", nil)) {
		t.Fatal("emoji assets accept arbitrary paths")
	}
}
