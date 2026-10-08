package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestKeybindingDefaults(t *testing.T) {
	manager := NewDefaultKeybindingsManager(nil)

	fixed := map[string][]string{
		"tui.editor.cursorUp":           {"up"},
		"tui.editor.cursorDown":         {"down"},
		"tui.editor.cursorLeft":         {"left", "ctrl+b"},
		"tui.editor.cursorRight":        {"right", "ctrl+f"},
		"tui.editor.cursorWordLeft":     {"alt+left", "ctrl+left", "alt+b"},
		"tui.editor.cursorWordRight":    {"alt+right", "ctrl+right", "alt+f"},
		"tui.editor.cursorLineStart":    {"home", "ctrl+home", "ctrl+a"},
		"tui.editor.cursorLineEnd":      {"end", "ctrl+end", "ctrl+e"},
		"tui.editor.jumpForward":        {"ctrl+]"},
		"tui.editor.jumpBackward":       {"ctrl+alt+]"},
		"tui.editor.pageUp":             {"pageUp", "ctrl+pageUp"},
		"tui.editor.pageDown":           {"pageDown", "ctrl+pageDown"},
		"tui.editor.deleteCharBackward": {"backspace"},
		"tui.editor.deleteCharForward":  {"delete", "ctrl+d"},
		"tui.editor.deleteWordBackward": {"ctrl+w", "alt+backspace"},
		"tui.editor.deleteWordForward":  {"alt+d", "alt+delete"},
		"tui.editor.deleteToLineStart":  {"ctrl+u"},
		"tui.editor.deleteToLineEnd":    {"ctrl+k"},
		"tui.editor.yank":               {"ctrl+y"},
		"tui.editor.yankPop":            {"alt+y"},
		"tui.editor.undo":               {"ctrl+-"},
		"tui.input.newLine":             {"shift+enter", "ctrl+j"},
		"tui.input.submit":              {"enter"},
		"tui.input.tab":                 {"tab"},
		"tui.input.copy":                {"ctrl+c"},
		"tui.select.up":                 {"up"},
		"tui.select.down":               {"down"},
		"tui.select.pageUp":             {"pageUp"},
		"tui.select.pageDown":           {"pageDown"},
		"tui.select.confirm":            {"enter"},
		"tui.select.cancel":             {"escape", "ctrl+c"},
		"tui.altScreen.pageUp":          {"pageUp"},
		"tui.altScreen.pageDown":        {"pageDown"},
		"tui.altScreen.previousPrompt":  {"ctrl+shift+up", "ctrl+up"},
		"tui.altScreen.nextPrompt":      {"ctrl+shift+down", "ctrl+down"},
		"tui.altScreen.search":          {"ctrl+shift+f"},
		"tui.altScreen.searchNext":      {"enter", "ctrl+g"},
		"tui.altScreen.searchPrevious":  {"shift+enter", "ctrl+shift+g"},
		"tui.altScreen.searchClose":     {"escape"},
		"tui.altScreen.top":             {"home"},
		"tui.altScreen.bottom":          {"end"},
		"app.interrupt":                 {"escape"},
		"app.clear":                     {"ctrl+c"},
		"app.exit":                      {"ctrl+d"},
		"app.suspend":                   {"ctrl+z"},
		"app.thinking.cycle":            {"shift+tab"},
		"app.thinking.save":             {"ctrl+s"},
		"app.model.cycleForward":        {"ctrl+p"},
		"app.model.cycleBackward":       {"shift+ctrl+p"},
		"app.model.select":              {"ctrl+l"},
		"app.tools.expand":              {"ctrl+o"},
		"app.thinking.toggle":           {"ctrl+t"},
		"app.session.toggleNamedFilter": {"ctrl+n"},
		"app.editor.external":           {"ctrl+g"},
		"app.message.copy":              {"ctrl+x"},
		"app.message.followUp":          {"alt+enter"},
		"app.message.dequeue":           {"alt+up"},
		"app.clipboard.pasteImage":      {"ctrl+v"},
		"app.tree.editLabel":            {"shift+l"},
		"app.tree.toggleLabelTimestamp": {"shift+t"},
		"app.session.togglePath":        {"ctrl+p"},
		"app.session.toggleSort":        {"ctrl+s"},
		"app.session.rename":            {"ctrl+r"},
		"app.session.delete":            {"ctrl+d"},
		"app.models.save":               {"ctrl+s"},
		"app.models.enableAll":          {"ctrl+a"},
		"app.models.clearAll":           {"ctrl+x"},
		"app.models.toggleProvider":     {"ctrl+p"},
		"app.models.reorderUp":          {"alt+up"},
		"app.models.reorderDown":        {"alt+down"},
		"app.tree.filter.default":       {"ctrl+d"},
		"app.tree.filter.noTools":       {"ctrl+t"},
		"app.tree.filter.userOnly":      {"ctrl+u"},
		"app.tree.filter.labeledOnly":   {"ctrl+l"},
		"app.tree.filter.all":           {"ctrl+a"},
		"app.tree.filter.cycleForward":  {"ctrl+o"},
		"app.tree.filter.cycleBackward": {"shift+ctrl+o"},
	}
	for action, want := range fixed {
		got := manager.Keys(action)
		if len(got) != len(want) {
			t.Fatalf("Keys(%s) = %q, want %q", action, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Keys(%s) = %q, want %q", action, got, want)
			}
		}
	}

	platformDependent := map[string][]string{
		"app.tree.foldOrUp":     {"ctrl+left", "alt+left"},
		"app.tree.unfoldOrDown": {"ctrl+right", "alt+right"},
	}
	for action, linuxWant := range platformDependent {
		got := manager.Keys(action)
		if runtime.GOOS == "darwin" {
			linuxWant = []string{linuxWant[1], linuxWant[0]}
		}
		if len(got) != len(linuxWant) {
			t.Fatalf("Keys(%s) = %q, want %q", action, got, linuxWant)
		}
		for i := range linuxWant {
			if got[i] != linuxWant[i] {
				t.Fatalf("Keys(%s) = %q, want %q", action, got, linuxWant)
			}
		}
	}

	if len(manager.Keys("tui.editor.historyPrevious")) != 0 {
		t.Fatal("historyPrevious should have no default keys")
	}
	if len(manager.Keys("app.session.new")) != 0 {
		t.Fatal("app.session.new should have no default keys")
	}
}

func TestKeybindingsJSONReplacement(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".smidja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keybindings.json")
	if err := os.WriteFile(path, []byte(`{
  "tui.input.submit": ["alt+s", "ctrl+enter"],
  "tui.select.up": "k"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadKeybindingsConfig(KeybindingsFilePath(home))
	if err != nil {
		t.Fatalf("LoadKeybindingsConfig: %v", err)
	}
	manager := NewDefaultKeybindingsManager(config)

	if keys := manager.Keys("tui.input.submit"); len(keys) != 2 || keys[0] != "alt+s" || keys[1] != "ctrl+enter" {
		t.Fatalf("submit keys = %q, want [alt+s ctrl+enter]", keys)
	}
	if keys := manager.Keys("tui.select.up"); len(keys) != 1 || keys[0] != "k" {
		t.Fatalf("select up keys = %q, want [k]", keys)
	}
	if keys := manager.Keys("tui.editor.cursorUp"); len(keys) != 1 || keys[0] != "up" {
		t.Fatalf("unchanged action keys = %q", keys)
	}

	if !manager.Matches("k", "tui.select.up") {
		t.Fatal("override key k should match select up")
	}
	if manager.Matches("k", "tui.select.down") {
		t.Fatal("k must not leak to other actions")
	}
	if len(manager.Conflicts()) != 0 {
		t.Fatalf("unexpected conflicts: %v", manager.Conflicts())
	}
}

func TestKeybindingsConflictDetection(t *testing.T) {
	config := KeybindingsConfig{
		"tui.input.submit": {"ctrl+x"},
		"app.clear":        {"ctrl+x"},
	}
	manager := NewDefaultKeybindingsManager(config)
	conflicts := manager.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, want one", conflicts)
	}
	if conflicts[0].Key != "ctrl+x" || len(conflicts[0].Actions) != 2 {
		t.Fatalf("conflict = %+v", conflicts[0])
	}
}

func TestKeybindingsLegacyActionNames(t *testing.T) {
	config, err := ParseKeybindingsConfig(`{"cursorWordLeft": "ctrl+arrow"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := config["tui.editor.cursorWordLeft"]; !ok {
		t.Fatalf("legacy name not migrated: %v", config)
	}
}

func TestKeybindingsMissingFile(t *testing.T) {
	config, err := LoadKeybindingsConfig(filepath.Join(t.TempDir(), "none", "keybindings.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(config) != 0 {
		t.Fatalf("config = %v, want empty", config)
	}
}

func TestKeybindingsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeybindingsConfig(path); err == nil {
		t.Fatal("invalid JSON should error")
	}
}

func TestKeybindingsManagerMatches(t *testing.T) {
	manager := NewDefaultKeybindingsManager(nil)
	if !manager.Matches("\x1b[A", "tui.editor.cursorUp") {
		t.Fatal("up arrow should match cursorUp")
	}
	if !manager.Matches("\x1b", "app.interrupt") {
		t.Fatal("escape should match interrupt")
	}
	if manager.Matches("\x1b[A", "tui.editor.cursorDown") {
		t.Fatal("up arrow must not match cursorDown")
	}
	definition, ok := manager.Definition("tui.editor.cursorUp")
	if !ok || definition.Description == "" {
		t.Fatalf("definition = %+v ok=%v", definition, ok)
	}
	resolved := manager.ResolvedBindings()
	if resolved["tui.input.submit"][0] != "enter" {
		t.Fatalf("resolved bindings submit = %v", resolved["tui.input.submit"])
	}

	manager.SetUserBindings(KeybindingsConfig{"tui.input.submit": {"ctrl+space"}})
	if !manager.Matches("\x00", "tui.input.submit") {
		t.Fatal("user binding replacement should apply")
	}
	if manager.UserBindings()["tui.input.submit"][0] != "ctrl+space" {
		t.Fatal("user bindings snapshot mismatch")
	}
}

func TestKeybindingsRejectsUnknownModifiers(t *testing.T) {
	if _, ok := parseKeyID("hyper+a"); ok {
		t.Fatal("unknown modifier should be rejected")
	}
	if _, ok := parseKeyID("ctrl+a"); !ok {
		t.Fatal("ctrl+a should parse")
	}
	if _, ok := parseKeyID(""); ok {
		t.Fatal("empty key should be rejected")
	}
}
