package tui

import (
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

type StopOptions struct {
	PreserveScreen bool
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

func (b *Base) Mode() string { return b.mode }

func (b *Base) FullRedraws() int { return b.fullRedraws }

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

func (b *Base) ClearOnShrink() bool { return b.clearOnShrink }

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
