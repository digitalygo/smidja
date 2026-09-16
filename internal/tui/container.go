package tui

import "sync"

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
	mu          sync.RWMutex
	children    []Component
	mouseWidth  int
	mouseLayout []mouseChild
}

func (c *Container) snapshot() []Component {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Component(nil), c.children...)
}

func (c *Container) AddChild(component Component) {
	c.mu.Lock()
	c.children = append(c.children, component)
	c.mu.Unlock()
}

func (c *Container) RemoveChild(component Component) {
	c.mu.Lock()
	for index, child := range c.children {
		if child == component {
			c.children = append(c.children[:index], c.children[index+1:]...)
			break
		}
	}
	c.mu.Unlock()
}

func (c *Container) Clear() {
	c.mu.Lock()
	c.children = nil
	c.mouseLayout = nil
	c.mu.Unlock()
}

func (c *Container) Children() []Component {
	return c.snapshot()
}

func (c *Container) Invalidate() {
	for _, child := range c.snapshot() {
		child.Invalidate()
	}
}

func (c *Container) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
	return containsComponentChildren(container.snapshot(), target)
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

func (b *Base) handleTerminalInput(data string) {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	listeners := append([]registeredListener(nil), b.inputListeners...)
	b.mu.Unlock()

	for _, registered := range listeners {
		result := registered.listener(data)
		if result.Consume {
			return
		}
		if result.HasData {
			data = result.Data
		}
	}
	if data == "" {
		return
	}

	b.mu.Lock()
	onDebug := b.onDebug
	if onDebug != nil && MatchesKey(data, "shift+ctrl+d") {
		b.mu.Unlock()
		onDebug()
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
