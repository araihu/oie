package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEmojiCatalogsPreserveLocalizedUnicodeSequences(t *testing.T) {
	var baseline []string
	var baselineSearch []string
	for _, locale := range []string{"en", "pt-BR"} {
		data, err := emojiFiles.ReadFile("emoji/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Locale   string            `json:"locale"`
			Items    []json.RawMessage `json:"items"`
			Families map[string]string `json:"families"`
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
				baselineSearch = append(baselineSearch, keywords)
			} else if index >= len(baseline) || baseline[index] != glyph {
				t.Fatal("localization altered the Unicode sequence", index)
			} else if baselineSearch[index] != keywords {
				t.Fatal("search language depends on interface language", index)
			}
			if glyph == "👍" || glyph == "👍🏽" {
				for _, term := range []string{"thumbs up", "joinha", "polegar para cima"} {
					if !strings.Contains(keywords, term) {
						t.Fatal("missing cross-language search term", locale, glyph, term)
					}
				}
			}
		}
		for _, required := range []string{"❤️", "👍🏽", "👩🏽‍💻", "🇧🇷"} {
			if !seen[required] {
				t.Fatal("missing sequence", locale, required)
			}
		}
		for glyph, base := range catalog.Families {
			if !seen[glyph] || !seen[base] || glyph == base || catalog.Families[base] != "" {
				t.Fatal("variant does not resolve to a retained base emoji", locale, glyph, base)
			}
		}
		for glyph, base := range map[string]string{"👍🏽": "👍", "👩🏽‍💻": "👩‍💻", "🫱🏻‍🫲🏽": "🤝"} {
			if catalog.Families[glyph] != base {
				t.Fatal("incorrect Unicode variant family", locale, glyph, catalog.Families[glyph])
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
