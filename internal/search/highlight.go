package search

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Span describes a half-open byte range in the original text.
type Span struct{ Start, End int }

const (
	MaxHighlightBytes = 64 << 10
	MaxHighlightSpans = 256
)

var highlightWords = regexp.MustCompile(`[\p{L}\p{Nd}]+`)

// Highlight finds literal and fuzzy occurrences for display. A bounded
// excerpt may contain only some of the terms that matched the full source.
type Highlight struct {
	literal *regexp.Regexp
	terms   []string
}

func NewHighlight(query string) *Highlight {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > 200 {
		return nil
	}
	query = Fold(query)
	if query == "" {
		return nil
	}
	words := Words(query)
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
	slices.Sort(words)
	return &Highlight{literal: regexp.MustCompile(strings.Join(terms, "|")), terms: slices.Compact(words)}
}

func (h *Highlight) Spans(original string) []Span {
	if h == nil || original == "" || len(original) > MaxHighlightBytes {
		return nil
	}
	text := Fold(original)
	if len(text) > MaxHighlightBytes {
		return nil
	}
	var spans []Span
	for _, match := range h.literal.FindAllStringIndex(text, MaxHighlightSpans) {
		spans = append(spans, Span{match[0], match[1]})
	}
	var words [][]int
	// Limit comparison work and the number of words inspected.
	budget := 1 << 20
fuzzy:
	for _, term := range h.terms {
		query := []rune(term)
		limit := EditLimit(query)
		if limit == 0 || strings.Contains(text, term) {
			continue
		}
		if words == nil {
			words = highlightWords.FindAllStringIndex(text, -1)
		}
		var scratch [256]rune
		for _, word := range words {
			budget--
			if budget < 0 || len(spans) == MaxHighlightSpans {
				break fuzzy
			}
			// editPrefix considers only the beginning of each word.
			value := text[word[0]:word[1]]
			prefix := scratch[:0]
			for _, char := range value {
				prefix = append(prefix, char)
				if len(prefix) == len(query)+limit {
					break
				}
			}
			if len(prefix) < len(query)-limit {
				continue
			}
			budget -= len(query) * len(prefix)
			if budget < 0 {
				break fuzzy
			}
			if length := editPrefix(query, prefix, limit); length > 0 {
				spans = append(spans, Span{word[0], word[0] + len(string(prefix[:length]))})
			}
		}
	}
	if len(spans) == 0 {
		return nil
	}
	if original != text && !ascii(original) {
		var folded strings.Builder
		positions := make([]Span, 0, len(text))
		for start := 0; start < len(original); {
			_, width := utf8.DecodeRuneInString(original[start:])
			end := start + width
			for end < len(original) {
				char, width := utf8.DecodeRuneInString(original[end:])
				if !unicode.Is(unicode.Mn, char) {
					break
				}
				end += width
			}
			value := Fold(original[start:end])
			folded.WriteString(value)
			for range len(value) {
				positions = append(positions, Span{start, end})
			}
			start = end
		}
		// Some combining sequences reorder across these boundaries. Leave
		// them unmarked if this mapping cannot reproduce the canonical fold.
		if folded.String() != text {
			return nil
		}
		for i, span := range spans {
			spans[i] = Span{positions[span.Start].Start, positions[span.End-1].End}
		}
	}
	slices.SortFunc(spans, func(a, b Span) int { return a.Start - b.Start })
	merged := spans[:0]
	for _, span := range spans {
		if len(merged) > 0 && span.Start < merged[len(merged)-1].End {
			last := &merged[len(merged)-1]
			last.End = max(last.End, span.End)
		} else {
			merged = append(merged, span)
		}
	}
	return merged
}

func ascii(text string) bool {
	for i := range len(text) {
		if text[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
