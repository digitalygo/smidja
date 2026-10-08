package tui

import (
	"testing"
)

func TestBaseSearchDialogOutOfOrderRestoresEditor(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	search := &staticComponent{lines: []string{"search"}}
	searchHandle := base.ShowOverlay(search, OverlayOptions{})
	dialog := &staticComponent{lines: []string{"dialog"}}
	dialogHandle := base.ShowOverlay(dialog, OverlayOptions{})
	if base.FocusedComponent() != Component(dialog) {
		t.Fatalf("dialog should hold focus, got %v", base.FocusedComponent())
	}
	searchHandle.Hide()
	if base.FocusedComponent() != Component(dialog) {
		t.Fatalf("dialog should stay focused after out-of-order close, got %v", base.FocusedComponent())
	}
	dialogHandle.Hide()
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("dialog close must restore editor, got %v", base.FocusedComponent())
	}
	if base.FocusedComponent() == Component(search) {
		t.Fatal("focus must never restore hidden search")
	}
}

func TestBaseMultipleNestedOverlaysOutOfOrder(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	first := &staticComponent{lines: []string{"first"}}
	second := &staticComponent{lines: []string{"second"}}
	third := &staticComponent{lines: []string{"third"}}
	firstHandle := base.ShowOverlay(first, OverlayOptions{})
	secondHandle := base.ShowOverlay(second, OverlayOptions{})
	thirdHandle := base.ShowOverlay(third, OverlayOptions{})
	if base.FocusedComponent() != Component(third) {
		t.Fatalf("third should hold focus, got %v", base.FocusedComponent())
	}
	secondHandle.Hide()
	if base.FocusedComponent() != Component(third) {
		t.Fatalf("third should stay focused after middle close, got %v", base.FocusedComponent())
	}
	thirdHandle.Hide()
	if base.FocusedComponent() != Component(first) {
		t.Fatalf("middle removal must route third close to first, got %v", base.FocusedComponent())
	}
	firstHandle.Hide()
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("final close must restore editor, got %v", base.FocusedComponent())
	}
}

func TestBaseLowerRemovalKeepsLifoRestore(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	lower := &staticComponent{lines: []string{"lower"}}
	middle := &staticComponent{lines: []string{"middle"}}
	upper := &staticComponent{lines: []string{"upper"}}
	lowerHandle := base.ShowOverlay(lower, OverlayOptions{})
	base.ShowOverlay(middle, OverlayOptions{})
	upperHandle := base.ShowOverlay(upper, OverlayOptions{})
	lowerHandle.Hide()
	if base.FocusedComponent() != Component(upper) {
		t.Fatalf("upper should stay focused, got %v", base.FocusedComponent())
	}
	upperHandle.Hide()
	if base.FocusedComponent() != Component(middle) {
		t.Fatalf("upper close must restore middle, got %v", base.FocusedComponent())
	}
	base.HideOverlay()
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("lifo pop must restore editor after lower removal, got %v", base.FocusedComponent())
	}
	if base.HasOverlay() {
		t.Fatal("stack should be empty")
	}
}

func TestBaseRemovedDescendantPreFocusRepaired(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	holder := &Container{}
	leaf := newRecordingComponent("leaf")
	holder.AddChild(leaf)
	holderHandle := base.ShowOverlay(holder, OverlayOptions{})
	base.SetFocus(leaf)
	upper := &staticComponent{lines: []string{"upper"}}
	upperHandle := base.ShowOverlay(upper, OverlayOptions{})
	base.mu.Lock()
	rewritten := false
	for _, entry := range base.overlayStack {
		if entry.component == Component(upper) && entry.preFocus == Component(leaf) {
			rewritten = true
		}
	}
	base.mu.Unlock()
	if !rewritten {
		t.Fatal("upper should capture leaf descendant as preFocus")
	}
	holderHandle.Hide()
	base.mu.Lock()
	for _, entry := range base.overlayStack {
		if entry.component == Component(upper) && (entry.preFocus == Component(leaf) || containsComponent(holder, entry.preFocus)) {
			base.mu.Unlock()
			t.Fatalf("descendant preFocus not repaired, got %v", entry.preFocus)
		}
	}
	base.mu.Unlock()
	upperHandle.Hide()
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("upper close must restore editor, got %v", base.FocusedComponent())
	}
}

func TestBaseFocusedDescendantRemovalRestores(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	holder := &Container{}
	leaf := newRecordingComponent("leaf")
	holder.AddChild(leaf)
	holderHandle := base.ShowOverlay(holder, OverlayOptions{})
	base.SetFocus(leaf)
	if base.FocusedComponent() != Component(leaf) {
		t.Fatalf("leaf should hold focus, got %v", base.FocusedComponent())
	}
	holderHandle.Hide()
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("descendant focus removal must restore editor, got %v", base.FocusedComponent())
	}
}

func TestBaseHiddenPreFocusSkippedOnRestore(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	lower := &staticComponent{lines: []string{"lower"}}
	lowerHandle := base.ShowOverlay(lower, OverlayOptions{})
	upper := &staticComponent{lines: []string{"upper"}}
	upperHandle := base.ShowOverlay(upper, OverlayOptions{})
	lowerHandle.SetHidden(true)
	upperHandle.Hide()
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("hidden predecessor must be skipped, got %v", base.FocusedComponent())
	}
	lowerHandle.SetHidden(false)
	if base.FocusedComponent() != Component(lower) {
		t.Fatalf("unhide must recapture focus, got %v", base.FocusedComponent())
	}
	lowerHandle.Hide()
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("lower close must restore editor, got %v", base.FocusedComponent())
	}
}

func TestBaseRemoveChildRepairsFocusReferences(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	overlay := &staticComponent{lines: []string{"overlay"}}
	overlayHandle := base.ShowOverlay(overlay, OverlayOptions{})
	base.RemoveChild(editor)
	base.mu.Lock()
	for _, entry := range base.overlayStack {
		if entry.preFocus == Component(editor) {
			base.mu.Unlock()
			t.Fatal("removed editor must not remain as preFocus")
		}
	}
	base.mu.Unlock()
	overlayHandle.Hide()
	if base.FocusedComponent() == Component(editor) {
		t.Fatal("removed editor must not regain focus")
	}
}

func TestBaseClearRepairsFocusReferences(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	overlay := &staticComponent{lines: []string{"overlay"}}
	overlayHandle := base.ShowOverlay(overlay, OverlayOptions{})
	base.Clear()
	overlayHandle.Hide()
	if base.FocusedComponent() == Component(editor) {
		t.Fatal("cleared editor must not regain focus")
	}
}

func TestBaseUnfocusUsesRepairedPredecessor(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)
	search := &staticComponent{lines: []string{"search"}}
	searchHandle := base.ShowOverlay(search, OverlayOptions{})
	dialog := &staticComponent{lines: []string{"dialog"}}
	dialogHandle := base.ShowOverlay(dialog, OverlayOptions{})
	searchHandle.Hide()
	dialogHandle.Unfocus(nil)
	if base.FocusedComponent() != Component(editor) {
		t.Fatalf("unfocus must restore editor, got %v", base.FocusedComponent())
	}
}

func TestBaseOutOfOrderStopKeepsValidFocus(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.AddChild(&plainComponent{lines: []string{"content"}})
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	if !screen.searchActive.Load() {
		t.Fatal("search should be active")
	}
	dialog := newRecordingComponent("dialog")
	dialogHandle := screen.ShowOverlay(dialog, OverlayOptions{Anchor: AnchorCenter})
	if screen.FocusedComponent() != Component(dialog) {
		t.Fatal("dialog should hold focus")
	}
	screen.CloseSearch()
	if screen.FocusedComponent() != Component(dialog) {
		t.Fatalf("dialog should stay focused after search close, got %v", screen.FocusedComponent())
	}
	screen.Stop(StopOptions{})
	if screen.searchActive.Load() {
		t.Fatal("stop must keep search closed")
	}
	dialogHandle.Hide()
	if screen.FocusedComponent() == Component(screen.searchOverlay) {
		t.Fatal("dialog close must never restore hidden search")
	}
}

func TestAltScreenSearchDialogOutOfOrderRestoresEditor(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	editor := newRecordingComponent("editor")
	screen.AddChild(editor)
	screen.SetFocus(editor)
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	searchOverlay := screen.searchOverlay
	if screen.FocusedComponent() != Component(searchOverlay) {
		t.Fatalf("search should hold focus, got %v", screen.FocusedComponent())
	}
	dialog := newRecordingComponent("dialog")
	dialogHandle := screen.ShowOverlay(dialog, OverlayOptions{Anchor: AnchorCenter})
	screen.CloseSearch()
	if screen.FocusedComponent() != Component(dialog) {
		t.Fatalf("dialog should stay focused, got %v", screen.FocusedComponent())
	}
	dialogHandle.Hide()
	if screen.FocusedComponent() != Component(editor) {
		t.Fatalf("dialog close must restore editor, got %v", screen.FocusedComponent())
	}
	if screen.FocusedComponent() == Component(searchOverlay) {
		t.Fatal("focus must never restore hidden search")
	}
	screen.Stop(StopOptions{})
}
