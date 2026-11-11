package web

import (
	"embed"
	"github.com/araihu/oie/internal/adapters/cachehttp"
	"net/http"
)

//go:embed emoji-picker.js
var emojiScript []byte

//go:embed emoji-completion.js
var emojiCompletionScript []byte

//go:embed emoji-picker.css
var emojiStylesheet []byte

//go:embed emoji/*.json emoji/LICENSE.txt
var emojiFiles embed.FS

// Emoji catalogs are static UI assets, not an engine operation. The browser
// loads its localized catalog when emoji browsing or completion begins.
func serveEmojiAsset(w http.ResponseWriter, r *http.Request) bool {
	var data []byte
	mime := "text/javascript"
	switch r.URL.Path {
	case "/ui/emoji-picker.js":
		data = emojiScript
	case "/ui/emoji-completion.js":
		data = emojiCompletionScript
	case "/ui/emoji-picker.css":
		data, mime = emojiStylesheet, "text/css"
	case "/ui/emoji/en.json", "/ui/emoji/pt-BR.json":
		data, _ = emojiFiles.ReadFile("emoji/" + r.URL.Path[len("/ui/emoji/"):])
		mime = "application/json"
	case "/ui/emoji/LICENSE.txt":
		data, _ = emojiFiles.ReadFile("emoji/LICENSE.txt")
		mime = "text/plain; charset=utf-8"
	default:
		return false
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", cachehttp.Digest(data))
	cachehttp.Revalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) })).ServeHTTP(w, r)
	return true
}

func emojiCategoryLabels(v view) []string {
	return []string{v.Text.EmojiSmileys, v.Text.EmojiPeople, v.Text.EmojiAnimals, v.Text.EmojiFood, v.Text.EmojiTravel, v.Text.EmojiActivities, v.Text.EmojiObjects, v.Text.EmojiSymbols, v.Text.EmojiFlags}
}

func emojiCategoryGlyph(index int) string {
	return []string{"😀", "👋", "🐻", "🍔", "🚗", "⚽", "💡", "🔣", "🚩"}[index]
}
