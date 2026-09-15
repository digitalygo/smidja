package tui

import (
	"strings"
	"unicode"
)

type fuzzyResult struct {
	matches bool
	score   int
}

func fuzzyMatch(query, text string) fuzzyResult {
	queryLower := strings.ToLower(query)
	textLower := strings.ToLower(text)

	match := func(normalized string) fuzzyResult {
		if normalized == "" {
			return fuzzyResult{matches: true, score: 0}
		}
		if len(normalized) > len(textLower) {
			return fuzzyResult{matches: false}
		}
		queryIndex := 0
		score := 0
		lastMatchIndex := -1
		consecutive := 0
		runes := []rune(textLower)
		queryRunes := []rune(normalized)
		for i := 0; i < len(runes) && queryIndex < len(queryRunes); i++ {
			if runes[i] != queryRunes[queryIndex] {
				continue
			}
			isWordBoundary := i == 0 || isFuzzyBoundary(runes[i-1])
			if lastMatchIndex == i-1 {
				consecutive++
				score -= consecutive * 5
			} else {
				consecutive = 0
				if lastMatchIndex >= 0 {
					score += (i - lastMatchIndex - 1) * 2
				}
			}
			if isWordBoundary {
				score -= 10
			}
			score += i / 10
			lastMatchIndex = i
			queryIndex++
		}
		if queryIndex < len(queryRunes) {
			return fuzzyResult{matches: false}
		}
		if normalized == textLower {
			score -= 100
		}
		return fuzzyResult{matches: true, score: score}
	}

	primary := match(queryLower)
	if primary.matches {
		return primary
	}

	letters, digits := splitAlphaNumeric(queryLower)
	if letters == "" && digits == "" {
		return primary
	}
	swapped := digits + letters
	if swapped == queryLower {
		return primary
	}
	swappedMatch := match(swapped)
	if !swappedMatch.matches {
		return primary
	}
	return fuzzyResult{matches: true, score: swappedMatch.score + 5}
}

func isFuzzyBoundary(r rune) bool {
	switch r {
	case ' ', '-', '_', '.', '/', ':':
		return true
	}
	return false
}

func splitAlphaNumeric(query string) (letters, digits string) {
	if query == "" {
		return "", ""
	}
	runes := []rune(query)
	i := 0
	for i < len(runes) && unicode.IsLetter(runes[i]) {
		i++
	}
	lettersEnd := i
	if lettersEnd == 0 || lettersEnd == len(runes) {
		return "", ""
	}
	j := lettersEnd
	for j < len(runes) && unicode.IsDigit(runes[j]) {
		j++
	}
	if j != len(runes) {
		return "", ""
	}
	return string(runes[:lettersEnd]), string(runes[lettersEnd:])
}

func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	if strings.TrimSpace(query) == "" {
		return items
	}
	tokens := strings.FieldsFunc(strings.TrimSpace(query), func(r rune) bool {
		return r == ' ' || r == '/' || r == '\t'
	})
	if len(tokens) == 0 {
		return items
	}
	type scored struct {
		item  T
		score int
	}
	var results []scored
	for _, item := range items {
		text := getText(item)
		total := 0
		allMatch := true
		for _, token := range tokens {
			result := fuzzyMatch(token, text)
			if !result.matches {
				allMatch = false
				break
			}
			total += result.score
		}
		if allMatch {
			results = append(results, scored{item: item, score: total})
		}
	}
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score < results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	filtered := make([]T, 0, len(results))
	for _, result := range results {
		filtered = append(filtered, result.item)
	}
	return filtered
}
