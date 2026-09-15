package tui

import "strings"

type Box struct {
	Container
	paddingX int
	paddingY int
	bgFn     func(string) string

	cacheChildren []string
	cacheWidth    int
	cacheBgSample string
	cacheHasBg    bool
	cacheLines    []string
	cacheValid    bool

	mouseCacheWidth int
	mouseCache      []mouseChild
}

func NewBox(paddingX, paddingY int, bgFn func(string) string) *Box {
	return &Box{paddingX: paddingX, paddingY: paddingY, bgFn: bgFn}
}

func (b *Box) SetBgFn(bgFn func(string) string) { b.bgFn = bgFn }

func (b *Box) invalidateCache() { b.cacheValid = false }

func (b *Box) AddChild(component Component) {
	b.Container.AddChild(component)
	b.invalidateCache()
}

func (b *Box) RemoveChild(component Component) {
	b.Container.RemoveChild(component)
	b.invalidateCache()
}

func (b *Box) Clear() {
	b.Container.Clear()
	b.invalidateCache()
}

func (b *Box) Invalidate() {
	b.invalidateCache()
	for _, child := range b.children {
		child.Invalidate()
	}
}

func (b *Box) dispatchMouse(event MouseEvent) *mouseDispatchResult {
	contentWidth := maxInt(1, event.Width-b.paddingX*2)
	contentY := event.Y - b.paddingY
	contentX := event.X - b.paddingX
	if contentY < 0 || contentX < 0 || contentX >= contentWidth {
		return nil
	}
	mouseChildren := b.mouseCache
	if b.mouseCacheWidth != contentWidth || mouseChildren == nil {
		mouseChildren = make([]mouseChild, 0, len(b.children))
		for _, child := range b.children {
			mouseChildren = append(mouseChildren, mouseChild{component: child, height: len(child.Render(contentWidth))})
		}
		b.mouseCacheWidth = contentWidth
		b.mouseCache = mouseChildren
	}
	childY := 0
	for _, entry := range mouseChildren {
		if contentY >= childY && contentY < childY+entry.height {
			childEvent := event
			childEvent.X = contentX
			childEvent.Y = contentY - childY
			childEvent.Width = contentWidth
			childEvent.Height = entry.height
			return dispatchMouseEvent(entry.component, childEvent)
		}
		childY += entry.height
	}
	return nil
}

func (b *Box) Render(width int) []string {
	if len(b.children) == 0 {
		return nil
	}

	contentWidth := maxInt(1, width-b.paddingX*2)
	leftPad := strings.Repeat(" ", b.paddingX)

	var childLines []string
	mouseChildren := make([]mouseChild, 0, len(b.children))
	for _, child := range b.children {
		lines := child.Render(contentWidth)
		mouseChildren = append(mouseChildren, mouseChild{component: child, height: len(lines)})
		for _, line := range lines {
			childLines = append(childLines, leftPad+line)
		}
	}
	b.mouseCacheWidth = contentWidth
	b.mouseCache = mouseChildren

	if len(childLines) == 0 {
		return nil
	}

	bgSample := ""
	hasBg := b.bgFn != nil
	if hasBg {
		bgSample = b.bgFn("t")
	}
	if b.cacheValid && b.cacheWidth == width && b.cacheBgSample == bgSample && b.cacheHasBg == hasBg &&
		len(b.cacheChildren) == len(childLines) {
		matches := true
		for i := range childLines {
			if b.cacheChildren[i] != childLines[i] {
				matches = false
				break
			}
		}
		if matches {
			return b.cacheLines
		}
	}

	result := make([]string, 0, len(childLines)+b.paddingY*2)
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}
	for _, line := range childLines {
		result = append(result, b.applyBg(line, width))
	}
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}

	b.cacheChildren = childLines
	b.cacheWidth = width
	b.cacheBgSample = bgSample
	b.cacheHasBg = hasBg
	b.cacheLines = result
	b.cacheValid = true
	return result
}

func (b *Box) applyBg(line string, width int) string {
	visible := VisibleWidth(line)
	padded := line + strings.Repeat(" ", maxInt(0, width-visible))
	if b.bgFn != nil {
		return ApplyBackgroundToLine(padded, width, b.bgFn)
	}
	return padded
}

type BorderStyle struct {
	Top      string
	Bottom   string
	Left     string
	Right    string
	CornerTL string
	CornerTR string
	CornerBL string
	CornerBR string
}

func DefaultBorderStyle() BorderStyle {
	return BorderStyle{
		Top: "─", Bottom: "─", Left: "│", Right: "│",
		CornerTL: "┌", CornerTR: "┐", CornerBL: "└", CornerBR: "┘",
	}
}

type Border struct {
	child    Component
	style    BorderStyle
	borderFn func(string) string
	title    string
}

func NewBorder(child Component, borderFn func(string) string) *Border {
	return &Border{child: child, style: DefaultBorderStyle(), borderFn: borderFn}
}

func (b *Border) SetBorderFn(borderFn func(string) string) {
	b.borderFn = borderFn
}

func (b *Border) SetTitle(title string) {
	b.title = title
}

func (b *Border) Invalidate() {
	b.child.Invalidate()
}

func (b *Border) Render(width int) []string {
	if width < 2 {
		return nil
	}
	contentWidth := width - 2
	contentLines := b.child.Render(contentWidth)

	style := func(text string) string {
		if b.borderFn != nil {
			return b.borderFn(text)
		}
		return text
	}

	titlePart := b.style.Top + b.style.Top
	if b.title != "" {
		titlePart += TruncateToWidth(b.title, maxInt(0, contentWidth-2), "…", false)
	}
	top := b.style.CornerTL + titlePart +
		strings.Repeat(b.style.Top, maxInt(0, width-2-VisibleWidth(titlePart))) + b.style.CornerTR
	top = style(top)

	bottom := b.style.CornerBL + strings.Repeat(b.style.Bottom, contentWidth) + b.style.CornerBR
	bottom = style(bottom)

	result := make([]string, 0, len(contentLines)+2)
	result = append(result, top)
	for _, line := range contentLines {
		visible := VisibleWidth(line)
		pad := maxInt(0, contentWidth-visible)
		result = append(result, style(b.style.Left)+line+strings.Repeat(" ", pad)+style(b.style.Right))
	}
	result = append(result, bottom)
	return result
}
