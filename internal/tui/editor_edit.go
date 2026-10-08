package tui

import (
	"strings"
)

func (b *editorBuffer) DeleteCharBackward() {
	b.lastAction = ""
	b.hasPreferred = false
	if b.cursorCol > 0 {
		b.pushUndo()
		line := b.lines[b.cursorLine]
		spans := findPasteMarkerSpans(line, b.validPasteSet())
		for _, span := range spans {
			if b.cursorCol >= span[1] && b.cursorCol <= span[1] {
				id, ok := parsePasteMarkerID(line[span[0]:span[1]])
				if ok {
					delete(b.pastes, id)
				}
				b.lines[b.cursorLine] = line[:span[0]] + line[span[1]:]
				b.cursorCol = span[0]
				b.renumberPasteMarkers()
				return
			}
			if b.cursorCol > span[0] && b.cursorCol < span[1] {
				id, ok := parsePasteMarkerID(line[span[0]:span[1]])
				if ok {
					delete(b.pastes, id)
				}
				b.lines[b.cursorLine] = line[:span[0]] + line[span[1]:]
				b.cursorCol = span[0]
				b.renumberPasteMarkers()
				return
			}
		}
		before := line[:b.cursorCol]
		graphemes := splitGraphemes(before)
		step := 1
		if len(graphemes) > 0 {
			step = len(graphemes[len(graphemes)-1].text)
		}
		if step > b.cursorCol {
			step = b.cursorCol
		}
		b.lines[b.cursorLine] = line[:b.cursorCol-step] + line[b.cursorCol:]
		b.cursorCol -= step
		return
	}
	if b.cursorLine > 0 {
		b.pushUndo()
		current := b.lines[b.cursorLine]
		previous := b.lines[b.cursorLine-1]
		b.lines[b.cursorLine-1] = previous + current
		b.lines = append(b.lines[:b.cursorLine], b.lines[b.cursorLine+1:]...)
		b.cursorLine--
		b.cursorCol = len(previous)
	}
}

func (b *editorBuffer) renumberPasteMarkers() {
	if len(b.pastes) == 0 {
		return
	}
	ids := make([]int, 0, len(b.pastes))
	for id := range b.pastes {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	remapped := make(map[int]string, len(b.pastes))
	next := 1
	for _, old := range ids {
		content := b.pastes[old]
		remapped[next] = content
		if next != old {
			oldMarkerLines := strings.Split(content, "\n")
			var oldMarker string
			if len(oldMarkerLines) > 10 {
				oldMarker = formatPasteMarker(old, len(oldMarkerLines), true)
			} else {
				oldMarker = formatPasteMarker(old, len(content), false)
			}
			var newMarker string
			if len(oldMarkerLines) > 10 {
				newMarker = formatPasteMarker(next, len(oldMarkerLines), true)
			} else {
				newMarker = formatPasteMarker(next, len(content), false)
			}
			for li, line := range b.lines {
				b.lines[li] = strings.ReplaceAll(line, oldMarker, newMarker)
			}
		}
		next++
	}
	b.pastes = remapped
	b.pasteCounter = len(remapped)
}

func (b *editorBuffer) DeleteCharForward() {
	b.lastAction = ""
	b.hasPreferred = false
	line := b.lines[b.cursorLine]
	if b.cursorCol < len(line) {
		b.pushUndo()
		spans := findPasteMarkerSpans(line, b.validPasteSet())
		for _, span := range spans {
			if b.cursorCol >= span[0] && b.cursorCol < span[1] {
				id, ok := parsePasteMarkerID(line[span[0]:span[1]])
				if ok {
					delete(b.pastes, id)
				}
				b.lines[b.cursorLine] = line[:span[0]] + line[span[1]:]
				b.renumberPasteMarkers()
				return
			}
		}
		after := line[b.cursorCol:]
		graphemes := splitGraphemes(after)
		step := 1
		if len(graphemes) > 0 {
			step = len(graphemes[0].text)
		}
		if b.cursorCol+step > len(line) {
			step = len(line) - b.cursorCol
		}
		b.lines[b.cursorLine] = line[:b.cursorCol] + line[b.cursorCol+step:]
		return
	}
	if b.cursorLine < len(b.lines)-1 {
		b.pushUndo()
		next := b.lines[b.cursorLine+1]
		b.lines[b.cursorLine] = line + next
		b.lines = append(b.lines[:b.cursorLine+1], b.lines[b.cursorLine+2:]...)
	}
}

func (b *editorBuffer) DeleteWordBackward() {
	if b.cursorCol == 0 {
		if b.cursorLine == 0 {
			return
		}
		b.pushUndo()
		wasKill := b.lastAction == "kill"
		b.kill.push("\n", true, wasKill)
		b.lastAction = "kill"
		b.hasPreferred = false
		current := b.lines[b.cursorLine]
		previous := b.lines[b.cursorLine-1]
		b.lines[b.cursorLine-1] = previous + current
		b.lines = append(b.lines[:b.cursorLine], b.lines[b.cursorLine+1:]...)
		b.cursorLine--
		b.cursorCol = len(previous)
		return
	}
	wasKill := b.lastAction == "kill"
	b.pushUndo()
	line := b.lines[b.cursorLine]
	oldCol := b.cursorCol
	deleteFrom := findWordBackward(line, oldCol)
	deleted := line[deleteFrom:oldCol]
	b.kill.push(deleted, true, wasKill)
	b.lastAction = "kill"
	b.hasPreferred = false
	b.lines[b.cursorLine] = line[:deleteFrom] + line[oldCol:]
	b.cursorCol = deleteFrom
}

func (b *editorBuffer) DeleteWordForward() {
	line := b.lines[b.cursorLine]
	if b.cursorCol >= len(line) {
		if b.cursorLine >= len(b.lines)-1 {
			return
		}
		b.pushUndo()
		wasKill := b.lastAction == "kill"
		b.kill.push("\n", false, wasKill)
		b.lastAction = "kill"
		b.hasPreferred = false
		next := b.lines[b.cursorLine+1]
		b.lines[b.cursorLine] = line + next
		b.lines = append(b.lines[:b.cursorLine+1], b.lines[b.cursorLine+2:]...)
		return
	}
	wasKill := b.lastAction == "kill"
	b.pushUndo()
	oldCol := b.cursorCol
	deleteTo := findWordForward(line, oldCol)
	deleted := line[oldCol:deleteTo]
	b.kill.push(deleted, false, wasKill)
	b.lastAction = "kill"
	b.hasPreferred = false
	b.lines[b.cursorLine] = line[:oldCol] + line[deleteTo:]
}

func (b *editorBuffer) DeleteToLineStart() {
	if b.cursorCol == 0 {
		if b.cursorLine == 0 {
			return
		}
		b.pushUndo()
		wasKill := b.lastAction == "kill"
		b.kill.push("\n", true, wasKill)
		b.lastAction = "kill"
		b.hasPreferred = false
		current := b.lines[b.cursorLine]
		previous := b.lines[b.cursorLine-1]
		b.lines[b.cursorLine-1] = previous + current
		b.lines = append(b.lines[:b.cursorLine], b.lines[b.cursorLine+1:]...)
		b.cursorLine--
		b.cursorCol = len(previous)
		return
	}
	b.pushUndo()
	line := b.lines[b.cursorLine]
	deleted := line[:b.cursorCol]
	b.kill.push(deleted, true, b.lastAction == "kill")
	b.lastAction = "kill"
	b.hasPreferred = false
	b.lines[b.cursorLine] = line[b.cursorCol:]
	b.cursorCol = 0
}

func (b *editorBuffer) DeleteToLineEnd() {
	line := b.lines[b.cursorLine]
	if b.cursorCol < len(line) {
		b.pushUndo()
		deleted := line[b.cursorCol:]
		b.kill.push(deleted, false, b.lastAction == "kill")
		b.lastAction = "kill"
		b.hasPreferred = false
		b.lines[b.cursorLine] = line[:b.cursorCol]
		return
	}
	if b.cursorLine >= len(b.lines)-1 {
		return
	}
	b.pushUndo()
	wasKill := b.lastAction == "kill"
	b.kill.push("\n", false, wasKill)
	b.lastAction = "kill"
	b.hasPreferred = false
	next := b.lines[b.cursorLine+1]
	b.lines[b.cursorLine] = line + next
	b.lines = append(b.lines[:b.cursorLine+1], b.lines[b.cursorLine+2:]...)
}

func (b *editorBuffer) Yank() {
	text, ok := b.kill.peek()
	if !ok {
		return
	}
	b.pushUndo()
	b.lastAction = "yank"
	b.hasPreferred = false
	b.insertYankedText(text)
}

func (b *editorBuffer) YankPop() {
	if b.lastAction != "yank" || b.kill.length() <= 1 {
		return
	}
	b.pushUndo()
	previous, _ := b.kill.peek()
	b.deleteYankedText(previous)
	b.kill.rotate()
	text, _ := b.kill.peek()
	b.insertYankedText(text)
	b.lastAction = "yank"
	b.hasPreferred = false
}

func (b *editorBuffer) insertYankedText(text string) {
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		current := b.lines[b.cursorLine]
		b.lines[b.cursorLine] = current[:b.cursorCol] + text + current[b.cursorCol:]
		b.cursorCol += len(text)
		return
	}
	current := b.lines[b.cursorLine]
	before := current[:b.cursorCol]
	after := current[b.cursorCol:]
	expanded := make([]string, 0, len(b.lines)+len(lines)-1)
	expanded = append(expanded, b.lines[:b.cursorLine]...)
	expanded = append(expanded, before+lines[0])
	if len(lines) > 2 {
		expanded = append(expanded, lines[1:len(lines)-1]...)
	}
	expanded = append(expanded, lines[len(lines)-1]+after)
	expanded = append(expanded, b.lines[b.cursorLine+1:]...)
	b.lines = expanded
	b.cursorLine += len(lines) - 1
	b.cursorCol = len(lines[len(lines)-1])
}

func (b *editorBuffer) deleteYankedText(text string) {
	if text == "" {
		return
	}
	yankLines := strings.Split(text, "\n")
	if len(yankLines) == 1 {
		current := b.lines[b.cursorLine]
		start := b.cursorCol - len(text)
		if start < 0 {
			start = 0
		}
		b.lines[b.cursorLine] = current[:start] + current[b.cursorCol:]
		b.cursorCol = start
		return
	}
	startLine := b.cursorLine - (len(yankLines) - 1)
	if startLine < 0 {
		startLine = 0
	}
	startCol := len(b.lines[startLine]) - len(yankLines[0])
	if startCol < 0 {
		startCol = 0
	}
	afterCursor := b.lines[b.cursorLine][b.cursorCol:]
	beforeYank := b.lines[startLine][:startCol]
	merged := beforeYank + afterCursor
	rebuilt := make([]string, 0, len(b.lines)-len(yankLines)+1)
	rebuilt = append(rebuilt, b.lines[:startLine]...)
	rebuilt = append(rebuilt, merged)
	if b.cursorLine+1 < len(b.lines) {
		rebuilt = append(rebuilt, b.lines[b.cursorLine+1:]...)
	}
	b.lines = rebuilt
	b.cursorLine = startLine
	b.cursorCol = startCol
}

func (b *editorBuffer) JumpToChar(char string, forward bool) {
	if char == "" {
		return
	}
	b.lastAction = ""
	b.hasPreferred = false
	if forward {
		for li := b.cursorLine; li < len(b.lines); li++ {
			line := b.lines[li]
			var from int
			if li == b.cursorLine {
				from = b.cursorCol + 1
				if from >= len(line) {
					continue
				}
			}
			rel := strings.Index(line[from:], char)
			if rel >= 0 {
				b.cursorLine = li
				b.cursorCol = from + rel
				return
			}
		}
		return
	}
	for li := b.cursorLine; li >= 0; li-- {
		line := b.lines[li]
		var upto int
		if li == b.cursorLine {
			upto = b.cursorCol - 1
			if upto < 0 {
				continue
			}
			rel := strings.LastIndex(line[:upto+1], char)
			if rel >= 0 {
				b.cursorLine = li
				b.cursorCol = rel
				return
			}
			continue
		}
		rel := strings.LastIndex(line, char)
		if rel >= 0 {
			b.cursorLine = li
			b.cursorCol = rel
			return
		}
	}
}

func (b *editorBuffer) HandlePaste(pasted string) {
	b.pushUndo()
	b.lastAction = ""
	b.hasPreferred = false
	filtered := filterPastedText(pasted)
	if strings.HasPrefix(filtered, "/") || strings.HasPrefix(filtered, "~") || strings.HasPrefix(filtered, ".") {
		current := b.lines[b.cursorLine]
		if b.cursorCol > 0 && len(current) > 0 {
			prev := current[b.cursorCol-1]
			if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == '_' {
				filtered = " " + filtered
			}
		}
	}
	pastedLines := strings.Split(filtered, "\n")
	if len(pastedLines) > 10 || len(filtered) > 1000 {
		b.pasteCounter++
		id := b.pasteCounter
		b.pastes[id] = filtered
		var marker string
		if len(pastedLines) > 10 {
			marker = formatPasteMarker(id, len(pastedLines), true)
		} else {
			marker = formatPasteMarker(id, len(filtered), false)
		}
		b.insertTextInternal(marker)
		return
	}
	b.insertTextInternal(filtered)
}
