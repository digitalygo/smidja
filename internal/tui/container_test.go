package tui

import "testing"

type mouseResultComponent struct {
	staticComponent
	result *MouseEventResult
	events []MouseEvent
}

func (m *mouseResultComponent) HandleMouse(event MouseEvent) *MouseEventResult {
	m.events = append(m.events, event)
	return m.result
}

type nestedChildrenComponent struct {
	children []Component
}

func (n *nestedChildrenComponent) Render(width int) []string { return nil }
func (n *nestedChildrenComponent) Invalidate()               {}
func (n *nestedChildrenComponent) containerChildren() []Component {
	return n.children
}

func TestContainerChildManagement(t *testing.T) {
	container := &Container{}
	first := newRecordingComponent("first")
	second := newRecordingComponent("second")
	container.AddChild(first)
	container.AddChild(second)
	if len(container.Children()) != 2 {
		t.Fatalf("children = %d, want 2", len(container.Children()))
	}
	container.Invalidate()
	container.RemoveChild(first)
	if len(container.Children()) != 1 || container.Children()[0] != second {
		t.Fatalf("remove left %d children", len(container.Children()))
	}
	container.RemoveChild(first)
	if len(container.Children()) != 1 {
		t.Fatalf("removing absent child changed children: %d", len(container.Children()))
	}
	container.Clear()
	if len(container.Children()) != 0 {
		t.Fatalf("clear left %d children", len(container.Children()))
	}
}

func TestContainerHandleMouse(t *testing.T) {
	container := &Container{}
	child := newRecordingComponent("child")
	container.AddChild(child)

	press := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 1, Y: 0, ScreenX: 1, ScreenY: 0, Width: 10, Height: 3}
	result := container.HandleMouse(press)
	if result == nil || !result.Handled {
		t.Fatalf("child press not handled: %+v", result)
	}

	outside := press
	outside.Y = -1
	if container.HandleMouse(outside) != nil {
		t.Fatal("negative row should not dispatch")
	}
	outside.Y = 9
	if container.HandleMouse(outside) != nil {
		t.Fatal("row past height should not dispatch")
	}

	sparse := &Container{}
	sparse.AddChild(child)
	below := MouseEvent{Type: MousePress, X: 0, Y: 2, ScreenX: 0, ScreenY: 2, Width: 10, Height: 4}
	if sparse.HandleMouse(below) != nil {
		t.Fatal("row past last child should not dispatch")
	}
}

func TestDispatchMouseEventResults(t *testing.T) {
	component := &mouseResultComponent{staticComponent: staticComponent{lines: []string{"x"}}}
	event := MouseEvent{Type: MousePress, X: 2, Y: 1, ScreenX: 5, ScreenY: 4, Width: 10, Height: 6}

	if dispatchMouseEvent(component, event) != nil {
		t.Fatal("nil mouse result should not dispatch")
	}

	component.result = &MouseEventResult{}
	if dispatchMouseEvent(component, event) != nil {
		t.Fatal("unhandled mouse result should not dispatch")
	}

	component.result = &MouseEventResult{Focus: true}
	dispatch := dispatchMouseEvent(component, event)
	if dispatch == nil || dispatch.focusTarget != component {
		t.Fatalf("focus result not dispatched: %+v", dispatch)
	}
	if !dispatch.result.Handled {
		t.Fatal("dispatched result should be marked handled")
	}
	if dispatch.target.originX != 3 || dispatch.target.originY != 3 {
		t.Fatalf("origin = (%d, %d), want (3, 3)", dispatch.target.originX, dispatch.target.originY)
	}

	component.result = &MouseEventResult{Capture: true}
	if dispatch := dispatchMouseEvent(component, event); dispatch == nil || dispatch.focusTarget != nil {
		t.Fatalf("capture result should dispatch without focus: %+v", dispatch)
	}

	if dispatchMouseEvent(&staticComponent{}, event) != nil {
		t.Fatal("static component should not dispatch")
	}
}

func TestContainsComponent(t *testing.T) {
	leaf := newRecordingComponent("leaf")
	other := newRecordingComponent("other")
	container := &Container{}
	container.AddChild(leaf)

	if !containsComponent(leaf, leaf) {
		t.Fatal("component should contain itself")
	}
	if !containsComponent(container, leaf) {
		t.Fatal("container should contain child")
	}
	if containsComponent(container, other) {
		t.Fatal("container should not contain unrelated component")
	}
	if containsComponent(leaf, other) {
		t.Fatal("leaf should not contain unrelated component")
	}

	provider := &nestedChildrenComponent{children: []Component{leaf}}
	if !containsComponent(provider, leaf) {
		t.Fatal("container provider should expose children")
	}
	if containsComponent(provider, other) {
		t.Fatal("container provider should not contain unrelated component")
	}

	outer := &Container{}
	inner := &Container{}
	inner.AddChild(leaf)
	outer.AddChild(inner)
	if !containsComponent(outer, leaf) {
		t.Fatal("nested containers should contain grandchild")
	}
}

func TestMouseEventResultWithRender(t *testing.T) {
	result := &MouseEventResult{}
	returned := result.WithRender(true)
	if returned != result || !result.Render || !result.renderSet {
		t.Fatalf("WithRender = %+v", result)
	}
	returned = result.WithRender(false)
	if returned.Render || !result.renderSet {
		t.Fatalf("WithRender(false) = %+v", result)
	}
}

func TestInputRoutingGuards(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	target := newRecordingComponent("target")
	base.AddChild(target)
	base.SetFocus(target)

	base.Mode()

	base.mu.Lock()
	base.stopped = true
	base.mu.Unlock()
	base.handleTerminalInput("x")
	if len(target.inputs) != 0 {
		t.Fatalf("stopped base should drop input: %q", target.inputs)
	}
	base.mu.Lock()
	base.stopped = false
	base.mu.Unlock()

	base.handleTerminalInput("")
	if len(target.inputs) != 0 {
		t.Fatalf("empty input should be dropped: %q", target.inputs)
	}

	base.SetFocus(nil)
	base.handleTerminalInput("x")
	if len(target.inputs) != 0 {
		t.Fatalf("no focus should drop input: %q", target.inputs)
	}

	base.SetFocus(&staticComponent{lines: []string{"plain"}})
	base.handleTerminalInput("x")
	if len(target.inputs) != 0 {
		t.Fatalf("non-input focus should drop input: %q", target.inputs)
	}
}

func TestInputRoutingDebugCallback(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	calls := 0
	base.SetOnDebug(func() { calls++ })
	data := "\x1b[100;6u"
	if !MatchesKey(data, "shift+ctrl+d") {
		t.Fatalf("test sequence %q does not match shift+ctrl+d", data)
	}
	base.handleTerminalInput(data)
	if calls != 1 {
		t.Fatalf("debug callback calls = %d, want 1", calls)
	}
}

func TestInputRoutingHiddenOverlayFocus(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)

	visible := true
	overlay := &overlayTestComponent{staticComponent: &staticComponent{lines: []string{"overlay"}}}
	base.ShowOverlay(overlay, OverlayOptions{Visible: func(width, height int) bool { return visible }})
	if base.FocusedComponent() != overlay {
		t.Fatal("overlay should capture focus")
	}
	visible = false
	base.handleTerminalInput("x")
	if base.FocusedComponent() != editor {
		t.Fatalf("hidden focused overlay should restore focus, got %v", base.FocusedComponent())
	}
}

func TestInputRoutingHiddenOverlayRestoresTopmost(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)

	first := &overlayTestComponent{staticComponent: &staticComponent{lines: []string{"first"}}}
	base.ShowOverlay(first, OverlayOptions{})
	secondVisible := true
	second := &overlayTestComponent{staticComponent: &staticComponent{lines: []string{"second"}}}
	base.ShowOverlay(second, OverlayOptions{Visible: func(width, height int) bool { return secondVisible }})
	if base.FocusedComponent() != second {
		t.Fatal("second overlay should capture focus")
	}
	secondVisible = false
	base.handleTerminalInput("x")
	if base.FocusedComponent() != first {
		t.Fatalf("hidden overlay should fall back to topmost visible, got %v", base.FocusedComponent())
	}
}
