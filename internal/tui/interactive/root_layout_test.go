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
