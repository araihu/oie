package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/search"
)

func searchSourceDigest(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

// Room ranges project the already-highlighted excerpt onto its original text.
// Only the excerpt is matched. The full body is used to locate its offsets.
func roomSearchRanges(text, excerpt string, spans []search.Span) string {
	if len(spans) == 0 || len(text) > search.MaxHighlightBytes {
		return ""
	}
	start := strings.Index(text, excerpt)
	if start < 0 {
		return ""
	}
	end := start + len(excerpt)
	// The engine may cut its excerpt through a word. Do not turn a
	// partial word at an excerpt boundary into a new fuzzy prefix.
	before, _ := utf8.DecodeLastRuneInString(text[:start])
	after, _ := utf8.DecodeRuneInString(text[end:])
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) }
	firstWordEnd := strings.IndexFunc(excerpt, func(r rune) bool { return !isWord(r) })
	if firstWordEnd < 0 {
		firstWordEnd = len(excerpt)
	}
	lastWordStart := len(strings.TrimRightFunc(excerpt, isWord))
	ranges := make([][2]int, 0, len(spans))
	byteOffset, units := 0, 0
	advance := func(end int) int {
		for _, char := range text[byteOffset:end] {
			units++
			if char > 0xffff {
				units++
			}
		}
		byteOffset = end
		return units
	}
	for _, span := range spans {
		if isWord(before) && span.Start < firstWordEnd || isWord(after) && span.End > lastWordStart {
			continue
		}
		ranges = append(ranges, [2]int{advance(start + span.Start), advance(start + span.End)})
	}
	if len(ranges) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(ranges)
	return string(encoded)
}

type searchTextPart struct {
	text  string
	match bool
}

func searchHighlightPattern(query string) *search.Highlight {
	return search.NewHighlight(query)
}

func searchTextParts(text string, pattern *search.Highlight, compact bool) []searchTextPart {
	return searchTextPartsForSpans(text, pattern.Spans(text), compact)
}

func searchTextPartsForSpans(text string, matches []search.Span, compact bool) []searchTextPart {
	var parts []searchTextPart
	start := 0
	if compact && len(matches) > 0 {
		first := matches[0].Start
		prefix := text[:first]
		// Keep the first match inside the floating dialog's two-line excerpt.
		start = first
		for range 12 {
			if start == 0 {
				break
			}
			r, width := utf8.DecodeLastRuneInString(prefix[:start])
			if r == '\n' || r == '\r' {
				break
			}
			start -= width
		}
		if start > 0 {
			parts = append(parts, searchTextPart{text: "…"})
		}
	}
	for _, original := range matches {
		if original.Start < start {
			continue
		}
		if original.Start > start {
			parts = append(parts, searchTextPart{text: text[start:original.Start]})
		}
		parts = append(parts, searchTextPart{text: text[original.Start:original.End], match: true})
		start = original.End
	}
	if start < len(text) {
		parts = append(parts, searchTextPart{text: text[start:]})
	}
	return parts
}
