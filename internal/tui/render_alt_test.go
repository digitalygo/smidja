package tui

import (
	"strings"
	"testing"
	"time"
)

func newTestAltScreen(t *testing.T, width, height int) (*AltScreen, *fakeTerminal) {
	t.Helper()
	terminal := newFakeTerminal(width, height)
	screen := NewAltScreen(terminal, false, AltScreenOptions{})
	screen.SetMinRenderInterval(0)
	renders := make(chan struct{}, 64)
	screen.hooks.doRender = func() {
		screen.doRender()
		select {
		case renders <- struct{}{}:
		default:
		}
	}
	renderSignalsAlt[screen] = renders
	t.Cleanup(func() { delete(renderSignalsAlt, screen) })
	return screen, terminal
}

var renderSignalsAlt = map[*AltScreen]chan struct{}{}

func waitForAltRender(t *testing.T, screen *AltScreen) {
	t.Helper()
	select {
	case <-renderSignalsAlt[screen]:
	case <-time.After(2 * time.Second):
		t.Fatal("alt render did not run")
	}
}

func TestAltScreenEnterAndRender(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 5)
	screen.AddChild(&plainComponent{lines: []string{"alpha", "beta"}})
	screen.Start()
	waitForAltRender(t, screen)

	output := terminal.Output()
	if !strings.Contains(output, AltScreenEnter) {
		t.Fatalf("alt screen not entered: %q", output)
	}
	if !strings.Contains(output, AutowrapDisable) {
		t.Fatalf("autowrap not disabled: %q", output)
	}
	if !strings.Contains(output, MouseSGROn) {
		t.Fatalf("mouse not enabled: %q", output)
	}
	if !strings.Contains(output, SyncOutputBegin) {
		t.Fatalf("frame not synchronized: %q", output)
	}
	if !strings.Contains(output, "alpha") || !strings.Contains(output, "beta") {
		t.Fatalf("content not rendered: %q", output)
	}
	if !strings.Contains(output, CursorTo(0, 0)) {
		t.Fatalf("rows should be positioned explicitly: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenPerRowUpdates(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 4)
	content := &plainComponent{lines: []string{"aaa", "bbb", "ccc"}}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	terminal.ResetWrites()

	content.lines[2] = "zzz"
	screen.RequestRender(false)
	waitForAltRender(t, screen)

	output := terminal.Output()
	if !strings.Contains(output, "zzz") {
		t.Fatalf("changed row not rendered: %q", output)
	}
	if strings.Count(output, CursorEraseLine) != 1 {
		t.Fatalf("only changed row should be erased: %d in %q", strings.Count(output, CursorEraseLine), output)
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenExitPrintsFinalDocument(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 3)
	screen.AddChild(&plainComponent{lines: []string{"final one", "final two", "final three"}})
	screen.Start()
	waitForAltRender(t, screen)
	terminal.ResetWrites()

	screen.Stop(StopOptions{})
	output := terminal.Output()
	if !strings.Contains(output, AltScreenExit) {
		t.Fatalf("alt screen not exited: %q", output)
	}
	if !strings.Contains(output, "final one") || !strings.Contains(output, "final three") {
		t.Fatalf("final document not printed: %q", output)
	}
	if !strings.Contains(output, AutowrapEnable) {
		t.Fatalf("autowrap should be re-enabled: %q", output)
	}
	if !strings.Contains(output, CursorShow) {
		t.Fatalf("cursor should be shown after exit: %q", output)
	}
	if len(screen.lastDocument) != 3 {
		t.Fatalf("lastDocument = %d lines", len(screen.lastDocument))
	}
}

func TestAltScreenFinalDocumentStripsMarkersAndResets(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 2)
	screen.AddChild(&staticComponent{lines: []string{"ab" + CursorMarker + "cd", "\x1b[31mstyled"}})
	screen.Start()
	waitForAltRender(t, screen)
	screen.Stop(StopOptions{})
	for i, line := range screen.lastDocument {
		if strings.Contains(line, CursorMarker) {
			t.Fatalf("line %d still has cursor marker: %q", i, line)
		}
		if !strings.HasSuffix(line, SegmentReset) {
			t.Fatalf("line %d missing trailing reset: %q", i, line)
		}
	}
}

func TestAltScreenStopPreserveScreen(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 3)
	screen.AddChild(&plainComponent{lines: []string{"hidden"}})
	screen.Start()
	waitForAltRender(t, screen)
	terminal.ResetWrites()

	screen.Stop(StopOptions{PreserveScreen: true})
	output := terminal.Output()
	if strings.Contains(output, "hidden") {
		t.Fatalf("preserve screen should not print content: %q", output)
	}
	if !strings.Contains(output, AltScreenExit) {
		t.Fatalf("alt screen should still exit: %q", output)
	}
}

func TestAltScreenImplicitScrollViewFollowsEnd(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	content := &plainComponent{lines: lines(20, 'x')}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)

	if screen.implicitScrollView.ScrollTop() != 16 {
		t.Fatalf("implicit scroll top = %d, want 16", screen.implicitScrollView.ScrollTop())
	}
	for i := 0; i < 5; i++ {
		content.lines = append(content.lines, "more")
	}
	screen.RequestRender(false)
	waitForAltRender(t, screen)
	if screen.implicitScrollView.ScrollTop() != len(content.lines)-4 {
		t.Fatalf("implicit view should follow end: %d", screen.implicitScrollView.ScrollTop())
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenScrollKeybindings(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 5)
	content := &plainComponent{lines: lines(40, 'c')}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	view := screen.implicitScrollView

	screen.handleViewportInput("\x1b[5~")
	if top := view.ScrollTop(); top != 34 {
		t.Fatalf("pageUp top = %d, want 34", top)
	}
	screen.handleViewportInput("\x1b[6~")
	if !view.IsFollowingEnd() {
		t.Fatal("pageDown should reach the end")
	}
	screen.handleViewportInput("\x1b[H")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("top binding = %d", top)
	}
	screen.handleViewportInput("\x1b[F")
	if !view.IsFollowingEnd() {
		t.Fatal("bottom binding should follow end")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenPromptJump(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	content := &plainComponent{lines: []string{
		OSC133PromptStart + "prompt 1", "a", "b", "c",
		OSC133PromptStart + "prompt 2", "d", "e", "f", "g", "h",
	}}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	view := screen.implicitScrollView

	screen.handleViewportInput("\x1b[1;5A")
	if view.ScrollTop() != 4 {
		t.Fatalf("previous prompt = %d, want 4", view.ScrollTop())
	}
	screen.handleViewportInput("\x1b[H")
	screen.handleViewportInput("\x1b[1;5B")
	if view.ScrollTop() != 4 {
		t.Fatalf("next prompt = %d, want 4", view.ScrollTop())
	}
	screen.handleViewportInput("\x1b[1;5B")
	if view.ScrollTop() != 4 {
		t.Fatalf("next prompt without more prompts should stay: %d", view.ScrollTop())
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenWheelRoutingWithChaining(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 6)
	inner := NewScrollView(&staticComponent{lines: lines(30, 'i')}, ScrollViewOptions{})
	innerHost := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: inner, Options: StackEntryOptions{Basis: intPtr(3), Shrink: ShrinkNone}},
	}}}
	trailer := &plainComponent{lines: lines(20, 't')}
	outerContent := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: innerHost},
		{Component: trailer},
	}}}
	outer := NewScrollView(outerContent, ScrollViewOptions{})
	screen.SetLayoutRoot(outer)
	screen.Start()
	waitForAltRender(t, screen)

	screen.handleViewportInput("\x1b[<65;5;2M")
	if inner.ScrollTop() != 1 {
		t.Fatalf("inner wheel scroll = %d, want 1", inner.ScrollTop())
	}
	for i := 0; i < 40; i++ {
		screen.handleViewportInput("\x1b[<65;5;2M")
	}
	if inner.ScrollTop() != 27 {
		t.Fatalf("inner should be pinned at its end: %d", inner.ScrollTop())
	}
	if outer.ScrollTop() == 0 {
		t.Fatal("wheel should chain into the outer view")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenMouseEventDispatch(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	target := &mouseTargetComponent{}
	wrapper := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{{Component: target}}}}
	screen.SetLayoutRoot(wrapper)
	screen.Start()
	waitForAltRender(t, screen)

	screen.handleViewportInput("\x1b[<0;4;1M")
	if !target.pressed {
		t.Fatal("press event not delivered")
	}
	screen.handleViewportInput("\x1b[<0;4;1m")
	if !target.released || !target.clicked {
		t.Fatalf("release/click not delivered: %+v", target)
	}
	screen.Stop(StopOptions{})
}

type mouseTargetComponent struct {
	staticComponent
	pressed  bool
	released bool
	clicked  bool
	clicks   int
}

func (m *mouseTargetComponent) Render(width int) []string { return []string{"[target]"} }

func (m *mouseTargetComponent) HandleMouse(event MouseEvent) *MouseEventResult {
	switch event.Type {
	case MousePress:
		m.pressed = true
	case MouseRelease:
		m.released = true
	case MouseClick:
		m.clicked = true
		m.clicks++
	}
	return &MouseEventResult{Handled: true, renderSet: true, Render: false}
}

func TestAltScreenFocusEvents(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	screen.AddChild(&plainComponent{lines: []string{"x"}})
	screen.Start()
	waitForAltRender(t, screen)

	if result := screen.handleViewportInput(FocusIn); !result.Consume {
		t.Fatal("focus in should be consumed")
	}
	if result := screen.handleViewportInput(FocusOut); !result.Consume {
		t.Fatal("focus out should be consumed")
	}
	screen.Stop(StopOptions{})
}

func TestParseSGRMouse(t *testing.T) {
	event, ok := parseSGRMouseEvent("\x1b[<0;5;3M")
	if !ok || event.button != 0 || event.x != 4 || event.y != 2 || event.release {
		t.Fatalf("press parse = %+v ok=%v", event, ok)
	}
	event, ok = parseSGRMouseEvent("\x1b[<0;5;3m")
	if !ok || !event.release {
		t.Fatalf("release parse = %+v", event)
	}
	event, ok = parseSGRMouseEvent("\x1b[<32;5;3M")
	if !ok || event.button&32 == 0 {
		t.Fatalf("motion bit = %+v", event)
	}
	if _, ok := parseSGRMouseEvent("\x1b[A"); ok {
		t.Fatal("arrow key is not a mouse event")
	}
}

func TestParseWheelEvent(t *testing.T) {
	event, ok := parseWheelEvent("\x1b[<64;5;3M")
	if !ok || event.direction != -1 || event.x != 4 || event.y != 2 {
		t.Fatalf("wheel up = %+v ok=%v", event, ok)
	}
	event, ok = parseWheelEvent("\x1b[<65;5;3M")
	if !ok || event.direction != 1 {
		t.Fatalf("wheel down = %+v", event)
	}
	event, ok = parseWheelEvent("\x1b[M`!!")
	if !ok {
		t.Fatalf("legacy wheel parse failed")
	}
	if _, ok := parseWheelEvent("\x1b[<0;5;3M"); ok {
		t.Fatal("press is not a wheel event")
	}
	if _, ok := parseWheelEvent("\x1b[<66;5;3M"); ok {
		t.Fatal("horizontal wheel should be rejected")
	}
}

func TestMouseButtonDecoding(t *testing.T) {
	if got := decodeMouseButton(0); got != MouseButtonLeft {
		t.Fatalf("button 0 = %v", got)
	}
	if got := decodeMouseButton(1); got != MouseButtonMiddle {
		t.Fatalf("button 1 = %v", got)
	}
	if got := decodeMouseButton(2); got != MouseButtonRight {
		t.Fatalf("button 2 = %v", got)
	}
	if got := decodeMouseButton(3); got != MouseButtonNone {
		t.Fatalf("button 3 = %v", got)
	}
}

func TestAltScreenLayoutRootSwitch(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	screen.AddChild(&plainComponent{lines: []string{"implicit"}})
	first := &plainComponent{lines: []string{"root one"}}
	screen.SetLayoutRoot(first)
	screen.Start()
	waitForAltRender(t, screen)

	if !strings.Contains(strings.Join(screen.previousScreen, "\n"), "root one") {
		t.Fatalf("layout root not rendered: %q", screen.previousScreen)
	}
	second := &plainComponent{lines: []string{"root two"}}
	screen.SetLayoutRoot(second)
	waitForAltRender(t, screen)
	if !strings.Contains(strings.Join(screen.previousScreen, "\n"), "root two") {
		t.Fatalf("layout root switch not rendered: %q", screen.previousScreen)
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenDoubleClickCounting(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	target := &mouseTargetComponent{}
	wrapper := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{{Component: target}}}}
	screen.SetLayoutRoot(wrapper)
	screen.Start()
	waitForAltRender(t, screen)

	screen.handleViewportInput("\x1b[<0;4;1M")
	screen.handleViewportInput("\x1b[<0;4;1m")
	first := target.clickCount()
	screen.handleViewportInput("\x1b[<0;4;1M")
	screen.handleViewportInput("\x1b[<0;4;1m")
	second := target.clickCount()
	if second != first+1 {
		t.Fatalf("click count = %d then %d", first, second)
	}
	screen.Stop(StopOptions{})
}

func (m *mouseTargetComponent) clickCount() int { return m.clicks }

func TestStripCursorMarker(t *testing.T) {
	if got := stripCursorMarker("a" + CursorMarker + "b"); got != "ab" {
		t.Fatalf("stripCursorMarker = %q", got)
	}
}
