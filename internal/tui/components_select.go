package tui

import "strings"

type SelectItem struct {
	Value       string
	Label       string
	Description string
}

type SelectListTheme struct {
	SelectedPrefix func(string) string
	SelectedText   func(string) string
	Description    func(string) string
	ScrollInfo     func(string) string
	NoMatch        func(string) string
}

func DefaultSelectListTheme(accent, muted func(string) string) SelectListTheme {
	return SelectListTheme{
		SelectedPrefix: accent,
		SelectedText:   accent,
		Description:    muted,
		ScrollInfo:     muted,
		NoMatch:        muted,
	}
}

type SelectListLayout struct {
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int
}

type SelectList struct {
	items         []SelectItem
	filteredItems []SelectItem
	selectedIndex int
	mousePressed  *int
	maxVisible    int
	theme         SelectListTheme
	layout        SelectListLayout

	onSelect          func(SelectItem)
	onCancel          func()
	onSelectionChange func(SelectItem)
}

func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme) *SelectList {
	if maxVisible <= 0 {
		maxVisible = 5
	}
	list := &SelectList{
		items:      append([]SelectItem(nil), items...),
		maxVisible: maxVisible,
		theme:      theme,
	}
	list.filteredItems = append([]SelectItem(nil), items...)
	list.applyThemeDefaults()
	return list
}

func (s *SelectList) applyThemeDefaults() {
	if s.theme.SelectedPrefix == nil {
		s.theme.SelectedPrefix = func(text string) string { return text }
	}
	if s.theme.SelectedText == nil {
		s.theme.SelectedText = s.theme.SelectedPrefix
	}
	if s.theme.Description == nil {
		s.theme.Description = func(text string) string { return text }
	}
	if s.theme.ScrollInfo == nil {
		s.theme.ScrollInfo = func(text string) string { return text }
	}
	if s.theme.NoMatch == nil {
		s.theme.NoMatch = func(text string) string { return text }
	}
}

func (s *SelectList) SetOnSelect(callback func(SelectItem)) { s.onSelect = callback }
func (s *SelectList) SetOnCancel(callback func())           { s.onCancel = callback }
func (s *SelectList) SetOnSelectionChange(callback func(SelectItem)) {
	s.onSelectionChange = callback
}

func (s *SelectList) SetFilter(filter string) {
	lowered := strings.ToLower(filter)
	filtered := make([]SelectItem, 0, len(s.items))
	for _, item := range s.items {
		if strings.HasPrefix(strings.ToLower(item.Value), lowered) {
			filtered = append(filtered, item)
		}
	}
	s.filteredItems = filtered
	s.selectedIndex = 0
}

func (s *SelectList) SetSelectedIndex(index int) {
	s.selectedIndex = maxInt(0, minInt(index, len(s.filteredItems)-1))
}

func (s *SelectList) SelectedItem() (SelectItem, bool) {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredItems) {
		return SelectItem{}, false
	}
	return s.filteredItems[s.selectedIndex], true
}

func (s *SelectList) Invalidate() {}

func (s *SelectList) visibleRange() (int, int) {
	startIndex := s.selectedIndex - s.maxVisible/2
	startIndex = maxInt(0, minInt(startIndex, len(s.filteredItems)-s.maxVisible))
	endIndex := minInt(startIndex+s.maxVisible, len(s.filteredItems))
	return startIndex, endIndex
}

func (s *SelectList) Render(width int) []string {
	var lines []string
	if len(s.filteredItems) == 0 {
		lines = append(lines, s.theme.NoMatch("  No matching commands"))
		return lines
	}

	primaryColumnWidth := s.primaryColumnWidth()
	startIndex, endIndex := s.visibleRange()
	for i := startIndex; i < endIndex; i++ {
		item := s.filteredItems[i]
		isSelected := i == s.selectedIndex
		description := ""
		if item.Description != "" {
			description = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(item.Description, "\r", " "), "\n", " "))
		}
		lines = append(lines, s.renderItem(item, isSelected, width, description, primaryColumnWidth))
	}

	if startIndex > 0 || endIndex < len(s.filteredItems) {
		scrollText := "  (" + itoa(s.selectedIndex+1) + "/" + itoa(len(s.filteredItems)) + ")"
		lines = append(lines, s.theme.ScrollInfo(TruncateToWidth(scrollText, maxInt(0, width-2), "", false)))
	}
	return lines
}

const (
	selectPrimaryColumnWidth  = 32
	selectPrimaryColumnGap    = 2
	selectMinDescriptionWidth = 10
)

func (s *SelectList) primaryColumnWidth() int {
	rawMin := s.layout.MinPrimaryColumnWidth
	rawMax := s.layout.MaxPrimaryColumnWidth
	if rawMin == 0 && rawMax == 0 {
		rawMin, rawMax = selectPrimaryColumnWidth, selectPrimaryColumnWidth
	}
	if rawMin == 0 {
		rawMin = rawMax
	}
	if rawMax == 0 {
		rawMax = rawMin
	}
	minWidth := maxInt(1, minInt(rawMin, rawMax))
	maxWidth := maxInt(1, maxInt(rawMin, rawMax))

	widest := 0
	for _, item := range s.filteredItems {
		width := VisibleWidth(s.displayValue(item)) + selectPrimaryColumnGap
		widest = maxInt(widest, width)
	}
	return maxInt(minWidth, minInt(widest, maxWidth))
}

func (s *SelectList) displayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

func (s *SelectList) renderItem(item SelectItem, isSelected bool, width int, description string, primaryColumnWidth int) string {
	prefix := "  "
	if isSelected {
		prefix = "→ "
	}
	prefixWidth := VisibleWidth(prefix)

	if description != "" && width > 40 {
		effectivePrimary := maxInt(1, minInt(primaryColumnWidth, width-prefixWidth-4))
		maxPrimaryWidth := maxInt(1, effectivePrimary-selectPrimaryColumnGap)
		truncatedValue := TruncateToWidth(s.displayValue(item), maxPrimaryWidth, "", false)
		truncatedValueWidth := VisibleWidth(truncatedValue)
		spacing := strings.Repeat(" ", maxInt(1, effectivePrimary-truncatedValueWidth))
		descriptionStart := prefixWidth + truncatedValueWidth + len(spacing)
		remainingWidth := width - descriptionStart - 2

		if remainingWidth > selectMinDescriptionWidth {
			truncatedDesc := TruncateToWidth(description, remainingWidth, "", false)
			if isSelected {
				return s.theme.SelectedText(prefix + truncatedValue + spacing + truncatedDesc)
			}
			return prefix + truncatedValue + s.theme.Description(spacing+truncatedDesc)
		}
	}

	maxWidth := width - prefixWidth - 2
	truncatedValue := TruncateToWidth(s.displayValue(item), maxInt(1, maxWidth), "", false)
	if isSelected {
		return s.theme.SelectedText(prefix + truncatedValue)
	}
	return prefix + truncatedValue
}

func (s *SelectList) HandleInput(data string) {
	keybindings := GlobalKeybindings()
	count := len(s.filteredItems)
	if count == 0 {
		if keybindings.Matches(data, "tui.select.cancel") && s.onCancel != nil {
			s.onCancel()
		}
		return
	}
	switch {
	case keybindings.Matches(data, "tui.select.up"):
		if s.selectedIndex == 0 {
			s.selectedIndex = count - 1
		} else {
			s.selectedIndex--
		}
		s.notifySelectionChange()
	case keybindings.Matches(data, "tui.select.down"):
		if s.selectedIndex == count-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.notifySelectionChange()
	case keybindings.Matches(data, "tui.select.confirm"):
		if item, ok := s.SelectedItem(); ok && s.onSelect != nil {
			s.onSelect(item)
		}
	case keybindings.Matches(data, "tui.select.cancel"):
		if s.onCancel != nil {
			s.onCancel()
		}
	}
}

func (s *SelectList) notifySelectionChange() {
	if item, ok := s.SelectedItem(); ok && s.onSelectionChange != nil {
		s.onSelectionChange(item)
	}
}

func (s *SelectList) HandleMouse(event MouseEvent) *MouseEventResult {
	if len(s.filteredItems) == 0 {
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != 0 {
		delta := -1
		if event.WheelDelta > 0 {
			delta = 1
		}
		previous := s.selectedIndex
		s.selectedIndex = maxInt(0, minInt(len(s.filteredItems)-1, s.selectedIndex+delta))
		if s.selectedIndex != previous {
			s.notifySelectionChange()
			return &MouseEventResult{Handled: true, Render: true, renderSet: true}
		}
		return &MouseEventResult{Handled: true, Render: false, renderSet: true}
	}
	if event.Button != MouseButtonLeft || (event.Type != MousePress && event.Type != MouseClick) {
		return nil
	}
	startIndex, endIndex := s.visibleRange()
	itemIndex := startIndex + event.Y
	if itemIndex < startIndex || itemIndex >= endIndex {
		return nil
	}

	if event.Type == MousePress {
		pressed := itemIndex
		s.mousePressed = &pressed
		if s.selectedIndex != itemIndex {
			s.selectedIndex = itemIndex
			s.notifySelectionChange()
		}
		return &MouseEventResult{Handled: true, Focus: true}
	}
	clickedIndex := itemIndex
	if s.mousePressed != nil {
		clickedIndex = *s.mousePressed
		s.mousePressed = nil
	}
	changed := s.selectedIndex != clickedIndex
	s.selectedIndex = clickedIndex
	if changed {
		s.notifySelectionChange()
	}
	if item, ok := s.SelectedItem(); ok && s.onSelect != nil {
		s.onSelect(item)
	}
	return &MouseEventResult{Handled: true}
}
