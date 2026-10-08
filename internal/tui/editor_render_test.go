package tui

import (
	"strings"
	"testing"
)

func TestEditorScrollBorder(t *testing.T) {
	if got := editorScrollBorder("↑", 0, 10); got != strings.Repeat("─", 10) {
		t.Fatalf("no count border = %q", got)
	}
	if got := editorScrollBorder("↑", 3, 20); !strings.Contains(got, "3 more") {
		t.Fatalf("count border = %q", got)
	}
	if got := editorScrollBorder("↓", 2, 5); VisibleWidth(got) != 5 {
		t.Fatalf("narrow border width = %q", got)
	}
	if got := editorScrollBorder("↑", 1, 0); got != "" {
		t.Fatalf("zero width = %q", got)
	}
}

func TestEditorMaxVisible(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.mu.Lock()
	if got := editor.maxVisibleLinesLocked(); got != 7 {
		t.Fatalf("max visible 24 rows = %d", got)
	}
	editor.mu.Unlock()
	editor.SetTerminalRows(10)
	editor.mu.Lock()
	if got := editor.maxVisibleLinesLocked(); got != 5 {
		t.Fatalf("min visible = %d", got)
	}
	editor.mu.Unlock()
	empty := NewEditor(EditorOptions{})
	empty.mu.Lock()
	empty.terminalRows = 0
	if got := empty.maxVisibleLinesLocked(); got < 5 {
		t.Fatalf("default rows visible = %d", got)
	}
	empty.mu.Unlock()
}

func TestEditorRenderWrappingAndCursor(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(true)
	editor.SetText("abcdefghijklmnopqrstuvwxyz")
	lines := editor.Render(15)
	if len(lines) < 4 {
		t.Fatalf("wrapped lines = %d: %q", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, CursorMarker) {
		t.Fatalf("focused render should contain cursor marker")
	}
	if !strings.Contains(joined, SGRInverse) {
		t.Fatalf("cursor inverse missing")
	}
	stripped := StripTerminalSequences(strings.ReplaceAll(joined, CursorMarker, ""))
	if !strings.Contains(stripped, "abc") {
		t.Fatalf("content missing: %q", stripped)
	}
	for _, line := range lines {
		if VisibleWidth(line) > 15 {
			t.Fatalf("line too wide %d: %q", VisibleWidth(line), line)
		}
	}
	editor.SetFocused(false)
	lines = editor.Render(15)
	if strings.Contains(strings.Join(lines, "\n"), CursorMarker) {
		t.Fatalf("unfocused should not contain marker")
	}
}

func TestEditorRenderCursorVisibleAcrossWrapping(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(true)
	editor.SetText("0123456789ABCDEFGHIJ")
	editor.mu.Lock()
	editor.buffer.SetCursor(0, 15)
	editor.mu.Unlock()
	lines := editor.Render(10)
	joined := strings.Join(lines, "\n")
	markerIdx := strings.Index(joined, CursorMarker)
	if markerIdx < 0 {
		t.Fatalf("marker missing")
	}
	afterMarker := joined[markerIdx:]
	if !strings.Contains(afterMarker, SGRInverse) {
		t.Fatalf("cursor inverse after marker missing")
	}
	editor.mu.Lock()
	editor.buffer.SetCursor(0, 0)
	editor.mu.Unlock()
	first := editor.Render(10)
	editor.mu.Lock()
	editor.buffer.SetCursor(0, len("0123456789ABCDEFGHIJ"))
	editor.mu.Unlock()
	last := editor.Render(10)
	if strings.Join(first, "\n") == strings.Join(last, "\n") {
		t.Fatalf("cursor at start vs end should render differently")
	}
}

func TestEditorRenderScrollKeepsCursorVisible(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 10})
	editor.SetFocused(true)
	long := ""
	for i := 0; i < 20; i++ {
		if i > 0 {
			long += "\n"
		}
		long += "line number " + itoa(i) + " with content"
	}
	editor.SetText(long)
	editor.mu.Lock()
	editor.buffer.SetCursor(19, 0)
	editor.mu.Unlock()
	lines := editor.Render(40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "↑") {
		t.Fatalf("scrolled render should show top indicator: %q", joined[:200])
	}
	editor.mu.Lock()
	editor.buffer.SetCursor(0, 0)
	editor.mu.Unlock()
	lines = editor.Render(40)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "↓") {
		t.Fatalf("top render should show bottom indicator")
	}
}

func TestEditorRenderHardCrop(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(false)
	long := strings.Repeat("y", 5000)
	editor.SetText(long)
	lines := editor.Render(80)
	joined := strings.Join(lines, "\n")
	if VisibleWidth(StripTerminalSequences(joined)) > 80*20 {
		t.Fatalf("hard crop failed, render too large")
	}
	if len(lines) > 100 {
		t.Fatalf("cropped lines too many: %d", len(lines))
	}
}

func TestEditorRenderBordersAndPadding(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24, Theme: mustEditorTheme(t)})
	editor.SetFocused(true)
	editor.SetText("hi")
	lines := editor.Render(20)
	if len(lines) < 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	top := StripTerminalSequences(lines[0])
	if !strings.Contains(top, "─") {
		t.Fatalf("top border = %q", top)
	}
	padded := NewEditor(EditorOptions{PaddingX: 4, TerminalRows: 24})
	padded.SetText("hi")
	lines = padded.Render(20)
	if !strings.HasPrefix(StripTerminalSequences(lines[1]), "    ") {
		t.Fatalf("padding missing: %q", lines[1])
	}
	if got := padded.Render(0); got != nil {
		t.Fatalf("zero width should be nil")
	}
	narrow := NewEditor(EditorOptions{TerminalRows: 24})
	narrow.SetText("hi")
	lines = narrow.Render(1)
	if len(lines) == 0 {
		t.Fatalf("narrow should still render")
	}
}

func TestEditorRenderAutocompleteList(t *testing.T) {
	root := t.TempDir()
	editor := NewEditor(EditorOptions{WorkspaceRoot: root, TerminalRows: 24})
	editor.SetFocused(true)
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("n")
	editor.HandleInput("e")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("should show autocomplete for /new")
	}
	lines := editor.Render(40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(StripTerminalSequences(joined), "new") {
		t.Fatalf("autocomplete list missing: %q", joined)
	}
	hint := editor.InlineHint()
	if hint == "" {
		t.Fatalf("hint should be present")
	}
	if !strings.Contains(joined, hint) && hint != "" {
		t.Fatalf("hint %q not in render", hint)
	}
}

func TestEditorRenderInlineHintAtEnd(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(true)
	editor.SetText("/ne")
	editor.mu.Lock()
	editor.triggerAutocompleteLocked(false)
	editor.mu.Unlock()
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("autocomplete should trigger")
	}
	lines := editor.Render(30)
	if len(lines) == 0 {
		t.Fatalf("no lines")
	}
}
func TestEditorRenderSelectionSingleLine(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(false)
	editor.SetText("hello world")
	editor.SetSelection(0, 0, 0, 5)
	lines := editor.Render(40)
	if len(lines) < 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	body := lines[1]
	if !strings.Contains(body, SGRInverse+"hello"+SGRInverseOff) {
		t.Fatalf("selection escape placement = %q", body)
	}
	stripped := StripTerminalSequences(body)
	if !strings.Contains(stripped, "hello world") {
		t.Fatalf("content = %q", stripped)
	}
	if VisibleWidth(body) > 40 {
		t.Fatalf("width %d > 40: %q", VisibleWidth(body), body)
	}
	plain := NewEditor(EditorOptions{TerminalRows: 24})
	plain.SetFocused(false)
	plain.SetText("hello world")
	plainLines := plain.Render(40)
	if StripTerminalSequences(plainLines[1]) != stripped {
		t.Fatalf("stripped mismatch %q vs %q", stripped, StripTerminalSequences(plainLines[1]))
	}
	if VisibleWidth(plainLines[1]) != VisibleWidth(body) {
		t.Fatalf("width changed with selection")
	}
}

func TestEditorRenderSelectionMultiline(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(false)
	editor.SetText("first\nsecond\nthird")
	editor.SetSelection(0, 2, 2, 3)
	lines := editor.Render(40)
	joined := strings.Join(lines, "\n")
	if strings.Count(joined, SGRInverse) < 3 {
		t.Fatalf("multiline should highlight 3 lines: %q", joined)
	}
	if !strings.Contains(joined, SGRInverse+"rst"+SGRInverseOff) {
		t.Fatalf("first line tail missing: %q", joined)
	}
	if !strings.Contains(joined, SGRInverse+"second"+SGRInverseOff) {
		t.Fatalf("middle line missing: %q", joined)
	}
	if !strings.Contains(joined, SGRInverse+"thi"+SGRInverseOff) {
		t.Fatalf("last line head missing: %q", joined)
	}
	stripped := StripTerminalSequences(joined)
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("content %q missing in %q", want, stripped)
		}
	}
	for _, line := range lines {
		if VisibleWidth(line) > 40 {
			t.Fatalf("width %d: %q", VisibleWidth(line), line)
		}
	}
}

func TestEditorRenderSelectionWrapped(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(true)
	editor.SetText("0123456789ABCDEFGHIJ")
	editor.SetSelection(0, 5, 0, 15)
	lines := editor.Render(10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, SGRInverse) {
		t.Fatalf("wrapped selection missing inverse: %q", joined)
	}
	if !strings.Contains(joined, CursorMarker) {
		t.Fatalf("cursor marker must remain: %q", joined)
	}
	stripped := StripTerminalSequences(strings.ReplaceAll(joined, CursorMarker, ""))
	for _, want := range []string{"01234", "9ABC", "IJ"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("wrapped content %q missing: %q", want, stripped)
		}
	}
	for _, line := range lines {
		if VisibleWidth(line) > 10 {
			t.Fatalf("width %d: %q", VisibleWidth(line), line)
		}
	}
	noSel := NewEditor(EditorOptions{TerminalRows: 24})
	noSel.SetFocused(true)
	noSel.SetText("0123456789ABCDEFGHIJ")
	noSel.mu.Lock()
	noSel.buffer.SetCursor(0, 20)
	noSel.mu.Unlock()
	editor.mu.Lock()
	editor.buffer.SetCursor(0, 20)
	editor.mu.Unlock()
	selLines := editor.Render(10)
	plainLines := noSel.Render(10)
	if VisibleWidth(strings.Join(selLines, "\n")) != VisibleWidth(strings.Join(plainLines, "\n")) {
		t.Fatalf("selection must not change visible width")
	}
}
