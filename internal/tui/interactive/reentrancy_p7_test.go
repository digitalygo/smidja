package interactive

import (
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type callbackFrameComponent struct {
	text     string
	callback func()
}

func (c *callbackFrameComponent) Render(width int) []string { return []string{c.text} }

func (c *callbackFrameComponent) Invalidate() {
	if c.callback != nil {
		c.callback()
	}
}

func runBounded(t *testing.T, what string, action func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		action()
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not complete; the callback is running under a held host lock", what)
	}
}

func TestCustomRendererReentrancyDuringAdd(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.SetCustomMessageRenderer("note", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		expanded := surface.ToolsExpanded()
		surface.SetToolsExpanded(!expanded)
		surface.SetWorkingVisible(false)
		return &frameComponent{text: "RENDERED " + entry.Text}
	})
	runBounded(t, "AddCustomMessage with a reentrant renderer", func() {
		surface.AddCustomMessage(CustomEntryView{CustomType: "note", Text: "payload"})
	})
	lines := renderPlainFrame(surface, 48, 20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "RENDERED payload") {
		t.Fatalf("custom renderer output missing:\n%s", joined)
	}
	if !surface.ToolsExpanded() {
		t.Fatal("renderer SetToolsExpanded did not apply")
	}
}

func TestMarkdownTransformerReentrancyDuringRender(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.SetMarkdownTransformer(func(markdown string, ctx MarkdownTransformContext) string {
		_ = surface.ToolsExpanded()
		surface.SetToolsExpanded(true)
		return markdown + "-transformed"
	})
	runBounded(t, "AddUserMessage with a reentrant transformer", func() {
		surface.AddUserMessage("hello")
	})
	runBounded(t, "RenderFrame with a reentrant transformer", func() {
		renderPlainFrame(surface, 48, 20)
	})
	lines := renderPlainFrame(surface, 48, 20)
	if !strings.Contains(strings.Join(lines, "\n"), "hello-transformed") {
		t.Fatalf("transformer output missing:\n%s", strings.Join(lines, "\n"))
	}
}

func TestAssistantTransformerReentrancyDuringStreaming(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.SetMarkdownTransformer(func(markdown string, ctx MarkdownTransformContext) string {
		if surface.ToolsExpanded() {
			surface.SetToolsExpanded(false)
		}
		return "[" + ctx.Kind + "]" + markdown
	})
	runBounded(t, "assistant streaming with a reentrant transformer", func() {
		surface.StartAssistantTurn()
		surface.AppendAssistantText("answer")
		surface.AppendAssistantThinking("thought")
	})
	runBounded(t, "assistant render with a reentrant transformer", func() {
		renderPlainFrame(surface, 48, 24)
	})
}

func TestWidgetInvalidateReentrancy(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	panel := &callbackFrameComponent{text: "PANEL"}
	panel.callback = func() {
		surface.ClearWidgetComponent("panel")
	}
	surface.SetWidgetComponent("panel", panel)
	runBounded(t, "widget Invalidate calling ClearWidgetComponent", func() {
		surface.ext.widgets.Invalidate()
	})
	runBounded(t, "SetTheme with a reentrant widget Invalidate", func() {
		surface.SetTheme(theme)
	})
}

func TestCustomRendererRefreshDisposesAndRerenders(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	first := &disposableFrameComponent{text: "FIRST RENDER"}
	surface.SetCustomMessageRenderer("note", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		return first
	})
	surface.AddCustomMessage(CustomEntryView{CustomType: "note", Text: "payload"})
	if joined := strings.Join(renderPlainFrame(surface, 48, 20), "\n"); !strings.Contains(joined, "FIRST RENDER") {
		t.Fatalf("first renderer output missing:\n%s", joined)
	}
	second := &disposableFrameComponent{text: "SECOND RENDER"}
	surface.SetCustomMessageRenderer("note", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		return second
	})
	joined := strings.Join(renderPlainFrame(surface, 48, 20), "\n")
	if strings.Contains(joined, "FIRST RENDER") || !strings.Contains(joined, "SECOND RENDER") {
		t.Fatalf("registry change did not re-render the custom message:\n%s", joined)
	}
	if first.DisposeCount() != 1 {
		t.Fatalf("replaced custom component dispose count = %d, want 1", first.DisposeCount())
	}
	surface.RemoveCustomMessageRenderer("note")
	surface.SetToolsExpanded(true)
	joined = strings.Join(renderPlainFrame(surface, 48, 24), "\n")
	if strings.Contains(joined, "SECOND RENDER") {
		t.Fatalf("removed renderer output still visible:\n%s", joined)
	}
	if !strings.Contains(joined, "payload") {
		t.Fatalf("fallback block missing after renderer removal:\n%s", joined)
	}
	if second.DisposeCount() != 1 {
		t.Fatalf("removed renderer component dispose count = %d, want 1", second.DisposeCount())
	}
}

func TestReplaceTranscriptDisposesCustomComponents(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	component := &disposableFrameComponent{text: "CUSTOM"}
	surface.SetCustomMessageRenderer("note", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		return component
	})
	surface.AddCustomMessage(CustomEntryView{CustomType: "note", Text: "payload"})
	surface.ReplaceTranscript(nil)
	if component.DisposeCount() != 1 {
		t.Fatalf("custom component dispose count after ReplaceTranscript = %d, want 1", component.DisposeCount())
	}
	surface.ReplaceTranscript(nil)
	if component.DisposeCount() != 1 {
		t.Fatalf("custom component disposed more than once: %d", component.DisposeCount())
	}
}

func TestDisposeExtensionComponentsReleasesTranscriptOwnership(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	component := &disposableFrameComponent{text: "CUSTOM"}
	surface.SetCustomMessageRenderer("note", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		return component
	})
	surface.AddCustomMessage(CustomEntryView{CustomType: "note", Text: "payload"})
	surface.DisposeExtensionComponents()
	surface.DisposeExtensionComponents()
	if component.DisposeCount() != 1 {
		t.Fatalf("custom component dispose count after cleanup = %d, want 1", component.DisposeCount())
	}
}
