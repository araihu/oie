package web

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/search"
)

type searchTextPart struct {
	text  string
	match bool
}

type searchHighlight struct {
	literal *regexp.Regexp
	terms   []string
}

var searchHighlightWords = regexp.MustCompile(`[\p{L}\p{Nd}]+`)

func searchHighlightPattern(query string) *searchHighlight {
	query = search.Fold(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	words := search.Words(query)
	terms := append([]string{query}, strings.Fields(query)...)
	terms = append(terms, words...)
	slices.SortFunc(terms, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	terms = slices.Compact(terms)
	for i, term := range terms {
		terms[i] = regexp.QuoteMeta(term)
	}
	return &searchHighlight{literal: regexp.MustCompile(strings.Join(terms, "|")), terms: words}
}

func (pattern *searchHighlight) matches(text string) [][2]int {
	var matches [][2]int
	for _, span := range pattern.literal.FindAllStringIndex(text, -1) {
		matches = append(matches, [2]int(span))
	}
	var words [][]int
	for _, term := range pattern.terms {
		if utf8.RuneCountInString(term) < 4 || strings.Contains(text, term) {
			continue
		}
		if words == nil {
			words = searchHighlightWords.FindAllStringIndex(text, -1)
		}
		query := []rune(term)
		for _, span := range words {
			word := []rune(text[span[0]:span[1]])
			if length := search.ApproximatePrefix(query, word); length > 0 {
				matches = append(matches, [2]int{span[0], span[0] + len(string(word[:length]))})
			}
		}
	}
	slices.SortFunc(matches, func(a, b [2]int) int { return a[0] - b[0] })
	merged := matches[:0]
	for _, match := range matches {
		if len(merged) > 0 && match[0] < merged[len(merged)-1][1] {
			last := &merged[len(merged)-1]
			last[1] = max(last[1], match[1])
		} else {
			merged = append(merged, match)
		}
	}
	return merged
}

func searchTextParts(text string, pattern *searchHighlight, compact bool) []searchTextPart {
	if pattern == nil {
		return []searchTextPart{{text: text}}
	}

	// Map each folded byte back to the original segment, including its accents.
	type span struct{ start, end int }
	var folded strings.Builder
	positions := make([]span, 0, len(text))
	for start := 0; start < len(text); {
		_, width := utf8.DecodeRuneInString(text[start:])
		end := start + width
		for end < len(text) {
			r, width := utf8.DecodeRuneInString(text[end:])
			if !unicode.Is(unicode.Mn, r) {
				break
			}
			end += width
		}
		value := search.Fold(text[start:end])
		folded.WriteString(value)
		for range len(value) {
			positions = append(positions, span{start, end})
		}
		start = end
	}

	matches := pattern.matches(folded.String())
	var parts []searchTextPart
	start := 0
	if compact && len(matches) > 0 {
		first := positions[matches[0][0]].start
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
	for _, match := range matches {
		original := span{positions[match[0]].start, positions[match[1]-1].end}
		if original.start < start {
			continue
		}
		if original.start > start {
			parts = append(parts, searchTextPart{text: text[start:original.start]})
		}
		parts = append(parts, searchTextPart{text: text[original.start:original.end], match: true})
		start = original.end
	}
	if start < len(text) {
		parts = append(parts, searchTextPart{text: text[start:]})
	}
	return parts
}
