package telegram

import (
	"fmt"
	"hash/fnv"
	"strings"
)

func chunkText(text string, max int) []string {
	if max <= 0 {
		max = legacyChunkMax
	}
	runes := []rune(text)
	if len(runes) <= max {
		return []string{text}
	}
	chunks := make([]string, 0, len(runes)/max+2)
	start := 0
	for start < len(runes) {
		end := start + max
		if end > len(runes) {
			end = len(runes)
		}
		cut := boundaryBefore(runes, start, end)
		chunks = append(chunks, string(runes[start:cut]))
		start = cut
		for start < len(runes) && isBoundaryRune(runes[start]) {
			start++
		}
	}
	return chunks
}

func boundaryBefore(runes []rune, start, end int) int {
	for i := end - 1; i > start; i-- {
		if isBoundaryRune(runes[i]) {
			return i
		}
	}
	return end
}

func isBoundaryRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func draftParts(text string, maxParts int) []string {
	if maxParts < 2 {
		return nil
	}
	raw := strings.Split(text, "\n\n")
	paragraphs := raw[:0]
	for _, p := range raw {
		if strings.TrimSpace(p) != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	if len(paragraphs) < 2 {
		return nil
	}
	parts := make([]string, 0, maxParts-1)
	for i := 1; i < len(paragraphs) && len(parts) < maxParts-1; i++ {
		parts = append(parts, strings.Join(paragraphs[:i], "\n\n"))
	}
	return parts
}

func draftIDFor(chatID int64, deliveryID string) int {
	h := fnv.New32a()
	fmt.Fprintf(h, "%d:%s", chatID, deliveryID)
	id := int(h.Sum32())
	if id == 0 {
		return 1
	}
	return id
}
