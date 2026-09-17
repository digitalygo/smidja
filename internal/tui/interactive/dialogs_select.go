package interactive

import (
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
)

type SelectDialogOptions struct {
	Title        string
	Items        []tui.SelectItem
	MaxVisible   int
	Searchable   bool
	Placeholder  string
	InitialValue string
}

type SelectDialog struct {
	mu         sync.Mutex
	theme      DialogTheme
	title      string
	list       *tui.SelectList
	search     *tui.Input
	searchable bool
	focused    bool
	onSelect   func(string)
	onCancel   func()
	settled    bool
}

func NewSelectDialog(opts SelectDialogOptions, theme DialogTheme, onSelect func(string), onCancel func()) *SelectDialog {
	list := tui.NewSelectList(opts.Items, opts.MaxVisible, tui.DefaultSelectListTheme(theme.Accent, theme.Muted))
	if opts.InitialValue != "" {
		for index, item := range opts.Items {
			if item.Value == opts.InitialValue {
				list.SetSelectedIndex(index)
				break
			}
		}
	}
	dialog := &SelectDialog{
		theme:      theme,
		title:      SanitizeSingleLine(opts.Title),
		list:       list,
		searchable: opts.Searchable,
		onSelect:   onSelect,
		onCancel:   onCancel,
	}
	if opts.Searchable {
		dialog.search = tui.NewInput(tui.InputOptions{
			Prompt:           "search> ",
			Placeholder:      SanitizeSingleLine(opts.Placeholder),
			PlaceholderStyle: theme.Dim,
		})
	}
	list.SetOnSelect(func(item tui.SelectItem) {
		dialog.settle("select", func() {
			if dialog.onSelect != nil {
				dialog.onSelect(item.Value)
			}
		})
	})
	list.SetOnCancel(func() {
		dialog.settle("cancel", func() {
			if dialog.onCancel != nil {
				dialog.onCancel()
			}
		})
	})
	return dialog
}

func (d *SelectDialog) settle(kind string, callback func()) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	d.settled = true
	d.mu.Unlock()
	callback()
}

func (d *SelectDialog) SetFocused(focused bool) {
	d.mu.Lock()
	d.focused = focused
	search := d.search
	d.mu.Unlock()
	if search != nil {
		search.SetFocused(focused)
	}
}

func (d *SelectDialog) SetTheme(theme DialogTheme) {
	d.mu.Lock()
	d.theme = theme
	list := d.list
	search := d.search
	d.mu.Unlock()
	if list != nil {
		list.SetTheme(tui.DefaultSelectListTheme(theme.Accent, theme.Muted))
	}
	if search != nil {
		search.SetPlaceholderStyle(theme.Dim)
	}
}

func (d *SelectDialog) Invalidate() {
	d.mu.Lock()
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.Invalidate()
	}
}

func (d *SelectDialog) Render(width int) []string {
	d.mu.Lock()
	title := SanitizeSingleLine(d.title)
	theme := d.theme
	searchable := d.searchable
	search := d.search
	list := d.list
	d.mu.Unlock()
	bodyWidth := dialogBodyWidth(width)
	var body []string
	if searchable && search != nil {
		body = append(body, search.Render(bodyWidth)...)
		body = append(body, "")
	}
	if list != nil {
		body = append(body, list.Render(bodyWidth)...)
	}
	hint := "Enter select · Esc cancel"
	if searchable {
		hint = "Type to filter · ↑/↓ select · Enter select · Esc cancel"
	}
	return renderDialogFrame(title, hint, body, width, theme)
}

func (d *SelectDialog) HandleInput(data string) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	list := d.list
	search := d.search
	searchable := d.searchable
	keybindings := tui.GlobalKeybindings()
	switch {
	case keybindings.Matches(data, "tui.select.cancel"):
		d.mu.Unlock()
		d.settle("cancel", func() {
			if d.onCancel != nil {
				d.onCancel()
			}
		})
	case keybindings.Matches(data, "tui.select.up") || keybindings.Matches(data, "tui.select.down") ||
		keybindings.Matches(data, "tui.select.pageUp") || keybindings.Matches(data, "tui.select.pageDown") ||
		keybindings.Matches(data, "tui.select.confirm"):
		d.mu.Unlock()
		list.HandleInput(data)
	case searchable && search != nil:
		d.mu.Unlock()
		search.HandleInput(data)
		list.SetFilterFuzzy(search.Value())
	default:
		d.mu.Unlock()
	}
}

type SettingsDialog struct {
	mu       sync.Mutex
	theme    DialogTheme
	title    string
	list     *tui.SettingsList
	draft    map[string]string
	onApply  func(map[string]string)
	onCancel func()
	settled  bool
}

func NewSettingsDialog(title string, items []tui.SettingItem, theme DialogTheme, onApply func(map[string]string), onCancel func()) *SettingsDialog {
	dialog := &SettingsDialog{
		theme:    theme,
		title:    SanitizeSingleLine(title),
		draft:    map[string]string{},
		onApply:  onApply,
		onCancel: onCancel,
	}
	dialog.list = tui.NewSettingsList(items, 10, tui.DefaultSettingsListTheme(theme.Accent, theme.Muted, theme.Dim),
		func(id, value string) {
			dialog.mu.Lock()
			dialog.draft[id] = value
			dialog.mu.Unlock()
		}, func() {
			dialog.settle(func() {
				if dialog.onCancel != nil {
					dialog.onCancel()
				}
			})
		}, true)
	return dialog
}

func (d *SettingsDialog) settle(callback func()) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	d.settled = true
	d.mu.Unlock()
	callback()
}

func (d *SettingsDialog) SetFocused(focused bool) {}

func (d *SettingsDialog) SetTheme(theme DialogTheme) {
	d.mu.Lock()
	d.theme = theme
	list := d.list
	d.mu.Unlock()
	if list == nil {
		return
	}
	list.SetTheme(tui.DefaultSettingsListTheme(theme.Accent, theme.Muted, theme.Dim))
	list.SetSubmenuTheme(func(component tui.Component) { applyDialogTheme(component, theme) })
}

func (d *SettingsDialog) Invalidate() {
	d.mu.Lock()
	list := d.list
	d.mu.Unlock()
	if list != nil {
		list.Invalidate()
	}
}

func (d *SettingsDialog) Render(width int) []string {
	d.mu.Lock()
	title := SanitizeSingleLine(d.title)
	theme := d.theme
	list := d.list
	d.mu.Unlock()
	bodyWidth := dialogBodyWidth(width)
	var body []string
	if list != nil {
		body = append(body, list.Render(bodyWidth)...)
	}
	return renderDialogFrame(title, "Enter/Space change · Ctrl+S apply · Esc cancel", body, width, theme)
}

func (d *SettingsDialog) HandleInput(data string) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	list := d.list
	d.mu.Unlock()
	if tui.MatchesKey(data, "ctrl+s") {
		d.mu.Lock()
		draft := make(map[string]string, len(d.draft))
		for key, value := range d.draft {
			draft[key] = value
		}
		d.mu.Unlock()
		d.settle(func() {
			if d.onApply != nil {
				d.onApply(draft)
			}
		})
		return
	}
	list.HandleInput(data)
}

type EditorDialog struct {
	mu       sync.Mutex
	theme    DialogTheme
	title    string
	editor   *tui.Editor
	onSubmit func(string)
	onCancel func()
	settled  bool
}

func NewEditorDialog(title, prefill string, theme DialogTheme, onSubmit func(string), onCancel func()) *EditorDialog {
	editor := tui.NewEditor(tui.EditorOptions{
		Theme:               theme.Base,
		TerminalRows:        12,
		DisableAutocomplete: true,
	})
	editor.SetText(prefill)
	editor.SetFocused(true)
	return &EditorDialog{theme: theme, title: SanitizeSingleLine(title), editor: editor, onSubmit: onSubmit, onCancel: onCancel}
}

func (d *EditorDialog) SetFocused(focused bool) {
	d.mu.Lock()
	if d.editor != nil {
		d.editor.SetFocused(focused)
	}
	d.mu.Unlock()
}

func (d *EditorDialog) SetTheme(theme DialogTheme) {
	d.mu.Lock()
	d.theme = theme
	editor := d.editor
	d.mu.Unlock()
	if editor != nil {
		editor.SetTheme(theme.Base)
	}
}

func (d *EditorDialog) Invalidate() {
	if d.editor != nil {
		d.editor.Invalidate()
	}
}

func (d *EditorDialog) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	bodyWidth := dialogBodyWidth(width)
	var body []string
	if d.editor != nil {
		body = append(body, d.editor.Render(bodyWidth)...)
	}
	return renderDialogFrame(SanitizeSingleLine(d.title), "Enter submit · Shift+Enter newline · Esc cancel", body, width, d.theme)
}

func (d *EditorDialog) HandleInput(data string) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	editor := d.editor
	keybindings := tui.GlobalKeybindings()
	switch {
	case keybindings.Matches(data, "tui.select.cancel"):
		d.settled = true
		callback := d.onCancel
		d.mu.Unlock()
		if callback != nil {
			callback()
		}
	case keybindings.Matches(data, "tui.input.submit"):
		d.settled = true
		value := ""
		if editor != nil {
			value = editor.ExpandedText()
		}
		callback := d.onSubmit
		d.mu.Unlock()
		if callback != nil {
			callback(value)
		}
	default:
		d.mu.Unlock()
		if editor != nil {
			editor.HandleInput(data)
		}
	}
}
