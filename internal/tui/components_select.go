package tui

import (
	"strings"
	"sync"
)

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
	mu            sync.RWMutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyThemeDefaultsLocked()
}

func (s *SelectList) applyThemeDefaultsLocked() {
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

func (s *SelectList) SetOnSelect(callback func(SelectItem)) {
	s.mu.Lock()
	s.onSelect = callback
	s.mu.Unlock()
}

func (s *SelectList) SetOnCancel(callback func()) {
	s.mu.Lock()
	s.onCancel = callback
	s.mu.Unlock()
}

func (s *SelectList) SetOnSelectionChange(callback func(SelectItem)) {
	s.mu.Lock()
	s.onSelectionChange = callback
	s.mu.Unlock()
}

func (s *SelectList) SetTheme(theme SelectListTheme) {
	s.mu.Lock()
	s.theme = theme
	s.applyThemeDefaultsLocked()
	s.mu.Unlock()
}

func (s *SelectList) SetFilter(filter string) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *SelectList) SetFilterFuzzy(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(query) == "" {
		s.filteredItems = append([]SelectItem(nil), s.items...)
		s.selectedIndex = 0
		return
	}
	s.filteredItems = FuzzyFilter(s.items, query, func(item SelectItem) string {
		text := item.Label
		if text == "" {
			text = item.Value
		}
		if item.Description != "" {
			text = text + " " + item.Description
		}
		return text
	})
	s.selectedIndex = 0
}

func (s *SelectList) SetSelectedIndex(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedIndex = maxInt(0, minInt(index, len(s.filteredItems)-1))
}

func (s *SelectList) SelectedItem() (SelectItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selectedItemLocked()
}

func (s *SelectList) selectedItemLocked() (SelectItem, bool) {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredItems) {
		return SelectItem{}, false
	}
	return s.filteredItems[s.selectedIndex], true
}

func (s *SelectList) Invalidate() {}

func sanitizeSelectSingleLine(text string) string {
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

func selectVisibleRange(selectedIndex, count, maxVisible int) (int, int) {
	startIndex := selectedIndex - maxVisible/2
	startIndex = maxInt(0, minInt(startIndex, count-maxVisible))
	endIndex := minInt(startIndex+maxVisible, count)
	return startIndex, endIndex
}

func (s *SelectList) Render(width int) []string {
	s.mu.RLock()
	theme := s.theme
	items := append([]SelectItem(nil), s.filteredItems...)
	selectedIndex := s.selectedIndex
	maxVisible := s.maxVisible
	layout := s.layout
	s.mu.RUnlock()
	return renderSelectList(width, theme, items, selectedIndex, maxVisible, layout)
}

func renderSelectList(width int, theme SelectListTheme, items []SelectItem, selectedIndex, maxVisible int, layout SelectListLayout) []string {
	var lines []string
	if len(items) == 0 {
		lines = append(lines, theme.NoMatch("  No matching commands"))
		return lines
	}

	primaryColumnWidth := computeSelectPrimaryColumnWidth(items, layout)
	startIndex, endIndex := selectVisibleRange(selectedIndex, len(items), maxVisible)
	for i := startIndex; i < endIndex; i++ {
		item := items[i]
		isSelected := i == selectedIndex
		description := ""
		if item.Description != "" {
			description = sanitizeSelectSingleLine(item.Description)
		}
		lines = append(lines, renderSelectItem(theme, item, isSelected, width, description, primaryColumnWidth))
	}

	if startIndex > 0 || endIndex < len(items) {
		scrollText := "  (" + itoa(selectedIndex+1) + "/" + itoa(len(items)) + ")"
		lines = append(lines, theme.ScrollInfo(TruncateToWidth(scrollText, maxInt(0, width-2), "", false)))
	}
	return lines
}

const (
	selectPrimaryColumnWidth  = 32
	selectPrimaryColumnGap    = 2
	selectMinDescriptionWidth = 10
)

func computeSelectPrimaryColumnWidth(items []SelectItem, layout SelectListLayout) int {
	rawMin := layout.MinPrimaryColumnWidth
	rawMax := layout.MaxPrimaryColumnWidth
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
	for _, item := range items {
		width := VisibleWidth(selectDisplayValue(item)) + selectPrimaryColumnGap
		widest = maxInt(widest, width)
	}
	return maxInt(minWidth, minInt(widest, maxWidth))
}

func selectDisplayValue(item SelectItem) string {
	if item.Label != "" {
		return sanitizeSelectSingleLine(item.Label)
	}
	return sanitizeSelectSingleLine(item.Value)
}

func renderSelectItem(theme SelectListTheme, item SelectItem, isSelected bool, width int, description string, primaryColumnWidth int) string {
	prefix := "  "
	if isSelected {
		prefix = "→ "
	}
	prefixWidth := VisibleWidth(prefix)

	if description != "" && width > 40 {
		effectivePrimary := maxInt(1, minInt(primaryColumnWidth, width-prefixWidth-4))
		maxPrimaryWidth := maxInt(1, effectivePrimary-selectPrimaryColumnGap)
		truncatedValue := TruncateToWidth(selectDisplayValue(item), maxPrimaryWidth, "", false)
		truncatedValueWidth := VisibleWidth(truncatedValue)
		spacing := strings.Repeat(" ", maxInt(1, effectivePrimary-truncatedValueWidth))
		descriptionStart := prefixWidth + truncatedValueWidth + len(spacing)
		remainingWidth := width - descriptionStart - 2

		if remainingWidth > selectMinDescriptionWidth {
			truncatedDesc := TruncateToWidth(description, remainingWidth, "", false)
			if isSelected {
				return theme.SelectedText(prefix + truncatedValue + spacing + truncatedDesc)
			}
			return prefix + truncatedValue + theme.Description(spacing+truncatedDesc)
		}
	}

	maxWidth := width - prefixWidth - 2
	truncatedValue := TruncateToWidth(selectDisplayValue(item), maxInt(1, maxWidth), "", false)
	if isSelected {
		return theme.SelectedText(prefix + truncatedValue)
	}
	return prefix + truncatedValue
}

func (s *SelectList) HandleInput(data string) {
	s.mu.Lock()
	keybindings := GlobalKeybindings()
	count := len(s.filteredItems)
	if count == 0 {
		cancel := s.onCancel
		matched := keybindings.Matches(data, "tui.select.cancel")
		s.mu.Unlock()
		if matched && cancel != nil {
			cancel()
		}
		return
	}
	var selectItem SelectItem
	var selectCallback func(SelectItem)
	var cancelCallback func()
	var changeItem SelectItem
	var changeCallback func(SelectItem)
	hasSelect := false
	hasChange := false
	hasCancel := false
	switch {
	case keybindings.Matches(data, "tui.select.up"):
		if s.selectedIndex == 0 {
			s.selectedIndex = count - 1
		} else {
			s.selectedIndex--
		}
		if item, ok := s.selectedItemLocked(); ok {
			changeItem, changeCallback, hasChange = item, s.onSelectionChange, s.onSelectionChange != nil
		}
	case keybindings.Matches(data, "tui.select.down"):
		if s.selectedIndex == count-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		if item, ok := s.selectedItemLocked(); ok {
			changeItem, changeCallback, hasChange = item, s.onSelectionChange, s.onSelectionChange != nil
		}
	case keybindings.Matches(data, "tui.select.confirm"):
		if item, ok := s.selectedItemLocked(); ok && s.onSelect != nil {
			selectItem, selectCallback, hasSelect = item, s.onSelect, true
		}
	case keybindings.Matches(data, "tui.select.cancel"):
		if s.onCancel != nil {
			cancelCallback, hasCancel = s.onCancel, true
		}
	}
	s.mu.Unlock()
	if hasChange && changeCallback != nil {
		changeCallback(changeItem)
	}
	if hasSelect && selectCallback != nil {
		selectCallback(selectItem)
	}
	if hasCancel && cancelCallback != nil {
		cancelCallback()
	}
}

func (s *SelectList) HandleMouse(event MouseEvent) *MouseEventResult {
	s.mu.Lock()
	if len(s.filteredItems) == 0 {
		s.mu.Unlock()
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != 0 {
		delta := -1
		if event.WheelDelta > 0 {
			delta = 1
		}
		previous := s.selectedIndex
		s.selectedIndex = maxInt(0, minInt(len(s.filteredItems)-1, s.selectedIndex+delta))
		changed := s.selectedIndex != previous
		var changeItem SelectItem
		var changeCallback func(SelectItem)
		if changed {
			if item, ok := s.selectedItemLocked(); ok {
				changeItem, changeCallback = item, s.onSelectionChange
			}
		}
		s.mu.Unlock()
		if changed && changeCallback != nil {
			changeCallback(changeItem)
			return &MouseEventResult{Handled: true, Render: true, renderSet: true}
		}
		if changed {
			return &MouseEventResult{Handled: true, Render: true, renderSet: true}
		}
		return &MouseEventResult{Handled: true, Render: false, renderSet: true}
	}
	if event.Button != MouseButtonLeft || (event.Type != MousePress && event.Type != MouseClick) {
		s.mu.Unlock()
		return nil
	}
	startIndex, endIndex := selectVisibleRange(s.selectedIndex, len(s.filteredItems), s.maxVisible)
	itemIndex := startIndex + event.Y
	if itemIndex < startIndex || itemIndex >= endIndex {
		s.mu.Unlock()
		return nil
	}

	if event.Type == MousePress {
		pressed := itemIndex
		s.mousePressed = &pressed
		changed := s.selectedIndex != itemIndex
		s.selectedIndex = itemIndex
		var changeItem SelectItem
		var changeCallback func(SelectItem)
		if changed {
			if item, ok := s.selectedItemLocked(); ok {
				changeItem, changeCallback = item, s.onSelectionChange
			}
		}
		s.mu.Unlock()
		if changed && changeCallback != nil {
			changeCallback(changeItem)
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
	var selectItem SelectItem
	var selectCallback func(SelectItem)
	var changeItem SelectItem
	var changeCallback func(SelectItem)
	hasSelect := false
	hasChange := false
	if changed {
		if item, ok := s.selectedItemLocked(); ok {
			changeItem, changeCallback, hasChange = item, s.onSelectionChange, s.onSelectionChange != nil
		}
	}
	if item, ok := s.selectedItemLocked(); ok && s.onSelect != nil {
		selectItem, selectCallback, hasSelect = item, s.onSelect, true
	}
	s.mu.Unlock()
	if hasChange && changeCallback != nil {
		changeCallback(changeItem)
	}
	if hasSelect && selectCallback != nil {
		selectCallback(selectItem)
	}
	return &MouseEventResult{Handled: true}
}
