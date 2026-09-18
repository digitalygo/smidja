package tui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type staticComponent struct {
	lines      []string
	focused    bool
	focusCalls int
}

func (s *staticComponent) Render(width int) []string {
	result := make([]string, len(s.lines))
	for i, line := range s.lines {
		result[i] = TruncateToWidth(line, width, "", true)
	}
	return result
}

func (s *staticComponent) Invalidate() {}

func (s *staticComponent) SetFocused(focused bool) {
	s.focused = focused
	s.focusCalls++
}

type recordingComponent struct {
	*staticComponent
	inputs       []string
	handled      bool
	releaseOptIn bool
}

func (r *recordingComponent) HandleInput(data string) {
	r.inputs = append(r.inputs, data)
	r.handled = true
}

func (r *recordingComponent) WantsKeyRelease() bool { return r.releaseOptIn }

func (r *recordingComponent) HandleMouse(event MouseEvent) *MouseEventResult {
	if event.Type != MousePress {
		return nil
	}
	r.handled = true
	return &MouseEventResult{Handled: true, Focus: true}
}

func newRecordingComponent(lines ...string) *recordingComponent {
	return &recordingComponent{staticComponent: &staticComponent{lines: lines}}
}

func TestBaseFocusManagement(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	base := NewBase(terminal, false, "regular")
	first := &staticComponent{lines: []string{"first"}}
	second := &staticComponent{lines: []string{"second"}}

	base.AddChild(first)
	base.AddChild(second)
	base.SetFocus(first)
	if base.FocusedComponent() != first || first.focusCalls != 1 {
		t.Fatalf("focus = %v calls %d", base.FocusedComponent(), first.focusCalls)
	}
	base.SetFocus(second)
	if !first.focused == false || first.focused {
		t.Fatal("first should be unfocused")
	}
	if !second.focused || second.focusCalls != 1 {
		t.Fatalf("second focused = %v calls %d", second.focused, second.focusCalls)
	}
	base.Stop(StopOptions{})
	if !base.IsStopped() {
		t.Fatal("base should be stopped")
	}
}

func TestBaseInputRouting(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	base := NewBase(terminal, false, "regular")
	base.SetMinRenderInterval(time.Millisecond)

	focused := newRecordingComponent("input")
	other := newRecordingComponent("hidden")
	base.AddChild(focused)
	base.AddChild(other)
	base.SetFocus(focused)

	base.handleTerminalInput("x")
	if len(focused.inputs) != 1 || focused.inputs[0] != "x" {
		t.Fatalf("focused inputs = %q", focused.inputs)
	}
	if len(other.inputs) != 0 {
		t.Fatalf("unfocused got input: %q", other.inputs)
	}

	base.handleTerminalInput("\x1b[97;1:3u")
	if len(focused.inputs) != 1 {
		t.Fatalf("key release should be dropped without opt-in: %q", focused.inputs)
	}
	focused.releaseOptIn = true
	base.handleTerminalInput("\x1b[97;1:3u")
	if len(focused.inputs) != 2 {
		t.Fatalf("key release should pass with opt-in: %q", focused.inputs)
	}

	base.SetFocus(other)
	base.handleTerminalInput("\x1b[200~text\x1b[201~")
	if len(other.inputs) != 1 || other.inputs[0] != BracketedPaste("text") {
		t.Fatalf("paste routing = %q", other.inputs)
	}
}

func TestBaseInputListener(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	base := NewBase(terminal, false, "regular")
	target := newRecordingComponent("editor")
	base.AddChild(target)
	base.SetFocus(target)

	consumed := false
	remove := base.AddInputListener(func(data string) InputListenerResult {
		if data == "q" {
			consumed = true
			return InputListenerResult{Consume: true}
		}
		return InputListenerResult{HasData: true, Data: "rewritten"}
	})
	base.handleTerminalInput("q")
	if !consumed || len(target.inputs) != 0 {
		t.Fatalf("listener consume failed: consumed=%v inputs=%q", consumed, target.inputs)
	}
	base.handleTerminalInput("z")
	if len(target.inputs) != 1 || target.inputs[0] != "rewritten" {
		t.Fatalf("listener rewrite failed: %q", target.inputs)
	}
	remove()
	base.handleTerminalInput("z")
	if len(target.inputs) != 2 || target.inputs[1] != "z" {
		t.Fatalf("listener removal failed: %q", target.inputs)
	}
}

type overlayTestComponent struct {
	*staticComponent
	inputs []string
}

func (o *overlayTestComponent) HandleInput(data string) {
	o.inputs = append(o.inputs, data)
}

func TestOverlayFocusCapture(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	base := NewBase(terminal, false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)

	overlay := &overlayTestComponent{staticComponent: &staticComponent{lines: []string{"overlay"}}}
	handle := base.ShowOverlay(overlay, OverlayOptions{Width: "50%", Anchor: AnchorCenter})
	if base.FocusedComponent() != overlay {
		t.Fatal("overlay should capture focus")
	}
	base.handleTerminalInput("o")
	if len(overlay.inputs) != 1 {
		t.Fatalf("overlay input = %q", overlay.inputs)
	}
	if len(editor.inputs) != 0 {
		t.Fatalf("editor should not receive input under overlay: %q", editor.inputs)
	}

	handle.SetHidden(true)
	if base.FocusedComponent() != editor {
		t.Fatalf("hiding overlay should restore focus, got %v", base.FocusedComponent())
	}
	base.handleTerminalInput("e")
	if len(editor.inputs) != 1 {
		t.Fatalf("editor input after hide = %q", editor.inputs)
	}

	handle.SetHidden(false)
	if base.FocusedComponent() != overlay {
		t.Fatal("unhiding overlay should recapture focus")
	}
	handle.Hide()
	if base.FocusedComponent() != editor {
		t.Fatal("closing overlay should restore editor focus")
	}
}

func TestOverlayNonCapturing(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	base := NewBase(terminal, false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)

	overlay := &overlayTestComponent{staticComponent: &staticComponent{lines: []string{"toast"}}}
	base.ShowOverlay(overlay, OverlayOptions{Anchor: AnchorTopRight, NonCapturing: true})
	if base.FocusedComponent() != editor {
		t.Fatal("non-capturing overlay must not steal focus")
	}
	if !base.HasOverlay() {
		t.Fatal("overlay should be present")
	}
	base.HideOverlay()
	if base.HasOverlay() {
		t.Fatal("overlay should be gone")
	}
}

func TestOverlayVisibilityCallback(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	terminal.SetSize(40, 10)
	base := NewBase(terminal, false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)

	visible := false
	overlay := &overlayTestComponent{staticComponent: &staticComponent{lines: []string{"overlay"}}}
	base.ShowOverlay(overlay, OverlayOptions{
		Width:   "80%",
		Visible: func(width, height int) bool { return visible },
	})
	if base.FocusedComponent() != editor {
		t.Fatal("invisible overlay must not capture focus")
	}
	if !base.isOverlayFocused() == false && base.isOverlayFocused() {
		t.Fatal("no overlay focused expected")
	}
	visible = true
	base.handleTerminalInput("z")
	if base.FocusedComponent() != editor {
		t.Fatal("focus should stay with editor until overlay shown explicitly")
	}
	visible = false
	base.handleTerminalInput("z")
}

func TestOverlayGeometryAnchors(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	base := NewBase(terminal, false, "regular")
	overlay := &staticComponent{lines: []string{"one", "two", "three"}}

	tests := []struct {
		name    string
		options OverlayOptions
		wantRow int
		wantCol int
		width   int
	}{
		{name: "center default", options: OverlayOptions{Width: "50%"}, wantRow: 10, wantCol: 20, width: 40},
		{name: "top left", options: OverlayOptions{Width: "10", Anchor: AnchorTopLeft}, wantRow: 0, wantCol: 0, width: 10},
		{name: "top right", options: OverlayOptions{Width: "10", Anchor: AnchorTopRight}, wantRow: 0, wantCol: 70, width: 10},
		{name: "bottom left", options: OverlayOptions{Width: "10", Anchor: AnchorBottomLeft}, wantRow: 21, wantCol: 0, width: 10},
		{name: "bottom right", options: OverlayOptions{Width: "10", Anchor: AnchorBottomRight}, wantRow: 21, wantCol: 70, width: 10},
		{name: "absolute", options: OverlayOptions{Width: "10", Row: "5", Col: "7"}, wantRow: 5, wantCol: 7, width: 10},
		{name: "percent row col", options: OverlayOptions{Width: "10", Row: "50%", Col: "50%"}, wantRow: 10, wantCol: 35, width: 10},
		{name: "offsets", options: OverlayOptions{Width: "10", Anchor: AnchorTopLeft, OffsetX: 3, OffsetY: 2}, wantRow: 2, wantCol: 3, width: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := base.resolveOverlayLayout(test.options, len(overlay.lines), 80, 24)
			if layout.width != test.width {
				t.Fatalf("width = %d, want %d", layout.width, test.width)
			}
			if layout.row != test.wantRow {
				t.Fatalf("row = %d, want %d", layout.row, test.wantRow)
			}
			if layout.col != test.wantCol {
				t.Fatalf("col = %d, want %d", layout.col, test.wantCol)
			}
		})
	}
}

func TestOverlayGeometryClampingAndMargins(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	base := NewBase(terminal, false, "regular")

	layout := base.resolveOverlayLayout(OverlayOptions{Width: "200"}, 30, 80, 24)
	if layout.width != 80 || layout.row != 0 {
		t.Fatalf("oversized overlay = %+v", layout)
	}

	capped := base.resolveOverlayLayout(OverlayOptions{Width: "20", MaxHeight: "50%"}, 30, 80, 24)
	if capped.maxHeight == nil || *capped.maxHeight != 12 {
		t.Fatalf("maxHeight = %v, want 12", capped.maxHeight)
	}

	margin := UniformMargin(2)
	layout = base.resolveOverlayLayout(OverlayOptions{Width: "10", Margin: margin, Anchor: AnchorTopLeft}, 3, 80, 24)
	if layout.row != 2 || layout.col != 2 {
		t.Fatalf("margin offset = %+v", layout)
	}
	layout = base.resolveOverlayLayout(OverlayOptions{Width: "10", Row: "0", Col: "-5", Anchor: AnchorTopLeft}, 3, 80, 24)
	if layout.col < 0 {
		t.Fatalf("negative col not clamped: %d", layout.col)
	}
}

func TestOverlayCompositing(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	base := NewBase(terminal, false, "regular")
	base.AddChild(&staticComponent{lines: []string{"aaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccc", "dddddddddddddddddd", "eeeeeeeeeeeeeeeeee"}})

	overlay := &staticComponent{lines: []string{"XY", "ZW"}}
	base.ShowOverlay(overlay, OverlayOptions{Width: "4", Anchor: AnchorTopLeft})
	base.mu.Lock()
	base.renderedOverlays = nil
	base.mu.Unlock()

	result := base.compositeOverlines20()
	if len(result) != 5 {
		t.Fatalf("composited height = %d", len(result))
	}
	first := StripTerminalSequences(result[0])
	if !strings.HasPrefix(first, "XY  ") {
		t.Fatalf("overlay not composited at top-left: %q", first)
	}
	if !strings.Contains(StripTerminalSequences(result[0]), "aaaa") {
		t.Fatalf("base content lost under overlay: %q", result[0])
	}
}

func (b *Base) compositeOverlines20() []string {
	lines := b.Container.Render(20)
	return b.compositeOverlays(lines, 20, 5)
}

func TestCompositeTuiLine(t *testing.T) {
	result := CompositeTuiLine("0123456789", "AB", 3, 4, 10)
	plain := StripTerminalSequences(result)
	if plain != "012AB  789" {
		t.Fatalf("CompositeTuiLine = %q (%q)", plain, result)
	}
	if VisibleWidth(plain) != 10 {
		t.Fatalf("composite width = %d", VisibleWidth(plain))
	}
}

func TestApplyLineResets(t *testing.T) {
	base := NewBase(newFakeTerminal(10, 3), false, "regular")
	lines := base.ApplyLineResets([]string{"\tstyled", "plain"})
	if !strings.HasSuffix(lines[0], SegmentReset) || !strings.HasSuffix(lines[1], SegmentReset) {
		t.Fatalf("line resets missing: %q", lines)
	}
	if !strings.HasPrefix(lines[0], "   ") {
		t.Fatalf("tab not normalized: %q", lines[0])
	}
}

func TestExtractCursorPosition(t *testing.T) {
	base := NewBase(newFakeTerminal(10, 3), false, "regular")
	lines := []string{"one", "tw" + CursorMarker + "o", "three"}
	row, col, found := base.ExtractCursorPosition(lines, 3)
	if !found || row != 1 || col != 2 {
		t.Fatalf("cursor = (%d, %d, %v)", row, col, found)
	}
	if strings.Contains(lines[1], CursorMarker) {
		t.Fatalf("marker not stripped: %q", lines[1])
	}
	if _, _, found := base.ExtractCursorPosition([]string{"none"}, 1); found {
		t.Fatal("no marker should not be found")
	}
}

func TestRenderThrottling(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	base := NewBase(terminal, false, "regular")
	base.SetMinRenderInterval(30 * time.Millisecond)
	var renders atomic.Int64
	base.SetHooks(tuiHooks{doRender: func() { renders.Add(1) }})

	base.RequestRender(false)
	time.Sleep(5 * time.Millisecond)
	base.RequestRender(false)
	base.RequestRender(false)
	time.Sleep(60 * time.Millisecond)
	if renders.Load() == 0 {
		t.Fatal("coalesced render never ran")
	}
	if renders.Load() > 2 {
		t.Fatalf("renders = %d, expected coalescing to at most 2", renders.Load())
	}

	base.RenderNow(true)
	if renders.Load() < 2 {
		t.Fatalf("RenderNow should force a render, renders = %d", renders.Load())
	}
	base.Stop(StopOptions{})
	base.RenderNow(false)
	stoppedRenders := renders.Load()
	time.Sleep(20 * time.Millisecond)
	if renders.Load() != stoppedRenders {
		t.Fatal("renders should not run after stop")
	}
}

func TestMouseDispatchTargeting(t *testing.T) {
	terminal := newFakeTerminal(20, 6)
	base := NewBase(terminal, false, "regular")

	child := newRecordingComponent("child")
	container := &Container{}
	container.AddChild(child)
	base.AddChild(&staticComponent{lines: []string{"", "top"}})
	base.AddChild(container)

	event := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 1, Y: 0, ScreenX: 1, ScreenY: 3, Width: 20, Height: 6}
	dispatch := dispatchMouseEvent(container, event)
	if dispatch == nil {
		t.Fatal("container should dispatch to child")
	}
	if dispatch.focusTarget != child && dispatch.focusTarget != container {
		t.Fatalf("focus target = %v", dispatch.focusTarget)
	}
	retargeted := retargetMouseEvent(event, dispatch.target)
	if retargeted.Y != 0 {
		t.Fatalf("retargeted Y = %d, want 0", retargeted.Y)
	}
}
