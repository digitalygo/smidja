package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestUserMessageEmitsTrustedPromptMarker(t *testing.T) {
	block := NewUserMessage("hello", mustTheme(t), false)
	lines := block.Render(40)
	if len(lines) == 0 {
		t.Fatal("no rendered lines")
	}
	if !strings.HasPrefix(lines[0], tui.OSC133PromptStart) {
		t.Fatalf("first line missing the trusted prompt marker: %q", lines[0])
	}
	if count := strings.Count(strings.Join(lines, "\n"), tui.OSC133PromptStart); count != 1 {
		t.Fatalf("expected exactly one prompt marker, got %d", count)
	}
}

func TestUserMessageUntrustedTextCannotInjectMarkers(t *testing.T) {
	theme := mustTheme(t)
	block := NewUserMessage("evil "+tui.OSC133PromptStart+" injected", theme, false)
	lines := block.Render(60)
	joined := strings.Join(lines, "\n")
	if strings.Count(joined, tui.OSC133PromptStart) != 1 {
		t.Fatalf("untrusted text injected a marker: %q", joined)
	}
	if !strings.HasPrefix(lines[0], tui.OSC133PromptStart) {
		t.Fatal("trusted marker should still be first")
	}
	if !strings.Contains(tui.StripTerminalSequences(joined), "injected") {
		t.Fatal("sanitized text lost content")
	}
}
