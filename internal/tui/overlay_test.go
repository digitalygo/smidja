package tui

import "testing"

func TestOverlayHandleState(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)

	overlay := &staticComponent{lines: []string{"overlay"}}
	handle := base.ShowOverlay(overlay, OverlayOptions{Width: "6"})
	if handle.IsHidden() {
		t.Fatal("new overlay should be visible")
	}
	if !handle.IsFocused() {
		t.Fatal("new overlay should be focused")
	}
	handle.Focus()
	if !handle.IsFocused() {
		t.Fatal("explicit focus should keep overlay focused")
	}
	handle.Unfocus(editor)
	if handle.IsFocused() || base.FocusedComponent() != editor {
		t.Fatalf("unfocus target not applied: %v", base.FocusedComponent())
	}

	handle.Focus()
	handle.Unfocus(nil)
	if handle.IsFocused() || base.FocusedComponent() != editor {
		t.Fatalf("unfocus fallback not applied: %v", base.FocusedComponent())
	}

	handle.SetHidden(true)
	if !handle.IsHidden() || handle.IsFocused() {
		t.Fatal("hidden overlay should not be focused")
	}
	handle.SetHidden(true)
	if !handle.IsHidden() {
		t.Fatal("repeated hide should stay hidden")
	}
	handle.SetHidden(false)
	if handle.IsHidden() || !handle.IsFocused() {
		t.Fatal("unhidden overlay should recapture focus")
	}
}

func TestOverlayHandleNonCapturingToggle(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.SetFocus(editor)

	overlay := &staticComponent{lines: []string{"toast"}}
	handle := base.ShowOverlay(overlay, OverlayOptions{Anchor: AnchorTopRight, NonCapturing: true})
	handle.SetHidden(true)
	handle.SetHidden(false)
	if base.FocusedComponent() != editor {
		t.Fatal("non-capturing overlay must not steal focus on unhide")
	}
}

func TestOverlayHandleDetachedFocus(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.SetFocus(editor)

	overlay := &staticComponent{lines: []string{"overlay"}}
	handle := base.ShowOverlay(overlay, OverlayOptions{})
	handle.Hide()
	handle.Focus()
	if handle.IsFocused() {
		t.Fatal("detached overlay should not focus")
	}
	handle.Unfocus(editor)
	if base.FocusedComponent() != editor {
		t.Fatalf("detached unfocus changed focus: %v", base.FocusedComponent())
	}
	handle.Hide()
	if _, ok := handle.Bounds(); ok {
		t.Fatal("hidden overlay should report no bounds")
	}
}

func TestHideOverlayRestoresTopmost(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.SetFocus(editor)

	first := &staticComponent{lines: []string{"first"}}
	second := &staticComponent{lines: []string{"second"}}
	base.ShowOverlay(first, OverlayOptions{})
	base.ShowOverlay(second, OverlayOptions{})
	if base.FocusedComponent() != second {
		t.Fatal("second overlay should hold focus")
	}
	base.HideOverlay()
	if base.FocusedComponent() != first {
		t.Fatalf("hide should restore topmost overlay, got %v", base.FocusedComponent())
	}
	base.HideOverlay()
	if base.FocusedComponent() != editor {
		t.Fatalf("hide should restore pre-overlay focus, got %v", base.FocusedComponent())
	}
	base.HideOverlay()
	if base.FocusedComponent() != editor {
		t.Fatal("hide with empty stack should be a no-op")
	}
}

func TestSetHiddenRestoresTopmost(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.SetFocus(editor)

	first := &staticComponent{lines: []string{"first"}}
	second := &staticComponent{lines: []string{"second"}}
	base.ShowOverlay(first, OverlayOptions{})
	secondHandle := base.ShowOverlay(second, OverlayOptions{})
	secondHandle.SetHidden(true)
	if base.FocusedComponent() != first {
		t.Fatalf("hiding top overlay should restore first, got %v", base.FocusedComponent())
	}
}

func TestResolveMouseFocusTarget(t *testing.T) {
	noOverlay := NewBase(newFakeTerminal(20, 5), false, "regular")
	leaf := newRecordingComponent("leaf")
	if got := noOverlay.resolveMouseFocusTarget(leaf); got != Component(leaf) {
		t.Fatalf("without overlays target = %v", got)
	}

	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	child := newRecordingComponent("child")
	container := &Container{}
	container.AddChild(child)
	base.AddChild(container)
	base.ShowOverlay(container, OverlayOptions{})
	if got := base.resolveMouseFocusTarget(child); got != Component(container) {
		t.Fatalf("child inside overlay should resolve to overlay root: %v", got)
	}
}

func TestDispatchMouseToOverlay(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	overlay := &mouseResultComponent{
		staticComponent: staticComponent{lines: []string{"XY"}},
		result:          &MouseEventResult{Handled: true, Focus: true},
	}
	base.ShowOverlay(overlay, OverlayOptions{Width: "4", Anchor: AnchorTopLeft})
	base.compositeOverlays([]string{"aaaa", "bbbb", "cccc", "dddd", "eeee"}, 20, 5)

	inside := MouseEvent{Type: MousePress, Button: MouseButtonLeft, ScreenX: 1, ScreenY: 0, Width: 20, Height: 5}
	hit, dispatch := base.dispatchMouseToOverlay(inside)
	if !hit || dispatch == nil {
		t.Fatalf("inside event should hit overlay: hit=%v dispatch=%+v", hit, dispatch)
	}
	if dispatch.focusTarget != Component(overlay) {
		t.Fatalf("overlay focus target = %v", dispatch.focusTarget)
	}

	outside := inside
	outside.ScreenX = 12
	if hit, dispatch := base.dispatchMouseToOverlay(outside); hit || dispatch != nil {
		t.Fatalf("outside event should miss: hit=%v dispatch=%+v", hit, dispatch)
	}

	overlay.result = nil
	if hit, dispatch := base.dispatchMouseToOverlay(inside); !hit || dispatch != nil {
		t.Fatalf("unhandled overlay event should report hit only: hit=%v dispatch=%+v", hit, dispatch)
	}
}

func TestApplyMouseDispatchResult(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.SetFocus(editor)
	target := &mouseResultComponent{staticComponent: staticComponent{lines: []string{"target"}}}
	event := MouseEvent{Type: MousePress, Button: MouseButtonLeft, Width: 20, Height: 5}

	focused := &mouseDispatchResult{
		result:      MouseEventResult{Focus: true, Handled: true},
		target:      mouseDispatchTarget{component: target},
		focusTarget: target,
	}
	if !base.applyMouseDispatchResult(event, focused) {
		t.Fatal("focus change should request a render")
	}
	if base.FocusedComponent() != Component(target) {
		t.Fatalf("focus target = %v", base.FocusedComponent())
	}

	explicit := &mouseDispatchResult{
		result: MouseEventResult{renderSet: true, Render: false},
		target: mouseDispatchTarget{component: target},
	}
	if base.applyMouseDispatchResult(event, explicit) {
		t.Fatal("explicit render false should suppress render")
	}

	fallback := &mouseDispatchResult{
		result: MouseEventResult{Focus: true},
		target: mouseDispatchTarget{component: target},
	}
	other := newRecordingComponent("other")
	base.SetFocus(other)
	if !base.applyMouseDispatchResult(event, fallback) {
		t.Fatal("fallback focus target should request a render")
	}
	if base.FocusedComponent() != Component(target) {
		t.Fatalf("fallback focus target = %v", base.FocusedComponent())
	}

	wheel := &mouseDispatchResult{
		result: MouseEventResult{Capture: true},
		target: mouseDispatchTarget{component: target},
	}
	if !base.applyMouseDispatchResult(MouseEvent{Type: MouseWheel, Width: 20, Height: 5}, wheel) {
		t.Fatal("wheel capture should request a render")
	}
}

func TestOverlaySizeParsers(t *testing.T) {
	if parseSizeValue("", 20) != nil {
		t.Fatal("empty size should be nil")
	}
	if got := parseSizeValue("50%", 20); got == nil || *got != 10 {
		t.Fatalf("percent size = %v", got)
	}
	if got := parseSizeValue("7", 20); got == nil || *got != 7 {
		t.Fatalf("absolute size = %v", got)
	}
	if got := parseSizeValue("-3", 20); got == nil || *got != -3 {
		t.Fatalf("negative size = %v", got)
	}
	if parseSizeValue("nope", 20) != nil {
		t.Fatal("invalid size should be nil")
	}
	if parseSizeValue("7x", 20) != nil {
		t.Fatal("partially numeric size should be nil")
	}

	if percent, ok := parsePercent("25%"); !ok || percent != 0.25 {
		t.Fatalf("parsePercent(25%%) = %v %v", percent, ok)
	}
	if percent, ok := parsePercent("-10%"); !ok || percent != -0.1 {
		t.Fatalf("parsePercent(-10%%) = %v %v", percent, ok)
	}
	if _, ok := parsePercent("25"); ok {
		t.Fatal("parsePercent without suffix should fail")
	}
	if _, ok := parsePercent("%"); ok {
		t.Fatal("parsePercent with empty number should fail")
	}
	if _, ok := parsePercent("a%"); ok {
		t.Fatal("parsePercent with non-numeric number should fail")
	}

	tests := []struct {
		value string
		want  int
		ok    bool
	}{
		{value: "", want: 0, ok: false},
		{value: "-", want: 0, ok: false},
		{value: "-5", want: -5, ok: true},
		{value: "0", want: 0, ok: true},
		{value: "123", want: 123, ok: true},
		{value: "12a", want: 0, ok: false},
	}
	for _, test := range tests {
		got, ok := parseIntString(test.value)
		if got != test.want || ok != test.ok {
			t.Errorf("parseIntString(%q) = (%d, %v), want (%d, %v)", test.value, got, ok, test.want, test.ok)
		}
	}
}

func TestResolveOverlayAxisAbsolute(t *testing.T) {
	if got := resolveOverlayAxis("5", AnchorCenter, 3, 20, 0, true); got != 5 {
		t.Fatalf("absolute row = %d", got)
	}
	if got := resolveOverlayAxis("-4", AnchorCenter, 3, 20, 0, false); got != -4 {
		t.Fatalf("absolute negative col = %d", got)
	}
	if got := resolveOverlayAxis("50%", AnchorCenter, 4, 20, 2, true); got != 10 {
		t.Fatalf("percent row = %d", got)
	}
	if got := resolveOverlayAxis("", AnchorBottomRight, 4, 20, 0, false); got != 16 {
		t.Fatalf("anchor col = %d", got)
	}
}
