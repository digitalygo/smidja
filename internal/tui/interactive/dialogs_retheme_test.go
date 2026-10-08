package interactive

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func loadTheme(t *testing.T, name string) *tui.Theme {
	t.Helper()
	registry := tui.NewThemeRegistry("", "", tui.ColorModeUnset)
	theme, err := registry.SetTheme(name)
	if err != nil {
		t.Fatalf("load theme %q: %v", name, err)
	}
	return theme
}

func themeFg(t *testing.T, theme *tui.Theme, token tui.ThemeColor) string {
	t.Helper()
	ansi, ok := theme.GetFgAnsi(token)
	if !ok {
		t.Fatalf("theme %q is missing fg token %q", theme.Name, token)
	}
	return ansi
}

func renderRaw(lines []string) string {
	return strings.Join(lines, "\n")
}

func requireAnsi(t *testing.T, rendered, ansi, label string) {
	t.Helper()
	if !strings.Contains(rendered, ansi) {
		t.Fatalf("%s: rendered output does not contain expected ANSI %q:\n%s", label, ansi, rendered)
	}
}

func requireNoAnsi(t *testing.T, rendered, ansi, label string) {
	t.Helper()
	if strings.Contains(rendered, ansi) {
		t.Fatalf("%s: rendered output still contains stale ANSI %q:\n%s", label, ansi, rendered)
	}
}

func TestSelectDialogRethemeUpdatesNestedRows(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	items := []tui.SelectItem{
		{Value: "alpha", Label: "alpha"},
		{Value: "beta", Label: "beta"},
		{Value: "gamma", Label: "gamma"},
	}
	var selected string
	dialog := NewSelectDialog(SelectDialogOptions{
		Title:       "Pick",
		Items:       items,
		Searchable:  true,
		Placeholder: "filter",
	}, NewDialogTheme(dark), func(value string) { selected = value }, func() {})

	dialog.HandleInput("a")
	dialog.HandleInput("\x1b[B")
	filterText := dialog.search.Value()
	selectedItem, ok := dialog.list.SelectedItem()
	if !ok {
		t.Fatal("no selected item after filtering")
	}

	before := renderRaw(dialog.Render(50))
	requireAnsi(t, before, themeFg(t, dark, "accent"), "dark selected row")
	requireAnsi(t, before, themeFg(t, dark, "border"), "dark frame border")

	dialog.SetTheme(NewDialogTheme(light))
	after := renderRaw(dialog.Render(50))
	requireAnsi(t, after, themeFg(t, light, "accent"), "light selected row")
	requireNoAnsi(t, after, themeFg(t, dark, "accent"), "light selected row")
	requireAnsi(t, after, themeFg(t, light, "border"), "light frame border")

	if dialog.search.Value() != filterText {
		t.Fatalf("search text lost after retheme: %q", dialog.search.Value())
	}
	currentItem, ok := dialog.list.SelectedItem()
	if !ok || currentItem.Value != selectedItem.Value {
		t.Fatalf("selected item changed after retheme: %v want %v", currentItem, selectedItem)
	}

	dialog.HandleInput("\r")
	if selected != selectedItem.Value {
		t.Fatalf("selection callback after retheme = %q, want %q", selected, selectedItem.Value)
	}
}

func TestInputDialogRethemeUpdatesPlaceholderAndPreservesValueCursor(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	var submitted string
	dialog := NewInputDialog("Name", "type here", NewDialogTheme(dark),
		func(value string) { submitted = value }, func() {})

	before := renderRaw(dialog.Render(40))
	requireAnsi(t, before, themeFg(t, dark, "dim"), "dark placeholder")

	dialog.SetTheme(NewDialogTheme(light))
	after := renderRaw(dialog.Render(40))
	requireAnsi(t, after, themeFg(t, light, "dim"), "light placeholder")
	requireNoAnsi(t, after, themeFg(t, dark, "dim"), "light placeholder")

	dialog.HandleInput("hello")
	dialog.HandleInput("\x1b[D")
	dialog.HandleInput("\x1b[D")
	value := dialog.input.Value()
	cursor := dialog.input.Cursor()

	dialog.SetTheme(NewDialogTheme(dark))
	rethemed := renderRaw(dialog.Render(40))
	requireAnsi(t, rethemed, themeFg(t, dark, "border"), "dark frame border")
	if dialog.input.Value() != value {
		t.Fatalf("input value lost after retheme: %q", dialog.input.Value())
	}
	if dialog.input.Cursor() != cursor {
		t.Fatalf("input cursor moved after retheme: %d want %d", dialog.input.Cursor(), cursor)
	}

	dialog.HandleInput("\r")
	if submitted != value {
		t.Fatalf("submit after retheme = %q, want %q", submitted, value)
	}
}

func TestMaskedInputRethemePreservesSecretCursorFocus(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	var submitted string
	dialog := NewMaskedInput("Token", NewDialogTheme(dark),
		func(value string) { submitted = value }, func() {})
	dialog.SetFocused(true)
	dialog.HandleInput("secret")
	dialog.HandleInput("\x1b[D")
	secret := dialog.Value()
	cursor := dialog.cursor

	before := renderRaw(dialog.Render(40))
	requireAnsi(t, before, themeFg(t, dark, "border"), "dark frame border")

	dialog.SetTheme(NewDialogTheme(light))
	after := renderRaw(dialog.Render(40))
	requireAnsi(t, after, themeFg(t, light, "border"), "light frame border")
	requireNoAnsi(t, after, themeFg(t, dark, "border"), "light frame border")

	if dialog.Value() != secret {
		t.Fatalf("secret lost after retheme: %q", dialog.Value())
	}
	if dialog.cursor != cursor {
		t.Fatalf("secret cursor moved after retheme: %d want %d", dialog.cursor, cursor)
	}
	if !dialog.focused {
		t.Fatal("focus lost after retheme")
	}

	dialog.HandleInput("\r")
	if submitted != secret {
		t.Fatalf("secret submit after retheme = %q, want %q", submitted, secret)
	}
}

func TestEditorDialogRethemePreservesContentCursorAndSubmit(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	var submitted string
	dialog := NewEditorDialog("Edit", "line one", NewDialogTheme(dark),
		func(value string) { submitted = value }, func() {})
	dialog.HandleInput("\x1b[D")
	text := dialog.editor.Text()
	row, column := dialog.editor.Cursor()

	before := renderRaw(dialog.Render(50))
	requireAnsi(t, before, themeFg(t, dark, "thinkingOff"), "dark editor border")

	dialog.SetTheme(NewDialogTheme(light))
	after := renderRaw(dialog.Render(50))
	requireAnsi(t, after, themeFg(t, light, "thinkingOff"), "light editor border")
	requireNoAnsi(t, after, themeFg(t, dark, "thinkingOff"), "light editor border")

	if dialog.editor.Text() != text {
		t.Fatalf("editor text lost after retheme: %q", dialog.editor.Text())
	}
	nextRow, nextColumn := dialog.editor.Cursor()
	if nextRow != row || nextColumn != column {
		t.Fatalf("editor cursor moved after retheme: (%d,%d) want (%d,%d)", nextRow, nextColumn, row, column)
	}
	if rendered := renderRaw(dialog.Render(50)); !strings.Contains(rendered, tui.CursorMarker) {
		t.Fatal("editor focus marker lost after retheme")
	}

	dialog.HandleInput("\r")
	if submitted != text {
		t.Fatalf("editor submit after retheme = %q, want %q", submitted, text)
	}
}

func TestSettingsDialogRethemeUpdatesSubmenuAndPreservesDraft(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	var submenu *tui.SelectList
	var applied map[string]string
	items := []tui.SettingItem{
		{ID: "retry", Label: "Retry", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) tui.Component {
			submenu = tui.NewSelectList([]tui.SelectItem{{Value: "vim"}, {Value: "emacs"}}, 5, tui.DefaultSelectListTheme(nil, nil))
			submenu.SetOnSelect(func(item tui.SelectItem) { done(item.Value, "") })
			return submenu
		}},
	}
	dialog := NewSettingsDialog("Settings", items, NewDialogTheme(dark),
		func(values map[string]string) { applied = values }, func() {})

	dialog.HandleInput("\r")
	if dialog.draft["retry"] != "off" {
		t.Fatalf("draft retry = %q, want off", dialog.draft["retry"])
	}
	dialog.HandleInput("\x1b[B")
	dialog.HandleInput("\r")
	if submenu == nil {
		t.Fatal("submenu did not open")
	}
	submenu.HandleInput("\x1b[B")

	before := renderRaw(dialog.Render(50))
	requireNoAnsi(t, before, themeFg(t, light, "accent"), "identity submenu")

	dialog.SetTheme(NewDialogTheme(light))
	after := renderRaw(dialog.Render(50))
	requireAnsi(t, after, themeFg(t, light, "accent"), "light submenu row")
	requireAnsi(t, after, themeFg(t, light, "border"), "light frame border")

	if dialog.draft["retry"] != "off" {
		t.Fatalf("draft retry lost after retheme: %q", dialog.draft["retry"])
	}
	currentItem, ok := submenu.SelectedItem()
	if !ok || currentItem.Value != "emacs" {
		t.Fatalf("submenu selected item changed after retheme: %v", currentItem)
	}

	submenu.HandleInput("\r")
	if dialog.draft["editor"] != "emacs" {
		t.Fatalf("submenu did not update draft after retheme: %q", dialog.draft["editor"])
	}
	dialog.HandleInput("\x13")
	if applied == nil || applied["retry"] != "off" || applied["editor"] != "emacs" {
		t.Fatalf("applied draft after retheme = %v", applied)
	}
}

func TestConfirmAndTextDialogRethemePreservesCallbacks(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")

	confirmed := 0
	confirm := NewConfirmDialog("Title", "Message", NewDialogTheme(dark), func(bool) { confirmed++ })
	before := renderRaw(confirm.Render(40))
	requireAnsi(t, before, themeFg(t, dark, "border"), "dark confirm border")
	confirm.SetTheme(NewDialogTheme(light))
	after := renderRaw(confirm.Render(40))
	requireAnsi(t, after, themeFg(t, light, "border"), "light confirm border")
	requireNoAnsi(t, after, themeFg(t, dark, "border"), "light confirm border")
	confirm.HandleInput("y")
	if confirmed != 1 {
		t.Fatalf("confirm callback count = %d, want 1", confirmed)
	}

	closed := 0
	text := NewTextDialog("Info", []string{"line one"}, "press enter", NewDialogTheme(dark), func() { closed++ })
	before = renderRaw(text.Render(40))
	requireAnsi(t, before, themeFg(t, dark, "border"), "dark text border")
	text.SetTheme(NewDialogTheme(light))
	after = renderRaw(text.Render(40))
	requireAnsi(t, after, themeFg(t, light, "border"), "light text border")
	text.HandleInput("\r")
	if closed != 1 {
		t.Fatalf("text dialog callback count = %d, want 1", closed)
	}
}

func TestSettingsDialogValueRowRetheme(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	items := []tui.SettingItem{
		{ID: "retry", Label: "Retry", CurrentValue: "on", Values: []string{"on", "off"}, Description: "Retry with backoff"},
	}
	dialog := NewSettingsDialog("Settings", items, NewDialogTheme(dark), func(map[string]string) {}, func() {})

	before := renderRaw(dialog.Render(60))
	requireAnsi(t, before, themeFg(t, dark, "accent"), "dark value row")
	requireAnsi(t, before, themeFg(t, dark, "dim"), "dark description")

	dialog.SetTheme(NewDialogTheme(light))
	after := renderRaw(dialog.Render(60))
	requireAnsi(t, after, themeFg(t, light, "accent"), "light value row")
	requireAnsi(t, after, themeFg(t, light, "dim"), "light description")
	requireNoAnsi(t, after, themeFg(t, dark, "accent"), "light value row")

	if rendered := frameText(dialog.Render(60)); !strings.Contains(rendered, "Retry") {
		t.Fatalf("settings items lost after retheme:\n%s", rendered)
	}
}

func TestConcurrentDialogRethemeRenderAndInput(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	items := make([]tui.SelectItem, 8)
	for index, value := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"} {
		items[index] = tui.SelectItem{Value: value, Label: value}
	}
	var settles atomic.Int64
	var dialog *SelectDialog
	dialog = NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: items, Searchable: true}, NewDialogTheme(dark),
		func(string) {
			settles.Add(1)
			_ = dialog.Render(50)
		},
		func() {
			settles.Add(1)
			_ = dialog.Render(50)
		})
	dialog.SetTheme(NewDialogTheme(light))

	const iterations = 200
	var wg sync.WaitGroup
	start := make(chan struct{})
	launch := func(run func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run()
		}()
	}
	launch(func() {
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				dialog.SetTheme(NewDialogTheme(dark))
			} else {
				dialog.SetTheme(NewDialogTheme(light))
			}
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			_ = dialog.Render(50)
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("a")
			dialog.HandleInput("\x7f")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("\x1b[B")
			dialog.HandleInput("\x1b[A")
		}
	})
	close(start)
	wg.Wait()

	dialog.HandleInput("\r")
	if settles.Load() == 0 {
		t.Fatal("dialog never settled during concurrent retheme")
	}
	if lines := dialog.Render(50); len(lines) == 0 {
		t.Fatal("dialog rendered empty after concurrent retheme")
	}
}

func TestConcurrentSettingsDialogRethemeSubmenuRenderAndInput(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	items := []tui.SettingItem{
		{ID: "retry", Label: "Retry", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) tui.Component {
			list := tui.NewSelectList([]tui.SelectItem{{Value: "vim"}, {Value: "emacs"}}, 5, tui.DefaultSelectListTheme(nil, nil))
			list.SetOnSelect(func(item tui.SelectItem) { done(item.Value, "") })
			return list
		}},
	}
	var dialog *SettingsDialog
	dialog = NewSettingsDialog("Settings", items, NewDialogTheme(dark),
		func(map[string]string) { _ = dialog.Render(60) },
		func() { _ = dialog.Render(60) })
	dialog.HandleInput("\x1b[B")
	dialog.HandleInput("\r")

	const iterations = 200
	var wg sync.WaitGroup
	start := make(chan struct{})
	launch := func(run func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run()
		}()
	}
	launch(func() {
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				dialog.SetTheme(NewDialogTheme(dark))
			} else {
				dialog.SetTheme(NewDialogTheme(light))
			}
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			_ = dialog.Render(60)
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("\x1b[B")
			dialog.HandleInput("\x1b[A")
		}
	})
	close(start)
	wg.Wait()

	dialog.HandleInput("\x13")
	dialog.HandleInput("\x1b")
	if lines := dialog.Render(60); len(lines) == 0 {
		t.Fatal("settings dialog rendered empty after concurrent retheme")
	}
}

func TestSettingsDialogSetThemeWithoutList(t *testing.T) {
	dialog := NewSettingsDialog("Settings", nil, NewDialogTheme(loadTheme(t, "dark")), func(map[string]string) {}, func() {})
	dialog.list = nil
	dialog.SetTheme(NewDialogTheme(loadTheme(t, "light")))
	if dialog.theme.Base == nil {
		t.Fatal("dialog theme was not stored")
	}
}

func TestApplyDialogThemeRecursesContainers(t *testing.T) {
	light := loadTheme(t, "light")
	container := &tui.Container{}
	list := tui.NewSelectList([]tui.SelectItem{{Value: "alpha"}}, 5, tui.DefaultSelectListTheme(nil, nil))
	container.AddChild(list)

	before := renderRaw(list.Render(40))
	requireNoAnsi(t, before, themeFg(t, light, "accent"), "identity list")

	applyDialogTheme(container, NewDialogTheme(light))
	after := renderRaw(list.Render(40))
	requireAnsi(t, after, themeFg(t, light, "accent"), "recursed container list")
}

func TestApplyDialogThemeRecursesEveryNestedType(t *testing.T) {
	dark := loadTheme(t, "dark")
	light := loadTheme(t, "light")
	container := &tui.Container{}
	input := tui.NewInput(tui.InputOptions{Prompt: "> ", Placeholder: "hint"})
	editor := tui.NewEditor(tui.EditorOptions{Theme: dark, TerminalRows: 5})
	settings := tui.NewSettingsList([]tui.SettingItem{
		{ID: "retry", Label: "Retry", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) tui.Component {
			return tui.NewSelectList([]tui.SelectItem{{Value: "vim"}, {Value: "emacs"}}, 5, tui.DefaultSelectListTheme(nil, nil))
		}},
	}, 5, tui.DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	settings.HandleInput("\x1b[B")
	settings.HandleInput("\r")
	submenu := tui.NewSelectList([]tui.SelectItem{{Value: "vim"}}, 5, tui.DefaultSelectListTheme(nil, nil))
	dialog := NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: []tui.SelectItem{{Value: "alpha"}}}, NewDialogTheme(dark), func(string) {}, func() {})
	container.AddChild(input)
	container.AddChild(editor)
	container.AddChild(settings)
	container.AddChild(submenu)
	container.AddChild(dialog)

	applyDialogTheme(container, NewDialogTheme(light))

	if rendered := renderRaw(input.Render(40)); !strings.Contains(rendered, themeFg(t, light, "dim")) {
		t.Fatalf("input placeholder was not rethemed:\n%s", rendered)
	}
	if rendered := renderRaw(editor.Render(40)); !strings.Contains(rendered, themeFg(t, light, "thinkingOff")) {
		t.Fatalf("editor border was not rethemed:\n%s", rendered)
	}
	if rendered := renderRaw(settings.Render(60)); !strings.Contains(rendered, themeFg(t, light, "accent")) {
		t.Fatalf("settings submenu was not rethemed:\n%s", rendered)
	}
	if rendered := renderRaw(submenu.Render(40)); !strings.Contains(rendered, themeFg(t, light, "accent")) {
		t.Fatalf("nested select list was not rethemed:\n%s", rendered)
	}
	if rendered := renderRaw(dialog.Render(50)); !strings.Contains(rendered, themeFg(t, light, "accent")) {
		t.Fatalf("nested dialog was not rethemed:\n%s", rendered)
	}
}
