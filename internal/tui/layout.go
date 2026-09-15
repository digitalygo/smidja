package tui

import (
	"strings"
)

type LayoutRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func (r LayoutRect) contains(x, y int) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

func intersectRects(a, b LayoutRect) LayoutRect {
	x := maxInt(a.X, b.X)
	y := maxInt(a.Y, b.Y)
	right := minInt(a.X+a.Width, b.X+b.Width)
	bottom := minInt(a.Y+a.Height, b.Y+b.Height)
	return LayoutRect{X: x, Y: y, Width: maxInt(0, right-x), Height: maxInt(0, bottom-y)}
}

type LayoutBox struct {
	Component          Component
	Rect               LayoutRect
	Clip               LayoutRect
	Children           []*LayoutBox
	parent             *LayoutBox
	lines              []string
	lineOffset         int
	scrollView         *ScrollView
	scrollContentLines []string
	layer              int
}

type LayoutFrame struct {
	Root              *LayoutBox
	Width             int
	Height            int
	Lines             []string
	PrimaryScrollView *ScrollView
}

type StackEntryOptions struct {
	Basis   *int
	Grow    int
	Shrink  int
	MinSize int
	MaxSize int
	Visible func(viewport Viewport) bool
}

type Viewport struct {
	Width  int
	Height int
}

type StackEntry struct {
	Component Component
	StackEntryOptions
}

type StackLayoutEntry struct {
	Component Component
	Options   StackEntryOptions
}

type StackAlign int

const (
	AlignStretch StackAlign = iota
	AlignStart
	AlignCenter
	AlignEnd
)

type StackLayoutSpec struct {
	Vertical bool
	Entries  []StackLayoutEntry
	Gap      int
	Align    StackAlign
}

type StackLayoutProvider interface {
	StackLayout() StackLayoutSpec
}

type ScrollLayoutSpec struct {
	Child Component
	State *ScrollView
}

type ScrollLayoutProvider interface {
	ScrollLayout() ScrollLayoutSpec
}

type layoutContext struct {
	viewport          Viewport
	renderCache       map[Component]map[int][]string
	requestRender     func()
	primaryScrollView *ScrollView
}

func renderCached(context *layoutContext, component Component, width int) []string {
	safeWidth := maxInt(1, width)
	widths, ok := context.renderCache[component]
	if !ok {
		widths = make(map[int][]string)
		context.renderCache[component] = widths
	}
	lines, cached := widths[safeWidth]
	if !cached {
		lines = component.Render(safeWidth)
		widths[safeWidth] = lines
	}
	return lines
}

func measureHeight(context *layoutContext, component Component, width int) int {
	return len(renderCached(context, component, width))
}

func measureWidth(context *layoutContext, component Component, width int) int {
	maxWidth := 0
	for _, line := range renderCached(context, component, width) {
		if w := VisibleWidth(line); w > maxWidth {
			maxWidth = w
		}
	}
	return maxWidth
}

func visibleStackEntries(entries []StackLayoutEntry, viewport Viewport) []StackLayoutEntry {
	result := make([]StackLayoutEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Options.Visible != nil && !entry.Options.Visible(viewport) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func clampStackSize(size int, entry StackLayoutEntry) int {
	minSize := maxInt(0, entry.Options.MinSize)
	maxSize := entry.Options.MaxSize
	if maxSize <= 0 {
		maxSize = 1 << 30
	}
	return maxInt(minSize, minInt(maxSize, maxInt(0, size)))
}

func distributeStack(sizes []int, entries []StackLayoutEntry, amount int, grow bool) {
	remaining := amount
	for remaining > 0 {
		type candidate struct {
			index    int
			weight   int
			capacity int
		}
		var candidates []candidate
		totalWeight := 0
		for index, entry := range entries {
			if grow {
				growFactor := entry.Options.Grow
				if growFactor <= 0 {
					continue
				}
				maxSize := entry.Options.MaxSize
				if maxSize == 0 {
					maxSize = 1 << 30
				}
				if sizes[index] >= maxSize {
					continue
				}
				candidates = append(candidates, candidate{index: index, weight: growFactor, capacity: maxSize - sizes[index]})
				totalWeight += growFactor
			} else {
				shrinkFactor := entry.Options.Shrink
				if shrinkFactor < 0 {
					continue
				}
				if shrinkFactor == 0 {
					shrinkFactor = 1
				}
				if sizes[index] <= entry.Options.MinSize {
					continue
				}
				weight := shrinkFactor * maxInt(1, sizes[index])
				candidates = append(candidates, candidate{index: index, weight: weight, capacity: sizes[index] - entry.Options.MinSize})
				totalWeight += weight
			}
		}
		if len(candidates) == 0 || totalWeight == 0 {
			return
		}
		distributed := 0
		for _, item := range candidates {
			if remaining <= 0 {
				break
			}
			proposed := maxInt(1, remaining*item.weight/totalWeight)
			delta := minInt(remaining, minInt(proposed, item.capacity))
			if delta <= 0 {
				continue
			}
			if grow {
				sizes[item.index] += delta
			} else {
				sizes[item.index] -= delta
			}
			remaining -= delta
			distributed += delta
		}
		if distributed == 0 {
			return
		}
	}
}

func AllocateStackSizes(entries []StackLayoutEntry, intrinsicSizes []int, availableSize int, hasAvailable bool, gap int) []int {
	sizes := make([]int, len(entries))
	for index, entry := range entries {
		size := intrinsicSizes[index]
		if entry.Options.Basis != nil {
			size = *entry.Options.Basis
		}
		sizes[index] = clampStackSize(size, entry)
	}
	if !hasAvailable {
		return sizes
	}
	contentSize := maxInt(0, availableSize-maxInt(0, len(entries)-1)*gap)
	total := 0
	for _, size := range sizes {
		total += size
	}
	if total < contentSize {
		distributeStack(sizes, entries, contentSize-total, true)
	} else if total > contentSize {
		distributeStack(sizes, entries, total-contentSize, false)
	}
	return sizes
}

func layoutComponent(context *layoutContext, component Component, x, y, width, height int, hasHeight bool, clip LayoutRect) *LayoutBox {
	safeWidth := maxInt(1, width)

	if scrollProvider, ok := component.(ScrollLayoutProvider); ok {
		spec := scrollProvider.ScrollLayout()
		state := spec.State
		previousScrollTop := state.scrollTop
		contentWidth := state.ContentWidth(safeWidth)
		childBox := layoutComponent(context, spec.Child, x, y-previousScrollTop, contentWidth, 0, false, clip)
		contentHeight := childBox.Rect.Height
		viewportHeight := contentHeight
		if hasHeight {
			viewportHeight = maxInt(0, height)
		}
		state.UpdateLayout(contentHeight, viewportHeight, context.requestRender)
		translateBox(childBox, previousScrollTop-state.scrollTop)
		if state.primary || context.primaryScrollView == nil {
			context.primaryScrollView = state
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: viewportHeight}
		childClip := intersectRects(clip, rect)
		box := &LayoutBox{
			Component:          component,
			Rect:               rect,
			Clip:               childClip,
			scrollView:         state,
			scrollContentLines: renderCached(context, spec.Child, contentWidth),
			layer:              0,
		}
		box.Children = []*LayoutBox{childBox}
		childBox.parent = box
		updateClips(childBox, childClip)
		return box
	}

	if stackProvider, ok := component.(StackLayoutProvider); ok {
		spec := stackProvider.StackLayout()
		entries := visibleStackEntries(spec.Entries, context.viewport)
		gapTotal := maxInt(0, len(entries)-1) * spec.Gap

		if spec.Vertical {
			intrinsicHeights := make([]int, len(entries))
			for index, entry := range entries {
				if entry.Options.Basis != nil {
					intrinsicHeights[index] = *entry.Options.Basis
				} else {
					intrinsicHeights[index] = measureHeight(context, entry.Component, safeWidth)
				}
			}
			var available int
			hasAvailable := false
			if hasHeight {
				available = height
				hasAvailable = true
			}
			sizes := AllocateStackSizes(entries, intrinsicHeights, available, hasAvailable, spec.Gap)
			naturalHeight := gapTotal
			for _, size := range sizes {
				naturalHeight += size
			}
			allocatedHeight := naturalHeight
			if hasHeight {
				allocatedHeight = maxInt(0, height)
			}
			rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
			box := &LayoutBox{Component: component, Rect: rect, Clip: intersectRects(clip, rect), layer: 0}
			childY := y
			for index, entry := range entries {
				childBox := layoutComponent(context, entry.Component, x, childY, safeWidth, sizes[index], true, box.Clip)
				childBox.parent = box
				box.Children = append(box.Children, childBox)
				childY += sizes[index] + spec.Gap
			}
			return box
		}

		intrinsicWidths := make([]int, len(entries))
		for index, entry := range entries {
			if entry.Options.Basis != nil {
				intrinsicWidths[index] = *entry.Options.Basis
			} else {
				intrinsicWidths[index] = measureWidth(context, entry.Component, safeWidth)
			}
		}
		widths := AllocateStackSizes(entries, intrinsicWidths, safeWidth, true, spec.Gap)
		intrinsicHeights := make([]int, len(entries))
		for index := range entries {
			intrinsicHeights[index] = measureHeight(context, entries[index].Component, maxInt(1, widths[index]))
		}
		allocatedHeight := 0
		if hasHeight {
			allocatedHeight = maxInt(0, height)
		} else {
			for _, childHeight := range intrinsicHeights {
				allocatedHeight = maxInt(allocatedHeight, childHeight)
			}
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
		box := &LayoutBox{Component: component, Rect: rect, Clip: intersectRects(clip, rect), layer: 0}
		childX := x
		for index, entry := range entries {
			naturalChildHeight := intrinsicHeights[index]
			childHeight := minInt(allocatedHeight, naturalChildHeight)
			if spec.Align == AlignStretch {
				childHeight = allocatedHeight
			}
			childY := y
			switch spec.Align {
			case AlignCenter:
				childY += (allocatedHeight - childHeight) / 2
			case AlignEnd:
				childY += allocatedHeight - childHeight
			}
			childWidth := widths[index]
			if childWidth == 0 {
				box.Children = append(box.Children, &LayoutBox{
					Component: entry.Component,
					Rect:      LayoutRect{X: childX, Y: childY, Width: 0, Height: childHeight},
					Clip:      LayoutRect{X: childX, Y: childY, Width: 0, Height: 0},
					parent:    box,
				})
			} else {
				childBox := layoutComponent(context, entry.Component, childX, childY, childWidth, childHeight, true, box.Clip)
				childBox.parent = box
				box.Children = append(box.Children, childBox)
			}
			childX += childWidth + spec.Gap
		}
		return box
	}

	lines := renderCached(context, component, safeWidth)
	allocatedHeight := len(lines)
	if hasHeight {
		allocatedHeight = maxInt(0, height)
	}
	lineOffset := 0
	if len(lines) > allocatedHeight && allocatedHeight > 0 {
		for index, line := range lines {
			if strings.Contains(line, CursorMarker) {
				if index >= allocatedHeight {
					lineOffset = index - allocatedHeight + 1
				}
				break
			}
		}
	}
	return &LayoutBox{
		Component:  component,
		Rect:       LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight},
		Clip:       intersectRects(clip, LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}),
		lines:      lines,
		lineOffset: lineOffset,
		layer:      0,
	}
}

func translateBox(box *LayoutBox, deltaY int) {
	box.Rect.Y += deltaY
	for _, child := range box.Children {
		translateBox(child, deltaY)
	}
}

func updateClips(box *LayoutBox, parentClip LayoutRect) {
	box.Clip = intersectRects(parentClip, box.Rect)
	for _, child := range box.Children {
		updateClips(child, box.Clip)
	}
}

func replaceScrollbarCell(line string, column int, totalWidth int, replacement string, preserveTargetBackground bool) string {
	start := column
	sliced := sliceByColumns(line, start, 1, true)
	if sliced.width == 0 {
		sliced = sliceByColumns(line, start, 2, false)
	}
	target := sliced.text
	targetWidth := sliced.width
	if targetWidth == 0 {
		target = " "
		targetWidth = 1
	}
	end := start + targetWidth
	before := sliceByColumns(line, 0, start, true)
	after := sliceByColumns(line, end, maxInt(0, totalWidth-end), true)

	beforePadding := strings.Repeat(" ", maxInt(0, start-before.width))
	targetStyle := SegmentReset
	if preserveTargetBackground {
		targetStyle += activeBackgroundANSI(target)
	}
	return before.text + beforePadding + targetStyle + replacement + after.text
}

func activeBackgroundANSI(text string) string {
	tracker := &styleTracker{}
	updateTracker(text, tracker)
	if tracker.bg == "" {
		return ""
	}
	return "\x1b[" + tracker.bg + "m"
}

type scrollbarGeometry struct {
	column       int
	trackTop     int
	trackHeight  int
	thumbTop     int
	thumbHeight  int
	maxScrollTop int
}

func getScrollbarGeometry(box *LayoutBox, includeHiddenAuto bool) *scrollbarGeometry {
	if box.scrollView == nil || box.Rect.Width <= 0 || box.Rect.Height <= 0 {
		return nil
	}
	contentHeight := 0
	if len(box.Children) > 0 {
		contentHeight = box.Children[0].Rect.Height
	}
	if contentHeight == 0 {
		contentHeight = len(box.scrollContentLines)
	}
	trackHeight := box.Rect.Height
	canRevealHiddenAuto := includeHiddenAuto && box.scrollView.scrollbar == ScrollbarAuto && contentHeight > trackHeight
	if !box.scrollView.IsScrollbarVisible() && !canRevealHiddenAuto {
		return nil
	}

	minThumbHeight := minInt(2, trackHeight)
	thumbHeight := maxInt(minThumbHeight, minInt(trackHeight, trackHeight*trackHeight/maxInt(1, contentHeight)))
	maxScrollTop := maxInt(0, contentHeight-trackHeight)
	maxThumbTop := trackHeight - thumbHeight
	thumbOffset := 0
	if maxScrollTop > 0 {
		thumbOffset = box.scrollView.scrollTop * maxThumbTop / maxScrollTop
	}
	column := box.Rect.X + box.Rect.Width - 1
	if column < box.Clip.X || column >= box.Clip.X+box.Clip.Width {
		return nil
	}
	return &scrollbarGeometry{
		column:       column,
		trackTop:     box.Rect.Y,
		trackHeight:  trackHeight,
		thumbTop:     box.Rect.Y + thumbOffset,
		thumbHeight:  thumbHeight,
		maxScrollTop: maxScrollTop,
	}
}

func paintScrollbar(box *LayoutBox, screen []string, totalWidth int) {
	geometry := getScrollbarGeometry(box, false)
	if geometry == nil || box.scrollView == nil {
		return
	}
	for offset := 0; offset < geometry.trackHeight; offset++ {
		row := geometry.trackTop + offset
		if row < box.Clip.Y || row >= box.Clip.Y+box.Clip.Height || row < 0 || row >= len(screen) {
			continue
		}
		isThumb := row >= geometry.thumbTop && row < geometry.thumbTop+geometry.thumbHeight
		var replacement string
		if isThumb {
			if box.scrollView.isScrollbarActive {
				replacement = box.scrollView.scrollbarThumbStyle("█")
			} else {
				replacement = box.scrollView.scrollbarThumbStyle("┃")
			}
		} else {
			replacement = box.scrollView.scrollbarTrackStyle("│")
		}
		screen[row] = replaceScrollbarCell(screen[row], geometry.column, totalWidth, replacement, box.scrollView.scrollbar != ScrollbarAlways)
	}
}

func paintBox(box *LayoutBox, screen []string, totalWidth int) {
	if box.lines != nil {
		offset := box.lineOffset
		firstRow := maxInt(box.Rect.Y, box.Clip.Y)
		firstRow = maxInt(firstRow, 0)
		lastRow := minInt(box.Rect.Y+box.Rect.Height, box.Clip.Y+box.Clip.Height)
		lastRow = minInt(lastRow, len(screen))
		for row := firstRow; row < lastRow; row++ {
			sourceIndex := offset + row - box.Rect.Y
			if sourceIndex < 0 || sourceIndex >= len(box.lines) {
				continue
			}
			sourceLine := box.lines[sourceIndex]
			sourceLine = trimOSC133Zone(sourceLine)
			if box.Rect.X == 0 && box.Rect.Width >= totalWidth && screen[row] == "" {
				screen[row] = sourceLine
			} else {
				screen[row] = CompositeTuiLine(screen[row], sourceLine, box.Rect.X, box.Rect.Width, totalWidth)
			}
		}
	}
	for _, child := range box.Children {
		paintBox(child, screen, totalWidth)
	}
	paintScrollbar(box, screen, totalWidth)
}

func trimOSC133Zone(line string) string {
	for {
		if strings.HasPrefix(line, OSC133PromptStart) {
			line = strings.TrimPrefix(line, OSC133PromptStart)
			continue
		}
		if strings.HasPrefix(line, OSC133CommandEnd) {
			line = strings.TrimPrefix(line, OSC133CommandEnd)
			continue
		}
		if strings.HasPrefix(line, OSC133OutputEnd) {
			line = strings.TrimPrefix(line, OSC133OutputEnd)
			continue
		}
		return line
	}
}

func RenderLayoutFrame(root Component, width, height int, requestRender func()) *LayoutFrame {
	safeWidth := maxInt(1, width)
	safeHeight := maxInt(1, height)
	context := &layoutContext{
		viewport:      Viewport{Width: safeWidth, Height: safeHeight},
		renderCache:   make(map[Component]map[int][]string),
		requestRender: requestRender,
	}
	rootBox := layoutComponent(context, root, 0, 0, safeWidth, safeHeight, true, LayoutRect{X: 0, Y: 0, Width: safeWidth, Height: safeHeight})
	lines := make([]string, safeHeight)
	for i := range lines {
		lines[i] = ""
	}
	paintBox(rootBox, lines, safeWidth)
	return &LayoutFrame{
		Root:              rootBox,
		Width:             safeWidth,
		Height:            safeHeight,
		Lines:             lines,
		PrimaryScrollView: context.primaryScrollView,
	}
}

func getLayoutBoxesAt(frame *LayoutFrame, x, y int) []*LayoutBox {
	var result []*LayoutBox
	var visit func(box *LayoutBox, depth int)
	depths := map[*LayoutBox]int{}
	visit = func(box *LayoutBox, depth int) {
		if !box.Clip.contains(x, y) {
			return
		}
		result = append(result, box)
		depths[box] = depth
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && depths[result[j]] > depths[result[j-1]]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

func GetScrollViewBox(frame *LayoutFrame, scrollView *ScrollView) *LayoutBox {
	var visit func(box *LayoutBox) *LayoutBox
	visit = func(box *LayoutBox) *LayoutBox {
		if box.scrollView == scrollView {
			return box
		}
		for _, child := range box.Children {
			if match := visit(child); match != nil {
				return match
			}
		}
		return nil
	}
	return visit(frame.Root)
}

func getScrollViewsAt(frame *LayoutFrame, x, y int) []*ScrollView {
	type entry struct {
		scrollView *ScrollView
		depth      int
	}
	var entries []entry
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if !box.Clip.contains(x, y) {
			return
		}
		if box.scrollView != nil && box.Rect.contains(x, y) {
			entries = append(entries, entry{scrollView: box.scrollView, depth: depth})
		}
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].depth > entries[j-1].depth; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	result := make([]*ScrollView, 0, len(entries))
	for _, item := range entries {
		result = append(result, item.scrollView)
	}
	return result
}

const ShrinkNone = -1
