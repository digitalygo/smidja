package tui

import (
	"strings"
	"testing"
	"time"
)

func TestTextRendering(t *testing.T) {
	text := NewText("hello world", 1, 1, nil)
	lines := text.Render(20)
	if len(lines) != 3 {
		t.Fatalf("paddingY 1 should add two empty lines: %d", len(lines))
	}
	body := strings.TrimRight(StripTerminalSequences(lines[1]), " ")
	if !strings.HasPrefix(body, " hello world") {
		t.Fatalf("paddingX missing: %q", body)
	}

	text.SetText("updated")
	if lines := text.Render(20); !strings.Contains(lines[1], "updated") {
		t.Fatalf("SetText not applied: %q", lines[1])
	}

	wrapped := NewText("word one two three four", 0, 0, nil)
	lines = wrapped.Render(10)
	if len(lines) != 3 {
		t.Fatalf("wrapped lines = %d: %q", len(lines), lines)
	}

	bgCalls := 0
	bgText := NewText("bg", 0, 0, func(line string) string {
		bgCalls++
		return "<" + line + ">"
	})
	bgText.Render(10)
	if bgCalls == 0 || !strings.Contains(StripTerminalSequences(bgText.cachedLines[0]), "<") {
		t.Fatalf("custom background not applied")
	}

	empty := NewText("   ", 0, 0, nil)
	if lines := empty.Render(10); len(lines) != 0 {
		t.Fatalf("whitespace-only text should render nothing: %q", lines)
	}
}

func TestTruncatedText(t *testing.T) {
	truncated := NewTruncatedText("a very long single line", 1, 1)
	lines := truncated.Render(10)
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	body := StripTerminalSequences(lines[1])
	if !strings.Contains(body, "...") {
		t.Fatalf("ellipsis missing: %q", body)
	}
	if VisibleWidth(body) > 10 {
		t.Fatalf("line too wide: %q", body)
	}
	truncated.SetText("short")
	if body := StripTerminalSequences(truncated.Render(10)[1]); strings.Contains(body, "...") {
		t.Fatalf("short text should not truncate: %q", body)
	}
	multiline := NewTruncatedText("first\nsecond", 0, 0)
	if body := StripTerminalSequences(multiline.Render(20)[0]); strings.Contains(body, "second") {
		t.Fatalf("only first line should render: %q", body)
	}
}

func TestSpacer(t *testing.T) {
	spacer := NewSpacer(3)
	if lines := spacer.Render(10); len(lines) != 3 {
		t.Fatalf("spacer lines = %d", len(lines))
	}
	spacer.SetLines(5)
	if lines := spacer.Render(10); len(lines) != 5 {
		t.Fatalf("spacer lines after SetLines = %d", len(lines))
	}
}

func TestBoxRenderingAndCache(t *testing.T) {
	box := NewBox(1, 1, nil)
	child := &staticComponent{lines: []string{"content"}}
	box.AddChild(child)

	lines := box.Render(12)
	if len(lines) != 3 {
		t.Fatalf("boxed lines = %d", len(lines))
	}
	if !strings.Contains(lines[1], "content") {
		t.Fatalf("content missing: %q", lines[1])
	}
	if VisibleWidth(lines[1]) != 12 {
		t.Fatalf("box line not padded to width: %q", lines[1])
	}

	cached := box.Render(12)
	if len(cached) != len(lines) || cached[1] != lines[1] {
		t.Fatal("cache should return identical lines")
	}

	box.SetBgFn(func(line string) string { return "[" + line + "]" })
	fresh := box.Render(12)
	if !strings.Contains(StripTerminalSequences(fresh[1]), "[") {
		t.Fatalf("background not applied after SetBgFn: %q", fresh[1])
	}

	box.Clear()
	if lines := box.Render(12); len(lines) != 0 {
		t.Fatalf("cleared box should render nothing: %q", lines)
	}
}

func TestBoxMouseDispatch(t *testing.T) {
	box := NewBox(1, 0, nil)
	child := &recordingComponent{staticComponent: &staticComponent{lines: []string{"hit"}}}
	box.AddChild(child)

	event := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 2, Y: 0, ScreenX: 2, ScreenY: 0, Width: 10, Height: 1}
	if dispatch := dispatchMouseEvent(box, event); dispatch == nil {
		t.Fatal("box should dispatch to padded child")
	}
	edge := event
	edge.X = 0
	if dispatch := dispatchMouseEvent(box, edge); dispatch != nil {
		t.Fatal("padding edge should not dispatch")
	}
}

func TestBorderRendering(t *testing.T) {
	child := &plainComponent{lines: []string{"inner"}}
	border := NewBorder(child, nil)
	lines := border.Render(11)
	if len(lines) != 3 {
		t.Fatalf("border lines = %d", len(lines))
	}
	top := StripTerminalSequences(lines[0])
	if !strings.HasPrefix(top, "┌") || !strings.HasSuffix(top, "┐") {
		t.Fatalf("top border = %q", top)
	}
	middle := StripTerminalSequences(lines[1])
	if !strings.HasPrefix(middle, "│") || !strings.Contains(middle, "inner") || !strings.HasSuffix(middle, "│") {
		t.Fatalf("content line = %q", middle)
	}
	bottom := StripTerminalSequences(lines[2])
	if !strings.HasPrefix(bottom, "└") || !strings.HasSuffix(bottom, "┘") {
		t.Fatalf("bottom border = %q", bottom)
	}
}

func TestBorderDynamicStyleAndTitle(t *testing.T) {
	child := &plainComponent{lines: []string{"x"}}
	border := NewBorder(child, nil)
	border.SetTitle("title")
	border.SetBorderFn(func(text string) string { return "\x1b[31m" + text + "\x1b[0m" })
	lines := border.Render(14)
	top := StripTerminalSequences(lines[0])
	if !strings.HasPrefix(top, "┌") || !strings.Contains(top, "title") {
		t.Fatalf("styled top = %q", top)
	}
	if VisibleWidth(top) != 14 {
		t.Fatalf("top width = %d", VisibleWidth(top))
	}
	if lines := border.Render(1); len(lines) != 0 {
		t.Fatalf("narrow border should render nothing: %q", lines)
	}
}

func TestLoaderAnimation(t *testing.T) {
	terminal := newFakeTerminal(20, 3)
	screen := NewMainScreen(terminal, false)
	loader := NewLoader(screen, nil, nil, "working", nil)
	loader.Start()
	defer loader.Stop()

	if got := StripTerminalSequences(loader.Render(20)[1]); !strings.Contains(got, "⠋") || !strings.Contains(got, "working") {
		t.Fatalf("loader render = %q", got)
	}
	loader.advanceFrame()
	if got := StripTerminalSequences(loader.Render(20)[1]); !strings.Contains(got, "⠙") {
		t.Fatalf("loader did not advance: %q", got)
	}
	loader.SetMessage("other")
	if got := StripTerminalSequences(loader.Render(20)[1]); !strings.Contains(got, "other") {
		t.Fatalf("SetMessage not applied: %q", got)
	}
	loader.Stop()
	loader.advanceFrame()

	custom := NewLoader(screen, nil, nil, "m", &LoaderIndicator{Frames: []string{"a", "b"}, IntervalMs: 5})
	custom.Start()
	defer custom.Stop()
	if got := StripTerminalSequences(custom.Render(20)[1]); !strings.Contains(got, "a") {
		t.Fatalf("custom frames = %q", got)
	}
}

func TestCancellableLoaderAbort(t *testing.T) {
	terminal := newFakeTerminal(20, 3)
	screen := NewMainScreen(terminal, false)
	loader := NewCancellableLoader(screen, nil, nil, "loading")
	loader.Start()
	defer loader.Dispose()

	aborted := 0
	loader.SetOnAbort(func() { aborted++ })
	if loader.Aborted() {
		t.Fatal("loader should not start aborted")
	}
	if loader.Context().Err() != nil {
		t.Fatal("context should be live")
	}
	loader.HandleInput("\x1b")
	if !loader.Aborted() || aborted != 1 {
		t.Fatalf("escape should abort: aborted=%v calls=%d", loader.Aborted(), aborted)
	}
	loader.HandleInput("x")
	if aborted != 1 {
		t.Fatal("non-escape input should not re-abort")
	}
}

func TestSelectListNavigationAndSelect(t *testing.T) {
	items := []SelectItem{
		{Value: "alpha", Description: "first"},
		{Value: "beta", Description: "second"},
		{Value: "gamma", Description: "third"},
	}
	var selected []string
	list := NewSelectList(items, 5, DefaultSelectListTheme(nil, nil))
	list.SetOnSelect(func(item SelectItem) { selected = append(selected, item.Value) })

	list.HandleInput("\x1b[B")
	if item, _ := list.SelectedItem(); item.Value != "beta" {
		t.Fatalf("down selection = %v", item)
	}
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[B")
	if item, _ := list.SelectedItem(); item.Value != "alpha" {
		t.Fatal("down should wrap to first")
	}
	list.HandleInput("\x1b[A")
	if item, _ := list.SelectedItem(); item.Value != "gamma" {
		t.Fatal("up should wrap to last")
	}
	list.HandleInput("\x1b[A")
	list.HandleInput("\r")
	if len(selected) != 1 || selected[0] != "beta" {
		t.Fatalf("selected = %q", selected)
	}

	lines := list.Render(60)
	if len(lines) != 3 {
		t.Fatalf("rendered lines = %d", len(lines))
	}
	if !strings.Contains(StripTerminalSequences(lines[1]), "→ ") {
		t.Fatalf("selection cursor missing: %q", lines[1])
	}
	if !strings.Contains(StripTerminalSequences(lines[0]), "first") {
		t.Fatalf("description missing: %q", lines[0])
	}
}

func TestSelectListCancelAndFilter(t *testing.T) {
	items := []SelectItem{{Value: "one"}, {Value: "two"}, {Value: "three"}}
	cancelled := 0
	list := NewSelectList(items, 5, DefaultSelectListTheme(nil, nil))
	list.SetOnCancel(func() { cancelled++ })

	list.HandleInput("\x1b")
	if cancelled != 1 {
		t.Fatalf("cancel count = %d", cancelled)
	}

	list.SetFilter("t")
	if len(list.filteredItems) != 2 {
		t.Fatalf("filtered = %d", len(list.filteredItems))
	}
	list.SetFilter("zzz")
	if lines := list.Render(20); !strings.Contains(StripTerminalSequences(lines[0]), "No matching") {
		t.Fatalf("empty state = %q", lines[0])
	}
	list.HandleInput("\r")
	if cancelled != 1 {
		t.Fatal("confirm on empty list must not select")
	}
	list.HandleInput("\x1b")
}

func TestSelectListScrollIndicatorAndWrap(t *testing.T) {
	items := make([]SelectItem, 12)
	for i := range items {
		items[i] = SelectItem{Value: "item" + itoa(i)}
	}
	list := NewSelectList(items, 5, DefaultSelectListTheme(nil, nil))
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[B")
	lines := list.Render(40)
	if len(lines) != 6 {
		t.Fatalf("maxVisible 5 + scroll info = 6, got %d", len(lines))
	}
	if !strings.Contains(StripTerminalSequences(lines[5]), "(") {
		t.Fatalf("scroll indicator missing: %q", lines[5])
	}
}

func TestSelectListMouse(t *testing.T) {
	items := []SelectItem{{Value: "a"}, {Value: "b"}, {Value: "c"}}
	var picked []string
	list := NewSelectList(items, 5, DefaultSelectListTheme(nil, nil))
	list.SetOnSelect(func(item SelectItem) { picked = append(picked, item.Value) })

	press := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 1, Y: 2, ScreenX: 1, ScreenY: 2, Width: 20, Height: 3}
	if result := list.HandleMouse(press); result == nil || !result.Focus {
		t.Fatal("press should focus and highlight")
	}
	if item, _ := list.SelectedItem(); item.Value != "c" {
		t.Fatalf("press selection = %v", item)
	}
	if result := list.HandleMouse(press.WithPosition(1, 2)); result == nil {
		t.Fatal("click should dispatch")
	}
	click := MouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: 1, Y: 2, ScreenX: 1, ScreenY: 2, Width: 20, Height: 3}
	if list.HandleMouse(click) == nil {
		t.Fatal("click dispatch failed")
	}
	if len(picked) != 1 || picked[0] != "c" {
		t.Fatalf("picked = %q", picked)
	}

	wheel := MouseEvent{Type: MouseWheel, WheelDelta: -1, X: 1, Y: 0, ScreenX: 1, ScreenY: 0, Width: 20, Height: 3}
	if result := list.HandleMouse(wheel); result == nil || !result.Render {
		t.Fatal("wheel should request render")
	}
	if item, _ := list.SelectedItem(); item.Value != "b" {
		t.Fatalf("wheel up selection = %v", item)
	}
	if result := list.HandleMouse(MouseEvent{Type: MouseWheel, WheelDelta: 5}); result == nil {
		t.Fatal("wheel down should dispatch")
	}
	if item, _ := list.SelectedItem(); item.Value != "c" {
		t.Fatalf("wheel down selection = %v", item)
	}
}

func TestFuzzyFilter(t *testing.T) {
	items := []string{"alpha-one", "beta-two", "gamma-three", "alpha-two", "2fast"}
	filtered := FuzzyFilter(items, "alpha", func(s string) string { return s })
	if len(filtered) != 2 {
		t.Fatalf("filtered = %q", filtered)
	}
	if filtered[0] != "alpha-one" || filtered[1] != "alpha-two" {
		t.Fatalf("ordering = %q", filtered)
	}
	filtered = FuzzyFilter(items, "a-o", func(s string) string { return s })
	if len(filtered) != 3 || filtered[0] != "alpha-one" || filtered[2] != "beta-two" {
		t.Fatalf("subsequence match = %q", filtered)
	}
	filtered = FuzzyFilter(items, "f2", func(s string) string { return s })
	if len(filtered) != 1 || filtered[0] != "2fast" {
		t.Fatalf("alpha-numeric swap = %q", filtered)
	}
	if got := FuzzyFilter(items, "", func(s string) string { return s }); len(got) != 5 {
		t.Fatalf("empty query = %q", got)
	}
	if got := FuzzyFilter(items, "zzz", func(s string) string { return s }); len(got) != 0 {
		t.Fatalf("no match = %q", got)
	}
}

func settingsItems() []SettingItem {
	return []SettingItem{
		{ID: "theme", Label: "Theme", CurrentValue: "dark", Values: []string{"dark", "light", "solarized"}, Description: "Color theme"},
		{ID: "model", Label: "Model", CurrentValue: "fast", Values: []string{"fast", "smart"}},
		{ID: "editor", Label: "External editor", CurrentValue: "vim", Submenu: func(current string, done func(string, string)) Component {
			list := NewSelectList([]SelectItem{{Value: "vim"}, {Value: "emacs"}}, 5, DefaultSelectListTheme(nil, nil))
			list.SetOnSelect(func(item SelectItem) { done(item.Value, "") })
			return list
		}},
	}
}

func TestSettingsListNavigationAndToggle(t *testing.T) {
	var changes []string
	list := NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, newValue string) { changes = append(changes, id+"="+newValue) }, nil, false)

	list.HandleInput("\x1b[B")
	list.HandleInput("\r")
	if len(changes) != 1 || changes[0] != "model=smart" {
		t.Fatalf("changes = %q", changes)
	}
	list.HandleInput("\x1b[A")
	list.HandleInput("\r")
	if len(changes) != 2 || changes[1] != "theme=light" {
		t.Fatalf("changes after toggle = %q", changes)
	}
	list.HandleInput("\r")
	if len(changes) != 3 || changes[2] != "theme=solarized" {
		t.Fatalf("cycling values = %q", changes)
	}
	list.HandleInput("\x1b[A")
	if item := list.displayItems()[list.selectedIndex]; item.ID != "editor" {
		t.Fatalf("selection should wrap up: %s", item.ID)
	}
	list.HandleInput("\x1b[B")
	if item := list.displayItems()[list.selectedIndex]; item.ID != "theme" {
		t.Fatalf("selection should wrap down: %s", item.ID)
	}
}

func TestSettingsListSearch(t *testing.T) {
	list := NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil), nil, nil, true)
	list.HandleInput("the")
	if len(list.displayItems()) != 1 || list.displayItems()[0].ID != "theme" {
		t.Fatalf("search filter = %d items", len(list.displayItems()))
	}
	lines := list.Render(60)
	if len(lines) < 2 || !strings.Contains(StripTerminalSequences(lines[0]), "the") {
		t.Fatalf("search input not rendered: %q", lines)
	}
	list.HandleInput("\x7f")
	list.HandleInput("\x7f")
	list.HandleInput("\x7f")
	if len(list.displayItems()) != 3 {
		t.Fatalf("clearing search should restore items: %d", len(list.displayItems()))
	}
}

func TestSettingsListSubmenu(t *testing.T) {
	var changes []string
	list := NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil),
		func(id, newValue string) { changes = append(changes, id+"="+newValue) }, nil, false)
	list.SelectItem("editor")
	list.HandleInput("\r")
	if list.submenuComponent == nil {
		t.Fatal("submenu should open")
	}
	list.HandleInput("\x1b[B")
	list.HandleInput("\r")
	if list.submenuComponent != nil {
		t.Fatal("submenu should close after selection")
	}
	if len(changes) != 1 || changes[0] != "editor=emacs" {
		t.Fatalf("submenu change = %q", changes)
	}
	if list.displayItems()[list.selectedIndex].ID != "editor" {
		t.Fatal("selection should return to the submenu item")
	}
}

func TestSettingsListEmptyStateAndMouse(t *testing.T) {
	list := NewSettingsList(nil, 10, DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	if lines := list.Render(40); !strings.Contains(StripTerminalSequences(lines[0]), "No settings") {
		t.Fatalf("empty state = %q", lines[0])
	}

	list = NewSettingsList(settingsItems(), 10, DefaultSettingsListTheme(nil, nil, nil), nil, nil, false)
	press := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 1, Y: 1, ScreenX: 1, ScreenY: 1, Width: 40, Height: 5}
	if result := list.HandleMouse(press); result == nil || !result.Focus {
		t.Fatal("press should focus")
	}
	if list.selectedIndex != 1 {
		t.Fatalf("mouse selection index = %d", list.selectedIndex)
	}
	if result := list.HandleMouse(MouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: 1, Y: 1, ScreenX: 1, ScreenY: 1, Width: 40, Height: 5}); result == nil {
		t.Fatal("click should activate")
	}
	if result := list.HandleMouse(MouseEvent{Type: MouseWheel, WheelDelta: -1, X: 1, Y: 0, ScreenX: 1, ScreenY: 0, Width: 40, Height: 5}); result == nil {
		t.Fatal("wheel should be handled")
	}
}

func TestInputTypingAndEditing(t *testing.T) {
	input := NewInput(InputOptions{})
	var submitted []string
	input.SetOnSubmit(func(value string) { submitted = append(submitted, value) })
	input.SetFocused(true)

	for _, ch := range "hello" {
		input.HandleInput(string(ch))
	}
	if input.Value() != "hello" || input.Cursor() != 5 {
		t.Fatalf("value = %q cursor = %d", input.Value(), input.Cursor())
	}

	input.HandleInput("\x1b[D")
	input.HandleInput("\x1b[D")
	input.HandleInput("X")
	if input.Value() != "helXlo" || input.Cursor() != 4 {
		t.Fatalf("insert = %q @%d", input.Value(), input.Cursor())
	}

	input.HandleInput("\x7f")
	if input.Value() != "hello" {
		t.Fatalf("backspace = %q", input.Value())
	}

	input.HandleInput("\x1b[3~")
	if input.Value() != "helo" {
		t.Fatalf("delete = %q", input.Value())
	}

	input.HandleInput("\r")
	if len(submitted) != 1 || submitted[0] != "helo" {
		t.Fatalf("submitted = %q", submitted)
	}
}

func TestInputWordOperations(t *testing.T) {
	input := NewInput(InputOptions{})
	input.SetValue("alpha beta gamma")
	input.SetFocused(true)
	input.HandleInput("\x1b[F")
	input.HandleInput("\x1b[1;3D")
	if input.Cursor() != 11 {
		t.Fatalf("word left cursor = %d", input.Cursor())
	}
	input.HandleInput("\x17")
	if input.Value() != "alpha gamma" || input.Cursor() != 6 {
		t.Fatalf("delete word backward = %q @%d", input.Value(), input.Cursor())
	}
	input.HandleInput("\x1bd")
	if input.Value() != "alpha " || input.Cursor() != 6 {
		t.Fatalf("delete word forward = %q @%d", input.Value(), input.Cursor())
	}
	input.HandleInput("\x15")
	if input.Value() != "" {
		t.Fatalf("delete to line start = %q", input.Value())
	}
	input.HandleInput("\x15")
	if input.Value() != "" {
		t.Fatalf("delete to line start at start = %q", input.Value())
	}
}

func TestInputUndo(t *testing.T) {
	input := NewInput(InputOptions{})
	input.SetFocused(true)
	input.HandleInput("ab")
	input.HandleInput("c")
	if input.Value() != "abc" {
		t.Fatalf("value = %q", input.Value())
	}
	input.HandleInput("\x1f")
	if input.Value() != "" {
		t.Fatalf("undo typed word = %q, want empty (word-level undo)", input.Value())
	}
	input.HandleInput("d")
	input.HandleInput(" ")
	input.HandleInput("e")
	input.HandleInput("f")
	if input.Value() != "d ef" {
		t.Fatalf("value after typing = %q", input.Value())
	}
	input.HandleInput("\x1f")
	if input.Value() != "d" {
		t.Fatalf("undo before space = %q", input.Value())
	}
	input.HandleInput("\x1f")
	if input.Value() != "" {
		t.Fatalf("undo past start = %q", input.Value())
	}
}

func TestInputYankAndKillRing(t *testing.T) {
	input := NewInput(InputOptions{})
	input.SetFocused(true)
	input.HandleInput("one two three four")
	input.HandleInput("\x17")
	if input.Value() != "one two three " {
		t.Fatalf("after kill = %q", input.Value())
	}
	input.HandleInput("X")
	input.HandleInput("\x17")
	if input.Value() != "one two three " {
		t.Fatalf("after second kill = %q", input.Value())
	}
	input.HandleInput("\x19")
	if input.Value() != "one two three X" {
		t.Fatalf("after yank = %q", input.Value())
	}
	input.HandleInput("\x1by")
	if input.Value() != "one two three four" {
		t.Fatalf("after yank pop = %q", input.Value())
	}
}

func TestInputLineOperations(t *testing.T) {
	input := NewInput(InputOptions{})
	input.SetFocused(true)
	input.HandleInput("keep drop keep")
	input.HandleInput("\x01")
	if input.Cursor() != 0 {
		t.Fatalf("line start cursor = %d", input.Cursor())
	}
	input.HandleInput("\x05")
	if input.Cursor() != len(input.Value()) {
		t.Fatalf("line end cursor = %d", input.Cursor())
	}
	input.HandleInput("\x01")
	input.HandleInput("\x0b")
	if input.Value() != "" {
		t.Fatalf("delete to line end = %q", input.Value())
	}
	input.HandleInput("abc")
	input.HandleInput("\n")
	if input.Value() != "abc" {
		t.Fatalf("newline in single-line input = %q", input.Value())
	}
	input.HandleInput("\x1b")
	if input.Value() != "abc" {
		t.Fatal("escape without callback should be a no-op")
	}
	escaped := false
	input.SetOnEscape(func() { escaped = true })
	input.HandleInput("\x1b")
	if !escaped {
		t.Fatal("escape callback not invoked")
	}
}

func TestInputPlaceholderAndMouse(t *testing.T) {
	input := NewInput(InputOptions{Placeholder: "type here", PlaceholderStyle: func(s string) string { return s }})
	input.SetFocused(true)
	lines := input.Render(30)
	if !strings.Contains(StripTerminalSequences(lines[0]), "type here") {
		t.Fatalf("placeholder missing: %q", lines[0])
	}
	if !strings.Contains(lines[0], CursorMarker) {
		t.Fatalf("focused input should carry cursor marker: %q", lines[0])
	}

	input.SetFocused(false)
	lines = input.Render(30)
	if strings.Contains(lines[0], CursorMarker) {
		t.Fatalf("unfocused input should not show cursor marker: %q", lines[0])
	}

	input.SetValue("hello world")
	input.SetFocused(true)
	event := MouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 5, Y: 0, ScreenX: 5, ScreenY: 0, Width: 30, Height: 1}
	if result := input.HandleMouse(event); result == nil || !result.Focus {
		t.Fatal("mouse press should focus input")
	}
	if input.Cursor() != 3 {
		t.Fatalf("mouse cursor = %d, want 3", input.Cursor())
	}
}

func TestInputHorizontalScroll(t *testing.T) {
	input := NewInput(InputOptions{})
	input.SetFocused(true)
	long := strings.Repeat("x", 50)
	input.SetValue(long)
	input.HandleInput("\x1b[F")
	lines := input.Render(10)
	visible := StripTerminalSequences(lines[0])
	if VisibleWidth(visible) != 10 || !strings.HasPrefix(visible, "> ") {
		t.Fatalf("scrolled render = %q", visible)
	}
	input.HandleInput("\x01")
	lines = input.Render(10)
	if got := StripTerminalSequences(lines[0]); !strings.HasPrefix(got, "> ") {
		t.Fatalf("cursor at start render = %q", got)
	}
}

func TestInputPaste(t *testing.T) {
	input := NewInput(InputOptions{})
	input.SetFocused(true)
	input.HandleInput(BracketedPaste("line1\nline2\ttabbed"))
	if input.Value() != "line1line2    tabbed" {
		t.Fatalf("paste value = %q", input.Value())
	}
	input.HandleInput("\x1f")
	if input.Value() != "" {
		t.Fatalf("paste undo = %q", input.Value())
	}
	input.HandleInput("a")
	input.HandleInput(BracketedPasteStart)
	input.HandleInput("b\x1b[201~")
	if input.Value() != "ab" {
		t.Fatalf("split paste = %q", input.Value())
	}
}

func TestInputKittyPrintables(t *testing.T) {
	SetKittyProtocolActive(true)
	defer SetKittyProtocolActive(false)
	input := NewInput(InputOptions{})
	input.SetFocused(true)
	input.HandleInput("\x1b[97u")
	input.HandleInput("\x1b[98:66;2u")
	if input.Value() != "aB" {
		t.Fatalf("kitty printable input = %q", input.Value())
	}
	input.HandleInput("\x1b[97;5u")
	if input.Value() != "aB" {
		t.Fatalf("ctrl+letter must not insert: %q", input.Value())
	}
}

func TestLoaderIndicatorInterval(t *testing.T) {
	if defaultLoaderInterval != 80*time.Millisecond {
		t.Fatalf("default interval = %v", defaultLoaderInterval)
	}
}

func TestLoaderSanitizesCustomFrames(t *testing.T) {
	terminal := newFakeTerminal(20, 3)
	screen := NewMainScreen(terminal, false)
	loader := NewLoader(screen, nil, nil, "m", &LoaderIndicator{Frames: []string{"a\x1b[31m", "b"}, IntervalMs: 5})
	loader.Start()
	defer loader.Stop()
	rendered := loader.Render(20)[1]
	if strings.Contains(rendered, "\x1b[31m") {
		t.Fatalf("custom loader frame escape leaked: %q", rendered)
	}
	if got := StripTerminalSequences(rendered); !strings.Contains(got, "a") {
		t.Fatalf("sanitized custom loader frame missing: %q", got)
	}
}
