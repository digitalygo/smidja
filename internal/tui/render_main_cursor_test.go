package tui

import (
	"strings"
	"testing"
)

func TestMainScreenUpwardMoveUsesUpSequence(t *testing.T) {
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
	want := CursorMoveLines(-1)
	if want != "\x1b[1A" {
		t.Fatalf("CursorMoveLines(-1) = %q, want %q", want, "\x1b[1A")
	}
	if !strings.Contains(output, want) {
		t.Fatalf("differential update should move up with %q, got %q", want, output)
	}
	if strings.Contains(output, CursorMoveLines(1)) {
		t.Fatalf("differential update must not move down for an upward delta: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenStopUpwardMoveUsesUpSequence(t *testing.T) {
	screen, terminal := newTestScreen(t, 10, 3)
	screen.AddChild(&plainComponent{lines: []string{"one", "two"}})
	screen.Start()
	waitForRender(t, screen)

	screen.mu.Lock()
	screen.previousLines = []string{"one", "two"}
	screen.hardwareCursorRow = 5
	screen.mu.Unlock()
	terminal.ResetWrites()

	screen.Stop(StopOptions{})
	output := terminal.Output()
	want := CursorMoveLines(-3)
	if want != "\x1b[3A" {
		t.Fatalf("CursorMoveLines(-3) = %q, want %q", want, "\x1b[3A")
	}
	if !strings.Contains(output, want) {
		t.Fatalf("stop should move up with %q, got %q", want, output)
	}
	if strings.Contains(output, CursorMoveLines(3)) {
		t.Fatalf("stop must not move down for an upward delta: %q", output)
	}
}
