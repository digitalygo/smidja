package tui

import (
	"testing"
)

func TestEditorMouseClickPositionsCursor(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("hello world")
	lines := editor.Render(30)
	if len(lines) == 0 {
		t.Fatalf("no render")
	}
	click := MouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: 3, Y: 1, ScreenX: 3, ScreenY: 1, Width: 30, Height: 5}
	if result := editor.HandleMouse(click); result == nil || !result.Focus {
		t.Fatalf("click should focus")
	}
	press := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 5, Y: 1, ScreenX: 5, ScreenY: 1, Width: 30, Height: 5}
	if result := editor.HandleMouse(press); result == nil {
		t.Fatalf("press should handle")
	}
}

func TestEditorMouseDragSelection(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("hello world selection")
	editor.Render(30)
	press := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 0, Y: 1, ScreenX: 0, ScreenY: 1, Width: 30, Height: 5}
	editor.HandleMouse(press)
	drag := MouseEvent{Type: MouseDrag, Button: MouseButtonLeft, X: 5, Y: 1, ScreenX: 5, ScreenY: 1, Width: 30, Height: 5}
	if result := editor.HandleMouse(drag); result == nil || !result.Focus {
		t.Fatalf("drag should select")
	}
	if !editor.HasSelection() {
		t.Fatalf("drag should create selection")
	}
	if got := editor.SelectedText(); got == "" {
		t.Fatalf("drag selection empty")
	}
	dragBack := MouseEvent{Type: MouseDrag, Button: MouseButtonLeft, X: 0, Y: 1, ScreenX: 0, ScreenY: 1, Width: 30, Height: 5}
	editor.HandleMouse(dragBack)
}

func TestEditorMouseAutocompleteArea(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("n")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("should show")
	}
	editor.Render(40)
	click := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 2, Y: 4, ScreenX: 2, ScreenY: 4, Width: 40, Height: 10}
	editor.HandleMouse(click)
	wheel := MouseEvent{Type: MouseWheel, WheelDelta: -1, X: 2, Y: 4, ScreenX: 2, ScreenY: 4, Width: 40, Height: 10}
	editor.HandleMouse(wheel)
	other := MouseEvent{Type: MouseWheel, Button: MouseButtonLeft, X: 0, Y: 0, ScreenX: 0, ScreenY: 0, Width: 40, Height: 10}
	if result := editor.HandleMouse(other); result != nil {
		t.Fatalf("wheel outside should be nil, got %v", result)
	}
}

func TestEditorMousePositionEdges(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 10})
	long := "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	editor.SetText(long)
	editor.Render(30)
	top := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 1, Y: 0, ScreenX: 1, ScreenY: 0, Width: 30, Height: 10}
	editor.HandleMouse(top)
	bottom := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 1, Y: 99, ScreenX: 1, ScreenY: 99, Width: 30, Height: 10}
	editor.HandleMouse(bottom)
	click := MouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: 50, Y: 1, ScreenX: 50, ScreenY: 1, Width: 30, Height: 10}
	editor.HandleMouse(click)
}
