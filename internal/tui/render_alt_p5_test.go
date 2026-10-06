package tui

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func newScrollRoot(lines int) *ScrollView {
	content := &staticComponent{lines: linesOf(lines)}
	return NewScrollView(content, ScrollViewOptions{Follow: "end", Primary: true, Scrollbar: ScrollbarAuto})
}

func linesOf(count int) []string {
	out := make([]string, count)
	for index := range out {
		out[index] = string(rune('a'+index%26)) + " line " + itoaTest(index)
	}
	return out
}

func itoaTest(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func TestAltScreenScrollbarDrag(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	root := newScrollRoot(30)
	root.SetScrollbar(ScrollbarAlways)
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)

	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	if layout == nil {
		t.Fatal("no published layout")
	}
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil {
		t.Fatal("no scroll box")
	}
	geometry := getScrollbarGeometry(box, true)
	if geometry == nil {
		t.Fatal("no scrollbar geometry")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: geometry.column, y: geometry.trackTop})
	if screen.scrollbarDrag == nil {
		t.Fatal("scrollbar press did not start a drag")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: geometry.column, y: geometry.trackTop + geometry.trackHeight - 1})
	if root.ScrollTop() == 0 {
		t.Fatal("scrollbar drag did not scroll")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: geometry.column, y: geometry.trackTop, release: true})
	if screen.scrollbarDrag != nil {
		t.Fatal("release did not end the drag")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSelectionCopiesPlainText(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 6)
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)

	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil {
		t.Fatal("no scroll box")
	}
	terminal.ResetWrites()
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	if !screen.hasSelectionCapture() {
		t.Fatal("press did not begin a selection")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: box.Rect.X + 3, y: box.Rect.Y + 1})
	start, end, ok := screen.selection.Range()
	if !ok {
		t.Fatal("drag did not grow the selection")
	}
	expected := SelectionText(box.scrollContentLines, start, end)
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X + 3, y: box.Rect.Y + 1, release: true})
	if screen.selection.Active() {
		t.Fatal("release should clear the selection")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(expected))
	if !strings.Contains(terminal.Output(), encoded) {
		t.Fatalf("clipboard sequence missing for %q: %s", expected, terminal.Output())
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSelectionSuppressesLink(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	linkLine := OSC8Hyperlink("", "https://example.com") + "link" + OSC8Close
	content := &staticComponent{lines: []string{linkLine, "second line", "third line", "fourth line", "fifth line", "sixth line", "seventh"}}
	root := NewScrollView(content, ScrollViewOptions{Primary: true})
	screen.SetLayoutRoot(root)
	opened := make(chan string, 4)
	screen.SetLinkOpener(LinkOpenerFunc(func(ctx context.Context, target string) error {
		opened <- target
		return nil
	}))
	screen.Start()
	waitForAltRender(t, screen)

	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil {
		t.Fatal("no scroll box")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: box.Rect.X + 2, y: box.Rect.Y + 1})
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X + 2, y: box.Rect.Y + 1, release: true})
	select {
	case target := <-opened:
		t.Fatalf("drag should suppress link open, got %q", target)
	case <-time.After(50 * time.Millisecond):
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y, release: true})
	select {
	case target := <-opened:
		if target != "https://example.com" {
			t.Fatalf("unexpected link target %q", target)
		}
	case <-time.After(time.Second):
		t.Fatal("click did not open the link")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSelectionInvalidatedOnContentChange(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	content := &staticComponent{lines: linesOf(30)}
	root := NewScrollView(content, ScrollViewOptions{Primary: true})
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)
	screen.selection.Begin(SelectionPoint{Line: 0, Column: 0}, 1)
	screen.selection.Update(SelectionPoint{Line: 2, Column: 3})
	if !screen.selection.Active() {
		t.Fatal("selection should be active")
	}
	screen.selection.InvalidateOnGeneration(2)
	if screen.selection.Active() {
		t.Fatal("generation change should clear the selection")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenGraphicsCleanupOnStop(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 6)
	screen.AddChild(&plainComponent{lines: []string{"x"}})
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.ImagePlacements().Allocate()
	screen.Start()
	waitForAltRender(t, screen)
	terminal.ResetWrites()
	screen.Stop(StopOptions{})
	if !strings.Contains(terminal.Output(), "\x1b_Ga=d") {
		t.Fatalf("graphics placements were not released: %q", terminal.Output())
	}
}

func TestGraphicsReplyTrackerMalformedAndLate(t *testing.T) {
	tracker := NewGraphicsReplyTracker(10 * time.Millisecond)
	tracker.Start()
	if tracker.Supported() {
		t.Fatal("should not be supported before a reply")
	}
	if tracker.Observe("not a reply") {
		t.Fatal("non-graphics input must not be consumed")
	}
	if !tracker.Observe("\x1b_Gi=31;ENOENT\x1b\\") {
		t.Fatal("malformed graphics reply should be consumed")
	}
	if tracker.Supported() {
		t.Fatal("error reply should not mark support")
	}
	tracker.Expire()
	if tracker.Pending() {
		t.Fatal("expire should clear pending")
	}
	if !tracker.Observe("\x1b_Gi=31;OK\x1b\\") {
		t.Fatal("late reply should be consumed")
	}
	if !tracker.Supported() {
		t.Fatal("late reply should still mark support")
	}
}

func TestKittyProbeDistinctFromKeyboard(t *testing.T) {
	if KittyGraphicsProbe == kittyQuery {
		t.Fatal("graphics probe must differ from the keyboard probe")
	}
	if !strings.HasPrefix(KittyGraphicsProbe, "\x1b_G") {
		t.Fatalf("graphics probe is not a kitty graphics query: %q", KittyGraphicsProbe)
	}
	if !strings.Contains(kittyQuery, "u") {
		t.Fatal("keyboard probe should use the keyboard protocol")
	}
}

func TestAltScreenNonCapturingOverlayFallsThroughToWheel(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)

	screen.ShowOverlay(&plainComponent{lines: []string{"overlay"}}, OverlayOptions{NonCapturing: true, Anchor: AnchorCenter, Width: "100%"})
	waitForAltRender(t, screen)
	before := root.ScrollTop()
	screen.handleViewportInput("\x1b[<64;5;3M")
	if root.ScrollTop() >= before {
		t.Fatalf("wheel did not fall through the non-capturing overlay: %d -> %d", before, root.ScrollTop())
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenCapturingOverlaySwallowsWheel(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)

	screen.ShowOverlay(&plainComponent{lines: []string{"overlay"}}, OverlayOptions{Anchor: AnchorCenter, Width: "100%"})
	waitForAltRender(t, screen)
	before := root.ScrollTop()
	screen.handleViewportInput("\x1b[<64;5;3M")
	if root.ScrollTop() != before {
		t.Fatalf("capturing overlay should swallow the wheel: %d -> %d", before, root.ScrollTop())
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchLifecycleAndHighlight(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 30, 6)
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	screen.SetSearchStyle(func(selected bool, text string) string { return "<" + text + ">" })
	screen.Start()
	screen.RenderNow(true)

	screen.openSearch()
	if !screen.searchActive.Load() || !screen.ModalCapture() {
		t.Fatal("search should activate with modal capture")
	}
	if screen.searchOverlay == nil {
		t.Fatal("search overlay missing")
	}
	screen.searchOverlay.HandleInput("l")
	screen.searchOverlay.HandleInput("i")
	screen.searchOverlay.HandleInput("n")
	screen.searchOverlay.HandleInput("e")
	screen.searchOverlay.HandleInput(" ")
	screen.searchOverlay.HandleInput("2")
	screen.RenderNow(true)
	if screen.Search().MatchCount() == 0 {
		t.Fatal("expected matches for the typed query")
	}
	terminal.ResetWrites()
	screen.RenderNow(true)
	if !strings.Contains(terminal.Output(), "<line 2>") {
		t.Fatalf("search highlight missing: %q", terminal.Output())
	}
	screen.closeSearch()
	if screen.searchActive.Load() || screen.ModalCapture() {
		t.Fatal("search close should release modal capture")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchModalSupersedesSearch(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.AddChild(&plainComponent{lines: []string{"content"}})
	screen.Start()
	waitForAltRender(t, screen)
	screen.SetModalCapture(true)
	screen.openSearch()
	if screen.searchActive.Load() {
		t.Fatal("an open modal must supersede search")
	}
	screen.SetModalCapture(false)
	screen.openSearch()
	if !screen.searchActive.Load() {
		t.Fatal("search should open once the modal is gone")
	}
	screen.closeSearch()
	screen.Stop(StopOptions{})
}

func TestAltScreenSelectionPainting(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 30, 6)
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	screen.SetSelectionStyle(func(text string) string { return "<" + text + ">" })
	screen.Start()
	waitForAltRender(t, screen)

	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	top := box.scrollView.ScrollTop()
	screen.selection.Begin(SelectionPoint{Line: top, Column: 0}, screen.contentGeneration(box))
	screen.selection.Update(SelectionPoint{Line: top + 1, Column: 3})
	terminal.ResetWrites()
	screen.RequestRender(true)
	waitForAltRender(t, screen)
	if !strings.Contains(terminal.Output(), "<") || !strings.Contains(terminal.Output(), ">") {
		t.Fatalf("selection highlight missing: %q", terminal.Output())
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSelectionAutoscrollLifecycle(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	root := newScrollRoot(40)
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)

	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: box.Rect.X, y: -1})
	if !screen.autoscroll.running() {
		t.Fatal("dragging past the top edge should start autoscroll")
	}
	deadline := time.Now().Add(time.Second)
	for root.ScrollTop() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if root.ScrollTop() == 0 {
		t.Fatal("autoscroll did not scroll")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: -1, release: true})
	if screen.autoscroll.running() {
		t.Fatal("release should stop autoscroll")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenTranscriptHelpers(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)
	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)

	if _, ok := screen.documentPoint(box, box.Rect.X-5, box.Rect.Y-5); !ok {
		t.Fatal("clamped document point should resolve")
	}
	if point, _ := screen.documentPoint(box, box.Rect.X-5, box.Rect.Y-5); point.Line < 0 || point.Column != 0 {
		t.Fatalf("negative clamp wrong: %+v", point)
	}
	if point, _ := screen.documentPoint(box, box.Rect.X+1000, box.Rect.Y+1000); point.Line != len(box.scrollContentLines)-1 {
		t.Fatalf("positive clamp wrong: %+v", point)
	}

	if _, ok := screen.linkAt(9999, 0); ok {
		t.Fatal("linkAt outside the frame should miss")
	}
	screen.openLink("https://example.com")
	screen.openLink("javascript:alert(1)")

	capture := newTranscriptPointerCapture(box, screen.contentGeneration(box), screen.layoutEpochSnapshot(), SelectionPoint{Line: 0, Column: 0}, box.Rect.X, box.Rect.Y)
	screen.pointerMu.Lock()
	screen.selectionCapture = capture
	screen.pointerMu.Unlock()
	screen.updateAutoscroll(capture, box.Rect.Y-1)
	if !screen.autoscroll.running() {
		t.Fatal("above-edge drag should activate autoscroll")
	}
	screen.updateAutoscroll(capture, box.Rect.Y+1)
	if screen.autoscroll.running() {
		t.Fatal("inside drag should stop autoscroll")
	}
	screen.updateAutoscroll(capture, box.Rect.Y+box.Rect.Height+1)
	if !screen.autoscroll.running() {
		t.Fatal("below-edge drag should activate autoscroll")
	}
	screen.stopSelectionAutoscroll()
	screen.clearSelectionCapture()

	screen.selection.Begin(SelectionPoint{Line: 0, Column: 0}, 0)
	screen.selection.Update(SelectionPoint{Line: 0, Column: 0})
	screen.copySelection(box)
	screen.selection.Clear()
	screen.Stop(StopOptions{})
}

func TestAltScreenSelectionGuards(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	screen.Start()
	waitForAltRender(t, screen)
	if screen.handleSelectionMouse(MouseEvent{Type: MouseDrag, Button: MouseButtonLeft, ScreenX: 0, ScreenY: 0}) {
		t.Fatal("drag without an anchor should be ignored")
	}
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	waitForAltRender(t, screen)
	if screen.handleSelectionMouse(MouseEvent{Type: MousePress, Button: MouseButtonLeft, ScreenX: 999, ScreenY: 999}) {
		t.Fatal("press outside the transcript should be ignored")
	}
	if screen.handleSelectionMouse(MouseEvent{Type: MouseRelease, Button: MouseButtonLeft, ScreenX: 0, ScreenY: 0}) {
		t.Fatal("release without an anchor should be ignored")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenScrollbarDragReleaseWithoutButton(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	root := newScrollRoot(30)
	root.SetScrollbar(ScrollbarAlways)
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)
	screen.scrollbarDrag = &scrollbarDragState{}
	screen.handleScrollbarDragEvent(MouseEvent{Type: MouseMove, Button: MouseButtonNone})
	if screen.scrollbarDrag != nil {
		t.Fatal("a non-drag event should end the scrollbar drag")
	}
	screen.scrollToScrollbar(3)
	screen.Stop(StopOptions{})
}

func TestAltScreenP5AccessorsAndGuards(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	if screen.Selection() == nil {
		t.Fatal("selection accessor missing")
	}
	if screen.Search() == nil {
		t.Fatal("search accessor missing")
	}
	screen.paintSelection(nil, nil)
	screen.paintSelection([]string{"x"}, nil)
	if sequence := screen.placementPass(nil, 6); sequence != "" {
		t.Fatalf("no layout should emit no graphics: %q", sequence)
	}
	if screen.handleScrollbarPress(MouseEvent{Type: MousePress, Button: MouseButtonRight, ScreenX: 0, ScreenY: 0}) {
		t.Fatal("right button should not start a scrollbar drag")
	}
	if screen.handleScrollbarPress(MouseEvent{Type: MouseRelease, Button: MouseButtonLeft, ScreenX: 0, ScreenY: 0}) {
		t.Fatal("release should not start a scrollbar drag")
	}
	screen.scrollbarDrag = &scrollbarDragState{}
	screen.scrollToScrollbar(0)
	screen.scrollbarDrag = nil
	screen.copySelection(&LayoutBox{})
	if capability := screen.Graphics(); capability.Protocol != GraphicsNone {
		t.Fatalf("default graphics %+v", capability)
	}
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsITerm2, Enabled: false})
	if screen.Graphics().Available() {
		t.Fatal("disabled capability should not be available")
	}
	screen.Stop(StopOptions{})
}
