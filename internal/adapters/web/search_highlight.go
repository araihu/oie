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

func searchTextParts(text string, pattern *regexp.Regexp) []searchTextPart {
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

	var parts []searchTextPart
	start := 0
	for _, match := range pattern.FindAllStringIndex(folded.String(), -1) {
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
