package sqlite

import (
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/search"
)

// matchSharedMetadata applies the catalog's bounded, token-based search policy.
// The repository applies this before pagination. It does not rank results or
// alter the retained source text.
func matchSharedMetadata(text, query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}
	if utf8.RuneCountInString(query) > 200 {
		return false
	}
	haystack, needle := search.Fold(text), search.Fold(query)
	if needle == "" {
		return false
	}
	if strings.Contains(haystack, needle) {
		return true
	}
	terms := search.Words(needle)
	if len(terms) == 0 {
		return false
	}
	words := search.Words(haystack)
	for _, term := range terms {
		if strings.Contains(haystack, term) {
			continue
		}
		query := []rune(term)
		matched := false
		for _, word := range words {
			if search.ApproximatePrefix(query, []rune(word)) > 0 {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
