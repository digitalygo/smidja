package tui

import (
	"strings"
	"testing"
	"time"
)

func TestEditorExternalAutocompleteMergesWithBuiltins(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.SetText("")
	unsubscribe := editor.AddAutocompleteProvider(func(ctx ExternalAutocompleteContext) []AutocompleteItem {
		if ctx.Kind != "slash" {
			t.Errorf("provider kind = %q, want slash", ctx.Kind)
		}
		return []AutocompleteItem{{Value: "extra", Label: "extra command"}}
	})
	defer unsubscribe()
	editor.HandleInput("/")
	if !editor.IsShowingAutocomplete() {
		t.Fatal("autocomplete should show merged suggestions")
	}
	items := editor.AutocompleteItems()
	if len(items) == 0 || items[0].Value != "extra" {
		t.Fatalf("merged items = %+v, want the external suggestion first", items)
	}
	found := false
	for _, item := range items {
		if item.Value == "new" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged items missing the built-in /new: %+v", items)
	}
}

func TestEditorExternalAutocompleteReplacementKeepsPosition(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	unsubscribe := editor.AddAutocompleteProvider(func(ctx ExternalAutocompleteContext) []AutocompleteItem {
		return []AutocompleteItem{{Value: "same", Label: "same"}}
	})
	defer unsubscribe()
	editor.HandleInput("x")
	first := editor.AutocompleteItems()
	if len(first) != 1 || first[0].Value != "same" {
		t.Fatalf("items = %+v", first)
	}
	editor.HandleInput("\t")
	if editor.IsShowingAutocomplete() {
		t.Fatal("tab should accept the external completion")
	}
	if editor.Text() != "same" {
		t.Fatalf("text after external insertion = %q, want same", editor.Text())
	}
}

func TestEditorExternalAutocompleteStaleResultDropped(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	var stop func()
	editorDone := make(chan struct{})
	editor.SetText("")
	stop = editor.AddAutocompleteProvider(func(ctx ExternalAutocompleteContext) []AutocompleteItem {
		if ctx.Text != "x" {
			t.Errorf("provider text = %q, want x", ctx.Text)
		}
		editor.SetText("changed")
		if editor.Text() != "changed" {
			t.Errorf("reentrant read = %q", editor.Text())
		}
		return []AutocompleteItem{{Value: "stale", Label: "stale"}}
	})
	go func() {
		editor.HandleInput("x")
		close(editorDone)
	}()
	select {
	case <-editorDone:
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant provider deadlocked the editor")
	}
	if editor.IsShowingAutocomplete() {
		t.Fatalf("stale suggestions applied after text change: %+v", editor.AutocompleteItems())
	}
	stop()
	editor.HandleInput("y")
	if editor.IsShowingAutocomplete() {
		t.Fatal("unsubscribed provider still produced suggestions")
	}
}

func TestEditorExternalAutocompleteSelfUnsubscribe(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	var stop func()
	calls := 0
	stop = editor.AddAutocompleteProvider(func(ctx ExternalAutocompleteContext) []AutocompleteItem {
		calls++
		stop()
		return []AutocompleteItem{{Value: "once", Label: "once"}}
	})
	editor.HandleInput("x")
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	if !editor.IsShowingAutocomplete() {
		t.Fatal("first trigger should still show the suggestion")
	}
	editor.HandleInput("\x1b")
	editor.SetText("")
	editor.HandleInput("y")
	if calls != 1 {
		t.Fatalf("provider calls after self unsubscribe = %d, want 1", calls)
	}
	if editor.IsShowingAutocomplete() {
		t.Fatal("provider resurfaced after unsubscribe")
	}
}

func TestEditorExternalAutocompleteSanitizesItems(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	unsubscribe := editor.AddAutocompleteProvider(func(ctx ExternalAutocompleteContext) []AutocompleteItem {
		return []AutocompleteItem{
			{Value: "", Label: "empty"},
			{Value: "sa\x1bfe", Label: "la\x07bel", Description: "de\x1b[31msc"},
		}
	})
	defer unsubscribe()
	editor.HandleInput("x")
	items := editor.AutocompleteItems()
	if len(items) != 1 {
		t.Fatalf("items = %+v, want only the sanitized suggestion", items)
	}
	if items[0].Value != "safe" || strings.ContainsRune(items[0].Label, 0x07) || strings.ContainsRune(items[0].Description, 0x1b) {
		t.Fatalf("sanitized item = %+v", items[0])
	}
	editor.HandleInput("\t")
	if editor.Text() != "safe" {
		t.Fatalf("text after sanitized insertion = %q", editor.Text())
	}
}

func TestEditorExternalAutocompleteTokenBounds(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	unsubscribe := editor.AddAutocompleteProvider(func(ctx ExternalAutocompleteContext) []AutocompleteItem {
		return []AutocompleteItem{{Value: "value", Label: "value"}}
	})
	defer unsubscribe()
	editor.SetText("one two")
	editor.mu.Lock()
	editor.buffer.cursorCol = 3
	editor.mu.Unlock()
	editor.HandleInput("\t")
	editor.HandleInput("x")
	editor.HandleInput("\t")
	if editor.Text() != "value two" {
		t.Fatalf("external insertion escaped the token bounds: %q", editor.Text())
	}
}

func TestEditorPasteTargetsBuffer(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	editor.Paste("hello\nworld")
	if text := editor.Text(); !strings.Contains(text, "hello") || !strings.Contains(text, "world") {
		t.Fatalf("pasted text = %q", text)
	}
	editor.Paste("<script>\x1b[31m")
	if strings.Contains(editor.Text(), "\x1b") {
		t.Fatalf("paste leaked terminal control: %q", editor.Text())
	}
}

func TestEditorAutocompleteNoDeadlockWithLockedCallers(t *testing.T) {
	editor := NewEditor(EditorOptions{TerminalRows: 24})
	unsubscribe := editor.AddAutocompleteProvider(func(ctx ExternalAutocompleteContext) []AutocompleteItem {
		return []AutocompleteItem{{Value: "ok", Label: "ok"}}
	})
	defer unsubscribe()
	done := make(chan struct{})
	go func() {
		editor.mu.Lock()
		editor.triggerAutocompleteLocked(true)
		editor.mu.Unlock()
		editor.flushExternalAutocomplete()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deferred autocomplete deadlocked")
	}
	items := editor.AutocompleteItems()
	if len(items) == 0 || items[0].Value != "ok" {
		t.Fatalf("items = %+v, want the external suggestion first", items)
	}
}
