package tui

type Stack struct {
	Container
	entries  []StackLayoutEntry
	gap      int
	align    StackAlign
	vertical bool
}

func NewVStack(gap int, align StackAlign) *Stack {
	return &Stack{gap: gap, align: align, vertical: true}
}

func NewHStack(gap int, align StackAlign) *Stack {
	return &Stack{gap: gap, align: align}
}

func (s *Stack) AddChild(component Component) {
	s.Container.AddChild(component)
	s.entries = append(s.entries, StackLayoutEntry{Component: component})
}

func (s *Stack) AddChildWithOptions(component Component, options StackEntryOptions) {
	s.Container.AddChild(component)
	s.entries = append(s.entries, StackLayoutEntry{Component: component, Options: options})
}

func (s *Stack) RemoveChild(component Component) {
	s.Container.RemoveChild(component)
	for index, entry := range s.entries {
		if entry.Component == component {
			s.entries = append(s.entries[:index], s.entries[index+1:]...)
			return
		}
	}
}

func (s *Stack) Clear() {
	s.Container.Clear()
	s.entries = nil
}

func (s *Stack) Invalidate() {
	for _, entry := range s.entries {
		entry.Component.Invalidate()
	}
}

func (s *Stack) StackLayout() StackLayoutSpec {
	return StackLayoutSpec{Vertical: s.vertical, Entries: s.entries, Gap: s.gap, Align: s.align}
}

func (s *Stack) entryRenderWidths(entries []StackLayoutEntry, width int) [][]string {
	rendered := make([][]string, len(entries))
	for index, entry := range entries {
		if entry.Options.Basis != nil && !s.vertical {
			continue
		}
		rendered[index] = entry.Component.Render(width)
	}
	return rendered
}

func (s *Stack) Render(width int) []string {
	safeWidth := maxInt(1, width)
	viewport := Viewport{Width: safeWidth, Height: 1 << 30}
	entries := visibleStackEntries(s.entries, viewport)
	if len(entries) == 0 {
		return nil
	}
	if s.vertical {
		return s.renderVertical(entries, safeWidth)
	}
	return s.renderHorizontal(entries, safeWidth)
}

func (s *Stack) renderVertical(entries []StackLayoutEntry, safeWidth int) []string {
	intrinsicHeights := make([]int, len(entries))
	rendered := make([][]string, len(entries))
	for index, entry := range entries {
		if entry.Options.Basis != nil {
			intrinsicHeights[index] = *entry.Options.Basis
		} else {
			rendered[index] = entry.Component.Render(safeWidth)
			intrinsicHeights[index] = len(rendered[index])
		}
	}
	sizes := AllocateStackSizes(entries, intrinsicHeights, 0, false, s.gap)

	var lines []string
	for index := range entries {
		if index > 0 {
			for gap := 0; gap < s.gap; gap++ {
				lines = append(lines, "")
			}
		}
		childLines := rendered[index]
		if childLines == nil {
			childLines = entries[index].Component.Render(safeWidth)
		}
		if len(childLines) > sizes[index] {
			childLines = childLines[:sizes[index]]
		}
		lines = append(lines, childLines...)
		for padding := len(childLines); padding < sizes[index]; padding++ {
			lines = append(lines, "")
		}
	}
	return lines
}

func (s *Stack) renderHorizontal(entries []StackLayoutEntry, safeWidth int) []string {
	intrinsicWidths := make([]int, len(entries))
	for index, entry := range entries {
		if entry.Options.Basis != nil {
			intrinsicWidths[index] = *entry.Options.Basis
		} else {
			intrinsicWidths[index] = measureWidthOf(entry.Component, safeWidth)
		}
	}
	widths := AllocateStackSizes(entries, intrinsicWidths, safeWidth, true, s.gap)

	rendered := make([][]string, len(entries))
	height := 0
	for index, entry := range entries {
		if widths[index] == 0 {
			continue
		}
		rendered[index] = entry.Component.Render(widths[index])
		if len(rendered[index]) > height {
			height = len(rendered[index])
		}
	}
	result := make([]string, height)
	x := 0
	for index := range entries {
		childLines := rendered[index]
		childWidth := widths[index]
		offset := 0
		switch s.align {
		case AlignCenter:
			offset = (height - len(childLines)) / 2
		case AlignEnd:
			offset = height - len(childLines)
		}
		for row := 0; row < len(childLines); row++ {
			target := row + offset
			if target < 0 || target >= len(result) {
				continue
			}
			result[target] = CompositeTuiLine(result[target], childLines[row], x, childWidth, safeWidth)
		}
		x += childWidth + s.gap
	}
	return result
}

func measureWidthOf(component Component, width int) int {
	maxWidth := 0
	for _, line := range component.Render(width) {
		if w := VisibleWidth(line); w > maxWidth {
			maxWidth = w
		}
	}
	return maxWidth
}
