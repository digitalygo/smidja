package tui

import (
	"strings"
)

var cjkBreakRunes = func() map[rune]struct{} {
	m := make(map[rune]struct{})
	ranges := []runeRange{
		{0x1100, 0x11FF}, {0x2E80, 0x303F}, {0x3041, 0x33FF}, {0x3400, 0x4DBF},
		{0x4E00, 0x9FFF}, {0xA000, 0xA4CF}, {0xAC00, 0xD7A3}, {0xF900, 0xFAFF},
		{0x20000, 0x2FFFD}, {0x30000, 0x3FFFD},
	}
	for _, r := range ranges {
		for c := r.lo; c <= r.hi; c++ {
			m[c] = struct{}{}
		}
	}
	return m
}()

func isCJKBreakRune(r rune) bool {
	_, ok := cjkBreakRunes[r]
	return ok
}

func isWhitespaceRune(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func isPunctuationRune(r rune) bool {
	switch r {
	case '(', ')', '{', '}', '[', ']', '<', '>', '.', ',', ';', ':', '\'',
		'"', '!', '?', '+', '-', '=', '*', '/', '\\', '|', '&', '%', '^', '$', '#', '@', '~', '`':
		return true
	}
	return false
}

type textToken struct {
	text    string
	isSpace bool
	cjkSolo bool
}

func splitTokens(text string) []textToken {
	var tokens []textToken
	var current strings.Builder
	var pendingANSI strings.Builder
	currentIsSpace := false
	currentIsCJK := false
	hasCurrent := false

	flush := func() {
		if !hasCurrent {
			return
		}
		tokens = append(tokens, textToken{text: current.String(), isSpace: currentIsSpace, cjkSolo: currentIsCJK})
		current.Reset()
		hasCurrent = false
	}

	for i := 0; i < len(text); {
		code, ok := extractANSI(text, i)
		if ok {
			pendingANSI.WriteString(code.code)
			i += code.length
			continue
		}
		end := i
		for end < len(text) {
			if _, isANSI := extractANSI(text, end); isANSI {
				break
			}
			end++
		}
		for _, g := range splitGraphemes(text[i:end]) {
			first, _ := firstRune(g.text)
			segmentIsSpace := isWhitespaceRune(first) && len(strings.TrimSpace(g.text)) == 0
			if !segmentIsSpace && isCJKBreakRune(first) {
				flush()
				current.WriteString(pendingANSI.String())
				pendingANSI.Reset()
				current.WriteString(g.text)
				currentIsSpace, currentIsCJK, hasCurrent = false, true, true
				flush()
				continue
			}
			if hasCurrent && currentIsSpace != segmentIsSpace {
				flush()
			}
			if pendingANSI.Len() > 0 {
				current.WriteString(pendingANSI.String())
				pendingANSI.Reset()
			}
			currentIsSpace = segmentIsSpace
			currentIsCJK = false
			hasCurrent = true
			current.WriteString(g.text)
		}
		i = end
	}
	if pendingANSI.Len() > 0 {
		if hasCurrent {
			current.WriteString(pendingANSI.String())
		} else if len(tokens) > 0 {
			tokens[len(tokens)-1].text += pendingANSI.String()
		} else {
			current.WriteString(pendingANSI.String())
			hasCurrent = true
		}
	}
	flush()
	return tokens
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, 1
	}
	return 0, 0
}

func breakLongWord(word string, width int, tracker *styleTracker) []string {
	var lines []string
	currentLine := tracker.activeCodes()
	currentWidth := 0

	for _, g := range splitGraphemes(word) {
		gWidth := graphemeWidth(g)
		if currentWidth+gWidth > width {
			if reset := tracker.lineEndReset(); reset != "" {
				currentLine += reset
			}
			lines = append(lines, currentLine)
			currentLine = tracker.activeCodes()
			currentWidth = 0
		}
		currentLine += g.text
		currentWidth += gWidth
		updateTracker(g.text, tracker)
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

func wrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if VisibleWidth(line) <= width {
		return []string{line}
	}

	var wrapped []string
	tracker := &styleTracker{}
	tokens := splitTokens(line)

	currentLine := ""
	currentWidth := 0
	for _, token := range tokens {
		tokenWidth := VisibleWidth(token.text)
		if tokenWidth > width && !token.isSpace {
			if currentLine != "" {
				if reset := tracker.lineEndReset(); reset != "" {
					currentLine += reset
				}
				wrapped = append(wrapped, currentLine)
				currentLine = ""
				currentWidth = 0
			}
			broken := breakLongWord(token.text, width, tracker)
			for i := 0; i < len(broken)-1; i++ {
				wrapped = append(wrapped, broken[i])
			}
			currentLine = broken[len(broken)-1]
			currentWidth = VisibleWidth(currentLine)
			continue
		}

		if currentWidth+tokenWidth > width && currentWidth > 0 {
			lineToWrap := strings.TrimRight(currentLine, " \t\n\v\f\r")
			if reset := tracker.lineEndReset(); reset != "" {
				lineToWrap += reset
			}
			wrapped = append(wrapped, lineToWrap)
			if token.isSpace {
				currentLine = tracker.activeCodes()
				currentWidth = 0
			} else {
				currentLine = tracker.activeCodes() + token.text
				currentWidth = tokenWidth
			}
		} else {
			currentLine += token.text
			currentWidth += tokenWidth
		}
		updateTracker(token.text, tracker)
	}

	if currentLine != "" {
		wrapped = append(wrapped, currentLine)
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	for i, line := range wrapped {
		wrapped[i] = strings.TrimRight(line, " \t\n\v\f\r")
	}
	return wrapped
}

func WrapTextWithANSI(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	inputLines := splitLines(text)
	result := make([]string, 0, len(inputLines))
	tracker := &styleTracker{}
	for _, inputLine := range inputLines {
		prefix := ""
		if len(result) > 0 {
			prefix = tracker.activeCodes()
		}
		result = append(result, wrapSingleLine(prefix+inputLine, width)...)
		updateTracker(inputLine, tracker)
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func splitLines(text string) []string {
	if !strings.ContainsAny(text, "\r\n") {
		return []string{text}
	}
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			end := i
			if end > start && text[end-1] == '\r' {
				end--
			}
			lines = append(lines, text[start:end])
			start = i + 1
		} else if text[i] == '\r' && (i+1 >= len(text) || text[i+1] != '\n') {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	lines = append(lines, text[start:])
	return lines
}

func ApplyBackgroundToLine(line string, width int, bgFn func(string) string) string {
	visible := VisibleWidth(line)
	padding := strings.Repeat(" ", maxInt(0, width-visible))
	return bgFn(line + padding)
}
