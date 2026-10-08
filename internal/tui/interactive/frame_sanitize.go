package interactive

import (
	"strings"
	"unicode/utf8"

	"github.com/digitalygo/smidja/internal/tui"
)

const (
	maxSGRParameterLength = 32
	maxOSC8ParamsLength   = 64
)

func SanitizeComponentFrame(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	cleaned := make([]string, len(lines))
	for i, line := range lines {
		cleaned[i] = sanitizeComponentLine(line)
	}
	return cleaned
}

func sanitizeComponentLine(line string) string {
	if line == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(line))
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == 0x1b:
			sequence, width, ok := extractComponentSequence(line, i)
			i += width
			if ok {
				b.WriteString(sequence)
			}
		case c == '\t':
			b.WriteString(tabReplacement)
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			if c >= 0x80 && c <= 0x9f {
				i += c1ComponentSequenceLength(line, i, rune(c))
				continue
			}
			r, size := decodeComponentRune(line[i:])
			if r >= 0x80 && r <= 0x9f && size > 1 {
				i += c1ComponentSequenceLength(line, i, r)
				continue
			}
			if size <= 0 {
				size = 1
			}
			b.WriteString(line[i : i+size])
			i += size
		}
	}
	return b.String()
}

func decodeComponentRune(s string) (rune, int) {
	return utf8.DecodeRuneInString(s)
}

func extractComponentSequence(s string, pos int) (string, int, bool) {
	if pos+1 >= len(s) {
		return "", len(s) - pos, false
	}
	switch s[pos+1] {
	case '[':
		j := pos + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j >= len(s) || s[j] < 0x40 || s[j] > 0x7e {
			return "", len(s) - pos, false
		}
		width := j + 1 - pos
		if s[j] != 'm' || j-(pos+2) > maxSGRParameterLength {
			return "", width, false
		}
		return s[pos : j+1], width, true
	case ']':
		end, width, ok := oscTerminator(s, pos)
		if !ok {
			return "", len(s) - pos, false
		}
		body := s[pos+2 : end]
		canonical, ok := canonicalOSC8(body)
		if !ok {
			return "", width, false
		}
		return canonical, width, true
	default:
		j := pos + 1
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7e {
			j++
		}
		return "", j - pos, false
	}
}

func oscTerminator(s string, pos int) (end int, width int, ok bool) {
	for j := pos + 2; j < len(s); j++ {
		if s[j] == 0x07 {
			return j, j + 1 - pos, true
		}
		if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
			return j, j + 2 - pos, true
		}
		if s[j] == 0xc2 && j+1 < len(s) && s[j+1] == 0x9c {
			return j, j + 2 - pos, true
		}
	}
	return 0, 0, false
}

func canonicalOSC8(body string) (string, bool) {
	if !strings.HasPrefix(body, "8;") {
		return "", false
	}
	rest := body[2:]
	separator := strings.IndexByte(rest, ';')
	if separator < 0 {
		return "", false
	}
	params := rest[:separator]
	target := rest[separator+1:]
	if !safeOSC8Params(params) {
		return "", false
	}
	if target == "" {
		return tui.OSC8Hyperlink(params, ""), true
	}
	safeTarget, ok := safeHyperlinkTarget(target)
	if !ok {
		return "", false
	}
	return tui.OSC8Hyperlink(params, safeTarget), true
}

func safeOSC8Params(params string) bool {
	if params == "" {
		return true
	}
	if len(params) > maxOSC8ParamsLength {
		return false
	}
	for _, part := range strings.Split(params, ":") {
		separator := strings.IndexByte(part, '=')
		if separator <= 0 {
			return false
		}
		for _, r := range part[:separator] {
			if !isOSC8ParamRune(r) {
				return false
			}
		}
		for _, r := range part[separator+1:] {
			if !isOSC8ParamRune(r) {
				return false
			}
		}
	}
	return true
}

func isOSC8ParamRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '_' || r == '-' || r == '.':
		return true
	}
	return false
}

func c1ComponentSequenceLength(s string, pos int, r rune) int {
	size := len(string(r))
	if r == 0x9b {
		j := pos + size
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
			return j + 1 - pos
		}
		return size
	}
	if r == 0x90 || r == 0x98 || r == 0x9d || r == 0x9e || r == 0x9f {
		for j := pos + size; j < len(s); j++ {
			if s[j] == 0x07 || s[j] == 0x9c {
				return j + 1 - pos
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2 - pos
			}
			if s[j] == 0xc2 && j+1 < len(s) && s[j+1] == 0x9c {
				return j + 2 - pos
			}
		}
		return len(s) - pos
	}
	return size
}
