package tui

import (
	"strings"
)

func (e *Editor) IsShowingAutocomplete() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.autocompleteActive
}

func (e *Editor) AutocompletePrefix() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.autocompletePrefix
}

func (e *Editor) AutocompleteItems() []AutocompleteItem {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]AutocompleteItem(nil), e.autocompleteItems...)
}

func (e *Editor) InlineHint() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inlineHintLocked()
}

func (e *Editor) inlineHintLocked() string {
	if !e.autocompleteActive || e.autocompleteList == nil {
		return ""
	}
	selected, ok := e.autocompleteList.SelectedItem()
	if !ok {
		return ""
	}
	return InlineHint(e.autocompletePrefix, AutocompleteItem{Value: selected.Value, Label: selected.Label, Description: selected.Description})
}

func (e *Editor) currentLineBeforeCursorLocked() string {
	line := e.buffer.lines[e.buffer.cursorLine]
	if e.buffer.cursorCol > len(line) {
		return line
	}
	return line[:e.buffer.cursorCol]
}

func (e *Editor) dismissAutocompleteLocked() {
	e.autocompleteActive = false
	e.autocompleteList = nil
	e.autocompletePrefix = ""
	e.autocompleteKind = ""
	e.autocompleteItems = nil
}

func (e *Editor) triggerAutocompleteLocked(force bool) {
	if e.provider == nil {
		return
	}
	before := e.currentLineBeforeCursorLocked()
	if prefix, ok := slashPrefixOf(before); ok && e.buffer.cursorLine == 0 {
		items := e.provider.SlashSuggestions(prefix)
		if len(items) == 0 {
			e.dismissAutocompleteLocked()
			return
		}
		e.showAutocompleteLocked(prefix, items, "slash")
		return
	}
	if token, ok := extractAtToken(before); ok {
		items := e.provider.PathSuggestions(token)
		if len(items) == 0 {
			e.dismissAutocompleteLocked()
			return
		}
		e.showAutocompleteLocked(token, items, "path")
		return
	}
	if force {
		fallback := before
		if idx := strings.LastIndexAny(fallback, " \t\"'="); idx >= 0 {
			fallback = fallback[idx+1:]
		}
		items := e.provider.PathSuggestions("@" + fallback)
		if len(items) == 0 {
			items = e.provider.SlashSuggestions(before)
			if len(items) == 0 {
				e.dismissAutocompleteLocked()
				return
			}
			e.showAutocompleteLocked(before, items, "slash")
			return
		}
		e.showAutocompleteLocked("@"+fallback, items, "path")
		return
	}
	e.dismissAutocompleteLocked()
}

func (e *Editor) showAutocompleteLocked(prefix string, items []AutocompleteItem, kind string) {
	selectItems := make([]SelectItem, 0, len(items))
	for _, item := range items {
		label := item.Label
		if label == "" {
			label = item.Value
		}
		selectItems = append(selectItems, SelectItem{Value: item.Value, Label: label, Description: item.Description})
	}
	theme := DefaultSelectListTheme(nil, nil)
	if e.theme != nil {
		accent := func(s string) string { return e.theme.Fg(ThemeColor("accent"), s) }
		muted := func(s string) string { return e.theme.Fg(ThemeColor("muted"), s) }
		theme = DefaultSelectListTheme(accent, muted)
	}
	list := NewSelectList(selectItems, e.autocompleteMaxVisible, theme)
	if kind == "slash" {
		list.layout.MinPrimaryColumnWidth = 12
		list.layout.MaxPrimaryColumnWidth = 32
		best := e.provider.BestSlashMatchIndex(items, prefix)
		if best >= 0 {
			list.SetSelectedIndex(best)
		}
	}
	e.autocompleteList = list
	e.autocompletePrefix = prefix
	e.autocompleteKind = kind
	e.autocompleteActive = true
	e.autocompleteItems = append([]AutocompleteItem(nil), items...)
}

func (e *Editor) updateAutocompleteLocked() {
	if !e.autocompleteActive {
		return
	}
	kind := e.autocompleteKind
	prefix := e.autocompletePrefix
	before := e.currentLineBeforeCursorLocked()
	if kind == "slash" {
		currentPrefix, ok := slashPrefixOf(before)
		if !ok {
			e.dismissAutocompleteLocked()
			return
		}
		items := e.provider.SlashSuggestions(currentPrefix)
		if len(items) == 0 {
			e.dismissAutocompleteLocked()
			return
		}
		previousSelected := ""
		if e.autocompleteList != nil {
			if sel, ok := e.autocompleteList.SelectedItem(); ok {
				previousSelected = sel.Value
			}
		}
		e.showAutocompleteLocked(currentPrefix, items, kind)
		if previousSelected != "" {
			for idx, item := range items {
				if item.Value == previousSelected {
					e.autocompleteList.SetSelectedIndex(idx)
					break
				}
			}
		}
		_ = prefix
		return
	}
	token, ok := extractAtToken(before)
	if !ok {
		e.dismissAutocompleteLocked()
		return
	}
	items := e.provider.PathSuggestions(token)
	if len(items) == 0 {
		e.dismissAutocompleteLocked()
		return
	}
	e.showAutocompleteLocked(token, items, kind)
}

func (e *Editor) acceptAutocompleteLocked() bool {
	if !e.autocompleteActive || e.autocompleteList == nil || e.provider == nil {
		return false
	}
	selected, ok := e.autocompleteList.SelectedItem()
	if !ok {
		return false
	}
	item := AutocompleteItem{Value: selected.Value, Label: selected.Label, Description: selected.Description}
	lineIdx := e.buffer.cursorLine
	line := e.buffer.lines[lineIdx]
	prefix := e.autocompletePrefix
	kind := e.autocompleteKind
	e.buffer.pushUndo()
	var completed string
	var newCol int
	if kind == "slash" {
		completed, newCol = e.provider.ApplySlashCompletion(line, e.buffer.cursorCol, item, prefix)
	} else {
		completed, newCol = e.provider.ApplyPathCompletion(line, e.buffer.cursorCol, item, prefix)
	}
	e.buffer.lines[lineIdx] = completed
	e.buffer.cursorCol = newCol
	if e.buffer.cursorCol > len(completed) {
		e.buffer.cursorCol = len(completed)
	}
	e.buffer.lastAction = ""
	e.buffer.hasPreferred = false
	e.dismissAutocompleteLocked()
	e.updateBorderLocked()
	return true
}
