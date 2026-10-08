package tui

import "strings"

type wordSegment struct {
	text     string
	wordLike bool
	space    bool
}

func wordSegments(text string) []wordSegment {
	graphemes := splitGraphemes(text)
	segments := make([]wordSegment, 0, len(graphemes))
	for _, g := range graphemes {
		first, _ := firstRune(g.text)
		isSpace := isWhitespaceRune(first) && len(strings.TrimSpace(g.text)) == 0
		isWord := !isSpace && isWordRune(first)
		if isWord && len(segments) > 0 && segments[len(segments)-1].wordLike {
			segments[len(segments)-1].text += g.text
			continue
		}
		if isSpace && len(segments) > 0 && segments[len(segments)-1].space {
			segments[len(segments)-1].text += g.text
			continue
		}
		segments = append(segments, wordSegment{text: g.text, wordLike: isWord, space: isSpace})
	}
	return segments
}

func findWordBackward(text string, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	segments := wordSegments(text[:cursor])
	newCursor := cursor
	for len(segments) > 0 && segments[len(segments)-1].space {
		newCursor -= len(segments[len(segments)-1].text)
		segments = segments[:len(segments)-1]
	}
	if len(segments) == 0 {
		return newCursor
	}
	last := segments[len(segments)-1]
	if last.wordLike {
		punctuationIndex := -1
		runes := []rune(last.text)
		for i, r := range runes {
			if isPunctuationRune(r) {
				punctuationIndex = i
			}
		}
		if punctuationIndex < 0 {
			return newCursor - len(last.text)
		}
		return newCursor - (len(runes) - (punctuationIndex + runeLen(runes[punctuationIndex])))
	}
	for len(segments) > 0 {
		segment := segments[len(segments)-1]
		if segment.wordLike || segment.space {
			break
		}
		newCursor -= len(segment.text)
		segments = segments[:len(segments)-1]
	}
	return newCursor
}

func findWordForward(text string, cursor int) int {
	if cursor >= len(text) {
		return len(text)
	}
	segments := wordSegments(text[cursor:])
	newCursor := cursor
	for len(segments) > 0 && segments[0].space {
		newCursor += len(segments[0].text)
		segments = segments[1:]
	}
	if len(segments) == 0 {
		return newCursor
	}
	first := segments[0]
	if first.wordLike {
		runes := []rune(first.text)
		for i, r := range runes {
			if isPunctuationRune(r) {
				return newCursor + len(string(runes[:i]))
			}
		}
		return newCursor + len(first.text)
	}
	for len(segments) > 0 {
		segment := segments[0]
		if segment.wordLike || segment.space {
			break
		}
		newCursor += len(segment.text)
		segments = segments[1:]
	}
	return newCursor
}

func runeLen(r rune) int {
	if r < 0x80 {
		return 1
	}
	return len(string(r))
}

func isWordRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	return r >= 0x80 && !isPunctuationRune(r) && !isWhitespaceRune(r)
}

type killRing struct {
	ring []string
}

func (k *killRing) push(text string, prepend bool, accumulate bool) {
	if text == "" {
		return
	}
	if accumulate && len(k.ring) > 0 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		if prepend {
			k.ring = append(k.ring, text+last)
		} else {
			k.ring = append(k.ring, last+text)
		}
		return
	}
	k.ring = append(k.ring, text)
}

func (k *killRing) peek() (string, bool) {
	if len(k.ring) == 0 {
		return "", false
	}
	return k.ring[len(k.ring)-1], true
}

func (k *killRing) rotate() {
	if len(k.ring) > 1 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		k.ring = append([]string{last}, k.ring...)
	}
}

func (k *killRing) length() int { return len(k.ring) }

type undoStack[T any] struct {
	stack []T
}

func (u *undoStack[T]) push(state T) {
	u.stack = append(u.stack, state)
}

func (u *undoStack[T]) pop() (T, bool) {
	var zero T
	if len(u.stack) == 0 {
		return zero, false
	}
	state := u.stack[len(u.stack)-1]
	u.stack = u.stack[:len(u.stack)-1]
	return state, true
}

func (u *undoStack[T]) clear() {
	u.stack = nil
}

func (u *undoStack[T]) length() int { return len(u.stack) }

func trailingTrim(s string) string {
	return strings.TrimRight(s, " \t\n\v\f\r")
}
