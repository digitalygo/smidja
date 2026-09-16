package tui

import (
	"strings"
)

const maxRenderWriteChars = 1024 * 1024

type boundedWriter struct {
	terminal Terminal
	buffer   strings.Builder
	written  int
}

func (w *boundedWriter) append(value string) {
	w.buffer.WriteString(value)
	if w.buffer.Len() >= maxRenderWriteChars {
		w.flush()
	}
}

func (w *boundedWriter) flush() {
	if w.buffer.Len() == 0 {
		return
	}
	w.terminal.Write(w.buffer.String())
	w.written += w.buffer.Len()
	w.buffer.Reset()
}

type MainScreen struct {
	*Base

	previousLines       []string
	previousWidth       int
	previousHeight      int
	cursorRow           int
	hardwareCursorRow   int
	maxLinesRendered    int
	previousViewportTop int

	overflowDetected bool
}

func NewMainScreen(terminal Terminal, showHardwareCursor bool) *MainScreen {
	screen := &MainScreen{
		Base:           NewBase(terminal, showHardwareCursor, "regular"),
		previousWidth:  0,
		previousHeight: 0,
	}
	screen.SetHooks(tuiHooks{
		resetRenderState: screen.resetRenderState,
		doRender:         func() { screen.doRender() },
		beforeStop:       screen.beforeTerminalStop,
	})
	return screen
}

func (s *MainScreen) resetRenderState() {
	s.previousLines = nil
	s.previousWidth = -1
	s.previousHeight = -1
	s.cursorRow = 0
	s.hardwareCursorRow = 0
	s.maxLinesRendered = 0
	s.previousViewportTop = 0
}

func (s *MainScreen) beforeTerminalStop(options StopOptions) {
	s.mu.Lock()
	previousCount := len(s.previousLines)
	hardwareCursorRow := s.hardwareCursorRow
	s.mu.Unlock()
	if options.PreserveScreen || previousCount == 0 {
		return
	}
	terminal := s.Terminal()
	terminal.Write(" ")
	lineDiff := previousCount - hardwareCursorRow
	if lineDiff > 0 {
		terminal.Write(CursorMoveLines(lineDiff))
	} else if lineDiff < 0 {
		terminal.Write(CursorMoveLines(lineDiff))
	}
	terminal.Write("\r\n")
}

func (s *MainScreen) doRender() {
	if s.IsStopped() || s.IsSuspended() {
		return
	}
	width := s.Terminal().Columns()
	height := s.Terminal().Rows()
	widthChanged := s.previousWidth != 0 && s.previousWidth != width
	heightChanged := s.previousHeight != 0 && s.previousHeight != height
	previousBufferLength := height
	if s.previousHeight > 0 {
		previousBufferLength = s.previousViewportTop + s.previousHeight
	}
	prevViewportTop := s.previousViewportTop
	if heightChanged {
		prevViewportTop = maxInt(0, previousBufferLength-height)
	}
	viewportTop := prevViewportTop
	hardwareCursorRow := s.hardwareCursorRow
	computeLineDiff := func(targetRow int) int {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		targetScreenRow := targetRow - viewportTop
		return targetScreenRow - currentScreenRow
	}

	newLines := s.Render(width)
	newLines = s.compositeOverlays(newLines, width, height)
	cursorRow, cursorCol, cursorFound := s.ExtractCursorPosition(newLines, height)
	newLines = s.ApplyLineResets(newLines)
	newLines = clampLines(newLines, width)

	fullRender := func(clear bool) {
		s.mu.Lock()
		s.fullRedraws++
		s.mu.Unlock()
		output := &boundedWriter{terminal: s.Terminal()}
		output.append(SyncOutputBegin)
		if clear {
			output.append(CursorEraseScreen + CursorHome + CursorClearScroll)
		}
		for i := 0; i < len(newLines); i++ {
			if i > 0 {
				output.append("\r\n")
			}
			output.append(newLines[i])
		}
		output.append(SyncOutputEnd)
		output.flush()
		s.mu.Lock()
		s.cursorRow = maxInt(0, len(newLines)-1)
		s.hardwareCursorRow = s.cursorRow
		if clear {
			s.maxLinesRendered = len(newLines)
		} else {
			s.maxLinesRendered = maxInt(s.maxLinesRendered, len(newLines))
		}
		bufferLength := maxInt(height, len(newLines))
		s.previousViewportTop = maxInt(0, bufferLength-height)
		s.mu.Unlock()
		s.positionHardwareCursor(cursorRow, cursorCol, cursorFound, len(newLines))
		s.mu.Lock()
		s.previousLines = newLines
		s.previousWidth = width
		s.previousHeight = height
		s.mu.Unlock()
	}

	if len(s.previousLines) == 0 && !widthChanged && !heightChanged {
		fullRender(false)
		return
	}

	if widthChanged {
		fullRender(true)
		return
	}

	if heightChanged {
		fullRender(true)
		return
	}

	if s.ClearOnShrink() && len(newLines) < s.maxLinesRendered && !s.HasOverlay() {
		fullRender(true)
		return
	}

	firstChanged := -1
	lastChanged := -1
	maxLines := maxInt(len(newLines), len(s.previousLines))
	for i := 0; i < maxLines; i++ {
		oldLine := ""
		if i < len(s.previousLines) {
			oldLine = s.previousLines[i]
		}
		newLine := ""
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(s.previousLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(s.previousLines)
		}
		lastChanged = len(newLines) - 1
	}
	appendStart := appendedLines && firstChanged == len(s.previousLines) && firstChanged > 0

	if firstChanged == -1 {
		s.positionHardwareCursor(cursorRow, cursorCol, cursorFound, len(newLines))
		s.mu.Lock()
		s.previousViewportTop = prevViewportTop
		s.previousHeight = height
		s.mu.Unlock()
		return
	}

	if firstChanged >= len(newLines) {
		if len(s.previousLines) > len(newLines) {
			output := &boundedWriter{terminal: s.Terminal()}
			output.append(SyncOutputBegin)
			targetRow := maxInt(0, len(newLines)-1)
			if targetRow < prevViewportTop {
				output.flush()
				fullRender(true)
				return
			}
			lineDiff := computeLineDiff(targetRow)
			if lineDiff > 0 {
				output.append(CursorMoveLines(lineDiff))
			} else if lineDiff < 0 {
				output.append(CursorMoveLines(lineDiff))
			}
			output.append("\r")
			extraLines := len(s.previousLines) - len(newLines)
			if extraLines > height {
				output.flush()
				fullRender(true)
				return
			}
			clearStartOffset := 0
			if len(newLines) != 0 {
				clearStartOffset = 1
			}
			if extraLines > 0 && clearStartOffset > 0 {
				output.append(CursorMoveLines(clearStartOffset))
			}
			for i := 0; i < extraLines; i++ {
				output.append("\r" + CursorEraseLine)
				if i < extraLines-1 {
					output.append(CursorUp)
				}
			}
			moveBack := maxInt(0, extraLines-1+clearStartOffset)
			if moveBack > 0 {
				output.append(CursorMoveLines(-moveBack))
			}
			output.append(SyncOutputEnd)
			output.flush()
			s.mu.Lock()
			s.cursorRow = targetRow
			s.hardwareCursorRow = targetRow
			s.mu.Unlock()
		}
		s.positionHardwareCursor(cursorRow, cursorCol, cursorFound, len(newLines))
		s.mu.Lock()
		s.previousLines = newLines
		s.previousWidth = width
		s.previousHeight = height
		s.previousViewportTop = prevViewportTop
		s.mu.Unlock()
		return
	}

	if firstChanged < prevViewportTop {
		fullRender(true)
		return
	}

	output := &boundedWriter{terminal: s.Terminal()}
	output.append(SyncOutputBegin)
	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		currentScreenRow = maxInt(0, minInt(height-1, currentScreenRow))
		moveToBottom := height - 1 - currentScreenRow
		if moveToBottom > 0 {
			output.append(CursorMoveLines(moveToBottom))
		}
		scroll := moveTargetRow - prevViewportBottom
		output.append(strings.Repeat("\r\n", scroll))
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	lineDiff := computeLineDiff(moveTargetRow)
	if lineDiff > 0 {
		output.append(CursorMoveLines(lineDiff))
	} else if lineDiff < 0 {
		output.append(CursorMoveLines(lineDiff))
	}

	if appendStart {
		output.append("\r\n")
	} else {
		output.append("\r")
	}

	renderEnd := minInt(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			output.append("\r\n")
		}
		line := newLines[i]
		output.append(CursorEraseLine)
		if VisibleWidth(line) > width {
			s.overflowDetected = true
			line = SliceByColumn(line, 0, width, true)
		}
		output.append(line)
	}

	finalCursorRow := renderEnd
	if len(s.previousLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			output.append(CursorMoveLines(moveDown))
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(s.previousLines) - len(newLines)
		for i := len(newLines); i < len(s.previousLines); i++ {
			output.append("\r\n" + CursorEraseLine)
		}
		output.append(CursorMoveLines(-extraLines))
	}

	output.append(SyncOutputEnd)
	output.flush()

	s.mu.Lock()
	s.cursorRow = maxInt(0, len(newLines)-1)
	s.hardwareCursorRow = finalCursorRow
	s.maxLinesRendered = maxInt(s.maxLinesRendered, len(newLines))
	s.previousViewportTop = maxInt(prevViewportTop, finalCursorRow-height+1)
	s.previousLines = newLines
	s.previousWidth = width
	s.previousHeight = height
	s.mu.Unlock()

	s.positionHardwareCursor(cursorRow, cursorCol, cursorFound, len(newLines))
}

func clampLines(lines []string, width int) []string {
	for i, line := range lines {
		if VisibleWidth(line) > width {
			lines[i] = SliceByColumn(line, 0, width, true)
		}
	}
	return lines
}

func (s *MainScreen) positionHardwareCursor(cursorRow, cursorCol int, found bool, totalLines int) {
	terminal := s.Terminal()
	if !found || totalLines <= 0 {
		terminal.Write(CursorHide)
		return
	}
	s.mu.Lock()
	targetRow := maxInt(0, minInt(cursorRow, totalLines-1))
	targetCol := maxInt(0, cursorCol)
	rowDelta := targetRow - s.hardwareCursorRow
	var buffer strings.Builder
	if rowDelta > 0 {
		buffer.WriteString(CursorMoveLines(rowDelta))
	} else if rowDelta < 0 {
		buffer.WriteString(CursorMoveLines(rowDelta))
	}
	buffer.WriteString(CursorColumn(targetCol))
	s.hardwareCursorRow = targetRow
	showCursor := s.showHardwareCursor
	s.mu.Unlock()

	if buffer.Len() > 0 {
		terminal.Write(buffer.String())
	}
	if showCursor {
		terminal.Write(CursorShow)
	} else {
		terminal.Write(CursorHide)
	}
}
