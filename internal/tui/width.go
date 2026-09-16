package tui

import (
	"strings"
	"unicode/utf8"
)

const tabWidth = 3

type ansiCode struct {
	code   string
	length int
}

func extractANSI(s string, pos int) (ansiCode, bool) {
	if pos >= len(s) || s[pos] != 0x1b {
		return ansiCode{}, false
	}
	if pos+1 >= len(s) {
		return ansiCode{}, false
	}
	switch s[pos+1] {
	case '[':
		j := pos + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
			return ansiCode{code: s[pos : j+1], length: j + 1 - pos}, true
		}
		return ansiCode{}, false
	case ']', '_':
		j := pos + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return ansiCode{code: s[pos : j+1], length: j + 1 - pos}, true
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return ansiCode{code: s[pos : j+2], length: j + 2 - pos}, true
			}
			j++
		}
		return ansiCode{}, false
	}
	return ansiCode{}, false
}

func forEachANSI(s string, fn func(code ansiCode) bool) bool {
	for i := 0; i < len(s); {
		if code, ok := extractANSI(s, i); ok {
			if !fn(code) {
				return false
			}
			i += code.length
			continue
		}
		i++
	}
	return true
}

func StripTerminalSequences(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if code, ok := extractANSI(s, i); ok {
			i += code.length
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func normalizeTerminalOutput(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if code, ok := extractANSI(s, i); ok {
			b.WriteString(code.code)
			i += code.length
			continue
		}
		if s[i] == '\t' {
			b.WriteString("   ")
		} else {
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}

type hyperlinkState struct {
	params     string
	url        string
	belTermEnd bool
}

func parseOsc8(code string) (hyperlinkState, bool) {
	if !strings.HasPrefix(code, "\x1b]8;") {
		return hyperlinkState{}, false
	}
	belTerminated := strings.HasSuffix(code, ANSIBEL)
	stTerminated := strings.HasSuffix(code, ANSIST)
	if !belTerminated && !stTerminated {
		return hyperlinkState{}, false
	}
	trim := 1
	if stTerminated {
		trim = 2
	}
	body := code[4 : len(code)-trim]
	sep := strings.IndexByte(body, ';')
	if sep < 0 {
		return hyperlinkState{}, false
	}
	return hyperlinkState{params: body[:sep], url: body[sep+1:], belTermEnd: belTerminated}, true
}

func (h hyperlinkState) open() string {
	return "\x1b]8;" + h.params + ";" + h.url + "\x07"
}

func (h hyperlinkState) close() string {
	if h.belTermEnd {
		return OSC8Close
	}
	return OSC8CloseST
}

type styleTracker struct {
	bold      bool
	dim       bool
	italic    bool
	underline bool
	blink     bool
	inverse   bool
	hidden    bool
	strike    bool
	fg        string
	bg        string
	link      hyperlinkState
	hasLink   bool
}

func (t *styleTracker) reset() {
	t.bold, t.dim, t.italic, t.underline = false, false, false, false
	t.blink, t.inverse, t.hidden, t.strike = false, false, false, false
	t.fg, t.bg = "", ""
}

func (t *styleTracker) process(code string) {
	if link, ok := parseOsc8(code); ok {
		if link.url == "" {
			t.link, t.hasLink = hyperlinkState{}, false
		} else {
			t.link, t.hasLink = link, true
		}
		return
	}
	if !strings.HasSuffix(code, "m") || !strings.HasPrefix(code, "\x1b[") {
		return
	}
	params := code[2 : len(code)-1]
	if params == "" || params == "0" {
		t.reset()
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		code := parseSGRInt(parts[i])
		switch code {
		case 38, 48:
			if i+2 < len(parts) && parts[i+1] == "5" {
				value := parts[i] + ";" + parts[i+1] + ";" + parts[i+2]
				i += 2
				if code == 38 {
					t.fg = value
				} else {
					t.bg = value
				}
			} else if i+4 < len(parts) && parts[i+1] == "2" {
				value := parts[i] + ";" + parts[i+1] + ";" + parts[i+2] + ";" + parts[i+3] + ";" + parts[i+4]
				i += 4
				if code == 38 {
					t.fg = value
				} else {
					t.bg = value
				}
			}
		case 0:
			t.reset()
		case 1:
			t.bold = true
		case 2:
			t.dim = true
		case 3:
			t.italic = true
		case 4:
			t.underline = true
		case 5:
			t.blink = true
		case 7:
			t.inverse = true
		case 8:
			t.hidden = true
		case 9:
			t.strike = true
		case 21:
			t.bold = false
		case 22:
			t.bold, t.dim = false, false
		case 23:
			t.italic = false
		case 24:
			t.underline = false
		case 25:
			t.blink = false
		case 27:
			t.inverse = false
		case 28:
			t.hidden = false
		case 29:
			t.strike = false
		case 39:
			t.fg = ""
		case 49:
			t.bg = ""
		default:
			if (code >= 30 && code <= 37) || (code >= 90 && code <= 97) {
				t.fg = parts[i]
			} else if (code >= 40 && code <= 47) || (code >= 100 && code <= 107) {
				t.bg = parts[i]
			}
		}
	}
}

func parseSGRInt(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return -1
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func (t *styleTracker) activeCodes() string {
	var codes []string
	if t.bold {
		codes = append(codes, "1")
	}
	if t.dim {
		codes = append(codes, "2")
	}
	if t.italic {
		codes = append(codes, "3")
	}
	if t.underline {
		codes = append(codes, "4")
	}
	if t.blink {
		codes = append(codes, "5")
	}
	if t.inverse {
		codes = append(codes, "7")
	}
	if t.hidden {
		codes = append(codes, "8")
	}
	if t.strike {
		codes = append(codes, "9")
	}
	if t.fg != "" {
		codes = append(codes, t.fg)
	}
	if t.bg != "" {
		codes = append(codes, t.bg)
	}
	result := ""
	if len(codes) > 0 {
		result = "\x1b[" + strings.Join(codes, ";") + "m"
	}
	if t.hasLink {
		result += t.link.open()
	}
	return result
}

func (t *styleTracker) lineEndReset() string {
	result := ""
	if t.underline {
		result += SGRUnderlineOff
	}
	if t.hasLink {
		result += t.link.close()
	}
	return result
}

func updateTracker(s string, tracker *styleTracker) {
	forEachANSI(s, func(code ansiCode) bool {
		tracker.process(code.code)
		return true
	})
}

type grapheme struct {
	text  string
	width int
}

func runeWidth(r rune) int {
	switch {
	case isWideRune(r):
		return 2
	case isZeroWidthRune(r):
		return 0
	case r < 0x20 || (r >= 0x7f && r <= 0x9f):
		return 0
	default:
		return 1
	}
}

func splitGraphemes(s string) []grapheme {
	var out []grapheme
	var cur strings.Builder
	curWidth := 0
	regionalOpen := false
	prevWasJoiner := false

	flush := func() {
		if cur.Len() > 0 {
			text := cur.String()
			width := curWidth
			if width == 1 && strings.ContainsRune(text, 0xFE0F) {
				width = 2
			}
			out = append(out, grapheme{text: text, width: width})
			cur.Reset()
			curWidth = 0
		}
	}

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case regionalOpen && isRegionalIndicator(r):
			cur.WriteRune(r)
			curWidth = 2
			regionalOpen = false
			flush()
		case isRegionalIndicator(r):
			flush()
			cur.WriteRune(r)
			curWidth = 2
			regionalOpen = true
		case isVariationSelector(r) || isCombiningMark(r) || isGraphemeJoiner(r):
			cur.WriteRune(r)
			prevWasJoiner = isGraphemeJoiner(r)
		case prevWasJoiner:
			cur.WriteRune(r)
			prevWasJoiner = false
		default:
			flush()
			cur.WriteRune(r)
			if r == '\t' {
				curWidth = tabWidth
			} else {
				curWidth = runeWidth(r)
			}
		}
	}
	flush()
	return out
}

func graphemeWidth(g grapheme) int {
	if g.text == "\t" {
		return tabWidth
	}
	return g.width
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func VisibleWidth(s string) int {
	if len(s) == 0 {
		return 0
	}
	if isPrintableASCII(s) {
		return len(s)
	}
	stripped := StripTerminalSequences(normalizeTerminalOutput(s))
	total := 0
	for _, g := range splitGraphemes(stripped) {
		total += graphemeWidth(g)
	}
	return total
}

type slicedText struct {
	text  string
	width int
}

func sliceByColumns(line string, startCol, length int, strict bool) slicedText {
	if length <= 0 {
		return slicedText{}
	}
	endCol := startCol + length
	var result strings.Builder
	resultWidth := 0
	currentCol := 0
	var pendingANSI strings.Builder

	i := 0
	for i < len(line) {
		code, ok := extractANSI(line, i)
		if ok {
			if currentCol >= startCol && currentCol < endCol {
				result.WriteString(code.code)
			} else if currentCol < startCol {
				pendingANSI.WriteString(code.code)
			}
			i += code.length
			continue
		}
		textEnd := i
		for textEnd < len(line) {
			if _, isANSI := extractANSI(line, textEnd); isANSI {
				break
			}
			textEnd++
		}
		for _, g := range splitGraphemes(line[i:textEnd]) {
			w := graphemeWidth(g)
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strict || currentCol+w <= endCol
			if inRange && fits {
				if pendingANSI.Len() > 0 {
					result.WriteString(pendingANSI.String())
					pendingANSI.Reset()
				}
				result.WriteString(g.text)
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				break
			}
		}
		i = textEnd
		if currentCol >= endCol {
			break
		}
	}
	return slicedText{text: result.String(), width: resultWidth}
}

func SliceByColumn(line string, startCol, length int, strict bool) string {
	return sliceByColumns(line, startCol, length, strict).text
}

func truncateFragment(text string, maxWidth int) slicedText {
	if maxWidth <= 0 || text == "" {
		return slicedText{}
	}
	if isPrintableASCII(text) {
		limit := len(text)
		if limit > maxWidth {
			limit = maxWidth
		}
		return slicedText{text: text[:limit], width: limit}
	}
	var result strings.Builder
	width := 0
	for _, g := range splitGraphemes(text) {
		w := graphemeWidth(g)
		if width+w > maxWidth {
			break
		}
		result.WriteString(g.text)
		width += w
	}
	return slicedText{text: result.String(), width: width}
}

func activeOsc8Close(prefix string) string {
	found := false
	var link hyperlinkState
	forEachANSI(prefix, func(code ansiCode) bool {
		if linkState, ok := parseOsc8(code.code); ok {
			if linkState.url == "" {
				link, found = hyperlinkState{}, false
			} else {
				link, found = linkState, true
			}
		}
		return true
	})
	if found {
		return link.close()
	}
	return ""
}

func finalizeTruncated(prefix string, prefixWidth int, ellipsis string, ellipsisWidth, maxWidth int, pad bool) string {
	visible := prefixWidth + ellipsisWidth
	result := prefix + activeOsc8Close(prefix) + SGRReset
	if ellipsis != "" {
		result += ellipsis + SGRReset
	}
	if pad {
		result += strings.Repeat(" ", maxInt(0, maxWidth-visible))
	}
	return result
}

func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	if maxWidth <= 0 {
		return ""
	}
	if text == "" {
		if pad {
			return strings.Repeat(" ", maxWidth)
		}
		return ""
	}
	ellipsisWidth := VisibleWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		if VisibleWidth(text) <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-VisibleWidth(text))
			}
			return text
		}
		clipped := truncateFragment(ellipsis, maxWidth)
		if clipped.width == 0 {
			if pad {
				return strings.Repeat(" ", maxWidth)
			}
			return ""
		}
		return finalizeTruncated("", 0, clipped.text, clipped.width, maxWidth, pad)
	}
	if isPrintableASCII(text) {
		if len(text) <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-len(text))
			}
			return text
		}
		target := maxWidth - ellipsisWidth
		return finalizeTruncated(text[:target], target, ellipsis, ellipsisWidth, maxWidth, pad)
	}

	targetWidth := maxWidth - ellipsisWidth
	var result strings.Builder
	var pendingANSI strings.Builder
	visibleSoFar := 0
	keptWidth := 0
	keepPrefix := true
	overflowed := false

	i := 0
	for i < len(text) {
		code, ok := extractANSI(text, i)
		if ok {
			if keepPrefix {
				pendingANSI.WriteString(code.code)
			}
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
		for _, gr := range splitGraphemes(text[i:end]) {
			w := graphemeWidth(gr)
			if keepPrefix && keptWidth+w <= targetWidth {
				if pendingANSI.Len() > 0 {
					result.WriteString(pendingANSI.String())
					pendingANSI.Reset()
				}
				result.WriteString(gr.text)
				keptWidth += w
			} else {
				keepPrefix = false
				pendingANSI.Reset()
			}
			visibleSoFar += w
			if visibleSoFar > maxWidth {
				overflowed = true
				break
			}
		}
		i = end
		if overflowed {
			break
		}
	}
	if !overflowed && i >= len(text) {
		if pad {
			return text + strings.Repeat(" ", maxInt(0, maxWidth-visibleSoFar))
		}
		return text
	}
	return finalizeTruncated(result.String(), keptWidth, ellipsis, ellipsisWidth, maxWidth, pad)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
