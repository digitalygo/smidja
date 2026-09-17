package tui

import (
	"strings"
	"testing"
)

func TestInputSetPlaceholderStyleUpdatesRender(t *testing.T) {
	input := NewInput(InputOptions{Prompt: "> ", Placeholder: "hint"})
	input.SetPlaceholderStyle(func(text string) string { return text + "@" })
	rendered := strings.Join(input.Render(40), "")
	if !strings.Contains(rendered, "h@") || !strings.Contains(rendered, "int@") {
		t.Fatalf("styled placeholder missing:\n%q", rendered)
	}
	styleCalls := 0
	input.SetPlaceholderStyle(func(text string) string { styleCalls++; return text })
	input.Render(40)
	if styleCalls == 0 {
		t.Fatal("placeholder style closure was not used")
	}
	input.SetPlaceholderStyle(func(text string) string { return text + "@" })
	rendered = strings.Join(input.Render(40), "")
	if !strings.Contains(rendered, "h@") {
		t.Fatalf("restyled placeholder missing:\n%q", rendered)
	}

	input.SetPlaceholderStyle(nil)
	rendered = strings.Join(input.Render(40), "")
	if strings.Contains(rendered, "hint@") {
		t.Fatalf("nil style kept the old closure:\n%q", rendered)
	}

	input.SetValue("kept")
	input.SetPlaceholderStyle(func(text string) string { return "<" + text + ">" })
	if input.Value() != "kept" {
		t.Fatalf("placeholder style change lost the value: %q", input.Value())
	}
}

func TestSettingsListSetSubmenuThemeAppliesToOpenSubmenu(t *testing.T) {
	var submenu *SelectList
	items := []SettingItem{
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) Component {
			submenu = NewSelectList([]SelectItem{{Value: "vim"}, {Value: "emacs"}}, 5, DefaultSelectListTheme(nil, nil))
			return submenu
		}},
	}
	list := NewSettingsList(items, 5, DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	calls := 0
	applier := func(component Component) {
		calls++
		if typed, ok := component.(*SelectList); ok {
			typed.SetTheme(DefaultSelectListTheme(func(text string) string { return "[a]" + text }, nil))
		}
	}
	list.SetSubmenuTheme(applier)
	if calls != 0 {
		t.Fatalf("applier ran without an open submenu: %d", calls)
	}

	list.HandleInput("\r")
	if submenu == nil {
		t.Fatal("submenu did not open")
	}
	if calls != 1 {
		t.Fatalf("applier calls after open = %d, want 1", calls)
	}
	if rendered := strings.Join(submenu.Render(40), "\n"); !strings.Contains(rendered, "[a]") {
		t.Fatalf("submenu did not receive the theme:\n%s", rendered)
	}

	list.SetSubmenuTheme(applier)
	if calls != 2 {
		t.Fatalf("applier calls after retheme = %d, want 2", calls)
	}
}

func TestSettingsListSetSubmenuThemeAppliesToFutureSubmenu(t *testing.T) {
	items := []SettingItem{
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) Component {
			return NewSelectList([]SelectItem{{Value: "vim"}}, 5, DefaultSelectListTheme(nil, nil))
		}},
	}
	list := NewSettingsList(items, 5, DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	calls := 0
	list.SetSubmenuTheme(func(Component) { calls++ })
	list.HandleInput("\r")
	if calls != 1 {
		t.Fatalf("applier calls for future submenu = %d, want 1", calls)
	}
}

func TestSettingsListSetSubmenuThemeNilApplier(t *testing.T) {
	var submenu *SelectList
	items := []SettingItem{
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) Component {
			submenu = NewSelectList([]SelectItem{{Value: "vim"}}, 5, DefaultSelectListTheme(nil, nil))
			return submenu
		}},
	}
	list := NewSettingsList(items, 5, DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	list.SetSubmenuTheme(func(Component) {})
	list.HandleInput("\r")
	if submenu == nil {
		t.Fatal("submenu did not open")
	}
	list.SetSubmenuTheme(nil)
	if rendered := strings.Join(submenu.Render(40), "\n"); rendered == "" {
		t.Fatal("submenu render empty after nil applier")
	}
}
