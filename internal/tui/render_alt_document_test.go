package tui

import (
	"strings"
	"testing"
)

type stubDocumentComponent struct {
	lines    []string
	document []string
}

func (s *stubDocumentComponent) Render(width int) []string { return append([]string(nil), s.lines...) }

func (s *stubDocumentComponent) Invalidate() {}

func (s *stubDocumentComponent) RenderDocument(width int) []string {
	return append([]string(nil), s.document...)
}

func TestAltScreenFinalDocumentUsesRenderer(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 5)
	stub := &stubDocumentComponent{
		lines:    []string{"clamped"},
		document: []string{"full-one", "full-two", "full-three"},
	}
	screen.SetLayoutRoot(stub)
	if got := screen.finalDocument(20); len(got) != 3 || got[0] != "full-one" {
		t.Fatalf("finalDocument = %q, want full document", got)
	}
}

func TestAltScreenFinalDocumentFallsBackToRender(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 5)
	plain := &plainComponent{lines: []string{"generic-one", "generic-two"}}
	screen.SetLayoutRoot(plain)
	got := screen.finalDocument(20)
	if len(got) != 2 || got[0] != "generic-one" {
		t.Fatalf("finalDocument fallback = %q", got)
	}
}

func TestAltScreenStopEmitsFullDocument(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 80, 5)
	document := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		document = append(document, "alt-full-marker")
	}
	document[0] = "alt-full-first"
	document[19] = "alt-full-last"
	stub := &stubDocumentComponent{
		lines:    []string{"clamped"},
		document: document,
	}
	screen.SetLayoutRoot(stub)
	screen.Start()
	waitForAltRender(t, screen)
	terminal.ResetWrites()
	screen.Stop(StopOptions{})
	output := terminal.Output()
	if !strings.Contains(output, AltScreenExit) {
		t.Fatalf("stop missing alt-screen exit: %q", output)
	}
	afterExit := output[strings.Index(output, AltScreenExit):]
	stripped := StripTerminalSequences(afterExit)
	if !strings.Contains(stripped, "alt-full-first") || !strings.Contains(stripped, "alt-full-last") {
		t.Fatalf("final document after exit missing full content:\n%s", stripped)
	}
	if count := strings.Count(stripped, "alt-full-marker"); count != 18 {
		t.Fatalf("final middle markers = %d, want 18:\n%s", count, stripped)
	}
	if len(screen.lastDocument) != 20 {
		t.Fatalf("lastDocument = %d lines, want 20", len(screen.lastDocument))
	}
}
