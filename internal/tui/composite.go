package tui

import "strings"

func (b *Base) compositeOverlays(lines []string, termWidth, termHeight int) []string {
	b.mu.Lock()
	if len(b.overlayStack) == 0 {
		b.renderedOverlays = nil
		b.mu.Unlock()
		return lines
	}
	var visible []*overlayEntry
	for _, entry := range b.overlayStack {
		entry.hasBounds = false
		if b.isOverlayVisibleLocked(entry) {
			visible = append(visible, entry)
		}
	}
	b.mu.Unlock()

	if len(visible) == 0 {
		b.mu.Lock()
		b.renderedOverlays = nil
		b.mu.Unlock()
		return lines
	}

	result := append([]string(nil), lines...)
	var rendered []overlayLayout
	minLinesNeeded := len(result)

	for _, entry := range visible {
		initial := b.resolveOverlayLayout(entry.options, 0, termWidth, termHeight)
		overlayLines := entry.component.Render(initial.width)
		if initial.maxHeight != nil && len(overlayLines) > *initial.maxHeight {
			overlayLines = overlayLines[:*initial.maxHeight]
		}
		final := b.resolveOverlayLayout(entry.options, len(overlayLines), termWidth, termHeight)
		b.mu.Lock()
		entry.bounds = OverlayBounds{Row: final.row, Col: final.col, Width: final.width, Height: len(overlayLines)}
		entry.hasBounds = true
		b.renderedOverlays = append(b.renderedOverlays, overlayLayout{
			entry: entry, row: final.row, col: final.col, width: final.width, height: len(overlayLines),
		})
		b.mu.Unlock()
		rendered = append(rendered, overlayLayout{entry: entry, lines: overlayLines, row: final.row, col: final.col, width: final.width})
		if final.row+len(overlayLines) > minLinesNeeded {
			minLinesNeeded = final.row + len(overlayLines)
		}
	}

	workingHeight := len(result)
	workingHeight = maxInt(workingHeight, termHeight)
	workingHeight = maxInt(workingHeight, minLinesNeeded)
	for len(result) < workingHeight {
		result = append(result, "")
	}
	viewportStart := maxInt(0, workingHeight-termHeight)

	for _, layout := range rendered {
		for i, overlayLine := range layout.lines {
			idx := viewportStart + layout.row + i
			if idx < 0 || idx >= len(result) {
				continue
			}
			line := overlayLine
			if VisibleWidth(line) > layout.width {
				line = SliceByColumn(line, 0, layout.width, true)
			}
			result[idx] = CompositeTuiLine(result[idx], line, layout.col, layout.width, termWidth)
		}
	}
	return result
}

func (b *Base) ApplyLineResets(lines []string) []string {
	for i, line := range lines {
		lines[i] = normalizeTerminalOutput(line) + SegmentReset
	}
	return lines
}

func (b *Base) ExtractCursorPosition(lines []string, height int) (row, col int, found bool) {
	viewportTop := len(lines) - height
	if viewportTop < 0 {
		viewportTop = 0
	}
	for r := len(lines) - 1; r >= viewportTop; r-- {
		line := lines[r]
		markerIndex := strings.Index(line, CursorMarker)
		if markerIndex == -1 {
			continue
		}
		col = VisibleWidth(line[:markerIndex])
		lines[r] = line[:markerIndex] + line[markerIndex+len(CursorMarker):]
		return r, col, true
	}
	return 0, 0, false
}

func CompositeTuiLine(baseLine, overlayLine string, startCol, overlayWidth, totalWidth int) string {
	baseBefore := sliceByColumns(baseLine, 0, startCol, true)
	baseAfter := sliceByColumns(baseLine, startCol+overlayWidth, totalWidth-(startCol+overlayWidth), true)
	overlay := sliceByColumns(overlayLine, 0, overlayWidth, true)

	beforePad := maxInt(0, startCol-baseBefore.width)
	overlayPad := maxInt(0, overlayWidth-overlay.width)
	actualBeforeWidth := maxInt(startCol, baseBefore.width)
	actualOverlayWidth := maxInt(overlayWidth, overlay.width)
	afterTarget := maxInt(0, totalWidth-actualBeforeWidth-actualOverlayWidth)
	afterPad := maxInt(0, afterTarget-baseAfter.width)

	var builder strings.Builder
	builder.WriteString(baseBefore.text)
	builder.WriteString(strings.Repeat(" ", beforePad))
	builder.WriteString(SegmentReset)
	builder.WriteString(overlay.text)
	builder.WriteString(strings.Repeat(" ", overlayPad))
	builder.WriteString(SegmentReset)
	builder.WriteString(baseAfter.text)
	builder.WriteString(strings.Repeat(" ", afterPad))
	result := builder.String()
	if VisibleWidth(result) <= totalWidth {
		return result
	}
	return SliceByColumn(result, 0, totalWidth, true)
}
