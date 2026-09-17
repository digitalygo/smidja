package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestSurfaceRootExposesLayoutStructure(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.AddUserMessage("layout marker")
	frame := tui.RenderLayoutFrame(surface.Root(), 80, 24, func() {})
	if frame.PrimaryScrollView != surface.Transcript() {
		t.Fatal("surface root should expose the transcript as the primary scroll view")
	}
	joined := strings.Join(frame.Lines, "\n")
	if !strings.Contains(tui.StripTerminalSequences(joined), "layout marker") {
		t.Fatalf("layout through the surface root should show the transcript:\n%s", joined)
	}
}

func TestSurfaceRootProvidesRuntimeOwnedLayoutFrame(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.AddUserMessage("provider marker")
	provider, ok := surface.Root().(tui.LayoutFrameProvider)
	if !ok {
		t.Fatal("surface root must provide a runtime-owned layout frame")
	}
	frame := provider.RenderLayoutFrame(80, 24, func() {})
	if frame == nil {
		t.Fatal("provider returned a nil layout frame")
	}
	if frame.PrimaryScrollView != surface.Transcript() {
		t.Fatal("provider frame should preserve the primary scroll view")
	}
	joined := tui.StripTerminalSequences(strings.Join(frame.Lines, "\n"))
	if !strings.Contains(joined, "provider marker") {
		t.Fatalf("provider frame should show the transcript:\n%s", joined)
	}
	if direct := surface.RenderFrame(80, 24); direct.PrimaryScrollView != frame.PrimaryScrollView {
		t.Fatal("provider frame and render frame disagree on the primary scroll view")
	}
}
