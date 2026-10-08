package tui

import (
	"context"
	"encoding/base64"
	"time"
)

type scrollbarDragState struct {
	box      *LayoutBox
	view     *ScrollView
	geometry *scrollbarGeometry
	primary  bool
}

func (s *AltScreen) transcriptBox() *LayoutBox {
	s.frameMu.RLock()
	layout := s.currentLayout
	s.frameMu.RUnlock()
	if layout == nil || layout.PrimaryScrollView == nil {
		return nil
	}
	return GetScrollViewBox(layout, layout.PrimaryScrollView)
}

func (s *AltScreen) contentGeneration(box *LayoutBox) uint64 {
	return searchGeneration(box.scrollContentLines, box.Rect.Width)
}

func (s *AltScreen) handleModalCaptureChanged(active bool) {
	if active {
		s.cancelPointerGestures()
	}
}

func (s *AltScreen) hasSelectionCapture() bool {
	s.pointerMu.Lock()
	defer s.pointerMu.Unlock()
	return s.selectionCapture != nil
}

func (s *AltScreen) currentSelectionCapture() *transcriptPointerCapture {
	s.pointerMu.Lock()
	defer s.pointerMu.Unlock()
	return s.selectionCapture
}

func (s *AltScreen) clearSelectionCapture() *transcriptPointerCapture {
	s.pointerMu.Lock()
	capture := s.selectionCapture
	s.selectionCapture = nil
	s.pointerMu.Unlock()
	return capture
}

func (s *AltScreen) cancelSelectionCapture() {
	s.clearSelectionCapture()
	if s.selection != nil {
		s.selection.Clear()
	}
}

func (s *AltScreen) cancelScrollbarDrag() {
	s.pointerMu.Lock()
	s.scrollbarDrag = nil
	s.pointerMu.Unlock()
}

func (s *AltScreen) cancelPointerGestures() {
	s.autoscroll.stop()
	s.cancelSelectionCapture()
	s.cancelScrollbarDrag()
}

func (s *AltScreen) layoutEpochSnapshot() uint64 {
	s.frameMu.RLock()
	defer s.frameMu.RUnlock()
	return s.layoutEpoch
}

func (s *AltScreen) updateSelectionGeneration(layout *LayoutFrame) {
	if layout == nil || layout.PrimaryScrollView == nil {
		return
	}
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil {
		return
	}
	generation := s.contentGeneration(box)
	s.selection.InvalidateOnGeneration(generation)
	capture := s.currentSelectionCapture()
	if capture == nil {
		return
	}
	epoch := s.layoutEpochSnapshot()
	s.pointerMu.Lock()
	stale := capture.generation != generation || capture.layoutEpoch != epoch
	s.pointerMu.Unlock()
	if stale {
		s.cancelPointerGestures()
	}
}

func (s *AltScreen) documentPoint(box *LayoutBox, x, y int) (SelectionPoint, bool) {
	if len(box.scrollContentLines) == 0 {
		return SelectionPoint{}, false
	}
	scrollTop := 0
	if box.scrollView != nil {
		scrollTop = box.scrollView.ScrollTop()
	}
	line := scrollTop + (y - box.Rect.Y)
	if line < 0 {
		line = 0
	}
	if line >= len(box.scrollContentLines) {
		line = len(box.scrollContentLines) - 1
	}
	width := VisibleWidth(plainSelectionLine(box.scrollContentLines[line]))
	column := x - box.Rect.X
	if column < 0 {
		column = 0
	}
	if column > width {
		column = width
	}
	return SelectionPoint{Line: line, Column: column}, true
}

func (s *AltScreen) handleSelectionMouse(event MouseEvent) bool {
	switch event.Type {
	case MousePress:
		return s.beginSelectionCapture(event)
	case MouseDrag, MouseMove:
		return s.updateSelectionCapture(event)
	case MouseRelease:
		return s.releaseSelectionCapture(event)
	}
	return false
}

func (s *AltScreen) beginSelectionCapture(event MouseEvent) bool {
	if event.Button != MouseButtonLeft {
		return false
	}
	if s.hasSelectionCapture() {
		return true
	}
	box := s.transcriptBox()
	if box == nil || box.scrollView == nil || !box.Rect.contains(event.ScreenX, event.ScreenY) {
		return false
	}
	point, ok := s.documentPoint(box, event.ScreenX, event.ScreenY)
	if !ok {
		return false
	}
	generation := s.contentGeneration(box)
	capture := newTranscriptPointerCapture(box, generation, s.layoutEpochSnapshot(), point, event.ScreenX, event.ScreenY)
	capture.pointerX = event.ScreenX
	if url, ok := s.linkAt(event.ScreenY, event.ScreenX); ok {
		capture.hasLink = true
		capture.linkTarget = url
	}
	s.pointerMu.Lock()
	if s.selectionCapture != nil {
		s.pointerMu.Unlock()
		return true
	}
	s.selectionCapture = capture
	s.pointerMu.Unlock()
	if !capture.hasLink {
		s.selection.Begin(point, generation)
	}
	s.RequestRender(false)
	return true
}

func (s *AltScreen) updateSelectionCapture(event MouseEvent) bool {
	s.pointerMu.Lock()
	capture := s.selectionCapture
	if capture == nil {
		s.pointerMu.Unlock()
		return false
	}
	if event.ScreenX != capture.pressX || event.ScreenY != capture.pressY {
		capture.moved = true
	}
	capture.pointerX = event.ScreenX
	box := capture.box
	anchor := capture.anchor
	generation := capture.generation
	s.pointerMu.Unlock()

	point, ok := s.documentPoint(box, event.ScreenX, event.ScreenY)
	if !ok {
		return true
	}
	s.pointerMu.Lock()
	if s.selectionCapture != capture {
		s.pointerMu.Unlock()
		return true
	}
	if point != anchor {
		capture.moved = true
	}
	if capture.moved {
		capture.hasLink = false
		capture.linkTarget = ""
	}
	moved := capture.moved
	s.pointerMu.Unlock()
	if moved {
		if !s.selection.Active() {
			s.selection.Begin(anchor, generation)
		}
		s.selection.Update(point)
	}
	s.updateAutoscroll(capture, event.ScreenY)
	s.RequestRender(false)
	return true
}

func (s *AltScreen) releaseSelectionCapture(event MouseEvent) bool {
	s.pointerMu.Lock()
	capture := s.selectionCapture
	s.pointerMu.Unlock()
	if capture == nil {
		return false
	}
	if event.Button != MouseButtonLeft {
		s.cancelPointerGestures()
		return true
	}
	s.autoscroll.stop()
	s.pointerMu.Lock()
	if s.selectionCapture != capture {
		s.pointerMu.Unlock()
		return true
	}
	moved := capture.moved
	hasLink := capture.hasLink
	linkTarget := capture.linkTarget
	pressX := capture.pressX
	pressY := capture.pressY
	box := capture.box
	s.pointerMu.Unlock()

	if !moved && hasLink && s.linkStillValid(capture, pressX, pressY, linkTarget) {
		s.clearSelectionCapture()
		s.selection.Clear()
		s.openLink(linkTarget)
		s.RequestRender(false)
		return true
	}
	if s.selection.Active() {
		s.copySelection(box)
	}
	s.clearSelectionCapture()
	s.selection.Clear()
	s.RequestRender(false)
	return true
}

func (s *AltScreen) linkStillValid(capture *transcriptPointerCapture, pressX, pressY int, linkTarget string) bool {
	s.frameMu.RLock()
	layout := s.currentLayout
	epoch := s.layoutEpoch
	s.frameMu.RUnlock()
	if layout == nil || layout.PrimaryScrollView == nil || epoch != capture.layoutEpoch {
		return false
	}
	s.pointerMu.Lock()
	sameCapture := s.selectionCapture == capture
	s.pointerMu.Unlock()
	if !sameCapture {
		return false
	}
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil || s.contentGeneration(box) != capture.generation {
		return false
	}
	target, ok := s.linkAt(pressY, pressX)
	return ok && target == linkTarget
}

func (s *AltScreen) updateAutoscroll(capture *transcriptPointerCapture, y int) {
	box := capture.box
	switch {
	case y < box.Rect.Y:
		s.startSelectionAutoscroll(capture, -1)
	case y >= box.Rect.Y+box.Rect.Height:
		s.startSelectionAutoscroll(capture, 1)
	default:
		s.stopSelectionAutoscroll()
	}
}

func (s *AltScreen) startSelectionAutoscroll(capture *transcriptPointerCapture, direction int) {
	s.autoscroll.start(direction, s.autoscrollIntervalValue(), func(int) {
		s.selectionAutoscrollStep(capture)
	})
}

func (s *AltScreen) autoscrollIntervalValue() time.Duration {
	if s.autoscrollInterval > 0 {
		return s.autoscrollInterval
	}
	return selectionAutoscrollInterval
}

func (s *AltScreen) selectionAutoscrollStep(capture *transcriptPointerCapture) {
	s.pointerMu.Lock()
	if s.selectionCapture != capture {
		s.pointerMu.Unlock()
		return
	}
	box := capture.box
	pointerX := capture.pointerX
	anchor := capture.anchor
	generation := capture.generation
	s.pointerMu.Unlock()
	if box == nil || box.scrollView == nil {
		return
	}
	direction := s.autoscroll.direction()
	if direction == 0 {
		return
	}
	before := box.scrollView.ScrollTop()
	box.scrollView.ScrollTo(before+direction, ScrollToOptions{DisableFollow: true})
	after := box.scrollView.ScrollTop()
	if after == before {
		return
	}
	edgeY := box.Rect.Y
	if direction > 0 {
		edgeY = box.Rect.Y + box.Rect.Height - 1
	}
	point, ok := s.documentPoint(box, pointerX, edgeY)
	if !ok {
		return
	}
	s.pointerMu.Lock()
	if s.selectionCapture != capture {
		s.pointerMu.Unlock()
		return
	}
	capture.moved = true
	capture.hasLink = false
	capture.linkTarget = ""
	s.pointerMu.Unlock()
	if !s.selection.Active() {
		s.selection.Begin(anchor, generation)
	}
	s.selection.Update(point)
	s.publishCapturedNavCursor(box.scrollView, after)
	s.RequestRender(false)
}

func (s *AltScreen) stopSelectionAutoscroll() {
	s.autoscroll.stop()
}

func (s *AltScreen) copySelection(box *LayoutBox) {
	start, end, ok := s.selection.Range()
	if !ok {
		return
	}
	text := boundedSelectionText(SelectionText(box.scrollContentLines, start, end), maxSelectionClipboardBytes)
	if text == "" {
		return
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	s.Terminal().Write(OSC52Clipboard(encoded))
}

func (s *AltScreen) publishCapturedNavCursor(view *ScrollView, target int) {
	s.frameMu.Lock()
	layout := s.currentLayout
	if layout == nil || layout.PrimaryScrollView != view {
		s.frameMu.Unlock()
		return
	}
	s.navRevision++
	s.navFrame = layout
	s.navCursor = maxInt(0, target)
	s.frameMu.Unlock()
}

func (s *AltScreen) linkAt(row, column int) (string, bool) {
	s.frameMu.RLock()
	layout := s.currentLayout
	s.frameMu.RUnlock()
	if layout == nil || row < 0 || row >= len(layout.Lines) {
		return "", false
	}
	return LinkAt(layout.Lines[row], row, column)
}

func (s *AltScreen) openLink(target string) {
	s.frameMu.RLock()
	opener := s.linkOpener
	s.frameMu.RUnlock()
	if opener == nil || !IsSafeLinkTarget(target) {
		return
	}
	go func() { _ = opener.Open(context.Background(), target) }()
}

func (s *AltScreen) paintSelection(screen []string, layout *LayoutFrame) {
	if layout == nil || layout.PrimaryScrollView == nil || s.selection == nil {
		return
	}
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil || box.scrollView == nil {
		return
	}
	start, end, ok := s.selection.Range()
	if !ok {
		return
	}
	s.frameMu.RLock()
	style := s.selectionStyle
	s.frameMu.RUnlock()
	if style == nil {
		style = func(text string) string { return SGRInverse + text + SGRInverseOff }
	}
	scrollTop := box.scrollView.ScrollTop()
	for line := start.Line; line <= end.Line; line++ {
		if line < 0 || line >= len(box.scrollContentLines) {
			continue
		}
		row := box.Rect.Y + (line - scrollTop)
		if row < box.Rect.Y || row >= box.Rect.Y+box.Rect.Height || row < 0 || row >= len(screen) {
			continue
		}
		lineWidth := VisibleWidth(plainSelectionLine(box.scrollContentLines[line]))
		segmentStart := 0
		if line == start.Line {
			segmentStart = start.Column
		}
		segmentEnd := lineWidth
		if line == end.Line {
			segmentEnd = end.Column
		}
		if segmentEnd > lineWidth {
			segmentEnd = lineWidth
		}
		segmentStart += box.Rect.X
		segmentEnd += box.Rect.X
		if segmentEnd <= segmentStart {
			continue
		}
		text := sliceByColumns(screen[row], segmentStart, segmentEnd-segmentStart, true).text
		if text == "" {
			continue
		}
		screen[row] = replaceColumnRange(screen[row], segmentStart, segmentEnd, style(text))
	}
}

func (s *AltScreen) handleScrollbarPress(event MouseEvent) bool {
	if event.Button != MouseButtonLeft || event.Type != MousePress {
		return false
	}
	s.frameMu.RLock()
	layout := s.currentLayout
	s.frameMu.RUnlock()
	if layout == nil {
		return false
	}
	for _, view := range getScrollViewsAt(layout, event.ScreenX, event.ScreenY) {
		box := GetScrollViewBox(layout, view)
		if box == nil {
			continue
		}
		geometry := getScrollbarGeometry(box, true)
		if geometry == nil || event.ScreenX != geometry.column {
			continue
		}
		if event.ScreenY < geometry.trackTop || event.ScreenY >= geometry.trackTop+geometry.trackHeight {
			continue
		}
		s.pointerMu.Lock()
		s.scrollbarDrag = &scrollbarDragState{
			box:      box,
			view:     view,
			geometry: geometry,
			primary:  layout.PrimaryScrollView == view,
		}
		s.pointerMu.Unlock()
		s.scrollToScrollbar(event.ScreenY)
		return true
	}
	return false
}

func (s *AltScreen) handleScrollbarDragEvent(event MouseEvent) {
	s.pointerMu.Lock()
	drag := s.scrollbarDrag
	s.pointerMu.Unlock()
	if drag == nil {
		return
	}
	if event.Type == MouseRelease || event.Button != MouseButtonLeft {
		s.cancelScrollbarDrag()
		s.RequestRender(false)
		return
	}
	s.scrollToScrollbar(event.ScreenY)
}

func (s *AltScreen) scrollToScrollbar(y int) {
	s.pointerMu.Lock()
	drag := s.scrollbarDrag
	s.pointerMu.Unlock()
	if drag == nil || drag.box == nil || drag.view == nil || drag.geometry == nil {
		return
	}
	geometry := drag.geometry
	maxThumbTop := geometry.trackHeight - geometry.thumbHeight
	if maxThumbTop <= 0 || geometry.maxScrollTop <= 0 {
		return
	}
	position := y - geometry.trackTop - geometry.thumbHeight/2
	position = maxInt(0, minInt(maxThumbTop, position))
	target := position * geometry.maxScrollTop / maxThumbTop
	target = maxInt(0, minInt(geometry.maxScrollTop, target))
	drag.view.ScrollTo(target, ScrollToOptions{DisableFollow: true})
	if drag.primary {
		s.publishCapturedNavCursor(drag.view, target)
	}
	s.RequestRender(false)
}
