package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hasFrameControl(s string) bool {
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if r <= 0x1F || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			return true
		}
	}
	return false
}

func TestPathSuggestionsOmitsControlNames(t *testing.T) {
	provider := NewAutocompleteProvider(t.TempDir())
	provider.SetLister(func(dir string) ([]autocompleteFileEntry, error) {
		return []autocompleteFileEntry{
			{name: "ok.txt"},
			{name: "evilesc\x1b[2J"},
			{name: "evilbel\x07x"},
			{name: "evildel\x7fx"},
			{name: "evilc1a\u0085x"},
			{name: "evilc1b\u009fx"},
			{name: "evilnul\x00x"},
			{name: "safe-subdir", isDir: true},
		}, nil
	})
	items := provider.PathSuggestions("@")
	if len(items) == 0 {
		t.Fatalf("expected safe entries, got none")
	}
	for _, item := range items {
		if hasFrameControl(item.Label) || hasFrameControl(item.Value) || hasFrameControl(item.Description) {
			t.Fatalf("unsafe field leaked: %+v", item)
		}
		if strings.Contains(item.Label, "evil") || strings.Contains(item.Value, "evil") || strings.Contains(item.Description, "evil") {
			t.Fatalf("unsafe entry suggested: %+v", item)
		}
		if hint := InlineHint("@", item); hasFrameControl(hint) {
			t.Fatalf("unsafe hint: %q from %+v", hint, item)
		}
	}
	found := false
	for _, item := range items {
		if item.Label == "ok.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("valid entry missing: %v", items)
	}
}

func TestPathSuggestionsOmitsRealFileWithESC(t *testing.T) {
	root := t.TempDir()
	evil := "evil\x1b[2J.txt"
	if err := os.WriteFile(filepath.Join(root, evil), []byte("x"), 0o600); err != nil {
		t.Fatalf("platform does not support ESC filename: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("y"), 0o600); err != nil {
		t.Fatalf("write ok = %v", err)
	}
	provider := NewAutocompleteProvider(root)
	items := provider.PathSuggestions("@")
	for _, item := range items {
		if strings.Contains(item.Value, "\x1b") || strings.Contains(item.Label, "\x1b") || strings.Contains(item.Description, "\x1b") {
			t.Fatalf("real ESC entry leaked: %+v", item)
		}
	}
	found := false
	for _, item := range items {
		if item.Label == "ok.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("valid real entry missing: %v", items)
	}
}

func TestEditorAutocompleteFrameOmitsControls(t *testing.T) {
	provider := NewAutocompleteProvider(t.TempDir())
	provider.SetLister(func(dir string) ([]autocompleteFileEntry, error) {
		return []autocompleteFileEntry{
			{name: "ok.txt"},
			{name: "evilesc\x1b[2J"},
			{name: "evilbel\x07x"},
			{name: "evildel\x7fx"},
			{name: "evilc1\u0085"},
		}, nil
	})
	editor := NewEditor(EditorOptions{Provider: provider, TerminalRows: 24})
	editor.SetFocused(true)
	editor.SetText("")
	editor.HandleInput("@")
	if !editor.IsShowingAutocomplete() {
		t.Fatalf("autocomplete should show for safe entry")
	}
	for _, item := range editor.AutocompleteItems() {
		if strings.Contains(item.Value, "evil") || strings.Contains(item.Label, "evil") {
			t.Fatalf("editor item leaked: %+v", item)
		}
	}
	if hint := editor.InlineHint(); hasFrameControl(hint) {
		t.Fatalf("editor hint leaked: %q", hint)
	}
	lines := editor.Render(60)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "evil") {
		t.Fatalf("frame leaked marker: %q", joined)
	}
	stripped := StripTerminalSequences(joined)
	if hasFrameControl(stripped) {
		t.Fatalf("frame leaked controls: %q", stripped)
	}
}
