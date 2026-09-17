package tui

import (
	"strings"
	"sync"
	"sync/atomic"
)

type SettingItem struct {
	ID           string
	Label        string
	Description  string
	CurrentValue string
	Values       []string
	Submenu      func(currentValue string, done func(selectedValue string, navigateTo string)) Component
}

type SettingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description func(text string) string
	Cursor      string
	Hint        func(text string) string
}

func DefaultSettingsListTheme(accent, muted, dim func(string) string) SettingsListTheme {
	if accent == nil {
		accent = func(text string) string { return text }
	}
	if muted == nil {
		muted = func(text string) string { return text }
	}
	if dim == nil {
		dim = func(text string) string { return text }
	}
	return SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return accent(text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return accent(text)
			}
			return muted(text)
		},
		Description: dim,
		Cursor:      accent("→ "),
		Hint:        dim,
	}
}

type SettingsList struct {
	mu            sync.RWMutex
	items         []SettingItem
	filtered      []SettingItem
	theme         SettingsListTheme
	selectedIndex int
	mousePressed  *int
	maxVisible    int
	onChange      func(id, newValue string)
	onCancel      func()
	searchInput   *Input
	searchEnabled bool

	submenuComponent      Component
	submenuItemIndex      *int
	applySubmenuTheme     func(Component)
	navigateAfterClose    string
	hasNavigateAfterClose bool
}

type settingsRenderState struct {
	theme         SettingsListTheme
	allItems      []SettingItem
	displayItems  []SettingItem
	selectedIndex int
	maxVisible    int
	searchEnabled bool
	searchInput   *Input
}

func NewSettingsList(items []SettingItem, maxVisible int, theme SettingsListTheme,
	onChange func(id, newValue string), onCancel func(), enableSearch bool) *SettingsList {

	if maxVisible <= 0 {
		maxVisible = 10
	}
	list := &SettingsList{
		items:         append([]SettingItem(nil), items...),
		theme:         theme,
		maxVisible:    maxVisible,
		onChange:      onChange,
		onCancel:      onCancel,
		searchEnabled: enableSearch,
	}
	list.filtered = append([]SettingItem(nil), items...)
	list.applyThemeDefaults()
	if enableSearch {
		list.searchInput = NewInput(InputOptions{})
	}
	return list
}

func (s *SettingsList) applyThemeDefaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyThemeDefaultsLocked()
}

func (s *SettingsList) applyThemeDefaultsLocked() {
	if s.theme.Label == nil {
		s.theme.Label = func(text string, selected bool) string { return text }
	}
	if s.theme.Value == nil {
		s.theme.Value = func(text string, selected bool) string { return text }
	}
	if s.theme.Description == nil {
		s.theme.Description = func(text string) string { return text }
	}
	if s.theme.Cursor == "" {
		s.theme.Cursor = "→ "
	}
	if s.theme.Hint == nil {
		s.theme.Hint = func(text string) string { return text }
	}
}

func (s *SettingsList) SetTheme(theme SettingsListTheme) {
	s.mu.Lock()
	s.theme = theme
	s.applyThemeDefaultsLocked()
	s.mu.Unlock()
}

func (s *SettingsList) SetSubmenuTheme(applier func(Component)) {
	s.mu.Lock()
	s.applySubmenuTheme = applier
	submenu := s.submenuComponent
	s.mu.Unlock()
	if submenu != nil && applier != nil {
		applier(submenu)
	}
}

func sanitizeSettingsSingleLine(text string) string {
	if text == "" {
		return ""
	}
	stripped := StripTerminalSequences(text)
	stripped = strings.ReplaceAll(stripped, "\r", " ")
	stripped = strings.ReplaceAll(stripped, "\n", " ")
	stripped = strings.ReplaceAll(stripped, "\t", "   ")
	var b strings.Builder
	b.Grow(len(stripped))
	for _, r := range stripped {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *SettingsList) UpdateValue(id, newValue string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateValueLocked(id, newValue)
}

func (s *SettingsList) updateValueLocked(id, newValue string) {
	for index := range s.items {
		if s.items[index].ID == id {
			s.items[index].CurrentValue = newValue
		}
	}
	for index := range s.filtered {
		if s.filtered[index].ID == id {
			s.filtered[index].CurrentValue = newValue
		}
	}
}

func (s *SettingsList) SelectItem(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectItemLocked(id)
}

func (s *SettingsList) selectItemLocked(id string) {
	items := s.displayItemsLocked()
	for index, item := range items {
		if item.ID == id {
			s.selectedIndex = index
			return
		}
	}
}

func (s *SettingsList) displayItemsLocked() []SettingItem {
	if s.searchEnabled {
		return s.filtered
	}
	return s.items
}

func (s *SettingsList) displayItems() []SettingItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.displayItemsLocked()
}

func (s *SettingsList) Invalidate() {
	s.mu.RLock()
	submenu := s.submenuComponent
	s.mu.RUnlock()
	if submenu != nil {
		submenu.Invalidate()
	}
}

func (s *SettingsList) Render(width int) []string {
	s.mu.RLock()
	submenu := s.submenuComponent
	if submenu == nil {
		state := s.renderStateLocked()
		s.mu.RUnlock()
		return renderSettingsList(width, state)
	}
	s.mu.RUnlock()
	return submenu.Render(width)
}

func (s *SettingsList) renderStateLocked() settingsRenderState {
	return settingsRenderState{
		theme:         s.theme,
		allItems:      append([]SettingItem(nil), s.items...),
		displayItems:  append([]SettingItem(nil), s.displayItemsLocked()...),
		selectedIndex: s.selectedIndex,
		maxVisible:    s.maxVisible,
		searchEnabled: s.searchEnabled,
		searchInput:   s.searchInput,
	}
}

func renderSettingsList(width int, state settingsRenderState) []string {
	var lines []string
	if state.searchEnabled && state.searchInput != nil {
		lines = append(lines, state.searchInput.Render(width)...)
		lines = append(lines, "")
	}

	if len(state.allItems) == 0 {
		lines = append(lines, state.theme.Hint("  No settings available"))
		if state.searchEnabled {
			settingsAddHintLine(&lines, width, state.theme, true)
		}
		return lines
	}

	if len(state.displayItems) == 0 {
		lines = append(lines, TruncateToWidth(state.theme.Hint("  No matching settings"), width, "...", false))
		settingsAddHintLine(&lines, width, state.theme, state.searchEnabled)
		return lines
	}

	startIndex, endIndex := settingsVisibleRange(state.selectedIndex, len(state.displayItems), state.maxVisible)
	maxLabelWidth := 0
	for _, item := range state.allItems {
		maxLabelWidth = maxInt(maxLabelWidth, VisibleWidth(sanitizeSettingsSingleLine(item.Label)))
	}
	maxLabelWidth = minInt(36, maxLabelWidth)

	for i := startIndex; i < endIndex; i++ {
		item := state.displayItems[i]
		isSelected := i == state.selectedIndex
		prefix := "  "
		if isSelected {
			prefix = state.theme.Cursor
		}
		prefixWidth := VisibleWidth(prefix)
		sanitizedLabel := sanitizeSettingsSingleLine(item.Label)
		sanitizedValue := sanitizeSettingsSingleLine(item.CurrentValue)
		labelPadded := sanitizedLabel + strings.Repeat(" ", maxInt(0, maxLabelWidth-VisibleWidth(sanitizedLabel)))
		labelText := state.theme.Label(labelPadded, isSelected)
		separator := "  "
		usedWidth := prefixWidth + maxLabelWidth + VisibleWidth(separator)
		valueMaxWidth := width - usedWidth - 2
		valueText := state.theme.Value(TruncateToWidth(sanitizedValue, maxInt(1, valueMaxWidth), "...", false), isSelected)
		lines = append(lines, TruncateToWidth(prefix+labelText+separator+valueText, width, "...", false))
	}

	if startIndex > 0 || endIndex < len(state.displayItems) {
		scrollText := "  (" + itoa(state.selectedIndex+1) + "/" + itoa(len(state.displayItems)) + ")"
		lines = append(lines, state.theme.Hint(TruncateToWidth(scrollText, maxInt(0, width-2), "", false)))
	}

	if state.selectedIndex >= 0 && state.selectedIndex < len(state.displayItems) && state.displayItems[state.selectedIndex].Description != "" {
		lines = append(lines, "")
		sanitizedDescription := sanitizeSettingsSingleLine(state.displayItems[state.selectedIndex].Description)
		wrapped := WrapTextWithANSI(sanitizedDescription, maxInt(1, width-4))
		for _, line := range wrapped {
			lines = append(lines, state.theme.Description("  "+line))
		}
	}

	settingsAddHintLine(&lines, width, state.theme, state.searchEnabled)
	return lines
}

func settingsAddHintLine(lines *[]string, width int, theme SettingsListTheme, searchEnabled bool) {
	hint := "  Enter/Space to change · Esc to cancel"
	if searchEnabled {
		hint = "  Type to search · Enter/Space to change · Esc to cancel"
	}
	*lines = append(*lines, "")
	*lines = append(*lines, TruncateToWidth(theme.Hint(hint), width, "...", false))
}

func settingsVisibleRange(selectedIndex, count, maxVisible int) (int, int) {
	startIndex := selectedIndex - maxVisible/2
	startIndex = maxInt(0, minInt(startIndex, count-maxVisible))
	endIndex := minInt(startIndex+maxVisible, count)
	return startIndex, endIndex
}

func (s *SettingsList) HandleInput(data string) {
	s.mu.Lock()
	if s.submenuComponent != nil {
		submenu := s.submenuComponent
		s.mu.Unlock()
		if handler, ok := submenu.(InputHandler); ok {
			handler.HandleInput(data)
		}
		return
	}

	keybindings := GlobalKeybindings()
	displayItems := s.displayItemsLocked()
	switch {
	case keybindings.Matches(data, "tui.select.up"):
		if len(displayItems) == 0 {
			s.mu.Unlock()
			return
		}
		if s.selectedIndex == 0 {
			s.selectedIndex = len(displayItems) - 1
		} else {
			s.selectedIndex--
		}
		s.mu.Unlock()
	case keybindings.Matches(data, "tui.select.down"):
		if len(displayItems) == 0 {
			s.mu.Unlock()
			return
		}
		if s.selectedIndex == len(displayItems)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.mu.Unlock()
	case keybindings.Matches(data, "tui.select.confirm") ||
		(data == " " && !s.searchEnabled) ||
		(data == " " && s.searchInput != nil && s.searchInput.Value() == ""):
		item, index, ok := s.selectedSettingLocked()
		s.mu.Unlock()
		if ok {
			s.activate(item, index)
		}
	case keybindings.Matches(data, "tui.select.cancel"):
		cancel := s.onCancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	default:
		searchInput := s.searchInput
		searchEnabled := s.searchEnabled
		s.mu.Unlock()
		if searchEnabled && searchInput != nil {
			searchInput.HandleInput(data)
			s.applyFilter(searchInput.Value())
		}
	}
}

func (s *SettingsList) applyFilter(query string) {
	s.mu.Lock()
	s.filtered = FuzzyFilter(s.items, query, func(item SettingItem) string { return item.Label })
	s.selectedIndex = 0
	s.mu.Unlock()
}

func (s *SettingsList) selectedSettingLocked() (SettingItem, int, bool) {
	items := s.displayItemsLocked()
	if s.selectedIndex < 0 || s.selectedIndex >= len(items) {
		return SettingItem{}, 0, false
	}
	return items[s.selectedIndex], s.selectedIndex, true
}

func (s *SettingsList) activateSelected() {
	s.mu.Lock()
	item, index, ok := s.selectedSettingLocked()
	s.mu.Unlock()
	if ok {
		s.activate(item, index)
	}
}

func (s *SettingsList) activate(item SettingItem, index int) {
	if item.Submenu != nil {
		var settled atomic.Bool
		submenu := item.Submenu(item.CurrentValue, func(selectedValue string, navigateTo string) {
			settled.Store(true)
			s.handleSubmenuDone(item.ID, selectedValue, navigateTo)
		})
		s.mu.Lock()
		if settled.Load() {
			s.mu.Unlock()
			return
		}
		s.submenuItemIndex = &index
		s.submenuComponent = submenu
		applier := s.applySubmenuTheme
		s.mu.Unlock()
		if applier != nil {
			applier(submenu)
		}
		return
	}
	if len(item.Values) == 0 {
		return
	}
	currentIndex := 0
	for i, value := range item.Values {
		if value == item.CurrentValue {
			currentIndex = i
			break
		}
	}
	newValue := item.Values[(currentIndex+1)%len(item.Values)]
	s.mu.Lock()
	s.updateValueLocked(item.ID, newValue)
	onChange := s.onChange
	s.mu.Unlock()
	if onChange != nil {
		onChange(item.ID, newValue)
	}
}

func (s *SettingsList) handleSubmenuDone(id, selectedValue, navigateTo string) {
	s.mu.Lock()
	if selectedValue != "" {
		s.updateValueLocked(id, selectedValue)
	}
	if navigateTo != "" {
		s.navigateAfterClose = navigateTo
		s.hasNavigateAfterClose = true
	}
	onChange := s.onChange
	s.mu.Unlock()
	if selectedValue != "" && onChange != nil {
		onChange(id, selectedValue)
	}
	s.closeSubmenu()
}

func (s *SettingsList) closeSubmenu() {
	s.mu.Lock()
	s.submenuComponent = nil
	if s.hasNavigateAfterClose {
		id := s.navigateAfterClose
		s.navigateAfterClose = ""
		s.hasNavigateAfterClose = false
		s.submenuItemIndex = nil
		s.mu.Unlock()
		s.SelectItem(id)
		s.activateSelected()
		return
	}
	if s.submenuItemIndex != nil {
		s.selectedIndex = *s.submenuItemIndex
		s.submenuItemIndex = nil
	}
	s.mu.Unlock()
}

func (s *SettingsList) HandleMouse(event MouseEvent) *MouseEventResult {
	s.mu.Lock()
	if s.submenuComponent != nil {
		submenu := s.submenuComponent
		s.mu.Unlock()
		if handler, ok := submenu.(MouseHandler); ok {
			if result := handler.HandleMouse(event); result != nil {
				result.Focus = true
				return result
			}
		}
		return nil
	}

	if s.searchEnabled && s.searchInput != nil {
		searchInput := s.searchInput
		if event.Y == 0 {
			s.mu.Unlock()
			if result := searchInput.HandleMouse(event); result != nil {
				result.Focus = true
				return result
			}
			return nil
		}
		if event.Y == 1 {
			s.mu.Unlock()
			return nil
		}
	}

	displayItems := s.displayItemsLocked()
	if len(displayItems) == 0 {
		s.mu.Unlock()
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != 0 {
		delta := -1
		if event.WheelDelta > 0 {
			delta = 1
		}
		previous := s.selectedIndex
		s.selectedIndex = maxInt(0, minInt(len(displayItems)-1, s.selectedIndex+delta))
		changed := s.selectedIndex != previous
		s.mu.Unlock()
		return &MouseEventResult{Handled: true, Render: changed, renderSet: true}
	}
	if event.Button != MouseButtonLeft || (event.Type != MousePress && event.Type != MouseClick) {
		s.mu.Unlock()
		return nil
	}

	rowOffset := 0
	if s.searchEnabled {
		rowOffset = 2
	}
	startIndex, endIndex := settingsVisibleRange(s.selectedIndex, len(displayItems), s.maxVisible)
	itemIndex := startIndex + event.Y - rowOffset
	if itemIndex < startIndex || itemIndex >= endIndex {
		s.mu.Unlock()
		return nil
	}
	if event.Type == MousePress {
		pressed := itemIndex
		s.mousePressed = &pressed
		s.selectedIndex = itemIndex
		s.mu.Unlock()
		return &MouseEventResult{Handled: true, Focus: true}
	}
	clickedIndex := itemIndex
	if s.mousePressed != nil {
		clickedIndex = *s.mousePressed
		s.mousePressed = nil
	}
	s.selectedIndex = clickedIndex
	item, index, ok := s.selectedSettingLocked()
	s.mu.Unlock()
	if ok {
		s.activate(item, index)
	}
	return &MouseEventResult{Handled: true}
}
