package tui

import (
	"strings"
)

const editorMaxLineCols = 4096

const editorMaxUndo = 200

type editorVisualLine struct {
	logicalLine int
	startCol    int
	length      int
}

type editorUndoSnapshot struct {
	lines        []string
	cursorLine   int
	cursorCol    int
	pastes       map[int]string
	pasteCounter int
}

type editorBuffer struct {
	lines              []string
	cursorLine         int
	cursorCol          int
	preferredVisualCol int
	hasPreferred       bool
	undo               []editorUndoSnapshot
	kill               killRing
	lastAction         string
	pastes             map[int]string
	pasteCounter       int
}

func newEditorBuffer() *editorBuffer {
	return &editorBuffer{
		lines:  []string{""},
		pastes: map[int]string{},
	}
}

func normalizeEditorText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	return s
}

func filterPastedText(s string) string {
	s = normalizeEditorText(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' {
			b.WriteRune(r)
			continue
		}
		if r < 32 || r == 127 || (r >= 128 && r <= 159) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func formatPasteMarker(id, count int, isLines bool) string {
	if isLines {
		return "[paste #" + itoa(id) + " +" + itoa(count) + " lines]"
	}
	return "[paste #" + itoa(id) + " " + itoa(count) + " chars]"
}

func parsePasteMarkerID(s string) (int, bool) {
	if !strings.HasPrefix(s, "[paste #") {
		return 0, false
	}
	rest := s[len("[paste #"):]
	digits := ""
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c < '0' || c > '9' {
			break
		}
		digits += string(c)
	}
	if digits == "" {
		return 0, false
	}
	if !strings.HasSuffix(s, "]") {
		return 0, false
	}
	n := 0
	for i := 0; i < len(digits); i++ {
		n = n*10 + int(digits[i]-'0')
	}
	return n, true
}

func findPasteMarkerSpans(line string, valid map[int]struct{}) [][2]int {
	var spans [][2]int
	searchFrom := 0
	for searchFrom < len(line) {
		idx := strings.Index(line[searchFrom:], "[paste #")
		if idx < 0 {
			break
		}
		start := searchFrom + idx
		endRel := strings.Index(line[start:], "]")
		if endRel < 0 {
			break
		}
		end := start + endRel + 1
		candidate := line[start:end]
		id, ok := parsePasteMarkerID(candidate)
		if !ok {
			searchFrom = start + 1
			continue
		}
		if valid != nil {
			if _, exists := valid[id]; !exists {
				searchFrom = end
				continue
			}
		}
		spans = append(spans, [2]int{start, end})
		searchFrom = end
	}
	return spans
}

func (b *editorBuffer) validPasteSet() map[int]struct{} {
	set := make(map[int]struct{}, len(b.pastes))
	for id := range b.pastes {
		set[id] = struct{}{}
	}
	return set
}

func (b *editorBuffer) Text() string {
	return strings.Join(b.lines, "\n")
}

func (b *editorBuffer) ExpandedText() string {
	joined := strings.Join(b.lines, "\n")
	if len(b.pastes) == 0 || !strings.Contains(joined, "[paste #") {
		return joined
	}
	result := joined
	for id, content := range b.pastes {
		markerLines := strings.Split(content, "\n")
		var marker string
		if len(markerLines) > 10 {
			marker = formatPasteMarker(id, len(markerLines), true)
		} else {
			marker = formatPasteMarker(id, len(content), false)
		}
		result = strings.ReplaceAll(result, marker, content)
	}
	return result
}

func (b *editorBuffer) Lines() []string {
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

func (b *editorBuffer) Cursor() (int, int) {
	return b.cursorLine, b.cursorCol
}

func (b *editorBuffer) SetCursor(line, col int) {
	if line < 0 {
		line = 0
	}
	if line >= len(b.lines) {
		line = len(b.lines) - 1
	}
	if col < 0 {
		col = 0
	}
	if col > len(b.lines[line]) {
		col = len(b.lines[line])
	}
	b.cursorLine = line
	b.cursorCol = col
	b.hasPreferred = false
}

func (b *editorBuffer) pushUndo() {
	snapshot := editorUndoSnapshot{
		lines:        append([]string(nil), b.lines...),
		cursorLine:   b.cursorLine,
		cursorCol:    b.cursorCol,
		pastes:       make(map[int]string, len(b.pastes)),
		pasteCounter: b.pasteCounter,
	}
	for k, v := range b.pastes {
		snapshot.pastes[k] = v
	}
	b.undo = append(b.undo, snapshot)
	if len(b.undo) > editorMaxUndo {
		b.undo = b.undo[len(b.undo)-editorMaxUndo:]
	}
}

func (b *editorBuffer) Undo() {
	if len(b.undo) == 0 {
		return
	}
	snapshot := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.lines = snapshot.lines
	b.cursorLine = snapshot.cursorLine
	b.cursorCol = snapshot.cursorCol
	b.pastes = snapshot.pastes
	b.pasteCounter = snapshot.pasteCounter
	b.lastAction = ""
	b.hasPreferred = false
}

func (b *editorBuffer) SetText(s string) {
	normalized := normalizeEditorText(s)
	if b.Text() != normalized {
		b.pushUndo()
	}
	b.pastes = map[int]string{}
	b.pasteCounter = 0
	b.lastAction = ""
	b.hasPreferred = false
	if normalized == "" {
		b.lines = []string{""}
	} else {
		b.lines = strings.Split(normalized, "\n")
	}
	b.cursorLine = len(b.lines) - 1
	b.cursorCol = len(b.lines[b.cursorLine])
}

func (b *editorBuffer) insertTextInternal(s string) {
	if s == "" {
		return
	}
	inserted := strings.Split(s, "\n")
	current := b.lines[b.cursorLine]
	before := current[:b.cursorCol]
	after := current[b.cursorCol:]
	if len(inserted) == 1 {
		b.lines[b.cursorLine] = before + s + after
		b.cursorCol += len(s)
		return
	}
	expanded := make([]string, 0, len(b.lines)+len(inserted)-1)
	expanded = append(expanded, b.lines[:b.cursorLine]...)
	expanded = append(expanded, before+inserted[0])
	if len(inserted) > 2 {
		expanded = append(expanded, inserted[1:len(inserted)-1]...)
	}
	expanded = append(expanded, inserted[len(inserted)-1]+after)
	expanded = append(expanded, b.lines[b.cursorLine+1:]...)
	b.lines = expanded
	b.cursorLine += len(inserted) - 1
	b.cursorCol = len(inserted[len(inserted)-1])
}

func (b *editorBuffer) InsertTextAtCursor(s string) {
	if s == "" {
		return
	}
	b.pushUndo()
	b.lastAction = ""
	b.insertTextInternal(normalizeEditorText(s))
}

func (b *editorBuffer) InsertChar(c string) {
	if c == "" {
		return
	}
	if isWhitespaceRune(firstRuneOf(c)) || b.lastAction != "type-word" {
		b.pushUndo()
	}
	b.lastAction = "type-word"
	b.hasPreferred = false
	b.insertTextInternal(c)
}

func (b *editorBuffer) Newline() {
	b.pushUndo()
	b.lastAction = ""
	b.hasPreferred = false
	current := b.lines[b.cursorLine]
	before := current[:b.cursorCol]
	after := current[b.cursorCol:]
	b.lines[b.cursorLine] = before
	rest := make([]string, 0, len(b.lines)+1)
	rest = append(rest, b.lines[:b.cursorLine+1]...)
	rest = append(rest, after)
	rest = append(rest, b.lines[b.cursorLine+1:]...)
	b.lines = rest
	b.cursorLine++
	b.cursorCol = 0
}

func (b *editorBuffer) MoveLeft() {
	b.lastAction = ""
	b.hasPreferred = false
	if b.cursorCol > 0 {
		spans := findPasteMarkerSpans(b.lines[b.cursorLine], b.validPasteSet())
		for _, span := range spans {
			if b.cursorCol > span[0] && b.cursorCol <= span[1] {
				b.cursorCol = span[0]
				return
			}
		}
		before := b.lines[b.cursorLine][:b.cursorCol]
		graphemes := splitGraphemes(before)
		if len(graphemes) > 0 {
			b.cursorCol -= len(graphemes[len(graphemes)-1].text)
		} else {
			b.cursorCol--
		}
		return
	}
	if b.cursorLine > 0 {
		b.cursorLine--
		b.cursorCol = len(b.lines[b.cursorLine])
	}
}

func (b *editorBuffer) MoveRight() {
	b.lastAction = ""
	b.hasPreferred = false
	line := b.lines[b.cursorLine]
	if b.cursorCol < len(line) {
		spans := findPasteMarkerSpans(line, b.validPasteSet())
		for _, span := range spans {
			if b.cursorCol >= span[0] && b.cursorCol < span[1] {
				b.cursorCol = span[1]
				return
			}
		}
		after := line[b.cursorCol:]
		graphemes := splitGraphemes(after)
		if len(graphemes) > 0 {
			b.cursorCol += len(graphemes[0].text)
		} else {
			b.cursorCol++
		}
		return
	}
	if b.cursorLine < len(b.lines)-1 {
		b.cursorLine++
		b.cursorCol = 0
	}
}

func (b *editorBuffer) MoveWordLeft() {
	b.lastAction = ""
	b.hasPreferred = false
	if b.cursorCol == 0 {
		if b.cursorLine > 0 {
			b.cursorLine--
			b.cursorCol = len(b.lines[b.cursorLine])
		}
		return
	}
	b.cursorCol = findWordBackward(b.lines[b.cursorLine], b.cursorCol)
}

func (b *editorBuffer) MoveWordRight() {
	b.lastAction = ""
	b.hasPreferred = false
	line := b.lines[b.cursorLine]
	if b.cursorCol >= len(line) {
		if b.cursorLine < len(b.lines)-1 {
			b.cursorLine++
			b.cursorCol = 0
		}
		return
	}
	b.cursorCol = findWordForward(line, b.cursorCol)
}

func (b *editorBuffer) MoveLineStart() {
	b.lastAction = ""
	b.hasPreferred = false
	b.cursorCol = 0
}

func (b *editorBuffer) MoveLineEnd() {
	b.lastAction = ""
	b.hasPreferred = false
	b.cursorCol = len(b.lines[b.cursorLine])
}

func (b *editorBuffer) MoveBufferStart() {
	b.lastAction = ""
	b.hasPreferred = false
	b.cursorLine = 0
	b.cursorCol = 0
}

func (b *editorBuffer) MoveBufferEnd() {
	b.lastAction = ""
	b.hasPreferred = false
	b.cursorLine = len(b.lines) - 1
	b.cursorCol = len(b.lines[b.cursorLine])
}

func (b *editorBuffer) cropForLayout(line string) string {
	if VisibleWidth(line) > editorMaxLineCols {
		return TruncateToWidth(line, editorMaxLineCols, "", false)
	}
	return line
}

func (b *editorBuffer) buildVisualLines(width int) []editorVisualLine {
	if width <= 0 {
		width = 1
	}
	var visuals []editorVisualLine
	for li, raw := range b.lines {
		line := b.cropForLayout(raw)
		if len(line) == 0 {
			visuals = append(visuals, editorVisualLine{logicalLine: li, startCol: 0, length: 0})
			continue
		}
		if VisibleWidth(line) <= width {
			visuals = append(visuals, editorVisualLine{logicalLine: li, startCol: 0, length: len(line)})
			continue
		}
		graphemes := splitGraphemes(line)
		bytePos := 0
		chunkStart := 0
		chunkWidth := 0
		for _, g := range graphemes {
			w := graphemeWidth(g)
			if chunkWidth+w > width && chunkWidth > 0 {
				visuals = append(visuals, editorVisualLine{logicalLine: li, startCol: chunkStart, length: bytePos - chunkStart})
				chunkStart = bytePos
				chunkWidth = 0
			}
			if chunkWidth == 0 && w > width {
				visuals = append(visuals, editorVisualLine{logicalLine: li, startCol: bytePos, length: len(g.text)})
				bytePos += len(g.text)
				chunkStart = bytePos
				continue
			}
			bytePos += len(g.text)
			chunkWidth += w
		}
		visuals = append(visuals, editorVisualLine{logicalLine: li, startCol: chunkStart, length: bytePos - chunkStart})
	}
	return visuals
}

func (b *editorBuffer) findVisualAt(visuals []editorVisualLine, line, col int) int {
	for i, vl := range visuals {
		if vl.logicalLine != line {
			continue
		}
		isLast := i == len(visuals)-1 || visuals[i+1].logicalLine != vl.logicalLine
		if col >= vl.startCol && (col < vl.startCol+vl.length || (isLast && col == vl.startCol+vl.length)) {
			return i
		}
	}
	for i := len(visuals) - 1; i >= 0; i-- {
		if visuals[i].logicalLine == line {
			return i
		}
	}
	return len(visuals) - 1
}

func visualColumnOf(line string, startCol, cursorCol int) int {
	if cursorCol <= startCol {
		return 0
	}
	if startCol < 0 {
		startCol = 0
	}
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	return VisibleWidth(line[startCol:cursorCol])
}

func byteOffsetForVisualCol(line string, startCol, targetCol int) int {
	if targetCol <= 0 {
		return startCol
	}
	segment := line[startCol:]
	width := 0
	offset := startCol
	for _, g := range splitGraphemes(segment) {
		w := graphemeWidth(g)
		if width+w > targetCol {
			break
		}
		width += w
		offset += len(g.text)
	}
	return offset
}

func (b *editorBuffer) moveVisual(delta, width int) {
	visuals := b.buildVisualLines(width)
	if len(visuals) == 0 {
		return
	}
	current := b.findVisualAt(visuals, b.cursorLine, b.cursorCol)
	target := current + delta
	if target < 0 {
		target = 0
	}
	if target >= len(visuals) {
		target = len(visuals) - 1
	}
	if target == current {
		return
	}
	b.lastAction = ""
	currentVL := visuals[current]
	targetVL := visuals[target]
	lineText := b.cropForLayout(b.lines[currentVL.logicalLine])
	currentVisualCol := visualColumnOf(lineText, currentVL.startCol, b.cursorCol)
	if !b.hasPreferred {
		b.preferredVisualCol = currentVisualCol
		b.hasPreferred = true
	}
	targetLineText := b.cropForLayout(b.lines[targetVL.logicalLine])
	targetWidth := VisibleWidth(targetLineText[targetVL.startCol : targetVL.startCol+targetVL.length])
	moveCol := b.preferredVisualCol
	if moveCol > targetWidth {
		moveCol = targetWidth
	}
	b.cursorLine = targetVL.logicalLine
	rawLine := b.lines[targetVL.logicalLine]
	cropped := b.cropForLayout(rawLine)
	mapped := byteOffsetForVisualCol(cropped, targetVL.startCol, moveCol)
	if len(cropped) != len(rawLine) && mapped > len(rawLine) {
		mapped = len(rawLine)
	}
	if mapped > len(rawLine) {
		mapped = len(rawLine)
	}
	b.cursorCol = mapped
	spans := findPasteMarkerSpans(rawLine, b.validPasteSet())
	for _, span := range spans {
		if b.cursorCol > span[0] && b.cursorCol < span[1] {
			b.cursorCol = span[0]
			break
		}
	}
}

func (b *editorBuffer) MoveVisualUp(width int) {
	b.moveVisual(-1, width)
}

func (b *editorBuffer) MoveVisualDown(width int) {
	b.moveVisual(1, width)
}

func (b *editorBuffer) MovePageUp(width, pageSize int) {
	if pageSize <= 0 {
		pageSize = 5
	}
	b.moveVisual(-pageSize, width)
}

func (b *editorBuffer) MovePageDown(width, pageSize int) {
	if pageSize <= 0 {
		pageSize = 5
	}
	b.moveVisual(pageSize, width)
}
