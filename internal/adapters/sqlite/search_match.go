package sqlite

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
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
	haystack, needle := foldSearch(text), foldSearch(query)
	if needle == "" {
		return false
	}
	if strings.Contains(haystack, needle) {
		return true
	}
	terms := searchWords(needle)
	if len(terms) == 0 {
		return false
	}
	words := searchWords(haystack)
	for _, term := range terms {
		if strings.Contains(haystack, term) {
			continue
		}
		runes := []rune(term)
		letters := false
		for _, char := range runes {
			letters = letters || unicode.IsLetter(char)
		}
		if len(runes) < 4 || !letters {
			return false
		}
		limit := 1
		if len(runes) >= 8 {
			limit = 2
		}
		matched := false
		for _, word := range words {
			if editPrefix(runes, []rune(word), limit) {
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

func foldSearch(value string) string {
	value = norm.NFKD.String(cases.Fold().String(value))
	return strings.Map(func(char rune) rune {
		if unicode.Is(unicode.Mn, char) {
			return -1
		}
		return char
	}, value)
}

func searchWords(value string) []string {
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

// editPrefix is a banded optimal-string-alignment distance. Adjacent swapped
// characters cost one edit. A candidate may continue after its matching prefix,
// so typo tolerance also works while a user is still typing a word.
func editPrefix(query, word []rune, limit int) bool {
	if len(word) < len(query)-limit {
		return false
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
	for j := max(0, len(query)-limit); j <= len(word); j++ {
		if previous[j] <= limit {
			return true
		}
	}
	return false
}
