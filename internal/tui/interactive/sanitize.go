package interactive

import (
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
)

const tabReplacement = "   "

func isControlRune(r rune) bool {
	return (r >= 0x00 && r <= 0x1f) || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func SanitizeDisplayText(text string) string {
	if text == "" {
		return ""
	}
	stripped := tui.StripTerminalSequences(text)
	if !strings.ContainsFunc(stripped, func(r rune) bool { return r == '\t' || isControlRune(r) }) {
		return stripped
	}
	var b strings.Builder
	b.Grow(len(stripped))
	for _, r := range stripped {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(tabReplacement)
		case isControlRune(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func SanitizeSingleLine(text string) string {
	sanitized := SanitizeDisplayText(text)
	if !strings.Contains(sanitized, "\n") {
		return sanitized
	}
	var b strings.Builder
	previousWasNewline := false
	for _, r := range sanitized {
		if r == '\n' {
			if !previousWasNewline {
				b.WriteByte(' ')
			}
			previousWasNewline = true
			continue
		}
		previousWasNewline = false
		b.WriteRune(r)
	}
	return b.String()
}

func sanitizeFrames(frames []string) []string {
	cleaned := make([]string, len(frames))
	for i, frame := range frames {
		cleaned[i] = SanitizeSingleLine(frame)
	}
	return cleaned
}

func SanitizeOSC8Target(target string) string {
	if !strings.ContainsFunc(target, func(r rune) bool { return isControlRune(r) }) {
		return target
	}
	var b strings.Builder
	b.Grow(len(target))
	for _, r := range target {
		if isControlRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func safeHyperlinkTarget(target string) (string, bool) {
	cleaned := SanitizeOSC8Target(strings.TrimSpace(target))
	if cleaned == "" {
		return "", false
	}
	lower := strings.ToLower(cleaned)
	for _, scheme := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(lower, scheme) {
			return cleaned, true
		}
	}
	return cleaned, false
}
