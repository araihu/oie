package web

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type searchTextPart struct {
	text  string
	match bool
}

func foldHighlightText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFKD.String(cases.Fold().String(value)))
}

func searchHighlightPattern(query string) *regexp.Regexp {
	query = foldHighlightText(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	terms := append([]string{query}, strings.Fields(query)...)
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
	return regexp.MustCompile(strings.Join(terms, "|"))
}

func searchTextParts(text string, pattern *regexp.Regexp, compact bool) []searchTextPart {
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
		value := foldHighlightText(text[start:end])
		folded.WriteString(value)
		for range len(value) {
			positions = append(positions, span{start, end})
		}
		start = end
	}

	matches := pattern.FindAllStringIndex(folded.String(), -1)
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
