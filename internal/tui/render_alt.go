package tui

import (
	"os"
	"strings"
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

type AltScreen struct {
	*Base

	previousScreen       []string
	lastDocument         []string
	previousScreenWidth  int
	previousScreenHeight int

	layoutRoot         Component
	currentLayout      *LayoutFrame
	implicitDocument   *Container
	implicitScrollView *ScrollView

	altScreenActive  bool
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
		mountedRoots: func() []Component {
			if screen.layoutRoot != nil {
				return []Component{screen.layoutRoot}
			}
			return screen.Container.children
		},
	})
	return screen
}

func (s *AltScreen) SetKeybindings(manager *KeybindingsManager) {
	s.activeKeybindings = manager
}

func (s *AltScreen) SetLayoutRoot(component Component) {
	if s.layoutRoot == component {
		return
	}
	s.layoutRoot = component
	s.currentLayout = nil
	s.RequestRender(false)
}

func (s *AltScreen) Render(width int) []string {
	if s.layoutRoot != nil {
		return s.layoutRoot.Render(width)
	}
	return s.Container.Render(width)
}

func (s *AltScreen) primaryScrollView() *ScrollView {
	if s.currentLayout != nil && s.currentLayout.PrimaryScrollView != nil {
		return s.currentLayout.PrimaryScrollView
	}
	return s.implicitScrollView
}

func (s *AltScreen) resetRenderState() {
	s.previousScreen = nil
	s.previousScreenWidth = 0
	s.previousScreenHeight = 0
	s.currentLayout = nil
}

func (s *AltScreen) beforeTerminalStart() {
	s.altScreenActive = true
	s.lastDocument = nil
	s.mouseCapture = nil
	s.mousePressTarget = nil
	s.mousePressPoint = nil
	s.mousePressMoved = false
	s.lastClick = nil
	s.resetRenderState()
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

func (s *AltScreen) beforeTerminalStop(StopOptions) {
	if !s.altScreenActive {
		return
	}
	s.Terminal().Write(SyncOutputBegin + MouseDisable() + AutowrapEnable + SyncOutputEnd)
}

func (s *AltScreen) afterTerminalStop(options StopOptions) {
	if !s.altScreenActive {
		return
	}
	s.altScreenActive = false
	terminal := s.Terminal()
	if options.PreserveScreen {
		terminal.Write(SyncOutputBegin + AltScreenExit + CursorShow + SyncOutputEnd)
		return
	}
	width := maxInt(1, terminal.Columns())
	document := s.Render(width)
	for i, line := range document {
		document[i] = stripCursorMarker(trimOSC133Zone(line))
	}
	document = s.ApplyLineResets(document)
	for i, line := range document {
		if VisibleWidth(line) > width {
			document[i] = SliceByColumn(line, 0, width, true)
		}
	}
	s.lastDocument = document

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
	if s.IsStopped() || !s.altScreenActive {
		return
	}
	width := maxInt(1, s.Terminal().Columns())
	height := maxInt(1, s.Terminal().Rows())

	root := s.layoutRoot
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

	fullRedraw := len(s.previousScreen) == 0 ||
		s.previousScreenWidth != width || s.previousScreenHeight != height

	var buffer strings.Builder
	buffer.WriteString(SyncOutputBegin)
	if fullRedraw {
		s.mu.Lock()
		s.fullRedraws++
		s.mu.Unlock()
		buffer.WriteString(CursorEraseScreen)
	}
	for row := 0; row < height; row++ {
		if !fullRedraw && screen[row] == s.previousScreen[row] {
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

	s.previousScreen = screen
	s.previousScreenWidth = width
	s.previousScreenHeight = height
	s.currentLayout = layout
}

func (s *AltScreen) handleViewportInput(data string) InputListenerResult {
	if data == FocusIn {
		return InputListenerResult{Consume: true}
	}
	if data == FocusOut {
		s.mouseCapture = nil
		s.mousePressTarget = nil
		s.mousePressPoint = nil
		s.mousePressMoved = false
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

	scrollView := s.primaryScrollView()
	if keybindings.Matches(data, "tui.altScreen.pageUp") {
		if !isRelease {
			s.scrollBy(scrollView, -maxInt(1, scrollView.ViewportHeight()-pageScrollOverlap))
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.pageDown") {
		if !isRelease {
			s.scrollBy(scrollView, maxInt(1, scrollView.ViewportHeight()-pageScrollOverlap))
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageUp") {
		if !isRelease {
			s.scrollBy(scrollView, -maxInt(1, scrollView.ViewportHeight()/2))
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageDown") {
		if !isRelease {
			s.scrollBy(scrollView, maxInt(1, scrollView.ViewportHeight()/2))
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineUp") {
		if !isRelease {
			s.scrollBy(scrollView, -1)
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineDown") {
		if !isRelease {
			s.scrollBy(scrollView, 1)
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
			s.scrollBy(scrollView, -scrollView.scrollTop-maxInt(1, scrollView.scrollTop))
		}
		return InputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.bottom") {
		if !isRelease {
			scrollView.ScrollToEnd()
			s.RequestRender(false)
		}
		return InputListenerResult{Consume: true}
	}
	return InputListenerResult{}
}

func (s *AltScreen) scrollBy(scrollView *ScrollView, lines int) {
	scrollView.ScrollBy(lines)
	s.RequestRender(false)
}

func (s *AltScreen) scrollToPrompt(direction int) {
	layout := s.currentLayout
	if layout == nil {
		return
	}
	scrollView := s.primaryScrollView()
	box := GetScrollViewBox(layout, scrollView)
	if box == nil || box.scrollContentLines == nil {
		return
	}
	lines := box.scrollContentLines
	if direction < 0 {
		for row := scrollView.scrollTop - 1; row >= 0; row-- {
			if strings.HasPrefix(lines[row], OSC133PromptStart) {
				scrollView.ScrollTo(row, ScrollToOptions{DisableFollow: true})
				s.RequestRender(false)
				return
			}
		}
		return
	}
	for row := scrollView.scrollTop + 1; row < len(lines); row++ {
		if strings.HasPrefix(lines[row], OSC133PromptStart) {
			scrollView.ScrollTo(row, ScrollToOptions{DisableFollow: true})
			s.RequestRender(false)
			return
		}
	}
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
	remaining := delta
	var layout *LayoutFrame
	s.mu.Lock()
	layout = s.currentLayout
	s.mu.Unlock()
	seen := make(map[*ScrollView]struct{})
	if layout != nil {
		for _, scrollView := range getScrollViewsAt(layout, event.x, event.y) {
			seen[scrollView] = struct{}{}
			remaining = scrollView.ScrollBy(remaining)
			if remaining == 0 || scrollView.overscroll == "contain" {
				break
			}
		}
	}
	primary := s.primaryScrollView()
	if _, wasSeen := seen[primary]; remaining != 0 && !wasSeen {
		primary.ScrollBy(remaining)
	}
	s.RequestRender(false)
}

func (s *AltScreen) dispatchMouseToLayout(event MouseEvent) *mouseDispatchResult {
	s.mu.Lock()
	layout := s.currentLayout
	s.mu.Unlock()
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

	if s.mouseCapture != nil || s.mousePressTarget != nil {
		target := s.mouseCapture
		if target == nil {
			target = s.mousePressTarget
		}
		if s.mousePressPoint != nil && (raw.x != s.mousePressPoint.x || raw.y != s.mousePressPoint.y) {
			s.mousePressMoved = true
			s.lastClick = nil
		}
		render := false
		targetResult := dispatchMouseEvent(target.component, retargetMouseEvent(event, *target))
		if targetResult != nil {
			render = s.applyMouseDispatchResult(event, targetResult)
		}
		if raw.release {
			if !s.mousePressMoved && s.mousePressPoint != nil &&
				s.mousePressPoint.x == raw.x && s.mousePressPoint.y == raw.y {
				clickEvent := event
				clickEvent.Type = MouseClick
				clickEvent.ClickCount = s.componentClickCount(target.component, raw.x, raw.y)
				clickResult := dispatchMouseEvent(target.component, retargetMouseEvent(clickEvent, *target))
				if clickResult != nil {
					render = s.applyMouseDispatchResult(clickEvent, clickResult) || render
				}
			}
			s.mouseCapture = nil
			s.mousePressTarget = nil
			s.mousePressPoint = nil
			s.mousePressMoved = false
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
			s.mousePressTarget = &result.target
			point := struct{ x, y int }{raw.x, raw.y}
			s.mousePressPoint = &point
			s.mousePressMoved = false
		}
		if result.result.Capture {
			s.mouseCapture = &result.target
		}
		if render {
			s.RequestRender(false)
		}
		return
	}
	if raw.release && s.mousePressTarget == nil {
		s.RequestRender(false)
	}
}

func (s *AltScreen) componentClickCount(component Component, x, y int) int {
	now := time.Now()
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
