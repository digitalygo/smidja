package tui

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var envLookup = os.Getenv

const pageScrollOverlap = 4
const altWheelScrollMultiplier = 5
const doubleClickInterval = 500 * time.Millisecond

type AltScreenOptions struct {
	WheelScrollLines int
	MouseDisabled    bool
}

type documentRenderer interface {
	RenderDocument(width int) []string
}

type AltScreen struct {
	*Base

	frameMu           sync.RWMutex
	layoutPublishGate atomic.Pointer[func()]

	previousScreen       []string
	lastDocument         []string
	previousScreenWidth  int
	previousScreenHeight int

	layoutRoot         Component
	currentLayout      *LayoutFrame
	navFrame           *LayoutFrame
	navCursor          int
	navRevision        uint64
	implicitDocument   *Container
	implicitScrollView *ScrollView

	altScreenActive  atomic.Bool
	wheelScrollLines int
	mouseEnabled     bool

	removeInputListener func()
	activeKeybindings   *KeybindingsManager

	mouseCapture     *mouseDispatchTarget
	mousePressTarget *mouseDispatchTarget
	mousePressPoint  *struct{ x, y int }
	mousePressMoved  bool
	lastClick        *struct {
		timestamp time.Time
		component Component
		x, y      int
		count     int
	}
}

func NewAltScreen(terminal Terminal, showHardwareCursor bool, options AltScreenOptions) *AltScreen {
	wheelLines := options.WheelScrollLines
	if wheelLines <= 0 {
		wheelLines = 1
	}
	screen := &AltScreen{
		Base:             NewBase(terminal, showHardwareCursor, "fullscreen"),
		wheelScrollLines: wheelLines,
		mouseEnabled:     !options.MouseDisabled,
	}
	screen.implicitDocument = &screen.Container
	screen.implicitScrollView = NewScrollView(screen.implicitDocument, ScrollViewOptions{
		Follow:  "end",
		Primary: true,
	})
	remove := screen.AddInputListener(screen.handleViewportInput)
	screen.removeInputListener = remove
	screen.SetHooks(tuiHooks{
		resetRenderState: screen.resetRenderState,
		doRender:         func() { screen.doRender() },
		beforeStart:      screen.beforeTerminalStart,
		beforeStop:       screen.beforeTerminalStop,
		afterStop:        screen.afterTerminalStop,
		suspendProtocols: screen.suspendTerminalProtocols,
		resumeProtocols:  screen.resumeTerminalProtocols,
		mountedRoots:     screen.mountedRoots,
	})
	return screen
}

func (s *AltScreen) SetKeybindings(manager *KeybindingsManager) {
	s.activeKeybindings = manager
}

func (s *AltScreen) SetLayoutRoot(component Component) {
	s.frameMu.Lock()
	if s.layoutRoot == component {
		s.frameMu.Unlock()
		return
	}
	s.layoutRoot = component
	s.currentLayout = nil
	s.navFrame = nil
	s.navCursor = 0
	s.navRevision++
	s.frameMu.Unlock()
	if s.altScreenActive.Load() {
		s.RequestRender(false)
	}
}

func (s *AltScreen) mountedRoots() []Component {
	s.frameMu.RLock()
	root := s.layoutRoot
	s.frameMu.RUnlock()
	if root != nil {
		return []Component{root}
	}
	return s.Container.snapshot()
}

func (s *AltScreen) Render(width int) []string {
	s.frameMu.RLock()
	root := s.layoutRoot
	s.frameMu.RUnlock()
	if root != nil {
		return root.Render(width)
	}
	return s.Container.Render(width)
}

func (s *AltScreen) finalDocument(width int) []string {
	s.frameMu.RLock()
	root := s.layoutRoot
	s.frameMu.RUnlock()
	if renderer, ok := root.(documentRenderer); ok {
		return renderer.RenderDocument(width)
	}
	return s.Render(width)
}

func (s *AltScreen) primaryScrollView() *ScrollView {
	s.frameMu.RLock()
	layout := s.currentLayout
	s.frameMu.RUnlock()
	if layout != nil && layout.PrimaryScrollView != nil {
		return layout.PrimaryScrollView
	}
	return s.implicitScrollView
}

func (s *AltScreen) resetRenderState() {
	s.frameMu.Lock()
	s.previousScreen = nil
	s.previousScreenWidth = 0
	s.previousScreenHeight = 0
	s.currentLayout = nil
	s.navFrame = nil
	s.navCursor = 0
	s.navRevision++
	s.frameMu.Unlock()
}

func (s *AltScreen) mouseTarget() *mouseDispatchTarget {
	s.frameMu.RLock()
	defer s.frameMu.RUnlock()
	if s.mouseCapture != nil {
		return s.mouseCapture
	}
	return s.mousePressTarget
}

func (s *AltScreen) noteMouseMovement(x, y int) {
	s.frameMu.Lock()
	if s.mousePressPoint != nil && (x != s.mousePressPoint.x || y != s.mousePressPoint.y) {
		s.mousePressMoved = true
		s.lastClick = nil
	}
	s.frameMu.Unlock()
}

func (s *AltScreen) mouseClickReady(x, y int) bool {
	s.frameMu.RLock()
	defer s.frameMu.RUnlock()
	return !s.mousePressMoved && s.mousePressPoint != nil && s.mousePressPoint.x == x && s.mousePressPoint.y == y
}

func (s *AltScreen) clearMousePress() {
	s.frameMu.Lock()
	s.mouseCapture = nil
	s.mousePressTarget = nil
	s.mousePressPoint = nil
	s.mousePressMoved = false
	s.frameMu.Unlock()
}

func (s *AltScreen) setMousePress(target *mouseDispatchTarget, x, y int) {
	point := struct{ x, y int }{x, y}
	s.frameMu.Lock()
	s.mousePressTarget = target
	s.mousePressPoint = &point
	s.mousePressMoved = false
	s.frameMu.Unlock()
}

func (s *AltScreen) setMouseCapture(target *mouseDispatchTarget) {
	s.frameMu.Lock()
	s.mouseCapture = target
	s.frameMu.Unlock()
}

func (s *AltScreen) hasMousePressTarget() bool {
	s.frameMu.RLock()
	defer s.frameMu.RUnlock()
	return s.mousePressTarget != nil
}

func (s *AltScreen) beforeTerminalStart() {
	s.altScreenActive.Store(true)
	s.frameMu.Lock()
	s.lastDocument = nil
	s.mouseCapture = nil
	s.mousePressTarget = nil
	s.mousePressPoint = nil
	s.mousePressMoved = false
	s.lastClick = nil
	s.previousScreen = nil
	s.previousScreenWidth = 0
	s.previousScreenHeight = 0
	s.currentLayout = nil
	s.navFrame = nil
	s.navCursor = 0
	s.navRevision++
	s.frameMu.Unlock()
	term := strings.ToLower(envLookup("TERM"))
	multiplexed := envLookup("TMUX") != "" || envLookup("ZELLIJ") != "" || envLookup("STY") != "" ||
		strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen")
	mouseSequence := MouseEnable(!multiplexed)
	if !s.mouseEnabled {
		mouseSequence = ""
	}
	s.Terminal().Write(AltScreenEnter + AutowrapDisable + mouseSequence +
		CursorEraseScreen + CursorHome + CursorHide)
}

func (s *AltScreen) suspendTerminalProtocols() {
	if !s.altScreenActive.Load() {
		return
	}
	s.Terminal().Write(SyncOutputBegin + MouseDisable() + AutowrapEnable + AltScreenExit + CursorShow + SyncOutputEnd)
}

func (s *AltScreen) resumeTerminalProtocols() {
	if !s.altScreenActive.Load() {
		return
	}
	s.beforeTerminalStart()
}

func (s *AltScreen) beforeTerminalStop(StopOptions) {
	if !s.altScreenActive.Load() {
		return
	}
	s.Terminal().Write(SyncOutputBegin + MouseDisable() + AutowrapEnable + SyncOutputEnd)
}

func (s *AltScreen) afterTerminalStop(options StopOptions) {
	if !s.altScreenActive.Load() {
		return
	}
	s.altScreenActive.Store(false)
	terminal := s.Terminal()
	if options.PreserveScreen {
		terminal.Write(SyncOutputBegin + AltScreenExit + CursorShow + SyncOutputEnd)
		return
	}
	width := maxInt(1, terminal.Columns())
	document := s.finalDocument(width)
	for i, line := range document {
		document[i] = stripCursorMarker(trimOSC133Zone(line))
	}
	document = s.ApplyLineResets(document)
	for i, line := range document {
		if VisibleWidth(line) > width {
			document[i] = SliceByColumn(line, 0, width, true)
		}
	}
	s.frameMu.Lock()
	s.lastDocument = document
	s.frameMu.Unlock()

	var buffer strings.Builder
	buffer.WriteString(SyncOutputBegin)
	buffer.WriteString(AltScreenExit)
	buffer.WriteString(AutowrapDisable)
	for row, line := range document {
		if row > 0 {
			buffer.WriteString("\r\n")
		}
		buffer.WriteString("\r")
		buffer.WriteString(CursorEraseLine)
		buffer.WriteString(line)
	}
	buffer.WriteString(SGRReset)
	buffer.WriteString(AutowrapEnable)
	buffer.WriteString("\r\n")
	buffer.WriteString(CursorShow)
	buffer.WriteString(SyncOutputEnd)
	terminal.Write(buffer.String())
}

func stripCursorMarker(line string) string {
	return strings.ReplaceAll(line, CursorMarker, "")
}

func (s *AltScreen) doRender() {
	if s.IsStopped() || s.IsSuspended() || !s.altScreenActive.Load() {
		return
	}
	width := maxInt(1, s.Terminal().Columns())
	height := maxInt(1, s.Terminal().Rows())

	s.frameMu.RLock()
	root := s.layoutRoot
	previousScreen := s.previousScreen
	previousScreenWidth := s.previousScreenWidth
	previousScreenHeight := s.previousScreenHeight
	startRevision := s.navRevision
	s.frameMu.RUnlock()
	if root == nil {
		root = s.implicitScrollView
	}
	layout := RenderLayoutFrame(root, width, height, func() { s.RequestRender(false) })

	screen := make([]string, len(layout.Lines))
	for i, line := range layout.Lines {
		screen[i] = trimOSC133Zone(line)
	}
	screen = s.compositeOverlays(screen, width, height)
	if len(screen) > height {
		screen = screen[len(screen)-height:]
	}
	for len(screen) < height {
		screen = append(screen, "")
	}
	cursorRow, cursorCol, cursorFound := s.ExtractCursorPosition(screen, height)
	screen = s.ApplyLineResets(screen)
	for i, line := range screen {
		if VisibleWidth(line) > width {
			screen[i] = SliceByColumn(line, 0, width, true)
		}
	}

	fullRedraw := len(previousScreen) == 0 ||
		previousScreenWidth != width || previousScreenHeight != height

	var buffer strings.Builder
	buffer.WriteString(SyncOutputBegin)
	if fullRedraw {
		s.mu.Lock()
		s.fullRedraws++
		s.mu.Unlock()
		buffer.WriteString(CursorEraseScreen)
	}
	for row := 0; row < height; row++ {
		if !fullRedraw && screen[row] == previousScreen[row] {
			continue
		}
		buffer.WriteString(CursorTo(row, 0))
		buffer.WriteString(CursorEraseLine)
		buffer.WriteString(screen[row])
	}
	if cursorFound {
		buffer.WriteString(CursorTo(cursorRow, minInt(width, cursorCol)))
		if s.ShowHardwareCursor() {
			buffer.WriteString(CursorShow)
		} else {
			buffer.WriteString(CursorHide)
		}
	} else {
		buffer.WriteString(CursorHide)
	}
	buffer.WriteString(SyncOutputEnd)
	s.Terminal().Write(buffer.String())

	s.enterLayoutPublishGate()
	s.frameMu.Lock()
	s.previousScreen = screen
	s.previousScreenWidth = width
	s.previousScreenHeight = height
	s.currentLayout = layout
	s.navFrame = layout
	if s.navRevision == startRevision {
		s.navCursor = layout.PrimaryScrollTop
	} else {
		s.navCursor = bindCursorToFrame(layout, s.navCursor)
	}
	s.frameMu.Unlock()
}

func (s *AltScreen) enterLayoutPublishGate() {
	if gate := s.layoutPublishGate.Load(); gate != nil {
		(*gate)()
	}
}

func (s *AltScreen) handleViewportInput(data string) InputListenerResult {
	if data == FocusIn {
		return InputListenerResult{Consume: true}
	}
	if data == FocusOut {
		s.clearMousePress()
		return InputListenerResult{Consume: true}
	}

	if event, ok := parseWheelEvent(data); ok {
		s.handleWheelEvent(event)
		return InputListenerResult{Consume: true}
	}
	if event, ok := parseSGRMouseEvent(data); ok {
		s.handleMouseEvent(event)
		return InputListenerResult{Consume: true}
	}
	if isMouseSequence(data) {
		return InputListenerResult{Consume: true}
	}

	keybindings := s.activeKeybindings
	if keybindings == nil {
		keybindings = NewDefaultKeybindingsManager(nil)
	}
	isRelease := IsKeyRelease(data)

	if keybindings.Matches(data, "tui.altScreen.pageUp") {
		if !isRelease {
			s.scrollNav(func(viewport int) int { return -maxInt(1, viewport-pageScrollOverlap) })
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.pageDown") {
		if !isRelease {
			s.scrollNav(func(viewport int) int { return maxInt(1, viewport-pageScrollOverlap) })
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageUp") {
		if !isRelease {
			s.scrollNav(func(viewport int) int { return -maxInt(1, viewport/2) })
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageDown") {
		if !isRelease {
			s.scrollNav(func(viewport int) int { return maxInt(1, viewport/2) })
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineUp") {
		if !isRelease {
			s.scrollNav(func(int) int { return -1 })
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineDown") {
		if !isRelease {
			s.scrollNav(func(int) int { return 1 })
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.previousPrompt") {
		if !isRelease {
			s.scrollToPrompt(-1)
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.nextPrompt") {
		if !isRelease {
			s.scrollToPrompt(1)
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.top") {
		if !isRelease {
			s.scrollToStartNav()
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.bottom") {
		if !isRelease {
			s.scrollToEndNav()
		}
		return InputListenerResult{Consume: true}
	}
	return InputListenerResult{}
}

type navPosition struct {
	layout       *LayoutFrame
	scrollView   *ScrollView
	box          *LayoutBox
	cursor       int
	maxScrollTop int
	viewport     int
	hasBounds    bool
}

func (s *AltScreen) navPositionLocked() navPosition {
	position := navPosition{layout: s.currentLayout}
	if position.layout == nil {
		position.scrollView = s.implicitScrollView
		return position
	}
	position.scrollView = position.layout.PrimaryScrollView
	if position.scrollView == nil {
		position.scrollView = s.implicitScrollView
	}
	if s.navFrame == position.layout {
		position.cursor = s.navCursor
	} else {
		position.cursor = position.layout.PrimaryScrollTop
	}
	position.box = GetScrollViewBox(position.layout, position.scrollView)
	if position.box != nil {
		position.hasBounds = true
		position.maxScrollTop = capturedMaxScrollTop(position.box)
		position.viewport = position.box.Rect.Height
		position.cursor = maxInt(0, minInt(position.maxScrollTop, position.cursor))
	}
	return position
}

func bindCursorToFrame(layout *LayoutFrame, cursor int) int {
	cursor = maxInt(0, cursor)
	if layout == nil || layout.PrimaryScrollView == nil {
		return cursor
	}
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil {
		return cursor
	}
	return maxInt(0, minInt(capturedMaxScrollTop(box), cursor))
}

func (s *AltScreen) publishNavCursor(layout *LayoutFrame, cursor int) {
	s.frameMu.Lock()
	s.navRevision++
	if s.currentLayout != nil {
		s.navFrame = s.currentLayout
	} else {
		s.navFrame = layout
	}
	s.navCursor = maxInt(0, cursor)
	s.frameMu.Unlock()
}

func (s *AltScreen) scrollNav(step func(viewport int) int) {
	s.frameMu.Lock()
	position := s.navPositionLocked()
	s.frameMu.Unlock()
	if position.scrollView == nil {
		return
	}
	if position.hasBounds {
		delta := step(position.viewport)
		next := maxInt(0, minInt(position.maxScrollTop, position.cursor+delta))
		s.publishNavCursor(position.layout, next)
		position.scrollView.ScrollTo(next, ScrollToOptions{})
		s.RequestRender(false)
		return
	}
	position.scrollView.ScrollBy(step(maxInt(1, position.scrollView.ViewportHeight())))
	s.RequestRender(false)
}

func (s *AltScreen) scrollToStartNav() {
	s.frameMu.Lock()
	position := s.navPositionLocked()
	s.frameMu.Unlock()
	if position.scrollView == nil {
		return
	}
	if position.hasBounds {
		s.publishNavCursor(position.layout, 0)
	}
	position.scrollView.ScrollToStart()
	s.RequestRender(false)
}

func (s *AltScreen) scrollToEndNav() {
	s.frameMu.Lock()
	position := s.navPositionLocked()
	s.frameMu.Unlock()
	if position.scrollView == nil {
		return
	}
	if position.hasBounds {
		s.publishNavCursor(position.layout, position.maxScrollTop)
	}
	position.scrollView.ScrollToEnd()
	s.RequestRender(false)
}

func (s *AltScreen) scrollToPrompt(direction int) {
	s.frameMu.Lock()
	position := s.navPositionLocked()
	s.frameMu.Unlock()
	if position.layout == nil || position.scrollView == nil || position.box == nil {
		return
	}
	lines := position.box.scrollContentLines
	if len(lines) == 0 {
		return
	}
	start := maxInt(0, minInt(position.cursor, len(lines)-1))
	target := -1
	if direction < 0 {
		for row := start - 1; row >= 0; row-- {
			if strings.HasPrefix(lines[row], OSC133PromptStart) {
				target = row
				break
			}
		}
	} else {
		for row := start + 1; row < len(lines); row++ {
			if strings.HasPrefix(lines[row], OSC133PromptStart) {
				target = row
				break
			}
		}
	}
	if target < 0 {
		return
	}
	s.publishNavCursor(position.layout, target)
	position.scrollView.ScrollTo(target, ScrollToOptions{DisableFollow: true})
	s.RequestRender(false)
}

func capturedMaxScrollTop(box *LayoutBox) int {
	if len(box.Children) == 0 {
		return maxInt(0, len(box.scrollContentLines)-1)
	}
	return maxInt(0, box.Children[0].Rect.Height-box.Rect.Height)
}

func (s *AltScreen) handleWheelEvent(event parsedWheelEvent) {
	delta := event.direction * s.wheelLinesFor(event.button)
	mouseEvent := MouseEvent{
		Type:       MouseWheel,
		Button:     MouseButtonNone,
		X:          event.x,
		Y:          event.y,
		ScreenX:    event.x,
		ScreenY:    event.y,
		Width:      maxInt(1, s.Terminal().Columns()),
		Height:     maxInt(1, s.Terminal().Rows()),
		Shift:      event.button&4 != 0,
		Alt:        event.button&8 != 0,
		Ctrl:       event.button&16 != 0,
		WheelDelta: delta,
	}
	hit, result := s.dispatchMouseToOverlay(mouseEvent)
	var dispatched *mouseDispatchResult
	if result != nil {
		dispatched = result
	} else if !hit {
		dispatched = s.dispatchMouseToLayout(mouseEvent)
	}
	if dispatched != nil {
		s.applyMouseDispatchResult(mouseEvent, dispatched)
		return
	}
	s.routeWheel(event, delta)
}

func (s *AltScreen) wheelLinesFor(button int) int {
	if button&8 != 0 {
		return s.wheelScrollLines * altWheelScrollMultiplier
	}
	return s.wheelScrollLines
}

func (s *AltScreen) routeWheel(event parsedWheelEvent, delta int) {
	s.frameMu.Lock()
	position := s.navPositionLocked()
	s.frameMu.Unlock()
	primary := position.scrollView
	remaining := delta
	cursor := position.cursor
	primaryConsumed := 0
	seen := make(map[*ScrollView]struct{})
	if position.layout != nil {
		for _, scrollView := range getScrollViewsAt(position.layout, event.x, event.y) {
			seen[scrollView] = struct{}{}
			if scrollView == primary && position.hasBounds {
				target := maxInt(0, minInt(position.maxScrollTop, cursor+remaining))
				consumed := target - cursor
				scrollView.ScrollTo(target, ScrollToOptions{})
				remaining -= consumed
				primaryConsumed += consumed
				cursor = target
			} else {
				before := scrollView.ScrollTop()
				scrollView.ScrollTo(before+remaining, ScrollToOptions{})
				remaining -= scrollView.ScrollTop() - before
			}
			if remaining == 0 || scrollView.overscroll == "contain" {
				break
			}
		}
	}
	if primary != nil {
		if _, wasSeen := seen[primary]; remaining != 0 && !wasSeen {
			if position.hasBounds {
				target := maxInt(0, minInt(position.maxScrollTop, cursor+remaining))
				consumed := target - cursor
				primary.ScrollTo(target, ScrollToOptions{})
				remaining -= consumed
				primaryConsumed += consumed
				cursor = target
			} else {
				before := primary.ScrollTop()
				primary.ScrollTo(before+remaining, ScrollToOptions{})
				primaryConsumed += primary.ScrollTop() - before
			}
		}
	}
	if primaryConsumed != 0 && position.hasBounds {
		s.publishNavCursor(position.layout, cursor)
	}
	s.RequestRender(false)
}

func (s *AltScreen) dispatchMouseToLayout(event MouseEvent) *mouseDispatchResult {
	s.frameMu.RLock()
	layout := s.currentLayout
	s.frameMu.RUnlock()
	if layout == nil {
		return nil
	}
	visited := make(map[Component]struct{})
	for _, box := range getLayoutBoxesAt(layout, event.ScreenX, event.ScreenY) {
		if _, seen := visited[box.Component]; seen {
			continue
		}
		if _, isStack := box.Component.(StackLayoutProvider); isStack {
			continue
		}
		if _, isScroll := box.Component.(ScrollLayoutProvider); isScroll {
			continue
		}
		visited[box.Component] = struct{}{}
		local := event
		local.X = event.ScreenX - box.Rect.X
		local.Y = event.ScreenY - box.Rect.Y
		local.Width = box.Rect.Width
		local.Height = box.Rect.Height
		if result := dispatchMouseEvent(box.Component, local); result != nil {
			return result
		}
	}
	return nil
}

type parsedMouseEvent struct {
	button  int
	x       int
	y       int
	release bool
}

func parseSGRMouseEvent(data string) (parsedMouseEvent, bool) {
	match := sgrMouseRe.FindStringSubmatch(data)
	if match == nil {
		return parsedMouseEvent{}, false
	}
	button := parseIntSafe(match[1])
	x := parseIntSafe(match[2]) - 1
	y := parseIntSafe(match[3]) - 1
	return parsedMouseEvent{button: button, x: x, y: y, release: strings.HasSuffix(data, "m")}, true
}

type parsedWheelEvent struct {
	direction int
	x         int
	y         int
	button    int
}

func parseWheelEvent(data string) (parsedWheelEvent, bool) {
	if match := sgrMouseRe.FindStringSubmatch(data); match != nil {
		button := parseIntSafe(match[1])
		if button&64 == 0 {
			return parsedWheelEvent{}, false
		}
		direction := button & 3
		if direction != 0 && direction != 1 {
			return parsedWheelEvent{}, false
		}
		wheel := parsedWheelEvent{
			x:      parseIntSafe(match[2]) - 1,
			y:      parseIntSafe(match[3]) - 1,
			button: button,
		}
		if direction == 0 {
			wheel.direction = -1
		} else {
			wheel.direction = 1
		}
		return wheel, true
	}
	if len(data) == 6 && strings.HasPrefix(data, "\x1b[M") {
		button := int(data[3]) - 32
		if button&64 == 0 {
			return parsedWheelEvent{}, false
		}
		direction := button & 3
		if direction != 0 && direction != 1 {
			return parsedWheelEvent{}, false
		}
		wheel := parsedWheelEvent{
			x:      int(data[4]) - 33,
			y:      int(data[5]) - 33,
			button: button,
		}
		if direction == 0 {
			wheel.direction = -1
		} else {
			wheel.direction = 1
		}
		return wheel, true
	}
	return parsedWheelEvent{}, false
}

func isMouseSequence(data string) bool {
	if sgrMouseRe.MatchString(data) {
		return true
	}
	return len(data) == 6 && strings.HasPrefix(data, "\x1b[M")
}

func parseIntSafe(value string) int {
	result := 0
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0
		}
		result = result*10 + int(value[i]-'0')
	}
	return result
}

func decodeMouseButton(button int) MouseButton {
	switch button & 3 {
	case 0:
		return MouseButtonLeft
	case 1:
		return MouseButtonMiddle
	case 2:
		return MouseButtonRight
	}
	return MouseButtonNone
}

func (s *AltScreen) handleMouseEvent(raw parsedMouseEvent) {
	isMotion := raw.button&32 != 0
	eventType := MousePress
	switch {
	case raw.release:
		eventType = MouseRelease
	case isMotion && decodeMouseButton(raw.button) == MouseButtonNone:
		eventType = MouseMove
	case isMotion:
		eventType = MouseDrag
	}
	event := MouseEvent{
		Type:    eventType,
		Button:  decodeMouseButton(raw.button),
		X:       raw.x,
		Y:       raw.y,
		ScreenX: raw.x,
		ScreenY: raw.y,
		Width:   maxInt(1, s.Terminal().Columns()),
		Height:  maxInt(1, s.Terminal().Rows()),
		Shift:   raw.button&4 != 0,
		Alt:     raw.button&8 != 0,
		Ctrl:    raw.button&16 != 0,
	}

	target := s.mouseTarget()
	if target != nil {
		s.noteMouseMovement(raw.x, raw.y)
		render := false
		targetResult := dispatchMouseEvent(target.component, retargetMouseEvent(event, *target))
		if targetResult != nil {
			render = s.applyMouseDispatchResult(event, targetResult)
		}
		if raw.release {
			if s.mouseClickReady(raw.x, raw.y) {
				clickEvent := event
				clickEvent.Type = MouseClick
				clickEvent.ClickCount = s.componentClickCount(target.component, raw.x, raw.y)
				clickResult := dispatchMouseEvent(target.component, retargetMouseEvent(clickEvent, *target))
				if clickResult != nil {
					render = s.applyMouseDispatchResult(clickEvent, clickResult) || render
				}
			}
			s.clearMousePress()
		}
		if render {
			s.RequestRender(false)
		}
		return
	}

	hit, overlayResult := s.dispatchMouseToOverlay(event)
	var result *mouseDispatchResult
	if overlayResult != nil {
		result = overlayResult
	} else if !hit {
		result = s.dispatchMouseToLayout(event)
	}
	if result != nil {
		render := s.applyMouseDispatchResult(event, result)
		if eventType == MousePress {
			s.setMousePress(&result.target, raw.x, raw.y)
		}
		if result.result.Capture {
			s.setMouseCapture(&result.target)
		}
		if render {
			s.RequestRender(false)
		}
		return
	}
	if raw.release && !s.hasMousePressTarget() {
		s.RequestRender(false)
	}
}

func (s *AltScreen) componentClickCount(component Component, x, y int) int {
	now := time.Now()
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	count := 1
	if s.lastClick != nil &&
		now.Sub(s.lastClick.timestamp) <= doubleClickInterval &&
		s.lastClick.component == component &&
		s.lastClick.x == x && s.lastClick.y == y {
		count = s.lastClick.count%3 + 1
	}
	s.lastClick = &struct {
		timestamp time.Time
		component Component
		x, y      int
		count     int
	}{timestamp: now, component: component, x: x, y: y, count: count}
	return count
}
