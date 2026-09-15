package tui

import (
	"strings"
)

func editorScrollBorder(direction string, count, width int) string {
	if width <= 0 {
		return ""
	}
	if count <= 0 {
		return strings.Repeat("─", width)
	}
	label := " " + direction + " " + itoa(count) + " more "
	labelWidth := VisibleWidth(label)
	if labelWidth+2 <= width {
		left := (width - labelWidth) / 2
		right := width - left - labelWidth
		return strings.Repeat("─", left) + label + strings.Repeat("─", right)
	}
	indicator := "─── " + direction + " " + itoa(count) + " more "
	if VisibleWidth(indicator) <= width {
		return indicator + strings.Repeat("─", width-VisibleWidth(indicator))
	}
	clipped := TruncateToWidth(indicator, width, "", false)
	return clipped
}

func (e *Editor) maxVisibleLinesLocked() int {
	rows := e.terminalRows
	if rows <= 0 {
		rows = 24
	}
	maxVisible := rows * 3 / 10
	if maxVisible < 5 {
		maxVisible = 5
	}
	return maxVisible
}

func (e *Editor) isEmptyLocked() bool {
	return len(e.buffer.lines) == 1 && e.buffer.lines[0] == ""
}

func (e *Editor) isOnFirstVisualLineLocked() bool {
	visuals := e.buffer.buildVisualLines(e.lastWidthForNavLocked())
	current := e.buffer.findVisualAt(visuals, e.buffer.cursorLine, e.buffer.cursorCol)
	return current == 0
}

func (e *Editor) isOnLastVisualLineLocked() bool {
	visuals := e.buffer.buildVisualLines(e.lastWidthForNavLocked())
	current := e.buffer.findVisualAt(visuals, e.buffer.cursorLine, e.buffer.cursorCol)
	return current == len(visuals)-1
}

func (e *Editor) selectionSpanForLineLocked(logicalLine int, lineLen int) (int, int, bool) {
	if !e.selection.active {
		return 0, 0, false
	}
	sl, sc := e.selection.startLine, e.selection.startCol
	el, ec := e.selection.endLine, e.selection.endCol
	if sl > el || (sl == el && sc > ec) {
		sl, el = el, sl
		sc, ec = ec, sc
	}
	if logicalLine < sl || logicalLine > el {
		return 0, 0, false
	}
	if logicalLine < 0 || logicalLine >= len(e.buffer.lines) {
		return 0, 0, false
	}
	var start, end int
	if sl == el {
		start = sc
		end = ec
	} else if logicalLine == sl {
		start = sc
		end = lineLen
	} else if logicalLine == el {
		start = 0
		end = ec
	} else {
		start = 0
		end = lineLen
	}
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start > lineLen {
		start = lineLen
	}
	if end > lineLen {
		end = lineLen
	}
	if start >= end {
		return 0, 0, false
	}
	return start, end, true
}

func wrapSelectionSegment(segment string, segStart, segEnd, selStart, selEnd int) string {
	if segment == "" {
		return segment
	}
	lo := segStart
	if selStart > lo {
		lo = selStart
	}
	hi := segEnd
	if selEnd < hi {
		hi = selEnd
	}
	if hi <= lo {
		return segment
	}
	rs := lo - segStart
	re := hi - segStart
	if rs < 0 {
		rs = 0
	}
	if re > len(segment) {
		re = len(segment)
	}
	if rs >= re {
		return segment
	}
	return segment[:rs] + SGRInverse + segment[rs:re] + SGRInverseOff + segment[re:]
}

func (e *Editor) lastWidthForNavLocked() int {
	if e.lastWidth <= 0 {
		return 80
	}
	return e.lastWidth
}

func (e *Editor) Render(width int) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if width <= 0 {
		return nil
	}
	maxPadding := 0
	if width > 1 {
		maxPadding = (width - 1) / 2
	}
	paddingX := e.paddingX
	if paddingX > maxPadding {
		paddingX = maxPadding
	}
	if paddingX < 0 {
		paddingX = 0
	}
	contentWidth := width - paddingX*2
	if contentWidth < 1 {
		contentWidth = 1
	}
	layoutWidth := contentWidth
	if paddingX == 0 {
		layoutWidth = contentWidth - 1
		if layoutWidth < 1 {
			layoutWidth = 1
		}
	}
	e.lastWidth = layoutWidth
	visuals := e.buffer.buildVisualLines(layoutWidth)
	cursorIdx := e.buffer.findVisualAt(visuals, e.buffer.cursorLine, e.buffer.cursorCol)
	maxVisible := e.maxVisibleLinesLocked()
	if cursorIdx < e.scrollOffset {
		e.scrollOffset = cursorIdx
	} else if cursorIdx >= e.scrollOffset+maxVisible {
		e.scrollOffset = cursorIdx - maxVisible + 1
	}
	maxOffset := 0
	if len(visuals) > maxVisible {
		maxOffset = len(visuals) - maxVisible
	}
	if e.scrollOffset < 0 {
		e.scrollOffset = 0
	}
	if e.scrollOffset > maxOffset {
		e.scrollOffset = maxOffset
	}
	end := e.scrollOffset + maxVisible
	if end > len(visuals) {
		end = len(visuals)
	}
	visible := visuals[e.scrollOffset:end]
	e.renderedVisible = len(visible)
	leftPadding := strings.Repeat(" ", paddingX)
	rightPadding := strings.Repeat(" ", paddingX)
	result := make([]string, 0, len(visible)+3)
	top := editorScrollBorder("↑", e.scrollOffset, width)
	result = append(result, e.borderColor(top))
	emitMarker := e.focused
	for i, vl := range visible {
		globalIdx := e.scrollOffset + i
		hasCursor := globalIdx == cursorIdx
		cropped := e.buffer.cropForLayout(e.buffer.lines[vl.logicalLine])
		var text string
		if vl.startCol < len(cropped) {
			limit := vl.startCol + vl.length
			if limit > len(cropped) {
				limit = len(cropped)
			}
			text = cropped[vl.startCol:limit]
		}
		display := text
		lineWidth := VisibleWidth(text)
		cursorInPadding := false
		selStart, selEnd, hasSel := e.selectionSpanForLineLocked(vl.logicalLine, len(cropped))
		relStart, relEnd := 0, 0
		hasVisualSel := false
		if hasSel {
			segS := vl.startCol
			segE := vl.startCol + len(text)
			lo := segS
			if selStart > lo {
				lo = selStart
			}
			hi := segE
			if selEnd < hi {
				hi = selEnd
			}
			if hi > lo {
				hasVisualSel = true
				relStart = lo - segS
				relEnd = hi - segS
				if relStart < 0 {
					relStart = 0
				}
				if relEnd > len(text) {
					relEnd = len(text)
				}
				if relStart >= relEnd {
					hasVisualSel = false
				}
			}
		}
		if !hasCursor && hasVisualSel {
			display = text[:relStart] + SGRInverse + text[relStart:relEnd] + SGRInverseOff + text[relEnd:]
		}
		if hasCursor {
			adjusted := e.buffer.cursorCol - vl.startCol
			if adjusted < 0 {
				adjusted = 0
			}
			if adjusted > len(text) {
				adjusted = len(text)
			}
			before := text[:adjusted]
			after := text[adjusted:]
			marker := ""
			if emitMarker {
				marker = CursorMarker
			}
			hint := e.inlineHintLocked()
			if after != "" {
				graphemes := splitGraphemes(after)
				first := ""
				rest := after
				if len(graphemes) > 0 {
					first = graphemes[0].text
					rest = after[len(first):]
				}
				cursorChar := SGRInverse + first + SGRInverseOff
				beforeWrapped := before
				restWrapped := rest
				if hasVisualSel {
					beforeWrapped = wrapSelectionSegment(before, vl.startCol, vl.startCol+adjusted, selStart, selEnd)
					restStart := vl.startCol + adjusted + len(first)
					restEnd := vl.startCol + len(text)
					restWrapped = wrapSelectionSegment(rest, restStart, restEnd, selStart, selEnd)
				}
				if hint != "" {
					display = beforeWrapped + marker + cursorChar + hint + restWrapped
				} else {
					display = beforeWrapped + marker + cursorChar + restWrapped
				}
			} else {
				cursorChar := SGRInverse + " " + SGRInverseOff
				beforeWrapped := before
				if hasVisualSel {
					beforeWrapped = wrapSelectionSegment(before, vl.startCol, vl.startCol+adjusted, selStart, selEnd)
				}
				if hint != "" {
					display = beforeWrapped + marker + cursorChar + hint
				} else {
					display = beforeWrapped + marker + cursorChar
				}
				lineWidth = VisibleWidth(text) + 1 + VisibleWidth(hint)
				if lineWidth > contentWidth && paddingX > 0 {
					cursorInPadding = true
				}
			}
			if after != "" {
				lineWidth = VisibleWidth(text) + VisibleWidth(hint)
			}
		}
		padding := strings.Repeat(" ", maxInt(0, contentWidth-lineWidth))
		lineRight := rightPadding
		if cursorInPadding && len(lineRight) > 0 {
			lineRight = lineRight[:len(lineRight)-1]
		}
		result = append(result, leftPadding+display+padding+lineRight)
	}
	linesBelow := len(visuals) - (e.scrollOffset + len(visible))
	bottom := editorScrollBorder("↓", linesBelow, width)
	result = append(result, e.borderColor(bottom))
	e.renderedAutocompleteHeight = 0
	if e.autocompleteActive && e.autocompleteList != nil {
		autoLines := e.autocompleteList.Render(contentWidth)
		e.renderedAutocompleteHeight = len(autoLines)
		for _, line := range autoLines {
			lineWidth := VisibleWidth(line)
			padding := strings.Repeat(" ", maxInt(0, contentWidth-lineWidth))
			result = append(result, leftPadding+line+padding+rightPadding)
		}
	}
	return result
}
