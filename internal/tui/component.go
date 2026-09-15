package tui

import (
	"strings"
	"sync"
	"time"
)

type Component interface {
	Render(width int) []string
	Invalidate()
}

type InputHandler interface {
	HandleInput(data string)
}

type MouseHandler interface {
	HandleMouse(event MouseEvent) *MouseEventResult
}

type KeyReleaseOptIn interface {
	WantsKeyRelease() bool
}

type Focusable interface {
	SetFocused(focused bool)
}

type MouseEventType int

const (
	MousePress MouseEventType = iota
	MouseRelease
	MouseMove
	MouseDrag
	MouseClick
	MouseWheel
)

type MouseButton int

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
	MouseButtonMiddle
	MouseButtonRight
)

type MouseEvent struct {
	Type       MouseEventType
	Button     MouseButton
	X          int
	Y          int
	ScreenX    int
	ScreenY    int
	Width      int
	Height     int
	Shift      bool
	Alt        bool
	Ctrl       bool
	WheelDelta int
	ClickCount int
}

func (e MouseEvent) WithPosition(x, y int) MouseEvent {
	event := e
	event.X = x
	event.Y = y
	return event
}

type MouseEventResult struct {
	Handled   bool
	Capture   bool
	Focus     bool
	Render    bool
	renderSet bool
}

func (r *MouseEventResult) WithRender(render bool) *MouseEventResult {
	r.Render = render
	r.renderSet = true
	return r
}

type mouseDispatchTarget struct {
	component Component
	originX   int
	originY   int
	width     int
	height    int
}

type mouseDispatchResult struct {
	result      MouseEventResult
	target      mouseDispatchTarget
	focusTarget Component
}

type mouseDispatcher interface {
	dispatchMouse(event MouseEvent) *mouseDispatchResult
}

func dispatchMouseEvent(component Component, event MouseEvent) *mouseDispatchResult {
	if dispatcher, ok := component.(mouseDispatcher); ok {
		return dispatcher.dispatchMouse(event)
	}
	handler, ok := component.(MouseHandler)
	if !ok {
		return nil
	}
	result := handler.HandleMouse(event)
	if result == nil {
		return nil
	}
	if !result.Handled && !result.Capture && !result.Focus {
		return nil
	}
	result.Handled = true
	dispatch := &mouseDispatchResult{
		result: *result,
		target: mouseDispatchTarget{
			component: component,
			originX:   event.ScreenX - event.X,
			originY:   event.ScreenY - event.Y,
			width:     event.Width,
			height:    event.Height,
		},
	}
	if result.Focus {
		dispatch.focusTarget = component
	}
	return dispatch
}

func retargetMouseEvent(event MouseEvent, target mouseDispatchTarget) MouseEvent {
	event.X = event.ScreenX - target.originX
	event.Y = event.ScreenY - target.originY
	event.Width = target.width
	event.Height = target.height
	return event
}

type mouseChild struct {
	component Component
	height    int
}

type Container struct {
	children    []Component
	mouseWidth  int
	mouseLayout []mouseChild
}

func (c *Container) AddChild(component Component) {
	c.children = append(c.children, component)
}

func (c *Container) RemoveChild(component Component) {
	for index, child := range c.children {
		if child == component {
			c.children = append(c.children[:index], c.children[index+1:]...)
			return
		}
	}
}

func (c *Container) Clear() {
	c.children = nil
	c.mouseLayout = nil
}

func (c *Container) Children() []Component {
	return c.children
}

func (c *Container) Invalidate() {
	for _, child := range c.children {
		child.Invalidate()
	}
}

func (c *Container) Render(width int) []string {
	lines := make([]string, 0, len(c.children))
	mouseChildren := make([]mouseChild, 0, len(c.children))
	for _, child := range c.children {
		childLines := child.Render(width)
		mouseChildren = append(mouseChildren, mouseChild{component: child, height: len(childLines)})
		lines = append(lines, childLines...)
	}
	c.mouseWidth = width
	c.mouseLayout = mouseChildren
	return lines
}

func (c *Container) HandleMouse(event MouseEvent) *MouseEventResult {
	if dispatch := c.dispatchMouse(event); dispatch != nil {
		result := dispatch.result
		return &result
	}
	return nil
}

func (c *Container) dispatchMouse(event MouseEvent) *mouseDispatchResult {
	if event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	mouseChildren := c.mouseLayout
	if mouseChildren == nil || c.mouseWidth != event.Width {
		mouseChildren = make([]mouseChild, 0, len(c.children))
		for _, child := range c.children {
			mouseChildren = append(mouseChildren, mouseChild{component: child, height: len(child.Render(event.Width))})
		}
		c.mouseWidth = event.Width
		c.mouseLayout = mouseChildren
	}
	childY := 0
	for _, entry := range mouseChildren {
		if event.Y >= childY && event.Y < childY+entry.height {
			childEvent := event.WithPosition(event.X, event.Y-childY)
			childEvent.Height = entry.height
			result := dispatchMouseEvent(entry.component, childEvent)
			if result != nil && result.result.Focus {
				if _, handlesInput := any(c).(InputHandler); handlesInput {
					result.focusTarget = c
				}
			}
			return result
		}
		childY += entry.height
	}
	return nil
}

func containsComponent(root Component, target Component) bool {
	if root == target {
		return true
	}
	container, ok := root.(*Container)
	if !ok {
		if base, isBase := root.(containerProvider); isBase {
			return containsComponentChildren(base.containerChildren(), target)
		}
		return false
	}
	return containsComponentChildren(container.children, target)
}

func containsComponentChildren(children []Component, target Component) bool {
	for _, child := range children {
		if containsComponent(child, target) {
			return true
		}
	}
	return false
}

type containerProvider interface {
	containerChildren() []Component
}

type StopOptions struct {
	PreserveScreen bool
}

type InputListenerResult struct {
	Consume bool
	Data    string
	HasData bool
}

type InputListener func(data string) InputListenerResult

type registeredListener struct {
	id       int
	listener InputListener
}

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

type tuiHooks struct {
	beforeStart      func()
	afterStart       func()
	beforeStop       func(options StopOptions)
	afterStop        func(options StopOptions)
	resetRenderState func()
	doRender         func()
	mountedRoots     func() []Component
}

type Base struct {
	Container

	mu sync.Mutex

	terminal Terminal
	hooks    tuiHooks

	focused     Component
	stopped     bool
	fullRedraws int

	renderMu sync.Mutex

	inputListeners []registeredListener
	nextListenerID int

	onDebug func()

	showHardwareCursor bool
	clearOnShrink      bool

	minRenderInterval  time.Duration
	renderRequested    bool
	renderTimer        *time.Timer
	lastRender         time.Time
	immediateScheduled bool

	focusOrderCounter int
	overlayStack      []*overlayEntry
	renderedOverlays  []overlayLayout

	mode string
}

const defaultMinRenderInterval = 16 * time.Millisecond

func NewBase(terminal Terminal, showHardwareCursor bool, mode string) *Base {
	return &Base{
		terminal:           terminal,
		showHardwareCursor: showHardwareCursor,
		minRenderInterval:  defaultMinRenderInterval,
		mode:               mode,
	}
}

func (b *Base) SetHooks(hooks tuiHooks) { b.hooks = hooks }

func (b *Base) Terminal() Terminal { return b.terminal }
func (b *Base) Mode() string       { return b.mode }
func (b *Base) FullRedraws() int   { return b.fullRedraws }

func (b *Base) IsStopped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopped
}

func (b *Base) ShowHardwareCursor() bool { return b.showHardwareCursor }

func (b *Base) SetShowHardwareCursor(enabled bool) {
	b.mu.Lock()
	if b.showHardwareCursor == enabled {
		b.mu.Unlock()
		return
	}
	b.showHardwareCursor = enabled
	b.mu.Unlock()
	if !enabled {
		b.terminal.Write(CursorHide)
	}
	b.RequestRender(false)
}

func (b *Base) ClearOnShrink() bool           { return b.clearOnShrink }
func (b *Base) SetClearOnShrink(enabled bool) { b.clearOnShrink = enabled }

func (b *Base) SetOnDebug(callback func()) { b.onDebug = callback }

func (b *Base) FocusedComponent() Component {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.focused
}

func (b *Base) SetFocus(component Component) {
	b.mu.Lock()
	b.setFocusLocked(component)
	b.mu.Unlock()
}

func (b *Base) setFocusLocked(component Component) {
	if b.focused != nil {
		if focusable, ok := b.focused.(Focusable); ok {
			focusable.SetFocused(false)
		}
	}
	b.focused = component
	if component != nil {
		if focusable, ok := component.(Focusable); ok {
			focusable.SetFocused(true)
		}
	}
}

func (b *Base) RemoveChild(component Component) {
	b.mu.Lock()
	b.Container.RemoveChild(component)
	b.mu.Unlock()
}

func (b *Base) Clear() {
	b.mu.Lock()
	b.Container.Clear()
	b.mu.Unlock()
}

func (b *Base) MountedRoots() []Component {
	if b.hooks.mountedRoots != nil {
		return b.hooks.mountedRoots()
	}
	return b.children
}

func (b *Base) Invalidate() {
	b.mu.Lock()
	roots := append([]Component(nil), b.MountedRoots()...)
	overlays := append([]*overlayEntry(nil), b.overlayStack...)
	b.mu.Unlock()
	for _, root := range roots {
		root.Invalidate()
	}
	for _, entry := range overlays {
		entry.component.Invalidate()
	}
}

func (b *Base) AddInputListener(listener InputListener) func() {
	b.mu.Lock()
	b.nextListenerID++
	id := b.nextListenerID
	b.inputListeners = append(b.inputListeners, registeredListener{id: id, listener: listener})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		for index, registered := range b.inputListeners {
			if registered.id == id {
				b.inputListeners = append(b.inputListeners[:index], b.inputListeners[index+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

func (b *Base) Start() {
	b.mu.Lock()
	b.stopped = false
	b.mu.Unlock()
	if b.hooks.beforeStart != nil {
		b.hooks.beforeStart()
	}
	b.terminal.Start(b.handleTerminalInput, func() { b.RequestRender(false) })
	if b.hooks.afterStart != nil {
		b.hooks.afterStart()
	}
	b.terminal.Write(CursorHide)
	b.RequestRender(false)
}

func (b *Base) Stop(options StopOptions) {
	b.mu.Lock()
	b.stopped = true
	b.cancelRenderTimerLocked()
	b.mu.Unlock()
	if b.hooks.beforeStop != nil {
		b.hooks.beforeStop(options)
	}
	b.terminal.Write(CursorShow)
	b.terminal.Stop()
	if b.hooks.afterStop != nil {
		b.hooks.afterStop(options)
	}
}

func (b *Base) RenderNow(force bool) {
	if force && b.hooks.resetRenderState != nil {
		b.hooks.resetRenderState()
	}
	b.mu.Lock()
	b.renderRequested = false
	b.cancelRenderTimerLocked()
	b.lastRender = time.Now()
	doRender := b.hooks.doRender
	stopped := b.stopped
	b.mu.Unlock()
	if !stopped && doRender != nil {
		b.renderMu.Lock()
		doRender()
		b.renderMu.Unlock()
	}
}

func (b *Base) RequestRender(force bool) {
	if force {
		if b.hooks.resetRenderState != nil {
			b.hooks.resetRenderState()
		}
		b.requestImmediateRender()
		return
	}
	b.mu.Lock()
	if b.renderRequested {
		b.mu.Unlock()
		return
	}
	b.renderRequested = true
	b.mu.Unlock()
	time.AfterFunc(0, b.scheduleRender)
}

func (b *Base) requestImmediateRender() {
	b.cancelRenderTimer()
	b.mu.Lock()
	b.renderRequested = true
	if b.immediateScheduled {
		b.mu.Unlock()
		return
	}
	b.immediateScheduled = true
	b.mu.Unlock()
	time.AfterFunc(0, func() {
		b.mu.Lock()
		b.immediateScheduled = false
		if b.stopped || !b.renderRequested {
			b.mu.Unlock()
			return
		}
		b.cancelRenderTimerLocked()
		b.renderRequested = false
		b.lastRender = time.Now()
		doRender := b.hooks.doRender
		stopped := b.stopped
		b.mu.Unlock()
		if !stopped && doRender != nil {
			b.renderMu.Lock()
			doRender()
			b.renderMu.Unlock()
		}
	})
}

func (b *Base) scheduleRender() {
	b.mu.Lock()
	if b.stopped || b.renderTimer != nil || !b.renderRequested {
		b.mu.Unlock()
		return
	}
	delay := b.minRenderInterval - time.Since(b.lastRender)
	if delay < 0 {
		delay = 0
	}
	timer := time.AfterFunc(delay, func() {
		b.mu.Lock()
		b.renderTimer = nil
		if b.stopped || !b.renderRequested {
			b.mu.Unlock()
			return
		}
		b.renderRequested = false
		b.lastRender = time.Now()
		doRender := b.hooks.doRender
		stopped := b.stopped
		b.mu.Unlock()
		if !stopped && doRender != nil {
			b.renderMu.Lock()
			doRender()
			b.renderMu.Unlock()
		}
		b.scheduleRender()
	})
	b.renderTimer = timer
	b.mu.Unlock()
}

func (b *Base) cancelRenderTimer() {
	b.mu.Lock()
	b.cancelRenderTimerLocked()
	b.mu.Unlock()
}

func (b *Base) cancelRenderTimerLocked() {
	if b.renderTimer != nil {
		b.renderTimer.Stop()
		b.renderTimer = nil
	}
}

func (b *Base) SetMinRenderInterval(interval time.Duration) {
	b.mu.Lock()
	b.minRenderInterval = interval
	b.mu.Unlock()
}

func (b *Base) handleTerminalInput(data string) {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	for _, registered := range append([]registeredListener(nil), b.inputListeners...) {
		result := registered.listener(data)
		if result.Consume {
			b.mu.Unlock()
			return
		}
		if result.HasData {
			data = result.Data
		}
	}
	if data == "" {
		b.mu.Unlock()
		return
	}

	if b.onDebug != nil && MatchesKey(data, "shift+ctrl+d") {
		callback := b.onDebug
		b.mu.Unlock()
		callback()
		return
	}

	var focusedOverlay *overlayEntry
	for _, entry := range b.overlayStack {
		if entry.component == b.focused {
			focusedOverlay = entry
		}
	}
	if focusedOverlay != nil && !b.isOverlayVisibleLocked(focusedOverlay) {
		top := b.topmostVisibleOverlayLocked()
		if top != nil {
			b.setFocusLocked(top.component)
		} else {
			b.setFocusLocked(focusedOverlay.preFocus)
		}
	}

	focused := b.focused
	b.mu.Unlock()

	if focused == nil {
		return
	}
	inputHandler, ok := focused.(InputHandler)
	if !ok {
		return
	}
	if optIn, canAsk := focused.(KeyReleaseOptIn); !canAsk || !optIn.WantsKeyRelease() {
		if IsKeyRelease(data) {
			return
		}
	}
	inputHandler.HandleInput(data)
	b.requestImmediateRender()
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

func (b *Base) compositeOverlays(lines []string, termWidth, termHeight int) []string {
	b.mu.Lock()
	if len(b.overlayStack) == 0 {
		b.renderedOverlays = nil
		b.mu.Unlock()
		return lines
	}
	var visible []*overlayEntry
	for _, entry := range b.overlayStack {
		entry.hasBounds = false
		if b.isOverlayVisibleLocked(entry) {
			visible = append(visible, entry)
		}
	}
	b.mu.Unlock()

	if len(visible) == 0 {
		b.mu.Lock()
		b.renderedOverlays = nil
		b.mu.Unlock()
		return lines
	}

	result := append([]string(nil), lines...)
	var rendered []overlayLayout
	minLinesNeeded := len(result)

	for _, entry := range visible {
		initial := b.resolveOverlayLayout(entry.options, 0, termWidth, termHeight)
		overlayLines := entry.component.Render(initial.width)
		if initial.maxHeight != nil && len(overlayLines) > *initial.maxHeight {
			overlayLines = overlayLines[:*initial.maxHeight]
		}
		final := b.resolveOverlayLayout(entry.options, len(overlayLines), termWidth, termHeight)
		b.mu.Lock()
		entry.bounds = OverlayBounds{Row: final.row, Col: final.col, Width: final.width, Height: len(overlayLines)}
		entry.hasBounds = true
		b.renderedOverlays = append(b.renderedOverlays, overlayLayout{
			entry: entry, row: final.row, col: final.col, width: final.width, height: len(overlayLines),
		})
		b.mu.Unlock()
		rendered = append(rendered, overlayLayout{entry: entry, lines: overlayLines, row: final.row, col: final.col, width: final.width})
		if final.row+len(overlayLines) > minLinesNeeded {
			minLinesNeeded = final.row + len(overlayLines)
		}
	}

	workingHeight := len(result)
	workingHeight = maxInt(workingHeight, termHeight)
	workingHeight = maxInt(workingHeight, minLinesNeeded)
	for len(result) < workingHeight {
		result = append(result, "")
	}
	viewportStart := maxInt(0, workingHeight-termHeight)

	for _, layout := range rendered {
		for i, overlayLine := range layout.lines {
			idx := viewportStart + layout.row + i
			if idx < 0 || idx >= len(result) {
				continue
			}
			line := overlayLine
			if VisibleWidth(line) > layout.width {
				line = SliceByColumn(line, 0, layout.width, true)
			}
			result[idx] = CompositeTuiLine(result[idx], line, layout.col, layout.width, termWidth)
		}
	}
	return result
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

func (b *Base) ApplyLineResets(lines []string) []string {
	for i, line := range lines {
		lines[i] = normalizeTerminalOutput(line) + SegmentReset
	}
	return lines
}

func (b *Base) ExtractCursorPosition(lines []string, height int) (row, col int, found bool) {
	viewportTop := len(lines) - height
	if viewportTop < 0 {
		viewportTop = 0
	}
	for r := len(lines) - 1; r >= viewportTop; r-- {
		line := lines[r]
		markerIndex := strings.Index(line, CursorMarker)
		if markerIndex == -1 {
			continue
		}
		col = VisibleWidth(line[:markerIndex])
		lines[r] = line[:markerIndex] + line[markerIndex+len(CursorMarker):]
		return r, col, true
	}
	return 0, 0, false
}

func CompositeTuiLine(baseLine, overlayLine string, startCol, overlayWidth, totalWidth int) string {
	baseBefore := sliceByColumns(baseLine, 0, startCol, true)
	baseAfter := sliceByColumns(baseLine, startCol+overlayWidth, totalWidth-(startCol+overlayWidth), true)
	overlay := sliceByColumns(overlayLine, 0, overlayWidth, true)

	beforePad := maxInt(0, startCol-baseBefore.width)
	overlayPad := maxInt(0, overlayWidth-overlay.width)
	actualBeforeWidth := maxInt(startCol, baseBefore.width)
	actualOverlayWidth := maxInt(overlayWidth, overlay.width)
	afterTarget := maxInt(0, totalWidth-actualBeforeWidth-actualOverlayWidth)
	afterPad := maxInt(0, afterTarget-baseAfter.width)

	var builder strings.Builder
	builder.WriteString(baseBefore.text)
	builder.WriteString(strings.Repeat(" ", beforePad))
	builder.WriteString(SegmentReset)
	builder.WriteString(overlay.text)
	builder.WriteString(strings.Repeat(" ", overlayPad))
	builder.WriteString(SegmentReset)
	builder.WriteString(baseAfter.text)
	builder.WriteString(strings.Repeat(" ", afterPad))
	result := builder.String()
	if VisibleWidth(result) <= totalWidth {
		return result
	}
	return SliceByColumn(result, 0, totalWidth, true)
}
