package tui

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func selectRenderedText(list *SelectList, width int) string {
	return StripTerminalSequences(strings.Join(list.Render(width), "\n"))
}

func settingsRenderedValue(list *SettingsList, label string) string {
	for _, line := range list.Render(80) {
		stripped := strings.TrimRight(StripTerminalSequences(line), " ")
		if !strings.Contains(stripped, label) {
			continue
		}
		fields := strings.Fields(stripped)
		if len(fields) == 0 {
			return ""
		}
		return fields[len(fields)-1]
	}
	return ""
}

func settingsConcurrencyItems() []SettingItem {
	return []SettingItem{
		{ID: "retry", Label: "Retry", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "verbose", Label: "Verbose", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "color", Label: "Color output", CurrentValue: "on", Values: []string{"on", "off"}},
	}
}

func TestSelectListFilterTransitionsAndSelection(t *testing.T) {
	items := []SelectItem{{Value: "alpha"}, {Value: "beta"}, {Value: "gamma"}}
	changes := 0
	list := NewSelectList(items, 5, DefaultSelectListTheme(nil, nil))
	list.SetOnSelectionChange(func(item SelectItem) {
		changes++
		if _, ok := list.SelectedItem(); !ok {
			t.Errorf("selection change reported without a selectable item")
		}
		_ = list.Render(30)
	})

	list.SetFilter("b")
	if item, ok := list.SelectedItem(); !ok || item.Value != "beta" {
		t.Fatalf("prefix filter selection = %v ok=%v", item, ok)
	}
	if rendered := selectRenderedText(list, 40); !strings.Contains(rendered, "beta") || strings.Contains(rendered, "gamma") {
		t.Fatalf("prefix filter render = %q", rendered)
	}

	list.SetSelectedIndex(9)
	if item, _ := list.SelectedItem(); item.Value != "beta" {
		t.Fatalf("clamped selection = %v", item)
	}

	list.SetFilter("")
	if item, _ := list.SelectedItem(); item.Value != "alpha" {
		t.Fatalf("cleared filter selection = %v", item)
	}
	if rendered := selectRenderedText(list, 40); !strings.Contains(rendered, "alpha") || !strings.Contains(rendered, "gamma") {
		t.Fatalf("cleared filter render = %q", rendered)
	}

	list.SetFilterFuzzy("gm")
	if item, ok := list.SelectedItem(); !ok || item.Value != "gamma" {
		t.Fatalf("fuzzy filter selection = %v ok=%v", item, ok)
	}

	list.SetFilterFuzzy("")
	list.SetSelectedIndex(2)
	list.HandleInput("\x1b[B")
	if item, _ := list.SelectedItem(); item.Value != "alpha" {
		t.Fatalf("down should wrap: %v", item)
	}
	if changes == 0 {
		t.Fatal("selection change callback never fired")
	}
}

func TestSelectListConcurrentInputFilterRender(t *testing.T) {
	items := make([]SelectItem, 8)
	for i := range items {
		items[i] = SelectItem{Value: "item" + itoa(i), Label: "item" + itoa(i)}
	}
	var callbacks atomic.Int64
	list := NewSelectList(items, 4, DefaultSelectListTheme(nil, nil))
	list.SetOnSelect(func(SelectItem) {
		callbacks.Add(1)
		_ = list.Render(40)
	})
	list.SetOnCancel(func() {
		callbacks.Add(1)
		_ = list.Render(40)
	})
	list.SetOnSelectionChange(func(SelectItem) {
		callbacks.Add(1)
		if _, ok := list.SelectedItem(); !ok {
			t.Errorf("selection change on empty list")
		}
	})

	const iterations = 100
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
			list.HandleInput("\x1b[B")
			list.HandleInput("\x1b[A")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			list.HandleInput("\r")
			list.HandleInput("\x1b")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			list.SetFilterFuzzy("item")
			list.SetFilter("")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			_ = list.Render(50)
			_, _ = list.SelectedItem()
		}
	})
	close(start)
	wg.Wait()

	if callbacks.Load() == 0 {
		t.Fatal("no callbacks fired during concurrent run")
	}
	if lines := list.Render(50); len(lines) == 0 {
		t.Fatal("list rendered empty after concurrent run")
	}
}

func TestSettingsListTwoTogglesRenderedValues(t *testing.T) {
	items := []SettingItem{{ID: "retry", Label: "Retry", CurrentValue: "on", Values: []string{"on", "off"}}}
	var applied []string
	var list *SettingsList
	list = NewSettingsList(items, 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, value string) {
			applied = append(applied, value)
			_ = list.Render(60)
		}, nil, true)

	if value := settingsRenderedValue(list, "Retry"); value != "on" {
		t.Fatalf("initial rendered value = %q, want on", value)
	}
	list.HandleInput("\r")
	if value := settingsRenderedValue(list, "Retry"); value != "off" {
		t.Fatalf("first toggle rendered value = %q, want off", value)
	}
	if len(applied) != 1 || applied[0] != "off" {
		t.Fatalf("first toggle callback = %q", applied)
	}
	list.HandleInput("\r")
	if value := settingsRenderedValue(list, "Retry"); value != "on" {
		t.Fatalf("second toggle rendered value = %q, want on", value)
	}
	if len(applied) != 2 || applied[1] != "on" {
		t.Fatalf("second toggle callback = %q", applied)
	}

	display := list.displayItems()
	if len(display) != 1 || display[0].CurrentValue != "on" {
		t.Fatalf("filtered display drifted from draft: %+v", display)
	}
	if list.items[0].CurrentValue != "on" {
		t.Fatalf("canonical item drifted: %+v", list.items[0])
	}
}

func TestSettingsListFilterSelectionPersists(t *testing.T) {
	var changes []string
	list := NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, value string) { changes = append(changes, id+"="+value) }, nil, true)

	list.HandleInput("mod")
	if display := list.displayItems(); len(display) != 1 || display[0].ID != "model" {
		t.Fatalf("search filter = %+v", display)
	}
	list.HandleInput("\r")
	if display := list.displayItems(); len(display) != 1 || display[0].CurrentValue != "smart" {
		t.Fatalf("filtered value after toggle = %+v", display)
	}
	if list.selectedIndex != 0 {
		t.Fatalf("selection moved under filter: %d", list.selectedIndex)
	}
	if len(changes) != 1 || changes[0] != "model=smart" {
		t.Fatalf("toggle changes = %q", changes)
	}
	rendered := StripTerminalSequences(strings.Join(list.Render(80), "\n"))
	if !strings.Contains(rendered, "smart") {
		t.Fatalf("filtered render missing new value: %q", rendered)
	}

	for i := 0; i < 3; i++ {
		list.HandleInput("\x7f")
	}
	display := list.displayItems()
	if len(display) != 3 {
		t.Fatalf("clearing filter did not restore items: %d", len(display))
	}
	model := SettingItem{}
	for _, item := range display {
		if item.ID == "model" {
			model = item
		}
	}
	if model.CurrentValue != "smart" {
		t.Fatalf("canonical item lost the draft: %+v", model)
	}
}

func TestSettingsListSubmenuCallbackOutsideLock(t *testing.T) {
	var changes []string
	var list *SettingsList
	list = NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, value string) {
			changes = append(changes, id+"="+value)
			_ = list.Render(60)
		}, nil, false)
	list.SelectItem("editor")
	list.HandleInput("\r")
	if list.submenuComponent == nil {
		t.Fatal("submenu should open")
	}
	list.HandleInput("\x1b[B")
	list.HandleInput("\r")
	if list.submenuComponent != nil {
		t.Fatal("submenu should close")
	}
	if len(changes) != 1 || changes[0] != "editor=emacs" {
		t.Fatalf("submenu callback = %q", changes)
	}
	if display := list.displayItems(); display[list.selectedIndex].ID != "editor" {
		t.Fatalf("selection did not return to submenu item: %+v", display)
	}
}

func TestSettingsListConcurrentInputFilterUpdateRender(t *testing.T) {
	var list *SettingsList
	var changes atomic.Int64
	list = NewSettingsList(settingsConcurrencyItems(), 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, value string) {
			changes.Add(1)
			_ = list.Render(60)
		},
		func() {
			_ = list.Render(60)
		}, true)

	const iterations = 100
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
			list.HandleInput("\r")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			list.HandleInput("\x1b[B")
			list.HandleInput("\x1b[A")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			list.HandleInput("re")
			list.HandleInput("\x7f")
			list.HandleInput("\x7f")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			list.UpdateValue("retry", "off")
			list.UpdateValue("color", "on")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			list.SetTheme(DefaultSettingsListTheme(nil, nil, nil))
			_ = list.Render(50)
		}
	})
	close(start)
	wg.Wait()

	if changes.Load() == 0 {
		t.Fatal("no change callbacks fired during concurrent run")
	}
	if lines := list.Render(50); len(lines) == 0 {
		t.Fatal("settings list rendered empty after concurrent run")
	}
}

type submenuStub struct {
	done func(selectedValue string, navigateTo string)
}

func (s *submenuStub) Render(int) []string { return nil }
func (s *submenuStub) Invalidate()         {}

func (s *submenuStub) finish(selectedValue, navigateTo string) {
	s.done(selectedValue, navigateTo)
}

func TestSelectListThemeInvalidateAndEmptyMouse(t *testing.T) {
	list := NewSelectList([]SelectItem{{Value: "only"}}, 0, DefaultSelectListTheme(nil, nil))
	list.SetTheme(SelectListTheme{})
	list.Invalidate()
	if lines := list.Render(20); len(lines) != 1 {
		t.Fatalf("default maxVisible render = %d lines", len(lines))
	}
	list.SetFilter("missing")
	if _, ok := list.SelectedItem(); ok {
		t.Fatal("filtered-out list still reports a selection")
	}
	if result := list.HandleMouse(MouseEvent{Type: MouseWheel, WheelDelta: 1, Y: 0}); result != nil {
		t.Fatalf("empty list wheel = %v", result)
	}
	if sanitizeSelectSingleLine("") != "" {
		t.Fatal("empty sanitize changed the value")
	}
	if got := sanitizeSelectSingleLine("a\x00b\x7f\u009fc"); got != "abc" {
		t.Fatalf("control sanitize = %q", got)
	}
	_ = computeSelectPrimaryColumnWidth([]SelectItem{{Value: "a"}}, SelectListLayout{MaxPrimaryColumnWidth: 10})
	_ = computeSelectPrimaryColumnWidth([]SelectItem{{Value: "a"}}, SelectListLayout{MinPrimaryColumnWidth: 10})
}

func TestSettingsListEmptyStatesAndCancel(t *testing.T) {
	cancelled := 0
	var list *SettingsList
	list = NewSettingsList(nil, 10, DefaultSettingsListTheme(nil, nil, nil), nil, func() {
		cancelled++
		_ = list.Render(40)
	}, true)
	if lines := list.Render(40); len(lines) == 0 {
		t.Fatal("empty settings render produced no lines")
	}
	list.HandleInput("\x1b")
	if cancelled != 1 {
		t.Fatalf("cancel count = %d", cancelled)
	}

	list = NewSettingsList(settingsConcurrencyItems(), 10, DefaultSettingsListTheme(nil, nil, nil), nil, nil, true)
	list.HandleInput("zzzzz")
	if display := list.displayItems(); len(display) != 0 {
		t.Fatalf("no-match filter = %+v", display)
	}
	list.HandleInput("\x1b[A")
	list.HandleInput("\x1b[B")
	list.HandleInput("\r")
	if lines := list.Render(40); len(lines) == 0 {
		t.Fatal("no-match settings render produced no lines")
	}
}

func TestSettingsListActivateWithoutSubmenuOrValues(t *testing.T) {
	changes := 0
	list := NewSettingsList([]SettingItem{{ID: "plain", Label: "Plain", CurrentValue: "value"}}, 10,
		DefaultSettingsListTheme(nil, nil, nil), func(string, string) { changes++ }, nil, false)
	list.HandleInput("\r")
	if changes != 0 {
		t.Fatalf("plain item fired a change: %d", changes)
	}
}

func TestSettingsListSubmenuNavigateTo(t *testing.T) {
	var stub *submenuStub
	var changes []string
	items := []SettingItem{
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) Component {
			stub = &submenuStub{done: done}
			return stub
		}},
		{ID: "theme", Label: "Theme", CurrentValue: "dark", Values: []string{"dark", "light"}},
	}
	list := NewSettingsList(items, 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, value string) { changes = append(changes, id+"="+value) }, nil, false)
	list.Invalidate()
	list.HandleInput("\r")
	if stub == nil || list.submenuComponent == nil {
		t.Fatal("submenu did not open")
	}
	list.Invalidate()
	stub.finish("", "theme")
	if list.submenuComponent != nil {
		t.Fatal("submenu did not close after navigation")
	}
	if list.selectedIndex != 1 {
		t.Fatalf("navigation selected index = %d", list.selectedIndex)
	}
	if len(changes) != 1 || changes[0] != "theme=light" {
		t.Fatalf("navigated activation = %q", changes)
	}
}

func TestSettingsListSubmenuMouseDelegation(t *testing.T) {
	list := NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	list.SelectItem("editor")
	list.HandleInput("\r")
	if list.submenuComponent == nil {
		t.Fatal("submenu did not open")
	}
	wheel := MouseEvent{Type: MouseWheel, WheelDelta: -1, Y: 1, Width: 40, Height: 6}
	result := list.HandleMouse(wheel)
	if result == nil || !result.Focus {
		t.Fatalf("submenu mouse result = %v", result)
	}
}

func TestSettingsListMouseBranches(t *testing.T) {
	var changes []string
	list := NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, value string) { changes = append(changes, id+"="+value) }, nil, true)

	searchPress := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 2, Y: 0, Width: 40, Height: 8}
	if result := list.HandleMouse(searchPress); result == nil || !result.Focus {
		t.Fatalf("search row result = %v", result)
	}
	if result := list.HandleMouse(MouseEvent{Type: MousePress, Button: MouseButtonLeft, Y: 1, Width: 40, Height: 8}); result != nil {
		t.Fatalf("separator row result = %v", result)
	}
	if result := list.HandleMouse(MouseEvent{Type: MouseWheel, WheelDelta: 1, Y: 3, Width: 40, Height: 8}); result == nil {
		t.Fatal("wheel should be handled")
	}
	if result := list.HandleMouse(MouseEvent{Type: MouseMove, Y: 3}); result != nil {
		t.Fatalf("non-button event result = %v", result)
	}

	press := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 1, Y: 2, Width: 40, Height: 8}
	if result := list.HandleMouse(press); result == nil || !result.Focus {
		t.Fatalf("item press result = %v", result)
	}
	click := press
	click.Type = MouseClick
	if result := list.HandleMouse(click); result == nil {
		t.Fatal("item click should be handled")
	}
	if len(changes) == 0 {
		t.Fatal("click activation did not change a setting")
	}
	outOfRange := click
	outOfRange.Y = 40
	if result := list.HandleMouse(outOfRange); result != nil {
		t.Fatalf("out of range click result = %v", result)
	}

	list.HandleInput("zzzzz")
	if result := list.HandleMouse(press); result != nil {
		t.Fatalf("empty filtered list mouse result = %v", result)
	}
}

func TestSettingsListRendersDescription(t *testing.T) {
	list := NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	rendered := StripTerminalSequences(strings.Join(list.Render(60), "\n"))
	if !strings.Contains(rendered, "Color theme") {
		t.Fatalf("description missing from render: %q", rendered)
	}
}

func TestSettingsListSubmenuSynchronousDone(t *testing.T) {
	var changes []string
	list := NewSettingsList([]SettingItem{
		{ID: "editor", Label: "Editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) Component {
			done("emacs", "")
			return &submenuStub{done: done}
		}},
	}, 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, value string) { changes = append(changes, id+"="+value) }, nil, false)
	list.HandleInput("\r")
	if list.submenuComponent != nil {
		t.Fatal("synchronous done left the submenu open")
	}
	if len(changes) != 1 || changes[0] != "editor=emacs" {
		t.Fatalf("synchronous submenu change = %q", changes)
	}
}
