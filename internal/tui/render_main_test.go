package tui

import (
	"strings"
	"testing"
	"time"
)

func stripAll(lines []string) []string {
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = strings.TrimRight(StripTerminalSequences(line), " ")
	}
	return result
}

func newTestScreen(t *testing.T, width, height int) (*MainScreen, *fakeTerminal) {
	t.Helper()
	terminal := newFakeTerminal(width, height)
	screen := NewMainScreen(terminal, false)
	screen.SetMinRenderInterval(0)
	renders := make(chan struct{}, 64)
	screen.hooks.doRender = func() {
		screen.doRender()
		select {
		case renders <- struct{}{}:
		default:
		}
	}
	renderSignals[screen] = renders
	t.Cleanup(func() { delete(renderSignals, screen) })
	return screen, terminal
}

var renderSignals = map[*MainScreen]chan struct{}{}

func waitForRender(t *testing.T, screen *MainScreen) {
	t.Helper()
	renders := renderSignals[screen]
	select {
	case <-renders:
	case <-time.After(2 * time.Second):
		t.Fatal("render did not run")
	}
}

func TestMainScreenFirstRender(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 3)
	screen.AddChild(&plainComponent{lines: []string{"one", "two", "three"}})
	screen.Start()
	waitForRender(t, screen)

	output := terminal.Output()
	if !strings.Contains(output, "one") || !strings.Contains(output, "two") {
		t.Fatalf("first render missing content: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenDifferentialAppend(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 4)
	content := &plainComponent{lines: []string{"one"}}
	screen.AddChild(content)
	screen.Start()
	waitForRender(t, screen)
	terminal.ResetWrites()

	content.lines = append(content.lines, "two")
	screen.RequestRender(false)
	waitForRender(t, screen)

	output := terminal.Output()
	if !strings.Contains(output, "two") {
		t.Fatalf("appended line not rendered: %q", output)
	}
	if strings.Count(output, SyncOutputBegin) != 1 {
		t.Fatalf("expected synchronized frame, got %d sync begins: %q", strings.Count(output, SyncOutputBegin), output)
	}
	if strings.Contains(output, CursorEraseScreen) {
		t.Fatalf("append should not clear the screen: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenDifferentialUpdateOnlyChangedLines(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 4)
	content := &plainComponent{lines: []string{"aaa", "bbb", "ccc"}}
	screen.AddChild(content)
	screen.Start()
	waitForRender(t, screen)
	terminal.ResetWrites()

	content.lines[1] = "XXX"
	screen.RequestRender(false)
	waitForRender(t, screen)

	output := terminal.Output()
	if !strings.Contains(output, "XXX") {
		t.Fatalf("changed line not rendered: %q", output)
	}
	if strings.Count(output, CursorEraseLine) != 1 {
		t.Fatalf("only the changed line should be erased: %d erases in %q", strings.Count(output, CursorEraseLine), output)
	}
	if strings.Contains(output, "ccc") {
		t.Fatalf("unchanged lines should not be rewritten: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenWidthChangeFullRedraw(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 3)
	content := &plainComponent{lines: []string{"aaaa", "bbbb"}}
	screen.AddChild(content)
	screen.Start()
	waitForRender(t, screen)
	fullRedrawsBefore := screen.FullRedraws()
	terminal.ResetWrites()

	terminal.SetSize(20, 3)
	waitForRender(t, screen)
	if screen.FullRedraws() != fullRedrawsBefore+1 {
		t.Fatalf("width change should trigger full redraw, redraws = %d", screen.FullRedraws())
	}
	output := terminal.Output()
	if !strings.Contains(output, CursorEraseScreen) {
		t.Fatalf("full redraw should clear: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenDeletedLinesCleared(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 6)
	content := &plainComponent{lines: []string{"aaa", "bbb", "ccc", "ddd"}}
	screen.AddChild(content)
	screen.Start()
	waitForRender(t, screen)
	terminal.ResetWrites()

	content.lines = []string{"aaa"}
	screen.RequestRender(false)
	waitForRender(t, screen)
	output := terminal.Output()
	if strings.Contains(output, "ddd") {
		t.Fatalf("stale lines rewritten: %q", output)
	}
	if !strings.Contains(output, CursorEraseLine) {
		t.Fatalf("deleted lines should be erased: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenTrailingResetPerLine(t *testing.T) {
	screen, terminal := newTestScreen(t, 20, 2)
	styled := &staticComponent{lines: []string{"\x1b[31mred"}}
	screen.AddChild(styled)
	screen.Start()
	waitForRender(t, screen)
	terminal.ResetWrites()

	styled.lines[0] = "\x1b[32mgreen"
	screen.RequestRender(false)
	waitForRender(t, screen)
	output := terminal.Output()
	segmentCount := strings.Count(output, SegmentReset)
	if segmentCount < 1 {
		t.Fatalf("expected per-line trailing resets, got %d in %q", segmentCount, output)
	}
	if strings.Contains(output, "\x1b[31m") {
		t.Fatalf("old style should be reset before new frame content: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenCursorMarker(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 2)
	screen.SetShowHardwareCursor(true)
	marker := &staticComponent{lines: []string{"ab" + CursorMarker + "cd"}}
	screen.AddChild(marker)
	screen.Start()
	waitForRender(t, screen)
	terminal.ResetWrites()

	screen.RequestRender(false)
	waitForRender(t, screen)
	output := terminal.Output()
	if strings.Contains(output, CursorMarker) {
		t.Fatalf("cursor marker leaked to terminal: %q", output)
	}
	if !strings.Contains(output, CursorShow) {
		t.Fatalf("hardware cursor should be shown at marker: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenStopPrintsPromptLine(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 3)
	screen.AddChild(&plainComponent{lines: []string{"one", "two"}})
	screen.Start()
	waitForRender(t, screen)
	terminal.ResetWrites()

	screen.Stop(StopOptions{})
	output := terminal.Output()
	if !strings.Contains(output, "\r\n") {
		t.Fatalf("stop should move below content: %q", output)
	}
	if !strings.Contains(output, CursorShow) {
		t.Fatalf("stop should show cursor: %q", output)
	}
}

func TestMainScreenStopPreserveScreen(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 3)
	screen.AddChild(&plainComponent{lines: []string{"one"}})
	screen.Start()
	waitForRender(t, screen)
	terminal.ResetWrites()

	screen.Stop(StopOptions{PreserveScreen: true})
	output := terminal.Output()
	if strings.Contains(output, "\r\n") && !strings.Contains(output, CursorShow+"\r\n") {
		t.Fatalf("preserve screen should not print a new prompt line: %q", output)
	}
}

func TestMainScreenOverlayComposited(t *testing.T) {
	screen, _ := newTestScreen(t, 20, 4)
	base := screen.Base
	base.AddChild(&plainComponent{lines: []string{"top", "bottom", "more", "lines"}})
	overlay := &staticComponent{lines: []string{"OV"}}
	handle := base.ShowOverlay(overlay, OverlayOptions{Width: "6", Anchor: AnchorTopRight})
	screen.Start()
	waitForRender(t, screen)

	bounds, ok := handle.Bounds()
	if !ok || bounds.Col != 14 || bounds.Row != 0 || bounds.Width != 6 {
		t.Fatalf("overlay bounds = %+v ok=%v", bounds, ok)
	}
	lines := screen.previousLines
	first := StripTerminalSequences(lines[0])
	if !strings.Contains(first, "OV") || !strings.Contains(first, "top") {
		t.Fatalf("overlay not composited: %q", first)
	}
	if !strings.HasPrefix(first, "top") {
		t.Fatalf("base content outside overlay lost: %q", first)
	}
	handle.Hide()
	waitForRender(t, screen)
	if strings.Contains(StripTerminalSequences(screen.previousLines[0]), "OV") {
		t.Fatalf("overlay still visible after hide: %q", screen.previousLines[0])
	}
	screen.Stop(StopOptions{})
}
