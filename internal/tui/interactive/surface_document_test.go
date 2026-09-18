package interactive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func documentText(lines []string) string {
	stripped := make([]string, len(lines))
	for i, line := range lines {
		stripped[i] = strings.TrimRight(tui.StripTerminalSequences(line), " ")
	}
	return strings.Join(stripped, "\n")
}

func TestSurfaceRenderDocumentLongTranscript(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	const count = 50
	for i := 0; i < count; i++ {
		surface.AddUserMessage(fmt.Sprintf("long-transcript-%02d", i))
	}
	document := surface.RenderDocument(80)
	text := documentText(document)
	for i := 0; i < count; i++ {
		marker := fmt.Sprintf("long-transcript-%02d", i)
		if !strings.Contains(text, marker) {
			t.Fatalf("document missing %q:\n%s", marker, text)
		}
	}
	if len(document) < count {
		t.Fatalf("document lines = %d, want at least %d", len(document), count)
	}
	rootDocument := surface.Root().Render(80)
	if len(rootDocument) >= len(document) {
		t.Fatalf("clamped root lines = %d, document lines = %d, want root clamped to one transcript line", len(rootDocument), len(document))
	}
	if renderer, ok := surface.Root().(interface{ RenderDocument(width int) []string }); !ok {
		t.Fatal("surface root should supply RenderDocument")
	} else if got := documentText(renderer.RenderDocument(80)); got != text {
		t.Fatalf("root RenderDocument mismatch:\n%s", got)
	}
}

func TestSurfaceRenderDocumentDockOrder(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.SetWorkspace("/home/tester/proj")
	surface.SetSessionName("main")
	surface.SetModel("claude-x")
	surface.AddUserMessage("transcript-anchor-doc")
	surface.Editor().SetText("pending-anchor-doc")
	surface.Editor().HandleInput("\x1b\r")
	if surface.Editor().QueuedCount() != 1 {
		t.Fatalf("queued = %d, want 1", surface.Editor().QueuedCount())
	}
	surface.Editor().SetText("editor-anchor-doc")
	surface.SetWorking(true)
	surface.SetWorkingMessage("status-region-anchor-doc")
	surface.SetWidget("dock-widget", []string{"widget-anchor-doc"})
	surface.SetStatus("dock-status", "footer-status-anchor-doc")

	document := surface.RenderDocument(80)
	text := documentText(document)
	markers := []string{
		"transcript-anchor-doc",
		"editor-anchor-doc",
		"pending-anchor-doc",
		"status-region-anchor-doc",
		"widget-anchor-doc",
		"footer-status-anchor-doc",
	}
	positions := make([]int, len(markers))
	for i, marker := range markers {
		index := strings.Index(text, marker)
		if index < 0 {
			t.Fatalf("document missing %q:\n%s", marker, text)
		}
		positions[i] = index
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] <= positions[i-1] {
			t.Fatalf("dock order wrong: %q at %d follows %q at %d\n%s", markers[i], positions[i], markers[i-1], positions[i-1], text)
		}
	}
	workspaceIndex := strings.Index(text, "proj")
	if workspaceIndex < 0 {
		t.Fatalf("document missing footer workspace:\n%s", text)
	}
	widgetIndex := positions[4]
	statusIndex := positions[5]
	if workspaceIndex <= widgetIndex {
		t.Fatalf("footer workspace should follow widget:\n%s", text)
	}
	if workspaceIndex >= statusIndex {
		t.Fatalf("footer workspace should precede footer status:\n%s", text)
	}
}
