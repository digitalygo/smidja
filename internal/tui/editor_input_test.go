package tui

import (
	"strings"
	"testing"
)

func TestEditorInputTypingAndUndo(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetFocused(true)
	for _, ch := range []string{"h", "e", "l", "l", "o"} {
		editor.HandleInput(ch)
	}
	if editor.Text() != "hello" {
		t.Fatalf("typing = %q", editor.Text())
	}
	editor.HandleInput("\x1f")
	if editor.Text() != "" {
		t.Fatalf("undo word = %q", editor.Text())
	}
	editor.HandleInput("a")
	editor.HandleInput(" ")
	editor.HandleInput("b")
	editor.HandleInput("\x1f")
	if editor.Text() != "a" {
		t.Fatalf("undo after space = %q", editor.Text())
	}
}

func TestEditorInputMovementKeys(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("alpha beta")
	editor.HandleInput("\x01")
	if _, col := editor.Cursor(); col != 0 {
		t.Fatalf("ctrl+a line start col=%d", col)
	}
	editor.HandleInput("\x05")
	if _, col := editor.Cursor(); col != len("alpha beta") {
		t.Fatalf("ctrl+e line end")
	}
	editor.HandleInput("\x1b[D")
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x1bb")
	if _, col := editor.Cursor(); col == len("alpha beta") {
		t.Fatalf("word left should move")
	}
	editor.HandleInput("\x1bf")
	editor.HandleInput("\x1b[H")
	if _, col := editor.Cursor(); col != 0 {
		t.Fatalf("home line start")
	}
	editor.HandleInput("\x1b[F")
	editor.HandleInput("\x1b[7^")
	if line, col := editor.Cursor(); line != 0 || col != 0 {
		t.Fatalf("ctrl+home buffer start = %d %d", line, col)
	}
	editor.HandleInput("\x1b[8^")
	editor.HandleInput("\x1b[5~")
	editor.HandleInput("\x1b[6~")
}

func TestEditorInputDeletionKeys(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("hello world")
	editor.HandleInput("\x01")
	editor.HandleInput("\x0b")
	if editor.Text() != "" {
		t.Fatalf("ctrl+k to end = %q", editor.Text())
	}
	editor.SetText("hello world")
	editor.HandleInput("\x05")
	editor.HandleInput("\x15")
	if editor.Text() != "" {
		t.Fatalf("ctrl+u to start = %q", editor.Text())
	}
	editor.SetText("alpha beta gamma")
	editor.HandleInput("\x05")
	editor.HandleInput("\x17")
	if editor.Text() != "alpha beta " {
		t.Fatalf("ctrl+w = %q", editor.Text())
	}
	editor.SetText("alpha beta gamma")
	editor.HandleInput("\x01")
	editor.HandleInput("\x1bd")
	if !strings.HasPrefix(editor.Text(), "beta") && editor.Text() != " beta gamma" {
		t.Fatalf("alt+d = %q", editor.Text())
	}
	editor.SetText("abc")
	editor.HandleInput("\x01")
	editor.HandleInput("\x1b[3~")
	if editor.Text() != "bc" {
		t.Fatalf("delete forward = %q", editor.Text())
	}
	editor.SetText("abc")
	editor.HandleInput("\x05")
	editor.HandleInput("\x7f")
	if editor.Text() != "ab" {
		t.Fatalf("backspace = %q", editor.Text())
	}
	editor.HandleInput("\x1b[3;2~")
	editor.HandleInput("\x1b[127;2u")
}

func TestEditorInputKillYank(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("one two three")
	editor.HandleInput("\x05")
	editor.HandleInput("\x17")
	editor.HandleInput("\x19")
	if !strings.Contains(editor.Text(), "three") {
		t.Fatalf("yank = %q", editor.Text())
	}
	editor.HandleInput("\x1by")
}

func TestEditorInputNewlineAndSubmit(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	var submitted []string
	editor.SetOnSubmit(func(s string) { submitted = append(submitted, s) })
	editor.SetText("line one")
	editor.HandleInput("\n")
	if editor.Text() != "line one\n" && !strings.Contains(editor.Text(), "\n") {
		t.Fatalf("newline = %q", editor.Text())
	}
	editor.SetText("to submit")
	editor.HandleInput("\r")
	if len(submitted) != 1 || submitted[0] != "to submit" {
		t.Fatalf("submitted = %q", submitted)
	}
	if editor.Text() != "" {
		t.Fatalf("should clear after submit")
	}
	editor.SetDisableSubmit(true)
	editor.SetText("blocked")
	editor.HandleInput("\r")
	if len(submitted) != 1 {
		t.Fatalf("disabled submit should not submit")
	}
	editor.SetDisableSubmit(false)
	editor.SetText("a\\")
	editor.HandleInput("\x05")
	editor.HandleInput("\r")
	editor.SetText("x")
	editor.HandleInput("\x1b[13;2u")
	if !strings.Contains(editor.Text(), "\n") {
		t.Fatalf("shift+enter newline = %q", editor.Text())
	}
}

func TestEditorInputHistoryViaUpDown(t *testing.T) {
	home := t.TempDir()
	history := NewHistoryStore(home, "proj")
	history.Add("first history")
	history.Add("second history")
	_ = history.Save()
	editor := NewEditor(EditorOptions{History: history, TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("\x1b[A")
	if editor.Text() != "second history" && editor.Text() != "first history" {
		t.Fatalf("up history = %q", editor.Text())
	}
	editor.HandleInput("\x1b[B")
	editor.HandleInput("\x1b[A")
	editor.HandleInput("\x1b[A")
	editor.HandleInput("\x1b[B")
	editor.HandleInput("\x1b[B")
}

func TestEditorInputDedicatedHistoryActions(t *testing.T) {
	home := t.TempDir()
	history := NewHistoryStore(home, "proj2")
	history.Add("entry one")
	history.Add("entry two")
	_ = history.Save()
	previous := GlobalKeybindings()
	manager := NewDefaultKeybindingsManager(KeybindingsConfig{
		"tui.editor.historyPrevious": []string{"ctrl+p"},
		"tui.editor.historyNext":     []string{"ctrl+n"},
	})
	SetGlobalKeybindings(manager)
	defer SetGlobalKeybindings(previous)
	editor := NewEditor(EditorOptions{History: history, TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("\x10")
	if editor.Text() == "" {
		t.Fatalf("ctrl+p should browse history")
	}
	editor.HandleInput("\x0e")
}

func TestEditorInputJumpMode(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("abc def ghi")
	editor.HandleInput("\x01")
	editor.HandleInput("\x1d")
	editor.HandleInput("d")
	if _, col := editor.Cursor(); col != 4 {
		t.Fatalf("jump forward col=%d", col)
	}
	editor.HandleInput("\x1b\x1d")
	editor.HandleInput("a")
	editor.HandleInput("\x1d")
	editor.HandleInput("\x1d")
	editor.HandleInput("\x03")
}

func TestEditorInputBracketedPaste(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.HandleInput(BracketedPasteStart + "pasted content" + BracketedPasteEnd)
	if editor.Text() != "pasted content" {
		t.Fatalf("paste = %q", editor.Text())
	}
	before := editor.Text()
	editor.HandleInput("\x1f")
	if editor.Text() == before && before != "" {
		t.Fatalf("paste undo should revert")
	}
	editor.HandleInput("\x1b[200~partial")
	editor.HandleInput(" continued\x1b[201~")
	if !strings.Contains(editor.Text(), "partial continued") {
		t.Fatalf("split paste = %q", editor.Text())
	}
	editor.HandleInput(BracketedPasteStart + BracketedPasteEnd + "after")
	if !strings.Contains(editor.Text(), "after") {
		t.Fatalf("empty paste + after = %q", editor.Text())
	}
}

func TestEditorInputLargePasteMarker(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	var submitted []string
	editor.SetOnSubmit(func(s string) { submitted = append(submitted, s) })
	large := ""
	for i := 0; i < 15; i++ {
		large += "pasted line number\n"
	}
	editor.HandleInput(BracketedPasteStart + large + BracketedPasteEnd)
	if !strings.Contains(editor.Text(), "[paste #1") {
		t.Fatalf("large paste marker missing: %q", editor.Text()[:100])
	}
	editor.HandleInput("\r")
	if len(submitted) != 1 || !strings.Contains(submitted[0], "pasted line") {
		t.Fatalf("submit should expand marker: %q", submitted)
	}
}

func TestEditorInputCopyClearKey(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("hello")
	var written []string
	editor.SetClipboard(true, func(s string) { written = append(written, s) })
	editor.SetSelection(0, 0, 0, 5)
	editor.HandleInput("\x03")
	if len(written) != 1 {
		t.Fatalf("ctrl+c with selection should copy")
	}
	if editor.HasSelection() {
		t.Fatalf("selection should clear after copy")
	}
	editor.HandleInput("\x03")
	if editor.Text() != "" {
		t.Fatalf("ctrl+c without selection should clear: %q", editor.Text())
	}
	editor.HandleInput("\x03")
	editor.SetText("x")
	editor.SetClipboard(false, nil)
	editor.HandleInput("\x03")
}

func TestEditorInputFollowUpDequeueKeys(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("queued item")
	editor.HandleInput("\x1b\r")
	if editor.QueuedCount() != 1 {
		t.Fatalf("followUp queue")
	}
	editor.HandleInput("\x1bp")
	if editor.QueuedCount() != 0 {
		t.Fatalf("dequeue should drain")
	}
	editor.SetText("")
	editor.HandleInput("\x1b\r")
	editor.HandleInput("\x1bp")
}

func TestEditorInputExternalKey(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("before")
	editor.SetExternalCommand("fake", RunnerFunc(func(command, filePath string) error {
		return writeTestFile(filePath, "after external")
	}))
	editor.HandleInput("\x07")
	if editor.Text() != "after external" {
		t.Fatalf("external via key = %q", editor.Text())
	}
}

func TestEditorInputShiftSpaceAndPrintables(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	SetKittyProtocolActive(false)
	editor.HandleInput(" ")
	if editor.Text() != " " {
		t.Fatalf("space = %q", editor.Text())
	}
	editor.SetText("")
	editor.HandleInput("hello world via direct")
	if editor.Text() != "hello world via direct" {
		t.Fatalf("direct insert = %q", editor.Text())
	}
	editor.HandleInput("\x00")
	editor.HandleInput("\x1b[97u")
}

func TestEditorInputPageAndCursorEdges(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("one\ntwo\nthree\nfour\nfive")
	editor.HandleInput("\x1b[5~")
	editor.HandleInput("\x1b[6~")
	editor.HandleInput("\x1b[D")
	editor.HandleInput("\x1b[C")
	editor.SetText("")
	editor.HandleInput("\x1b[A")
	editor.HandleInput("\x1b[B")
}
func TestEditorInputEmptySubmitNoop(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	var submitted []string
	var changed []string
	editor.SetOnSubmit(func(s string) { submitted = append(submitted, s) })
	editor.SetOnChange(func(s string) { changed = append(changed, s) })
	editor.SetText("")
	changed = nil
	editor.HandleInput("\r")
	if len(submitted) != 0 {
		t.Fatalf("empty submit called onSubmit: %q", submitted)
	}
	if len(changed) != 0 {
		t.Fatalf("empty submit called onChange: %q", changed)
	}
	if editor.Text() != "" {
		t.Fatalf("empty submit changed text: %q", editor.Text())
	}
}

func TestEditorInputWhitespaceSubmitNoop(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	home := t.TempDir()
	history := NewHistoryStore(home, "proj-ws")
	editor2 := NewEditor(EditorOptions{History: history, TerminalRows: 24})
	var submitted []string
	var changed []string
	editor2.SetOnSubmit(func(s string) { submitted = append(submitted, s) })
	editor2.SetOnChange(func(s string) { changed = append(changed, s) })
	editor2.SetText("   \n  \t  ")
	before := editor2.Text()
	changed = nil
	editor2.HandleInput("\r")
	if len(submitted) != 0 {
		t.Fatalf("whitespace submit called onSubmit: %q", submitted)
	}
	if len(changed) != 0 {
		t.Fatalf("whitespace submit called onChange: %q", changed)
	}
	if editor2.Text() != before {
		t.Fatalf("whitespace submit cleared: %q -> %q", before, editor2.Text())
	}
	if len(history.Entries()) != 0 {
		t.Fatalf("whitespace added history: %q", history.Entries())
	}
	_ = editor
}
