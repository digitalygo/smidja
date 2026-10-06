package ui

import (
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type extensionComponent struct {
	component sdk.Component

	stateMu        sync.Mutex
	prepared       []string
	preparedWidth  int
	preparedValid  bool
	dirty          bool
	disposed       bool
	failed         bool
	inflight       int
	disposePending bool
	rawDisposed    bool
	syncRender     bool
	reporter       func(kind string, recovered any)
}

func wrapExtensionComponent(component sdk.Component) *extensionComponent {
	return wrapExtensionComponentWithReporter(component, nil)
}

func wrapExtensionComponentWithReporter(component sdk.Component, reporter func(kind string, recovered any)) *extensionComponent {
	if component == nil {
		return nil
	}
	if wrapped, ok := component.(*extensionComponent); ok {
		return wrapped
	}
	return &extensionComponent{component: component, reporter: reporter}
}

func wrapExtensionComponentSync(component sdk.Component, reporter func(kind string, recovered any)) *extensionComponent {
	wrapped := wrapExtensionComponentWithReporter(component, reporter)
	if wrapped != nil {
		wrapped.syncRender = true
	}
	return wrapped
}

func (c *extensionComponent) report(kind string, recovered any) {
	if c.reporter != nil {
		c.reporter(kind, recovered)
	}
}

func (c *extensionComponent) beginCallback(ignoreFailed bool) (dirty bool, ok bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.disposed || (c.failed && !ignoreFailed) {
		return false, false
	}
	dirty = c.dirty
	c.dirty = false
	c.inflight++
	return dirty, true
}

func (c *extensionComponent) endCallback() {
	c.stateMu.Lock()
	if c.inflight > 0 {
		c.inflight--
	}
	pending := c.disposePending && c.inflight == 0
	if pending {
		c.disposePending = false
	}
	c.stateMu.Unlock()
	if pending {
		c.disposeRaw()
	}
}

func (c *extensionComponent) renderRaw(dirty bool, width int) (lines []string, failed bool, retired bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failed = true
			lines = nil
			c.report("component-render", recovered)
		}
	}()
	if dirty {
		c.component.Invalidate()
		c.stateMu.Lock()
		retired = c.disposed
		c.stateMu.Unlock()
		if retired {
			return nil, false, true
		}
	}
	return c.component.Render(width), false, false
}

func (c *extensionComponent) PrepareExternalFrame(width int) {
	if c.syncRender {
		return
	}
	dirty, ok := c.beginCallback(false)
	if !ok {
		return
	}
	lines, failed, retired := c.renderRaw(dirty, width)
	cleaned := interactive.SanitizeComponentFrame(lines)
	c.stateMu.Lock()
	switch {
	case c.disposed || retired:
	case failed:
		c.failed = true
		c.prepared = []string{"[extension component error]"}
		c.preparedWidth = width
		c.preparedValid = true
	default:
		c.prepared = cleaned
		c.preparedWidth = width
		c.preparedValid = true
	}
	c.stateMu.Unlock()
	c.endCallback()
}

func (c *extensionComponent) Render(width int) []string {
	if c.syncRender {
		return c.renderSync(width)
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.disposed || !c.preparedValid || c.preparedWidth != width {
		return nil
	}
	return append([]string(nil), c.prepared...)
}

func (c *extensionComponent) renderSync(width int) []string {
	dirty, ok := c.beginCallback(false)
	if !ok {
		return nil
	}
	lines, failed, retired := c.renderRaw(dirty, width)
	if failed {
		c.stateMu.Lock()
		c.failed = true
		c.stateMu.Unlock()
		c.endCallback()
		return nil
	}
	if !retired {
		c.stateMu.Lock()
		retired = c.disposed
		c.stateMu.Unlock()
	}
	if retired {
		c.endCallback()
		return nil
	}
	cleaned := interactive.SanitizeComponentFrame(lines)
	c.endCallback()
	return cleaned
}

func (c *extensionComponent) Invalidate() {
	c.stateMu.Lock()
	c.dirty = true
	c.stateMu.Unlock()
}

func (c *extensionComponent) HandleInput(data string) {
	handler, ok := c.component.(sdk.InputHandler)
	if !ok {
		return
	}
	if _, ok := c.beginCallback(true); !ok {
		return
	}
	defer c.endCallback()
	defer func() {
		if recovered := recover(); recovered != nil {
			c.report("component-input", recovered)
		}
	}()
	handler.HandleInput(data)
}

func (c *extensionComponent) Dispose() {
	c.stateMu.Lock()
	if c.disposed {
		c.stateMu.Unlock()
		return
	}
	c.disposed = true
	c.prepared = nil
	c.preparedValid = false
	if c.inflight > 0 {
		c.disposePending = true
		c.stateMu.Unlock()
		return
	}
	c.stateMu.Unlock()
	c.disposeRaw()
}

func (c *extensionComponent) disposeRaw() {
	c.stateMu.Lock()
	if c.rawDisposed {
		c.stateMu.Unlock()
		return
	}
	c.rawDisposed = true
	c.stateMu.Unlock()
	disposable, ok := c.component.(sdk.Disposable)
	if !ok {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			c.report("component-dispose", recovered)
		}
	}()
	disposable.Dispose()
}

type sdkTheme struct {
	theme *tui.Theme
}

func (t sdkTheme) Name() string {
	if t.theme == nil {
		return ""
	}
	return t.theme.Name
}

func (t sdkTheme) Fg(token string, text string) string {
	if t.theme == nil {
		return text
	}
	return t.theme.Fg(tui.ThemeColor(token), text)
}

func (t sdkTheme) Bg(token string, text string) string {
	if t.theme == nil {
		return text
	}
	return t.theme.Bg(tui.ThemeColor(token), text)
}

type sdkKeybindings struct {
	manager *tui.KeybindingsManager
}

func (k sdkKeybindings) Keys(action string) []string {
	if k.manager == nil {
		return nil
	}
	return k.manager.Keys(action)
}

type sdkEditorAdapter struct {
	editor    sdk.EditorComponent
	component *extensionComponent
	mu        sync.Mutex
	onSubmit  func(string)
	onChange  func(string)
	reporter  func(kind string, recovered any)
	fallback  func() string
}

func newSDKEditorAdapter(component sdk.EditorComponent, reporter func(kind string, recovered any), fallback func() string) (*sdkEditorAdapter, error) {
	adapter := &sdkEditorAdapter{
		editor:    component,
		component: wrapExtensionComponentWithReporter(component, reporter),
		reporter:  reporter,
		fallback:  fallback,
	}
	if err := invokeEditorCallback(reporter, "editor-set-on-submit", func() { component.SetOnSubmit(adapter.dispatchSubmit) }); err != nil {
		return nil, err
	}
	if err := invokeEditorCallback(reporter, "editor-set-on-change", func() { component.SetOnChange(adapter.dispatchChange) }); err != nil {
		return nil, err
	}
	return adapter, nil
}

func (a *sdkEditorAdapter) dispatchSubmit(text string) {
	a.mu.Lock()
	callback := a.onSubmit
	a.mu.Unlock()
	if callback != nil {
		_ = invokeEditorCallback(a.reporter, "editor-submit", func() { callback(text) })
	}
}

func (a *sdkEditorAdapter) dispatchChange(text string) {
	a.mu.Lock()
	callback := a.onChange
	a.mu.Unlock()
	if callback != nil {
		_ = invokeEditorCallback(a.reporter, "editor-change", func() { callback(text) })
	}
}

func (a *sdkEditorAdapter) Render(width int) []string { return a.component.Render(width) }

func (a *sdkEditorAdapter) PrepareExternalFrame(width int) { a.component.PrepareExternalFrame(width) }

func (a *sdkEditorAdapter) Invalidate() { a.component.Invalidate() }

func (a *sdkEditorAdapter) HandleInput(data string) { a.component.HandleInput(data) }

func (a *sdkEditorAdapter) Dispose() { a.component.Dispose() }

func (a *sdkEditorAdapter) Text() (text string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.report("editor-text", recovered)
			text = a.fallbackText()
		}
	}()
	return a.editor.Text()
}

func (a *sdkEditorAdapter) SetText(value string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.report("editor-set-text", recovered)
		}
	}()
	a.editor.SetText(value)
}

func (a *sdkEditorAdapter) report(kind string, recovered any) {
	if a.reporter != nil {
		a.reporter(kind, recovered)
	}
}

func (a *sdkEditorAdapter) fallbackText() string {
	if a.fallback != nil {
		return a.fallback()
	}
	return ""
}

func (a *sdkEditorAdapter) SetOnSubmit(fn func(string)) {
	a.mu.Lock()
	a.onSubmit = fn
	a.mu.Unlock()
}

func (a *sdkEditorAdapter) SetOnChange(fn func(string)) {
	a.mu.Lock()
	a.onChange = fn
	a.mu.Unlock()
}

type defaultEditorComponent struct {
	mu           sync.Mutex
	editor       *tui.Editor
	baseOnSubmit func(string)
	baseOnChange func(string)
	onSubmit     func(string)
	onChange     func(string)
	reporter     func(kind string, recovered any)
}

func newDefaultEditorComponent(editor *tui.Editor, reporter func(kind string, recovered any)) *defaultEditorComponent {
	component := &defaultEditorComponent{editor: editor, reporter: reporter}
	component.baseOnSubmit = editor.OnSubmitCallback()
	component.baseOnChange = editor.OnChangeCallback()
	editor.SetOnSubmit(component.dispatchSubmit)
	editor.SetOnChange(component.dispatchChange)
	return component
}

func (d *defaultEditorComponent) dispatchSubmit(text string) {
	d.mu.Lock()
	base := d.baseOnSubmit
	callback := d.onSubmit
	d.mu.Unlock()
	if base != nil {
		_ = invokeEditorCallback(d.reporter, "editor-submit", func() { base(text) })
	}
	if callback != nil {
		_ = invokeEditorCallback(d.reporter, "editor-submit", func() { callback(text) })
	}
}

func (d *defaultEditorComponent) dispatchChange(text string) {
	d.mu.Lock()
	base := d.baseOnChange
	callback := d.onChange
	d.mu.Unlock()
	if base != nil {
		_ = invokeEditorCallback(d.reporter, "editor-change", func() { base(text) })
	}
	if callback != nil {
		_ = invokeEditorCallback(d.reporter, "editor-change", func() { callback(text) })
	}
}

func (d *defaultEditorComponent) Render(width int) []string { return d.editor.Render(width) }

func (d *defaultEditorComponent) Invalidate() { d.editor.Invalidate() }

func (d *defaultEditorComponent) HandleInput(data string) { d.editor.HandleInput(data) }

func (d *defaultEditorComponent) Text() string { return d.editor.Text() }

func (d *defaultEditorComponent) SetText(text string) { d.editor.SetText(text) }

func (d *defaultEditorComponent) SetOnSubmit(fn func(string)) {
	d.mu.Lock()
	d.onSubmit = fn
	d.mu.Unlock()
}

func (d *defaultEditorComponent) SetOnChange(fn func(string)) {
	d.mu.Lock()
	d.onChange = fn
	d.mu.Unlock()
}
