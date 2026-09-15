package tui

func (e *Editor) HandleMouse(event MouseEvent) *MouseEventResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.autocompleteActive && e.autocompleteList != nil {
		startRow := e.renderedVisible + 2
		if event.Y >= startRow && event.Y < startRow+e.renderedAutocompleteHeight {
			maxPadding := 0
			if event.Width > 1 {
				maxPadding = (event.Width - 1) / 2
			}
			paddingX := e.paddingX
			if paddingX > maxPadding {
				paddingX = maxPadding
			}
			contentWidth := event.Width - paddingX*2
			if contentWidth < 1 {
				contentWidth = 1
			}
			adjusted := event
			adjusted.X = event.X - paddingX
			adjusted.Y = event.Y - startRow
			adjusted.Width = contentWidth
			adjusted.Height = e.renderedAutocompleteHeight
			if result := e.autocompleteList.HandleMouse(adjusted); result != nil {
				result.Focus = true
				return result
			}
			return &MouseEventResult{Handled: true, Focus: true}
		}
	}
	if event.Type == MouseDrag && e.mouseAnchorSet {
		targetLine, targetCol := e.mousePositionLocked(event)
		e.selection.active = true
		e.selection.startLine = e.mouseAnchorLine
		e.selection.startCol = e.mouseAnchorCol
		e.selection.endLine = targetLine
		e.selection.endCol = targetCol
		if e.selection.startLine > e.selection.endLine || (e.selection.startLine == e.selection.endLine && e.selection.startCol > e.selection.endCol) {
			e.selection.startLine, e.selection.endLine = e.selection.endLine, e.selection.startLine
			e.selection.startCol, e.selection.endCol = e.selection.endCol, e.selection.startCol
		}
		if e.selection.startLine == e.selection.endLine && e.selection.startCol == e.selection.endCol {
			e.selection.active = false
		}
		return &MouseEventResult{Handled: true, Focus: true}
	}
	if event.Type == MousePress && event.Button == MouseButtonLeft {
		targetLine, targetCol := e.mousePositionLocked(event)
		e.mouseAnchorLine = targetLine
		e.mouseAnchorCol = targetCol
		e.mouseAnchorSet = true
		e.buffer.cursorLine = targetLine
		e.buffer.cursorCol = targetCol
		e.buffer.hasPreferred = false
		e.selection.active = false
		e.exitHistoryLocked()
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		}
		return &MouseEventResult{Handled: true, Focus: true}
	}
	if event.Type == MouseClick && event.Button == MouseButtonLeft {
		targetLine, targetCol := e.mousePositionLocked(event)
		e.buffer.cursorLine = targetLine
		e.buffer.cursorCol = targetCol
		e.buffer.hasPreferred = false
		e.selection.active = false
		e.mouseAnchorSet = false
		e.exitHistoryLocked()
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		}
		return &MouseEventResult{Handled: true, Focus: true}
	}
	return nil
}

func (e *Editor) mousePositionLocked(event MouseEvent) (int, int) {
	maxPadding := 0
	if event.Width > 1 {
		maxPadding = (event.Width - 1) / 2
	}
	paddingX := e.paddingX
	if paddingX > maxPadding {
		paddingX = maxPadding
	}
	width := e.lastWidthForNavLocked()
	visuals := e.buffer.buildVisualLines(width)
	if len(visuals) == 0 {
		return 0, 0
	}
	if event.Y <= 0 || event.Y > e.renderedVisible {
		if event.Y <= 0 {
			first := visuals[e.scrollOffset]
			return first.logicalLine, first.startCol
		}
		lastIdx := e.scrollOffset + e.renderedVisible - 1
		if lastIdx >= len(visuals) {
			lastIdx = len(visuals) - 1
		}
		last := visuals[lastIdx]
		line := e.buffer.lines[last.logicalLine]
		return last.logicalLine, len(line)
	}
	visualIdx := e.scrollOffset + event.Y - 1
	if visualIdx < 0 {
		visualIdx = 0
	}
	if visualIdx >= len(visuals) {
		visualIdx = len(visuals) - 1
	}
	vl := visuals[visualIdx]
	line := e.buffer.lines[vl.logicalLine]
	cropped := e.buffer.cropForLayout(line)
	var chunk string
	if vl.startCol < len(cropped) {
		limit := vl.startCol + vl.length
		if limit > len(cropped) {
			limit = len(cropped)
		}
		chunk = cropped[vl.startCol:limit]
	}
	targetColumn := event.X - paddingX
	if targetColumn < 0 {
		targetColumn = 0
	}
	visibleCol := 0
	targetByte := 0
	for _, g := range splitGraphemes(chunk) {
		next := visibleCol + graphemeWidth(g)
		if targetColumn < next {
			break
		}
		visibleCol = next
		targetByte += len(g.text)
	}
	isLast := visualIdx == len(visuals)-1 || visuals[visualIdx+1].logicalLine != vl.logicalLine
	if !isLast && targetByte == len(chunk) && len(chunk) > 0 {
		graphemes := splitGraphemes(chunk)
		if len(graphemes) > 0 {
			targetByte -= len(graphemes[len(graphemes)-1].text)
		}
	}
	return vl.logicalLine, vl.startCol + targetByte
}
