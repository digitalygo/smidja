package tui

import (
	"context"
	"encoding/base64"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func numberedLines(count int) []string {
	out := make([]string, count)
	for index := range out {
		out[index] = "line-" + twoDigitString(index)
	}
	return out
}

func twoDigitString(value int) string {
	if value < 10 {
		return "0" + string(rune('0'+value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}

func altNavCursor(screen *AltScreen) int {
	screen.frameMu.RLock()
	defer screen.frameMu.RUnlock()
	return screen.navCursor
}

func decodeOSC52(output string) (string, bool) {
	const marker = "\x1b]52;c;"
	index := strings.Index(output, marker)
	if index < 0 {
		return "", false
	}
	rest := output[index+len(marker):]
	end := strings.Index(rest, ANSIBEL)
	if end < 0 {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(rest[:end])
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

func startTranscriptScreen(t *testing.T, width, height int, root Component) *AltScreen {
	t.Helper()
	screen, _ := newTestAltScreen(t, width, height)
	screen.autoscrollInterval = time.Hour
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)
	return screen
}

func TestPointerCapturePressMoveTickRelease(t *testing.T) {
	content := &staticComponent{lines: numberedLines(30)}
	root := NewScrollView(content, ScrollViewOptions{Primary: true, Scrollbar: ScrollbarAlways})
	screen, terminal := newTestAltScreen(t, 12, 4)
	screen.autoscrollInterval = time.Hour
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)

	box := screen.transcriptBox()
	if box == nil {
		t.Fatal("no transcript box")
	}
	if box.Rect.Height != 4 {
		t.Fatalf("viewport height %d", box.Rect.Height)
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	capture := screen.currentSelectionCapture()
	if capture == nil {
		t.Fatal("press did not capture the pointer")
	}
	if capture.anchor != (SelectionPoint{Line: 0, Column: 0}) {
		t.Fatalf("captured anchor %+v", capture.anchor)
	}
	if capture.generation != screen.contentGeneration(box) {
		t.Fatal("captured generation differs from the published frame")
	}
	if capture.layoutEpoch != screen.layoutEpochSnapshot() {
		t.Fatal("captured layout epoch differs")
	}

	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: box.Rect.X + 4, y: box.Rect.Y + 1})
	start, end, ok := screen.selection.Range()
	if !ok || start != (SelectionPoint{Line: 0, Column: 0}) || end != (SelectionPoint{Line: 1, Column: 4}) {
		t.Fatalf("after move range %+v %+v ok=%v", start, end, ok)
	}

	edgeY := box.Rect.Y + box.Rect.Height
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: box.Rect.X + 4, y: edgeY})
	if screen.autoscroll.direction() != 1 {
		t.Fatalf("edge direction %d", screen.autoscroll.direction())
	}
	screen.autoscroll.stop()
	screen.selectionAutoscrollStep(capture)
	screen.selectionAutoscrollStep(capture)
	if root.ScrollTop() != 2 {
		t.Fatalf("autoscroll scroll top %d", root.ScrollTop())
	}
	_, end, _ = screen.selection.Range()
	if end.Line != root.ScrollTop()+box.Rect.Height-1 {
		t.Fatalf("autoscroll endpoint line %d for scroll top %d", end.Line, root.ScrollTop())
	}
	if altNavCursor(screen) != root.ScrollTop() {
		t.Fatalf("nav cursor %d did not follow the captured viewport %d", altNavCursor(screen), root.ScrollTop())
	}

	expected := SelectionText(box.scrollContentLines, start, end)
	terminal.ResetWrites()
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X + 4, y: edgeY, release: true})
	if screen.hasSelectionCapture() {
		t.Fatal("release did not clear the pointer capture")
	}
	if screen.selection.Active() {
		t.Fatal("release did not clear the selection")
	}
	copied, ok := decodeOSC52(terminal.Output())
	if !ok || copied != expected {
		t.Fatalf("clipboard %q, want %q", copied, expected)
	}
	screen.Stop(StopOptions{})
}

func TestPointerCaptureFrameChangeCancels(t *testing.T) {
	content := &staticComponent{lines: numberedLines(30)}
	root := NewScrollView(content, ScrollViewOptions{Primary: true})
	screen := startTranscriptScreen(t, 12, 4, root)
	box := screen.transcriptBox()
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	if !screen.hasSelectionCapture() {
		t.Fatal("press did not capture")
	}
	content.lines = append(content.lines, "line-30")
	screen.RequestRender(false)
	waitForAltRender(t, screen)
	if screen.hasSelectionCapture() {
		t.Fatal("frame generation change did not cancel the capture")
	}
	screen.Stop(StopOptions{})
}

func TestPointerCaptureStationaryLinkOpens(t *testing.T) {
	linkLine := OSC8Hyperlink("", "https://example.com") + "link" + OSC8Close
	content := &staticComponent{lines: []string{linkLine, "second", "third", "fourth", "fifth", "sixth"}}
	root := NewScrollView(content, ScrollViewOptions{Primary: true})
	screen, _ := newTestAltScreen(t, 30, 6)
	opened := make(chan string, 4)
	screen.SetLinkOpener(LinkOpenerFunc(func(ctx context.Context, target string) error {
		opened <- target
		return nil
	}))
	screen.autoscrollInterval = time.Hour
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)
	box := screen.transcriptBox()

	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	capture := screen.currentSelectionCapture()
	if capture == nil || !capture.hasLink || capture.linkTarget != "https://example.com" {
		t.Fatalf("link not captured: %+v", capture)
	}
	if screen.selection.Active() {
		t.Fatal("link press should not begin a text selection")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y, release: true})
	select {
	case target := <-opened:
		if target != "https://example.com" {
			t.Fatalf("unexpected link %q", target)
		}
	case <-time.After(time.Second):
		t.Fatal("stationary same-frame click did not open the link")
	}
	if screen.hasSelectionCapture() {
		t.Fatal("link release did not clear the capture")
	}
	screen.Stop(StopOptions{})
}

func TestPointerCaptureDragSuppressesLink(t *testing.T) {
	linkLine := OSC8Hyperlink("", "https://example.com") + "link" + OSC8Close
	content := &staticComponent{lines: []string{linkLine, "second", "third", "fourth", "fifth", "sixth"}}
	root := NewScrollView(content, ScrollViewOptions{Primary: true})
	screen, _ := newTestAltScreen(t, 30, 6)
	opened := make(chan string, 4)
	screen.SetLinkOpener(LinkOpenerFunc(func(ctx context.Context, target string) error {
		opened <- target
		return nil
	}))
	screen.autoscrollInterval = time.Hour
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)
	box := screen.transcriptBox()
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	capture := screen.currentSelectionCapture()
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: box.Rect.X + 2, y: box.Rect.Y + 1})
	if capture.hasLink || capture.linkTarget != "" {
		t.Fatal("drag did not suppress the captured link")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X + 2, y: box.Rect.Y + 1, release: true})
	select {
	case target := <-opened:
		t.Fatalf("drag released a link: %q", target)
	case <-time.After(50 * time.Millisecond):
	}
	screen.Stop(StopOptions{})
}

func TestPointerCaptureModalSearchSuspendStopCancel(t *testing.T) {
	newCaptured := func(t *testing.T) *AltScreen {
		t.Helper()
		root := NewScrollView(&staticComponent{lines: numberedLines(30)}, ScrollViewOptions{Primary: true, Scrollbar: ScrollbarAlways})
		screen := startTranscriptScreen(t, 12, 4, root)
		box := screen.transcriptBox()
		screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
		if !screen.hasSelectionCapture() {
			t.Fatal("press did not capture")
		}
		return screen
	}

	t.Run("modal", func(t *testing.T) {
		screen := newCaptured(t)
		screen.SetModalCapture(true)
		if screen.hasSelectionCapture() {
			t.Fatal("modal capture did not cancel the pointer capture")
		}
		screen.SetModalCapture(false)
		screen.Stop(StopOptions{})
	})

	t.Run("search", func(t *testing.T) {
		screen := newCaptured(t)
		screen.openSearch()
		if screen.hasSelectionCapture() {
			t.Fatal("search takeover did not cancel the pointer capture")
		}
		screen.closeSearch()
		screen.Stop(StopOptions{})
	})

	t.Run("suspend", func(t *testing.T) {
		screen := newCaptured(t)
		screen.SuspendScreen()
		if screen.hasSelectionCapture() {
			t.Fatal("suspend did not cancel the pointer capture")
		}
		if screen.autoscroll.running() {
			t.Fatal("suspend did not stop autoscroll")
		}
		screen.ResumeScreen()
		screen.Stop(StopOptions{})
	})

	t.Run("stop", func(t *testing.T) {
		screen := newCaptured(t)
		screen.Stop(StopOptions{})
		if screen.hasSelectionCapture() {
			t.Fatal("stop did not cancel the pointer capture")
		}
		if screen.autoscroll.running() {
			t.Fatal("stop did not stop autoscroll")
		}
	})

	t.Run("layout", func(t *testing.T) {
		screen := newCaptured(t)
		screen.SetLayoutRoot(NewScrollView(&staticComponent{lines: numberedLines(10)}, ScrollViewOptions{Primary: true}))
		if screen.hasSelectionCapture() {
			t.Fatal("layout change did not cancel the pointer capture")
		}
		screen.Stop(StopOptions{})
	})

	t.Run("focus", func(t *testing.T) {
		screen := newCaptured(t)
		screen.handleViewportInput(FocusOut)
		if screen.hasSelectionCapture() {
			t.Fatal("focus loss did not cancel the pointer capture")
		}
		screen.Stop(StopOptions{})
	})

	t.Run("scrollbar", func(t *testing.T) {
		root := NewScrollView(&staticComponent{lines: numberedLines(30)}, ScrollViewOptions{Primary: true, Scrollbar: ScrollbarAlways})
		screen := startTranscriptScreen(t, 12, 4, root)
		box := screen.transcriptBox()
		geometry := getScrollbarGeometry(box, true)
		if geometry == nil {
			t.Fatal("no scrollbar geometry")
		}
		screen.handleScrollbarPress(MouseEvent{Type: MousePress, Button: MouseButtonLeft, ScreenX: geometry.column, ScreenY: geometry.trackTop})
		if screen.scrollbarDrag == nil {
			t.Fatal("scrollbar press did not capture")
		}
		screen.SetModalCapture(true)
		if screen.scrollbarDrag != nil {
			t.Fatal("modal capture did not cancel the scrollbar capture")
		}
		screen.SetModalCapture(false)
		screen.Stop(StopOptions{})
	})
}

func TestPointerCaptureAutoscrollTimerLifecycle(t *testing.T) {
	content := &staticComponent{lines: numberedLines(60)}
	root := NewScrollView(content, ScrollViewOptions{Primary: true})
	screen, _ := newTestAltScreen(t, 12, 4)
	screen.SetLayoutRoot(root)
	screen.autoscrollInterval = 5 * time.Millisecond
	screen.Start()
	waitForAltRender(t, screen)
	box := screen.transcriptBox()

	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y})
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: box.Rect.X, y: box.Rect.Y + box.Rect.Height})
	if !screen.autoscroll.running() {
		t.Fatal("edge drag did not start the autoscroll timer")
	}
	deadline := time.Now().Add(time.Second)
	for root.ScrollTop() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if root.ScrollTop() == 0 {
		t.Fatal("autoscroll timer did not tick")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: box.Rect.X, y: box.Rect.Y + box.Rect.Height, release: true})
	if screen.autoscroll.running() {
		t.Fatal("release did not stop the autoscroll timer")
	}
	if screen.hasSelectionCapture() {
		t.Fatal("release did not clear the capture")
	}
	screen.Stop(StopOptions{})
}

func TestAutoscrollControllerStopBarrier(t *testing.T) {
	var controller autoscrollController
	var ticks atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.start(1, time.Millisecond, func(int) {
		if ticks.Add(1) == 1 {
			close(entered)
			<-release
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("autoscroll callback did not run")
	}
	stopped := make(chan struct{})
	go func() {
		controller.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned before the running callback finished")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not return after the callback finished")
	}
	if controller.running() {
		t.Fatal("controller still reports running")
	}
	before := ticks.Load()
	time.Sleep(20 * time.Millisecond)
	if ticks.Load() != before {
		t.Fatal("controller ticked after stop")
	}
}

func TestAutoscrollControllerDirectionChangeJoins(t *testing.T) {
	var controller autoscrollController
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.start(1, time.Millisecond, func(int) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
	})
	<-entered
	started := make(chan struct{})
	go func() {
		controller.start(-1, time.Millisecond, func(int) {})
		close(started)
	}()
	select {
	case <-started:
		t.Fatal("direction change returned before joining the running callback")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("direction change did not restart after the join")
	}
	if controller.direction() != -1 {
		t.Fatalf("direction %d", controller.direction())
	}
	controller.stop()
}

func TestScrollbarCaptureNestedDoesNotMovePrimaryCursor(t *testing.T) {
	inner := NewScrollView(&staticComponent{lines: lines(30, 'i')}, ScrollViewOptions{Scrollbar: ScrollbarAlways})
	innerHost := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: inner, Options: StackEntryOptions{Basis: intPtr(3), Shrink: ShrinkNone}},
	}}}
	trailer := &plainComponent{lines: lines(20, 't')}
	outerContent := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: innerHost},
		{Component: trailer},
	}}}
	outer := NewScrollView(outerContent, ScrollViewOptions{Primary: true, Scrollbar: ScrollbarAlways})
	screen := startTranscriptScreen(t, 20, 6, outer)

	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	innerBox := GetScrollViewBox(layout, inner)
	outerBox := GetScrollViewBox(layout, outer)
	if innerBox == nil || outerBox == nil {
		t.Fatal("missing nested scroll boxes")
	}
	innerGeometry := getScrollbarGeometry(innerBox, true)
	outerGeometry := getScrollbarGeometry(outerBox, true)
	if innerGeometry == nil || outerGeometry == nil {
		t.Fatal("missing nested scrollbar geometry")
	}

	before := altNavCursor(screen)
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: innerGeometry.column, y: innerGeometry.trackTop})
	if screen.scrollbarDrag == nil || screen.scrollbarDrag.view != inner {
		t.Fatalf("nested scrollbar not captured: %+v", screen.scrollbarDrag)
	}
	if screen.scrollbarDrag.primary {
		t.Fatal("nested scrollbar marked primary")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: innerGeometry.column, y: innerGeometry.trackTop + innerGeometry.trackHeight - 1})
	if inner.ScrollTop() == 0 {
		t.Fatal("nested scrollbar drag did not scroll the nested view")
	}
	if altNavCursor(screen) != before {
		t.Fatalf("nested scrollbar drag moved the primary nav cursor: %d -> %d", before, altNavCursor(screen))
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: innerGeometry.column, y: innerGeometry.trackTop, release: true})
	if screen.scrollbarDrag != nil {
		t.Fatal("release did not end the nested scrollbar drag")
	}

	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: outerGeometry.column, y: outerGeometry.trackTop + 4})
	if screen.scrollbarDrag == nil || screen.scrollbarDrag.view != outer || !screen.scrollbarDrag.primary {
		t.Fatalf("primary scrollbar not captured: %+v", screen.scrollbarDrag)
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 32, x: outerGeometry.column, y: outerGeometry.trackTop + outerGeometry.trackHeight - 1})
	if outer.ScrollTop() == 0 {
		t.Fatal("primary scrollbar drag did not scroll")
	}
	if altNavCursor(screen) != outer.ScrollTop() {
		t.Fatalf("primary nav cursor %d does not follow the captured view %d", altNavCursor(screen), outer.ScrollTop())
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: outerGeometry.column, y: outerGeometry.trackTop + 4, release: true})
	screen.Stop(StopOptions{})
}

func TestWheelContainBlocksPrimaryFallback(t *testing.T) {
	build := func(overscroll string) (*AltScreen, *ScrollView, *ScrollView) {
		primary := NewScrollView(&staticComponent{lines: lines(30, 'p')}, ScrollViewOptions{Primary: true})
		nested := NewScrollView(&staticComponent{lines: lines(30, 'n')}, ScrollViewOptions{Overscroll: overscroll})
		root := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
			{Component: primary, Options: StackEntryOptions{Basis: intPtr(3), Shrink: ShrinkNone}},
			{Component: nested, Options: StackEntryOptions{Basis: intPtr(3), Shrink: ShrinkNone}},
		}}}
		screen := startTranscriptScreen(t, 20, 6, root)
		primary.ScrollTo(10, ScrollToOptions{DisableFollow: true})
		waitForAltRender(t, screen)
		return screen, primary, nested
	}

	t.Run("contain", func(t *testing.T) {
		screen, primary, nested := build("contain")
		if nested.ScrollTop() != 0 {
			t.Fatalf("nested top %d", nested.ScrollTop())
		}
		before := primary.ScrollTop()
		screen.handleViewportInput("\x1b[<64;2;4M")
		if nested.ScrollTop() != 0 {
			t.Fatalf("nested scrolled on contain: %d", nested.ScrollTop())
		}
		if primary.ScrollTop() != before {
			t.Fatalf("contain leaked to the primary fallback: %d -> %d", before, primary.ScrollTop())
		}
		screen.Stop(StopOptions{})
	})

	t.Run("chain", func(t *testing.T) {
		screen, primary, nested := build("")
		before := primary.ScrollTop()
		screen.handleViewportInput("\x1b[<64;2;4M")
		if nested.ScrollTop() != 0 {
			t.Fatalf("nested scrolled: %d", nested.ScrollTop())
		}
		if primary.ScrollTop() != before-1 {
			t.Fatalf("chain did not pass residual to the primary fallback: %d -> %d", before, primary.ScrollTop())
		}
		screen.Stop(StopOptions{})
	})
}

func TestWheelFallbackScrollsUnseenPrimary(t *testing.T) {
	innerContent := &plainComponent{lines: lines(30, 'i')}
	inner := NewScrollView(innerContent, ScrollViewOptions{Primary: true})
	innerHost := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: inner, Options: StackEntryOptions{Basis: intPtr(3), Shrink: ShrinkNone}},
	}}}
	trailer := &plainComponent{lines: lines(20, 't')}
	outerContent := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: innerHost},
		{Component: trailer},
	}}}
	outer := NewScrollView(outerContent, ScrollViewOptions{})
	screen := startTranscriptScreen(t, 20, 10, outer)
	inner.ScrollTo(5, ScrollToOptions{DisableFollow: true})
	waitForAltRender(t, screen)
	screen.handleViewportInput("\x1b[<64;2;9M")
	if inner.ScrollTop() != 4 {
		t.Fatalf("unseen primary did not receive the fallback wheel: %d", inner.ScrollTop())
	}
	screen.Stop(StopOptions{})
}

func TestSelectionTextExcludesControlsAndPadding(t *testing.T) {
	lines := []string{
		"\x1b[31mhéllo \x00\x020\x00\x02 wörld   \x1b[0m",
		"漢字テスト     ",
	}
	text := SelectionText(lines, SelectionPoint{Line: 0, Column: 0}, SelectionPoint{Line: 1, Column: 10})
	if text != "héllo  wörld\n漢字テスト" {
		t.Fatalf("selection text %q", text)
	}
	if strings.ContainsAny(text, "\x00\x02\x1b") {
		t.Fatalf("selection text leaked control payload: %q", text)
	}
}

func TestBoundedSelectionTextGraphemeSafe(t *testing.T) {
	if got := boundedSelectionText("abcdef", 3); got != "abc" {
		t.Fatalf("bounded %q", got)
	}
	wide := strings.Repeat("a", 4) + "é" + strings.Repeat("b", 8)
	bound := boundedSelectionText(wide, 5)
	if bound != "aaaa" {
		t.Fatalf("multibyte boundary split: %q", bound)
	}
	if got := boundedSelectionText("short", maxSelectionClipboardBytes); got != "short" {
		t.Fatalf("under bound changed: %q", got)
	}
}

func TestSelectionCopyUnicodeAndBoundedOSC52(t *testing.T) {
	lines := []string{"a漢b𠀋c    ", "e\u0301fgh     "}
	screen, terminal := newTestAltScreen(t, 14, 4)
	root := NewScrollView(&staticComponent{lines: lines}, ScrollViewOptions{Primary: true})
	screen.autoscrollInterval = time.Hour
	screen.SetLayoutRoot(root)
	screen.Start()
	waitForAltRender(t, screen)
	box := screen.transcriptBox()
	screen.selection.Begin(SelectionPoint{Line: 0, Column: 0}, screen.contentGeneration(box))
	screen.selection.Update(SelectionPoint{Line: 1, Column: 4})
	terminal.ResetWrites()
	screen.copySelection(box)
	decoded, ok := decodeOSC52(terminal.Output())
	if !ok {
		t.Fatalf("no clipboard payload: %q", terminal.Output())
	}
	if decoded != "a漢b𠀋c\ne\u0301fgh" {
		t.Fatalf("unicode clipboard %q", decoded)
	}
	if len(decoded) > maxSelectionClipboardBytes {
		t.Fatalf("clipboard payload %d exceeds bound", len(decoded))
	}
	screen.Stop(StopOptions{})
}

func TestNonCapturingOverlayMouseFallsThroughToTranscript(t *testing.T) {
	root := NewScrollView(&staticComponent{lines: numberedLines(30)}, ScrollViewOptions{Primary: true})
	screen := startTranscriptScreen(t, 20, 6, root)

	nonCapturing := screen.ShowOverlay(&plainComponent{lines: []string{"overlay"}}, OverlayOptions{NonCapturing: true, Anchor: AnchorCenter, Width: "100%"})
	waitForAltRender(t, screen)
	nonBounds, ok := nonCapturing.Bounds()
	if !ok {
		t.Fatal("non-capturing overlay has no bounds")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: nonBounds.Col, y: nonBounds.Row})
	if !screen.hasSelectionCapture() {
		t.Fatal("non-capturing overlay blocked transcript pointer capture")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: nonBounds.Col, y: nonBounds.Row, release: true})
	nonCapturing.Hide()
	waitForAltRender(t, screen)

	capturing := screen.ShowOverlay(&plainComponent{lines: []string{"overlay"}}, OverlayOptions{Anchor: AnchorCenter, Width: "100%"})
	waitForAltRender(t, screen)
	captureBounds, ok := capturing.Bounds()
	if !ok {
		t.Fatal("capturing overlay has no bounds")
	}
	screen.handleMouseEvent(parsedMouseEvent{button: 0, x: captureBounds.Col, y: captureBounds.Row})
	if screen.hasSelectionCapture() {
		t.Fatal("capturing overlay did not swallow the pointer press")
	}
	capturing.Hide()
	screen.Stop(StopOptions{})
}
