package tui

import "strings"

type OverlayAnchor string

const (
	AnchorCenter       OverlayAnchor = "center"
	AnchorTopLeft      OverlayAnchor = "top-left"
	AnchorTopRight     OverlayAnchor = "top-right"
	AnchorBottomLeft   OverlayAnchor = "bottom-left"
	AnchorBottomRight  OverlayAnchor = "bottom-right"
	AnchorTopCenter    OverlayAnchor = "top-center"
	AnchorBottomCenter OverlayAnchor = "bottom-center"
	AnchorLeftCenter   OverlayAnchor = "left-center"
	AnchorRightCenter  OverlayAnchor = "right-center"
)

type OverlayMargin struct {
	Top    int
	Right  int
	Bottom int
	Left   int
}

func UniformMargin(all int) OverlayMargin {
	return OverlayMargin{Top: all, Right: all, Bottom: all, Left: all}
}

type OverlayOptions struct {
	Width        string
	MinWidth     int
	MaxHeight    string
	Anchor       OverlayAnchor
	OffsetX      int
	OffsetY      int
	Row          string
	Col          string
	Margin       OverlayMargin
	Visible      func(termWidth, termHeight int) bool
	NonCapturing bool
}

type OverlayBounds struct {
	Row    int
	Col    int
	Width  int
	Height int
}

type OverlayHandle interface {
	Hide()
	SetHidden(hidden bool)
	IsHidden() bool
	Focus()
	Unfocus(target Component)
	IsFocused() bool
	Bounds() (OverlayBounds, bool)
}

type overlayEntry struct {
	component  Component
	options    OverlayOptions
	preFocus   Component
	hidden     bool
	focusOrder int
	bounds     OverlayBounds
	hasBounds  bool
}

type overlayLayout struct {
	entry  *overlayEntry
	lines  []string
	row    int
	col    int
	width  int
	height int
}

func (b *Base) isOverlayVisibleLocked(entry *overlayEntry) bool {
	if entry.hidden {
		return false
	}
	if entry.options.Visible != nil {
		return entry.options.Visible(b.terminal.Columns(), b.terminal.Rows())
	}
	return true
}

func (b *Base) topmostVisibleOverlayLocked() *overlayEntry {
	var topmost *overlayEntry
	for _, entry := range b.overlayStack {
		if entry.options.NonCapturing {
			continue
		}
		if !b.isOverlayVisibleLocked(entry) {
			continue
		}
		if topmost == nil || entry.focusOrder > topmost.focusOrder {
			topmost = entry
		}
	}
	return topmost
}

func (b *Base) HasOverlay() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, entry := range b.overlayStack {
		if b.isOverlayVisibleLocked(entry) {
			return true
		}
	}
	return false
}

func (b *Base) isOverlayFocused() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, entry := range b.overlayStack {
		if entry.component == b.focused && b.isOverlayVisibleLocked(entry) {
			return true
		}
	}
	return false
}

func (b *Base) ShowOverlay(component Component, options OverlayOptions) OverlayHandle {
	b.mu.Lock()
	b.focusOrderCounter++
	entry := &overlayEntry{
		component:  component,
		options:    options,
		preFocus:   b.focused,
		focusOrder: b.focusOrderCounter,
	}
	b.overlayStack = append(b.overlayStack, entry)
	if !options.NonCapturing && b.isOverlayVisibleLocked(entry) {
		b.setFocusLocked(component)
	}
	b.mu.Unlock()
	b.terminal.Write(CursorHide)
	b.RequestRender(false)
	return &overlayHandle{base: b, entry: entry}
}

func (b *Base) HideOverlay() {
	b.mu.Lock()
	if len(b.overlayStack) == 0 {
		b.mu.Unlock()
		return
	}
	overlay := b.overlayStack[len(b.overlayStack)-1]
	b.overlayStack = b.overlayStack[:len(b.overlayStack)-1]
	if b.focused == overlay.component {
		top := b.topmostVisibleOverlayLocked()
		if top != nil {
			b.setFocusLocked(top.component)
		} else {
			b.setFocusLocked(overlay.preFocus)
		}
	}
	empty := len(b.overlayStack) == 0
	b.mu.Unlock()
	if empty {
		b.terminal.Write(CursorHide)
	}
	b.RequestRender(false)
}

func (b *Base) removeOverlayEntry(entry *overlayEntry) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for index, candidate := range b.overlayStack {
		if candidate != entry {
			continue
		}
		b.overlayStack = append(b.overlayStack[:index], b.overlayStack[index+1:]...)
		if b.focused == entry.component {
			top := b.topmostVisibleOverlayLocked()
			if top != nil {
				b.setFocusLocked(top.component)
			} else {
				b.setFocusLocked(entry.preFocus)
			}
		}
		return true
	}
	return false
}

func (b *Base) resolveMouseFocusTarget(component Component) Component {
	b.mu.Lock()
	defer b.mu.Unlock()
	for index := len(b.overlayStack) - 1; index >= 0; index-- {
		entry := b.overlayStack[index]
		if b.isOverlayVisibleLocked(entry) && containsComponent(entry.component, component) {
			return entry.component
		}
	}
	return component
}

func (b *Base) dispatchMouseToOverlay(event MouseEvent) (hit bool, result *mouseDispatchResult) {
	b.mu.Lock()
	layouts := append([]overlayLayout(nil), b.renderedOverlays...)
	b.mu.Unlock()
	for index := len(layouts) - 1; index >= 0; index-- {
		layout := layouts[index]
		if event.ScreenX < layout.col || event.ScreenX >= layout.col+layout.width ||
			event.ScreenY < layout.row || event.ScreenY >= layout.row+layout.height {
			continue
		}
		local := event
		local.X = event.ScreenX - layout.col
		local.Y = event.ScreenY - layout.row
		local.Width = layout.width
		local.Height = layout.height
		dispatch := dispatchMouseEvent(layout.entry.component, local)
		if dispatch != nil {
			if dispatch.result.Focus {
				dispatch.focusTarget = layout.entry.component
			}
			return true, dispatch
		}
		return true, nil
	}
	return false, nil
}

func (b *Base) applyMouseDispatchResult(event MouseEvent, result *mouseDispatchResult) bool {
	focusTarget := b.resolveMouseFocusTarget(result.focusTarget)
	if focusTarget == nil {
		focusTarget = result.target.component
	}
	focusChanged := result.result.Focus && b.FocusedComponent() != focusTarget
	if result.result.Focus {
		b.SetFocus(focusTarget)
	}
	if result.result.renderSet {
		return result.result.Render
	}
	return focusChanged || result.result.Handled ||
		event.Type == MousePress || event.Type == MouseClick || event.Type == MouseDrag || event.Type == MouseWheel
}

type resolvedOverlayLayout struct {
	width     int
	row       int
	col       int
	maxHeight *int
}

func (b *Base) resolveOverlayLayout(options OverlayOptions, overlayHeight, termWidth, termHeight int) resolvedOverlayLayout {
	margin := options.Margin
	marginTop := maxInt(0, margin.Top)
	marginRight := maxInt(0, margin.Right)
	marginBottom := maxInt(0, margin.Bottom)
	marginLeft := maxInt(0, margin.Left)
	availWidth := maxInt(1, termWidth-marginLeft-marginRight)
	availHeight := maxInt(1, termHeight-marginTop-marginBottom)

	width := parseSizeValue(options.Width, termWidth)
	if width == nil {
		defaultWidth := minInt(80, availWidth)
		width = &defaultWidth
	}
	if options.MinWidth > 0 {
		*width = maxInt(*width, options.MinWidth)
	}
	*width = maxInt(1, minInt(*width, availWidth))

	var maxHeight *int
	if parsed := parseSizeValue(options.MaxHeight, termHeight); parsed != nil {
		clamped := maxInt(1, minInt(*parsed, availHeight))
		maxHeight = &clamped
	}
	effectiveHeight := overlayHeight
	if maxHeight != nil && overlayHeight > *maxHeight {
		effectiveHeight = *maxHeight
	}

	anchor := options.Anchor
	if anchor == "" {
		anchor = AnchorCenter
	}

	row := resolveOverlayAxis(options.Row, anchor, effectiveHeight, availHeight, marginTop, true)
	col := resolveOverlayAxis(options.Col, anchor, *width, availWidth, marginLeft, false)
	row += options.OffsetY
	col += options.OffsetX
	row = maxInt(marginTop, minInt(row, termHeight-marginBottom-effectiveHeight))
	col = maxInt(marginLeft, minInt(col, termWidth-marginRight-*width))

	return resolvedOverlayLayout{width: *width, row: row, col: col, maxHeight: maxHeight}
}

func resolveOverlayAxis(value string, anchor OverlayAnchor, size, avail, margin int, vertical bool) int {
	if value != "" {
		if percent, ok := parsePercent(value); ok {
			maxOffset := maxInt(0, avail-size)
			return margin + int(float64(maxOffset)*percent)
		}
		if absolute, ok := parseIntString(value); ok {
			return absolute
		}
	}
	if vertical {
		return resolveAnchorRow(anchor, size, avail, margin)
	}
	return resolveAnchorCol(anchor, size, avail, margin)
}

func resolveAnchorRow(anchor OverlayAnchor, height, availHeight, marginTop int) int {
	switch anchor {
	case AnchorTopLeft, AnchorTopCenter, AnchorTopRight:
		return marginTop
	case AnchorBottomLeft, AnchorBottomCenter, AnchorBottomRight:
		return marginTop + availHeight - height
	default:
		return marginTop + (availHeight-height)/2
	}
}

func resolveAnchorCol(anchor OverlayAnchor, width, availWidth, marginLeft int) int {
	switch anchor {
	case AnchorTopLeft, AnchorLeftCenter, AnchorBottomLeft:
		return marginLeft
	case AnchorTopRight, AnchorRightCenter, AnchorBottomRight:
		return marginLeft + availWidth - width
	default:
		return marginLeft + (availWidth-width)/2
	}
}

func parseSizeValue(value string, reference int) *int {
	if value == "" {
		return nil
	}
	if percent, ok := parsePercent(value); ok {
		result := int(float64(reference) * percent)
		return &result
	}
	if absolute, ok := parseIntString(value); ok {
		return &absolute
	}
	return nil
}

func parsePercent(value string) (float64, bool) {
	if !strings.HasSuffix(value, "%") {
		return 0, false
	}
	number, ok := parseIntString(strings.TrimSuffix(value, "%"))
	if !ok {
		return 0, false
	}
	return float64(number) / 100, true
}

func parseIntString(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	number := 0
	negative := value[0] == '-'
	start := 0
	if negative {
		start = 1
		if len(value) == 1 {
			return 0, false
		}
	}
	for i := start; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
		number = number*10 + int(value[i]-'0')
	}
	if negative {
		return -number, true
	}
	return number, true
}

type overlayHandle struct {
	base  *Base
	entry *overlayEntry
}

func (h *overlayHandle) Hide() {
	if h.base.removeOverlayEntry(h.entry) {
		h.base.RequestRender(false)
	}
}

func (h *overlayHandle) SetHidden(hidden bool) {
	h.base.mu.Lock()
	if h.entry.hidden == hidden {
		h.base.mu.Unlock()
		return
	}
	h.entry.hidden = hidden
	if hidden {
		if h.base.focused == h.entry.component {
			top := h.base.topmostVisibleOverlayLocked()
			if top != nil {
				h.base.setFocusLocked(top.component)
			} else {
				h.base.setFocusLocked(h.entry.preFocus)
			}
		}
	} else if !h.entry.options.NonCapturing && h.base.isOverlayVisibleLocked(h.entry) {
		h.base.focusOrderCounter++
		h.entry.focusOrder = h.base.focusOrderCounter
		h.base.setFocusLocked(h.entry.component)
	}
	h.base.mu.Unlock()
	h.base.RequestRender(false)
}

func (h *overlayHandle) IsHidden() bool {
	h.base.mu.Lock()
	defer h.base.mu.Unlock()
	return h.entry.hidden
}

func (h *overlayHandle) Focus() {
	h.base.mu.Lock()
	found := false
	for _, entry := range h.base.overlayStack {
		if entry == h.entry {
			found = true
			break
		}
	}
	if !found || !h.base.isOverlayVisibleLocked(h.entry) {
		h.base.mu.Unlock()
		return
	}
	h.base.focusOrderCounter++
	h.entry.focusOrder = h.base.focusOrderCounter
	h.base.setFocusLocked(h.entry.component)
	h.base.mu.Unlock()
	h.base.RequestRender(false)
}

func (h *overlayHandle) Unfocus(target Component) {
	h.base.mu.Lock()
	isFocused := h.base.focused == h.entry.component
	if !isFocused {
		h.base.mu.Unlock()
		return
	}
	if target != nil {
		h.base.setFocusLocked(target)
	} else {
		top := h.base.topmostVisibleOverlayLocked()
		if top != nil && top != h.entry {
			h.base.setFocusLocked(top.component)
		} else {
			h.base.setFocusLocked(h.entry.preFocus)
		}
	}
	h.base.mu.Unlock()
	h.base.RequestRender(false)
}

func (h *overlayHandle) IsFocused() bool {
	h.base.mu.Lock()
	defer h.base.mu.Unlock()
	return h.base.focused == h.entry.component
}

func (h *overlayHandle) Bounds() (OverlayBounds, bool) {
	h.base.mu.Lock()
	defer h.base.mu.Unlock()
	if h.entry.hasBounds && h.base.isOverlayVisibleLocked(h.entry) {
		return h.entry.bounds, true
	}
	return OverlayBounds{}, false
}
