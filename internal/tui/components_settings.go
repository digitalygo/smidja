package tui

import "strings"

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
	navigateAfterClose    string
	hasNavigateAfterClose bool
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

func (s *SettingsList) UpdateValue(id, newValue string) {
	for index := range s.items {
		if s.items[index].ID == id {
			s.items[index].CurrentValue = newValue
		}
	}
}

func (s *SettingsList) SelectItem(id string) {
	items := s.displayItems()
	for index, item := range items {
		if item.ID == id {
			s.selectedIndex = index
			return
		}
	}
}

func (s *SettingsList) displayItems() []SettingItem {
	if s.searchEnabled {
		return s.filtered
	}
	return s.items
}

func (s *SettingsList) Invalidate() {
	if s.submenuComponent != nil {
		s.submenuComponent.Invalidate()
	}
}

func (s *SettingsList) Render(width int) []string {
	if s.submenuComponent != nil {
		return s.submenuComponent.Render(width)
	}
	return s.renderMainList(width)
}

func (s *SettingsList) renderMainList(width int) []string {
	var lines []string
	if s.searchEnabled && s.searchInput != nil {
		lines = append(lines, s.searchInput.Render(width)...)
		lines = append(lines, "")
	}

	if len(s.items) == 0 {
		lines = append(lines, s.theme.Hint("  No settings available"))
		if s.searchEnabled {
			s.addHintLine(&lines, width)
		}
		return lines
	}

	displayItems := s.displayItems()
	if len(displayItems) == 0 {
		lines = append(lines, TruncateToWidth(s.theme.Hint("  No matching settings"), width, "...", false))
		s.addHintLine(&lines, width)
		return lines
	}

	startIndex, endIndex := s.visibleRange(displayItems)
	maxLabelWidth := 0
	for _, item := range s.items {
		maxLabelWidth = maxInt(maxLabelWidth, VisibleWidth(item.Label))
	}
	maxLabelWidth = minInt(36, maxLabelWidth)

	for i := startIndex; i < endIndex; i++ {
		item := displayItems[i]
		isSelected := i == s.selectedIndex
		prefix := "  "
		if isSelected {
			prefix = s.theme.Cursor
		}
		prefixWidth := VisibleWidth(prefix)
		labelPadded := item.Label + strings.Repeat(" ", maxInt(0, maxLabelWidth-VisibleWidth(item.Label)))
		labelText := s.theme.Label(labelPadded, isSelected)
		separator := "  "
		usedWidth := prefixWidth + maxLabelWidth + VisibleWidth(separator)
		valueMaxWidth := width - usedWidth - 2
		valueText := s.theme.Value(TruncateToWidth(item.CurrentValue, maxInt(1, valueMaxWidth), "...", false), isSelected)
		lines = append(lines, TruncateToWidth(prefix+labelText+separator+valueText, width, "...", false))
	}

	if startIndex > 0 || endIndex < len(displayItems) {
		scrollText := "  (" + itoa(s.selectedIndex+1) + "/" + itoa(len(displayItems)) + ")"
		lines = append(lines, s.theme.Hint(TruncateToWidth(scrollText, maxInt(0, width-2), "", false)))
	}

	if s.selectedIndex < len(displayItems) && displayItems[s.selectedIndex].Description != "" {
		lines = append(lines, "")
		wrapped := WrapTextWithANSI(displayItems[s.selectedIndex].Description, maxInt(1, width-4))
		for _, line := range wrapped {
			lines = append(lines, s.theme.Description("  "+line))
		}
	}

	s.addHintLine(&lines, width)
	return lines
}

func (s *SettingsList) addHintLine(lines *[]string, width int) {
	hint := "  Enter/Space to change · Esc to cancel"
	if s.searchEnabled {
		hint = "  Type to search · Enter/Space to change · Esc to cancel"
	}
	*lines = append(*lines, "")
	*lines = append(*lines, TruncateToWidth(s.theme.Hint(hint), width, "...", false))
}

func (s *SettingsList) visibleRange(displayItems []SettingItem) (int, int) {
	startIndex := s.selectedIndex - s.maxVisible/2
	startIndex = maxInt(0, minInt(startIndex, len(displayItems)-s.maxVisible))
	endIndex := minInt(startIndex+s.maxVisible, len(displayItems))
	return startIndex, endIndex
}

func (s *SettingsList) HandleInput(data string) {
	if s.submenuComponent != nil {
		if handler, ok := s.submenuComponent.(InputHandler); ok {
			handler.HandleInput(data)
		}
		return
	}

	keybindings := GlobalKeybindings()
	displayItems := s.displayItems()
	switch {
	case keybindings.Matches(data, "tui.select.up"):
		if len(displayItems) == 0 {
			return
		}
		if s.selectedIndex == 0 {
			s.selectedIndex = len(displayItems) - 1
		} else {
			s.selectedIndex--
		}
	case keybindings.Matches(data, "tui.select.down"):
		if len(displayItems) == 0 {
			return
		}
		if s.selectedIndex == len(displayItems)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
	case keybindings.Matches(data, "tui.select.confirm") ||
		(data == " " && (!s.searchEnabled || s.searchInput.Value() == "")):
		s.activateItem()
	case keybindings.Matches(data, "tui.select.cancel"):
		if s.onCancel != nil {
			s.onCancel()
		}
	case s.searchEnabled && s.searchInput != nil:
		s.searchInput.HandleInput(data)
		s.applyFilter(s.searchInput.Value())
	}
}

func (s *SettingsList) applyFilter(query string) {
	s.filtered = FuzzyFilter(s.items, query, func(item SettingItem) string { return item.Label })
	s.selectedIndex = 0
}

func (s *SettingsList) activateItem() {
	displayItems := s.displayItems()
	if s.selectedIndex < 0 || s.selectedIndex >= len(displayItems) {
		return
	}
	item := displayItems[s.selectedIndex]

	if item.Submenu != nil {
		index := s.selectedIndex
		s.submenuItemIndex = &index
		s.submenuComponent = item.Submenu(item.CurrentValue, func(selectedValue string, navigateTo string) {
			if selectedValue != "" {
				item.CurrentValue = selectedValue
				for i := range s.items {
					if s.items[i].ID == item.ID {
						s.items[i].CurrentValue = selectedValue
					}
				}
				if s.onChange != nil {
					s.onChange(item.ID, selectedValue)
				}
			}
			if navigateTo != "" {
				s.navigateAfterClose = navigateTo
				s.hasNavigateAfterClose = true
			}
			s.closeSubmenu()
		})
		return
	}
	if len(item.Values) > 0 {
		currentIndex := 0
		for i, value := range item.Values {
			if value == item.CurrentValue {
				currentIndex = i
				break
			}
		}
		newValue := item.Values[(currentIndex+1)%len(item.Values)]
		for i := range s.items {
			if s.items[i].ID == item.ID {
				s.items[i].CurrentValue = newValue
			}
		}
		if s.onChange != nil {
			s.onChange(item.ID, newValue)
		}
	}
}

func (s *SettingsList) closeSubmenu() {
	s.submenuComponent = nil
	if s.hasNavigateAfterClose {
		id := s.navigateAfterClose
		s.navigateAfterClose = ""
		s.hasNavigateAfterClose = false
		s.submenuItemIndex = nil
		s.SelectItem(id)
		s.activateItem()
		return
	}
	if s.submenuItemIndex != nil {
		s.selectedIndex = *s.submenuItemIndex
		s.submenuItemIndex = nil
	}
}

func (s *SettingsList) HandleMouse(event MouseEvent) *MouseEventResult {
	if s.submenuComponent != nil {
		if handler, ok := s.submenuComponent.(MouseHandler); ok {
			if result := handler.HandleMouse(event); result != nil {
				result.Focus = true
				return result
			}
		}
		return nil
	}

	if s.searchEnabled && s.searchInput != nil {
		if event.Y == 0 {
			if result := s.searchInput.HandleMouse(event); result != nil {
				result.Focus = true
				return result
			}
			return nil
		}
		if event.Y == 1 {
			return nil
		}
	}

	displayItems := s.displayItems()
	if len(displayItems) == 0 {
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != 0 {
		delta := -1
		if event.WheelDelta > 0 {
			delta = 1
		}
		previous := s.selectedIndex
		s.selectedIndex = maxInt(0, minInt(len(displayItems)-1, s.selectedIndex+delta))
		return &MouseEventResult{Handled: true, Render: s.selectedIndex != previous, renderSet: true}
	}
	if event.Button != MouseButtonLeft || (event.Type != MousePress && event.Type != MouseClick) {
		return nil
	}

	rowOffset := 0
	if s.searchEnabled {
		rowOffset = 2
	}
	startIndex, endIndex := s.visibleRange(displayItems)
	itemIndex := startIndex + event.Y - rowOffset
	if itemIndex < startIndex || itemIndex >= endIndex {
		return nil
	}
	if event.Type == MousePress {
		pressed := itemIndex
		s.mousePressed = &pressed
		s.selectedIndex = itemIndex
		return &MouseEventResult{Handled: true, Focus: true}
	}
	clickedIndex := itemIndex
	if s.mousePressed != nil {
		clickedIndex = *s.mousePressed
		s.mousePressed = nil
	}
	s.selectedIndex = clickedIndex
	s.activateItem()
	return &MouseEventResult{Handled: true}
}
