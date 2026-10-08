package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorAutocompleteTriggerSlash(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("n")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("slash should trigger")
	}
	if prefix := editor.AutocompletePrefix(); prefix != "/n" {
		t.Fatalf("prefix = %q", prefix)
	}
	items := editor.AutocompleteItems()
	if len(items) == 0 {
		t.Fatalf("no items")
	}
	hint := editor.InlineHint()
	if hint == "" {
		t.Fatalf("hint missing for /n")
	}
	editor.HandleInput("\x1b")
	if editor.IsShowingAutocomplete() {
		t.Fatalf("escape should dismiss")
	}
}

func TestEditorAutocompleteTabAccept(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("n")
	editor.HandleInput("e")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("should show")
	}
	editor.HandleInput("\t")
	if editor.IsShowingAutocomplete() {
		t.Fatalf("tab should accept and dismiss")
	}
	if editor.Text() != "/new " {
		t.Fatalf("tab accept = %q", editor.Text())
	}
}

func TestEditorAutocompleteArrowsNavigate(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("/")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("should show")
	}
	first := editor.AutocompleteItems()
	if len(first) < 2 {
		t.Fatalf("need multiple items")
	}
	editor.HandleInput("\x1b[B")
	editor.HandleInput("\x1b[A")
	editor.HandleInput("\x1b")
}

func TestEditorAutocompleteEnterSubmitsSlash(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	var submitted []string
	editor.SetOnSubmit(func(s string) { submitted = append(submitted, s) })
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("n")
	editor.HandleInput("e")
	editor.HandleInput("w")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("should show for /new")
	}
	editor.HandleInput("\r")
	if len(submitted) != 1 {
		t.Fatalf("enter with slash autocomplete should submit: %q", submitted)
	}
	if !strings.Contains(submitted[0], "new") {
		t.Fatalf("submitted = %q", submitted[0])
	}
}

func TestEditorAutocompletePathAt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("a"), 0o600); err != nil {
		t.Fatalf("write = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "alpaca.txt"), []byte("b"), 0o600); err != nil {
		t.Fatalf("write = %v", err)
	}
	editor := NewEditor(EditorOptions{WorkspaceRoot: root, TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("@")
	editor.HandleInput("a")
	editor.HandleInput("l")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("@ should trigger path completion")
	}
	items := editor.AutocompleteItems()
	if len(items) == 0 {
		t.Fatalf("path items empty")
	}
	editor.HandleInput("\t")
	if editor.IsShowingAutocomplete() {
		t.Fatalf("tab should accept path")
	}
	if !strings.Contains(editor.Text(), "@") {
		t.Fatalf("path accept = %q", editor.Text())
	}
}

func TestEditorAutocompleteForceTab(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write = %v", err)
	}
	editor := NewEditor(EditorOptions{WorkspaceRoot: root, TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("\t")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("force tab should attempt completion")
	}
	editor.HandleInput("\x1b")
}

func TestEditorAutocompleteUpdateAndDismiss(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("n")
	editor.HandleInput("e")
	editor.HandleInput("w")
	editor.HandleInput(" ")
	if editor.IsShowingAutocomplete() {
		t.Fatalf("space after command with no arg completion should dismiss")
	}
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("z")
	editor.HandleInput("z")
	editor.HandleInput("z")
	if editor.IsShowingAutocomplete() {
		t.Fatalf("no match should dismiss")
	}
}

func TestEditorAutocompleteWithCatalog(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetCommandCatalog([]AutocompleteItem{{Value: "deploy", Label: "deploy", Description: "custom deploy"}})
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("d")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("catalog should trigger")
	}
	found := false
	for _, item := range editor.AutocompleteItems() {
		if item.Value == "deploy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("catalog item missing: %v", editor.AutocompleteItems())
	}
}

func TestEditorAutocompleteCursorMoveUpdates(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("")
	editor.HandleInput("/")
	editor.HandleInput("n")
	editor.HandleInput("\x1b[D")
	if !editor.IsShowingAutocomplete() && editor.Text() == "" {
		t.Fatalf("cursor move should update, not crash")
	}
	editor.HandleInput("\x1b[C")
	editor.HandleInput("\x7f")
}
