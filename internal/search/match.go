// Package search defines text matching shared by the engine and presentation.
package search

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fold normalizes case, accents and compatibility characters for search.
func Fold(value string) string {
	value = norm.NFKD.String(cases.Fold().String(value))
	return strings.Map(func(char rune) rune {
		if unicode.Is(unicode.Mn, char) {
			return -1
		}
		return char
	}, value)
}

// Words returns alphanumeric runs and individual symbols from folded text.
func Words(value string) []string {
	var terms []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			terms = append(terms, word.String())
			word.Reset()
		}
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			word.WriteRune(char)
		} else {
			flush()
			// Symbols such as emoji remain exact terms instead of disappearing
			// from mixed queries and accidentally broadening their results.
			if unicode.IsSymbol(char) {
				terms = append(terms, string(char))
			}
		}
	}
	flush()
	return terms
}

// ApproximatePrefix returns the length in runes of the closest prefix under the
// search edit limit. Adjacent transpositions count as one edit.
func ApproximatePrefix(query, word []rune) int {
	if len(query) < 4 || !slices.ContainsFunc(query, unicode.IsLetter) {
		return 0
	}
	limit := 1
	if len(query) >= 8 {
		limit = 2
	}
	return editPrefix(query, word, limit)
}

// editPrefix is a banded optimal-string-alignment distance. Adjacent swapped
// characters cost one edit. A candidate may continue after its matching prefix,
// so typo tolerance also works while a user is still typing a word.
func editPrefix(query, word []rune, limit int) int {
	if len(word) < len(query)-limit {
		return 0
	}
	word = word[:min(len(word), len(query)+limit)]
	previous, current, older := make([]int, len(word)+1), make([]int, len(word)+1), make([]int, len(word)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(query); i++ {
		for j := range current {
			current[j] = limit + 1
		}
		current[0] = i
		for j := max(1, i-limit); j <= min(len(word), i+limit); j++ {
			cost := 0
			if query[i-1] != word[j-1] {
				cost = 1
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			if i > 1 && j > 1 && query[i-1] == word[j-2] && query[i-2] == word[j-1] {
				current[j] = min(current[j], older[j-2]+1)
			}
		}
		older, previous, current = previous, current, older
	}
	// Prefer the same length as the query when distances tie.
	best, distance := 0, limit+1
	for j := max(0, len(query)-limit); j <= len(word); j++ {
		if previous[j] < distance || (best > 0 && previous[j] == distance && abs(j-len(query)) < abs(best-len(query))) {
			best, distance = j, previous[j]
		}
	}
	return best
}

func abs(value int) int {
	return max(value, -value)
}
