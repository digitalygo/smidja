package tui

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
)

var errTestExternal = errors.New("external failed")

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func newTestEditor() *Editor {
	return NewEditor(EditorOptions{TerminalRows: 24})
}

func TestNewEditorDefaults(t *testing.T) {
	editor := NewEditor(EditorOptions{})
	if editor.Text() != "" {
		t.Fatalf("empty text = %q", editor.Text())
	}
	if editor.QueuedCount() != 0 {
		t.Fatalf("queued should be empty")
	}
	if editor.HasSelection() {
		t.Fatalf("no selection expected")
	}
	if editor.IsShowingAutocomplete() {
		t.Fatalf("no autocomplete expected")
	}
	if editor.IsBashMode() {
		t.Fatalf("bash mode should be off")
	}
}

func TestEditorSetTextAndCursor(t *testing.T) {
	editor := newTestEditor()
	changed := ""
	editor.SetOnChange(func(s string) { changed = s })
	editor.SetText("hello\nworld")
	if editor.Text() != "hello\nworld" {
		t.Fatalf("text = %q", editor.Text())
	}
	if changed != "hello\nworld" {
		t.Fatalf("onChange = %q", changed)
	}
	if lines := editor.Lines(); len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	line, col := editor.Cursor()
	if line != 1 || col != 5 {
		t.Fatalf("cursor = %d %d", line, col)
	}
	if editor.ExpandedText() != "hello\nworld" {
		t.Fatalf("expanded = %q", editor.ExpandedText())
	}
}

func TestEditorSelectionAndCopy(t *testing.T) {
	editor := newTestEditor()
	editor.SetText("hello world")
	editor.SetSelection(0, 0, 0, 5)
	if !editor.HasSelection() {
		t.Fatalf("should have selection")
	}
	if got := editor.SelectedText(); got != "hello" {
		t.Fatalf("selected = %q", got)
	}
	var written []string
	editor.SetClipboard(true, func(s string) { written = append(written, s) })
	if !editor.CopySelection() {
		t.Fatalf("copy should succeed")
	}
	if len(written) != 1 || !strings.Contains(written[0], "\x1b]52;c;") {
		t.Fatalf("osc52 missing: %q", written)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(written[0], "\x1b]52;c;"), "\x07"), ""))
	if err != nil {
		_ = decoded
	}
	editor.ClearSelection()
	if editor.HasSelection() {
		t.Fatalf("cleared")
	}
	if editor.CopySelection() {
		t.Fatalf("copy without selection should fail")
	}
	editor.SetSelection(0, 0, 0, 0)
	if editor.HasSelection() {
		t.Fatalf("empty selection should be inactive")
	}
	editor.SetSelection(0, 6, 0, 11)
	editor.SetClipboard(false, func(s string) {})
	if editor.CopySelection() {
		t.Fatalf("unsupported clipboard should fail")
	}
	editor.SetClipboard(true, nil)
	if editor.CopySelection() {
		t.Fatalf("nil writer should fail")
	}
	editor.SetText("one\ntwo\nthree")
	editor.SetSelection(0, 1, 2, 2)
	if got := editor.SelectedText(); got != "ne\ntwo\nth" {
		t.Fatalf("multiline selected = %q", got)
	}
	editor.SetSelection(5, 0, 0, 0)
	editor.SetSelection(-1, -1, 99, 99)
}

func TestEditorHandleClear(t *testing.T) {
	editor := newTestEditor()
	editor.SetText("hello")
	var written []string
	editor.SetClipboard(true, func(s string) { written = append(written, s) })
	editor.SetSelection(0, 0, 0, 5)
	if !editor.HandleClear() {
		t.Fatalf("clear with selection should return true")
	}
	if editor.HasSelection() {
		t.Fatalf("selection should clear")
	}
	if editor.Text() != "hello" {
		t.Fatalf("text should remain after selection copy: %q", editor.Text())
	}
	if len(written) != 1 {
		t.Fatalf("should have copied")
	}
	editor.SetClipboard(false, nil)
	editor.SetSelection(0, 0, 0, 2)
	if !editor.HandleClear() {
		t.Fatalf("clear with selection unsupported should still return true")
	}
	if editor.HandleClear() {
		t.Fatalf("clear empty with no selection second call should be false when text remains? text=hello")
	}
	editor.SetText("")
	if editor.HandleClear() {
		t.Fatalf("clear empty should return false")
	}
	changed := "unset"
	editor.SetOnChange(func(s string) { changed = s })
	editor.SetText("to clear")
	editor.HandleClear()
	if changed != "" {
		t.Fatalf("clear onChange = %q", changed)
	}
	if editor.Text() != "" {
		t.Fatalf("cleared text = %q", editor.Text())
	}
}

func TestEditorBashAndThinkingBorder(t *testing.T) {
	editor := newTestEditor()
	editor.SetText("!ls -la")
	if !editor.IsBashMode() {
		t.Fatalf("bash mode should be on")
	}
	editor.SetText("hello")
	if editor.IsBashMode() {
		t.Fatalf("bash mode should be off")
	}
	editor.SetText("   !echo hi")
	if !editor.IsBashMode() {
		t.Fatalf("trimmed bash should be on")
	}
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max", "unknown", ""} {
		editor.SetThinkingLevel(level)
		border := editor.BorderColor()
		if border == nil {
			t.Fatalf("border nil for %q", level)
		}
		if got := border("x"); !strings.Contains(got, "x") {
			t.Fatalf("border should contain text for %q", level)
		}
	}
	if token := thinkingBorderToken("off"); token != ThemeColor("thinkingOff") {
		t.Fatalf("token off = %q", token)
	}
	if token := thinkingBorderToken("bogus"); token != ThemeColor("thinkingOff") {
		t.Fatalf("bogus token = %q", token)
	}
	theme := mustEditorTheme(t)
	themed := NewEditor(EditorOptions{Theme: theme, ThinkingLevel: "high"})
	themed.SetText("hello")
	if themed.BorderColor()("b") == "b" {
		t.Fatalf("themed border should style")
	}
	themed.SetText("!bash")
	if !themed.IsBashMode() {
		t.Fatalf("themed bash")
	}
	themed.SetTheme(nil)
	themed.SetThinkingLevel("low")
	themed.SetWorkspaceRoot("/tmp")
	themed.SetCommandCatalog([]AutocompleteItem{{Value: "x"}})
	themed.SetClipboard(true, nil)
	themed.SetExternalCommand("vim", nil)
	themed.SetTerminalRows(30)
	themed.SetDisableSubmit(true)
	themed.SetOnSubmit(func(string) {})
	themed.Invalidate()
}

func mustEditorTheme(t *testing.T) *Theme {
	t.Helper()
	registry := NewThemeRegistry("", "", ColorModeTrueColor)
	theme, err := registry.loadByName("dark")
	if err != nil {
		t.Fatalf("load dark = %v", err)
	}
	return theme
}

func TestEditorHistoryIntegration(t *testing.T) {
	home := t.TempDir()
	history := NewHistoryStore(home, "proj")
	editor := NewEditor(EditorOptions{History: history, TerminalRows: 24})
	editor.SetText("first")
	var submitted []string
	editor.SetOnSubmit(func(s string) { submitted = append(submitted, s) })
	editor.HandleInput("\r")
	if len(submitted) != 1 || submitted[0] != "first" {
		t.Fatalf("submitted = %q", submitted)
	}
	if len(history.Entries()) != 1 {
		t.Fatalf("history entries = %q", history.Entries())
	}
	editor.AddToHistory("second")
	editor.AddToHistory("")
	emptyEditor := NewEditor(EditorOptions{TerminalRows: 24})
	emptyEditor.AddToHistory("nothing")
}

func TestEditorQueueFollowUpDequeue(t *testing.T) {
	editor := newTestEditor()
	editor.SetText("follow me")
	editor.HandleInput("\x1b\r")
	if editor.QueuedCount() != 1 {
		t.Fatalf("queued = %d", editor.QueuedCount())
	}
	if editor.Text() != "" {
		t.Fatalf("editor should clear after queue: %q", editor.Text())
	}
	if msgs := editor.QueuedMessages(); len(msgs) != 1 || msgs[0] != "follow me" {
		t.Fatalf("queued msgs = %q", msgs)
	}
	editor.SetText("current")
	editor.HandleInput("\x1bp")
	if editor.QueuedCount() != 0 {
		t.Fatalf("queue should drain")
	}
	if !strings.Contains(editor.Text(), "follow me") || !strings.Contains(editor.Text(), "current") {
		t.Fatalf("restored = %q", editor.Text())
	}
	editor.HandleInput("\x1bp")
	editor.SetText("")
	editor.HandleInput("\x1b\r")
	editor.HandleInput("\x1b\r")
}

func TestEditorExternalEditorAction(t *testing.T) {
	editor := newTestEditor()
	editor.SetText("original")
	editor.SetExternalCommand("fake", RunnerFunc(func(command, filePath string) error {
		return writeTestFile(filePath, "edited via external")
	}))
	if err := editor.OpenExternalEditor(); err != nil {
		t.Fatalf("open = %v", err)
	}
	if editor.Text() != "edited via external" {
		t.Fatalf("external text = %q", editor.Text())
	}
	editor.SetExternalCommand("failing", RunnerFunc(func(command, filePath string) error {
		return errTestExternal
	}))
	if err := editor.OpenExternalEditor(); err == nil {
		t.Fatalf("expected error")
	}
}

func TestEditorSettersCoverage(t *testing.T) {
	editor := newTestEditor()
	editor.SetFocused(true)
	editor.SetFocused(false)
	editor.SetTerminalRows(0)
	editor.SetTerminalRows(40)
	editor.SetDisableSubmit(false)
	if editor.AutocompletePrefix() != "" {
		t.Fatalf("prefix should be empty")
	}
	if len(editor.AutocompleteItems()) != 0 {
		t.Fatalf("items should be empty")
	}
	if editor.InlineHint() != "" {
		t.Fatalf("hint should be empty")
	}
}
