package ui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

var (
	ErrExtensionUIKeyEmpty    = errors.New("ui: extension UI key is empty")
	ErrExtensionUINilFactory  = errors.New("ui: extension component factory returned nil")
	ErrExtensionUINilHandler  = errors.New("ui: extension handler is nil")
	ErrExtensionUIUnavailable = errors.New("ui: extension UI is unavailable")
	ErrExtensionUIUnexpected  = errors.New("ui: extension UI registry is already attached")
)

type registeredInputHook struct {
	id      int
	handler sdk.TerminalInputHandler
}

type registeredAutocomplete struct {
	id       int
	provider sdk.AutocompleteProvider
}

type runnerExtensions struct {
	runner *Runner

	refreshMu      sync.Mutex
	refreshRunning bool
	refreshPending bool
	publishMu      sync.Mutex

	mu                     sync.Mutex
	registry               *extensionui.Registry
	headerFactory          sdk.ComponentFactory
	footerFactory          sdk.ComponentFactory
	headerComponent        tui.Component
	footerComponent        tui.Component
	widgetFactories        map[string]sdk.ComponentFactory
	widgetComponents       map[string]tui.Component
	widgetGenerations      map[string]uint64
	editorFactory          sdk.EditorFactory
	editorComponent        sdk.EditorComponent
	editorDefault          *defaultEditorComponent
	editorGeneration       uint64
	inputHooks             []registeredInputHook
	nextInputHookID        int
	autocomplete           []registeredAutocomplete
	nextAutocomplete       int
	autocompleteRegistered bool
	autocompleteStop       func()
	cleaned                bool
	warned                 map[string]struct{}
	editorText             string
}

func newRunnerExtensions(runner *Runner) *runnerExtensions {
	return &runnerExtensions{
		runner:            runner,
		widgetFactories:   map[string]sdk.ComponentFactory{},
		widgetComponents:  map[string]tui.Component{},
		widgetGenerations: map[string]uint64{},
		warned:            map[string]struct{}{},
	}
}

func (x *runnerExtensions) rememberEditorText(text string) {
	x.mu.Lock()
	x.editorText = text
	x.mu.Unlock()
}

func (x *runnerExtensions) cachedEditorText() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.editorText
}

func (x *runnerExtensions) isCleaned() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.cleaned
}

func buildExtensionComponent(factory sdk.ComponentFactory) (component tui.Component, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("ui: extension component factory panic: %v", recovered)
		}
	}()
	built := factory()
	if built == nil {
		return nil, ErrExtensionUINilFactory
	}
	return built, nil
}

func (r *Runner) wrapComponent(component sdk.Component) *extensionComponent {
	return wrapExtensionComponentWithReporter(component, r.reportExtensionPanic)
}

func (r *Runner) wrappedComponent(previous tui.Component, raw sdk.Component) tui.Component {
	if wrapped, ok := previous.(*extensionComponent); ok && sameSDKComponent(wrapped.component, raw) {
		return wrapped
	}
	return r.wrapComponent(raw)
}

func sameSDKComponent(first, second sdk.Component) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	firstValue := reflect.ValueOf(first)
	secondValue := reflect.ValueOf(second)
	if firstValue.Type() != secondValue.Type() {
		return false
	}
	if firstValue.Type().Comparable() {
		return first == second
	}
	if firstValue.Kind() == reflect.Pointer {
		return firstValue.Pointer() == secondValue.Pointer()
	}
	return false
}

func (r *Runner) buildWrappedComponent(factory sdk.ComponentFactory) (tui.Component, error) {
	built, err := buildExtensionComponent(factory)
	if err != nil {
		return nil, err
	}
	return r.wrapComponent(built), nil
}

func (r *Runner) reportExtensionPanic(kind string, recovered any) {
	if r.surface == nil {
		return
	}
	key := fmt.Sprintf("%s:%v", kind, recovered)
	r.ext.mu.Lock()
	if _, seen := r.ext.warned[key]; seen {
		r.ext.mu.Unlock()
		return
	}
	r.ext.warned[key] = struct{}{}
	r.ext.mu.Unlock()
	r.surface.AddNotice(interactive.NoticeWarning, fmt.Sprintf("extensions: %s callback panic: %v", kind, recovered))
}

func (r *Runner) AttachExtensionUI(registry *extensionui.Registry) error {
	if registry == nil {
		return errors.New("ui: extension UI registry is nil")
	}
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		return ErrExtensionUIUnavailable
	}
	if r.ext.registry != nil {
		r.ext.mu.Unlock()
		return ErrExtensionUIUnexpected
	}
	r.ext.registry = registry
	r.ext.mu.Unlock()
	registry.SetOnChange(r.refreshExtensionUI)
	if r.ext.isCleaned() {
		registry.SetOnChange(nil)
		return ErrExtensionUIUnavailable
	}
	r.refreshExtensionUI()
	return nil
}

func (r *Runner) extensionRegistry() *extensionui.Registry {
	r.ext.mu.Lock()
	defer r.ext.mu.Unlock()
	return r.ext.registry
}

const maxCoalescedExtensionRefreshes = 16

func (r *Runner) refreshExtensionUI() {
	r.ext.refreshMu.Lock()
	if r.ext.refreshRunning {
		r.ext.refreshPending = true
		r.ext.refreshMu.Unlock()
		return
	}
	r.ext.refreshRunning = true
	r.ext.refreshMu.Unlock()
	for pass := 0; ; pass++ {
		r.runExtensionRefresh()
		r.ext.refreshMu.Lock()
		more := r.ext.refreshPending
		r.ext.refreshPending = false
		atLimit := pass+1 >= maxCoalescedExtensionRefreshes
		if !more || atLimit {
			r.ext.refreshRunning = false
			r.ext.refreshMu.Unlock()
			if more {
				r.reportExtensionWarning("refresh", "extension refresh notification storm was coalesced and truncated")
			}
			return
		}
		r.ext.refreshMu.Unlock()
	}
}

func (r *Runner) runExtensionRefresh() {
	registry := r.extensionRegistry()
	if registry == nil || r.surface == nil || r.ext.isCleaned() {
		return
	}
	r.commitExtensionRenderers(registry)
	r.refreshExtensionWidgets(registry)
	if r.Active() {
		r.surface.RequestRender()
	}
}

func (r *Runner) commitExtensionRenderers(registry *extensionui.Registry) {
	messageRenderers := map[string]interactive.CustomRenderer{}
	for _, customType := range registry.MessageRendererTypes() {
		messageRenderers[customType] = r.extensionMessageRenderer(customType)
	}
	entryRenderers := map[string]interactive.CustomRenderer{}
	for _, customType := range registry.EntryRendererTypes() {
		entryRenderers[customType] = r.extensionEntryRenderer(customType)
	}
	transformer := r.composeMarkdownTransformers(registry.MarkdownTransformers())
	r.ext.publishMu.Lock()
	if r.ext.isCleaned() {
		r.ext.publishMu.Unlock()
		return
	}
	r.surface.SetMarkdownTransformer(transformer)
	discarded := r.surface.SwapCustomRenderers(messageRenderers, entryRenderers)
	r.ext.publishMu.Unlock()
	for _, component := range discarded {
		disposeTUIComponent(component)
	}
}

func (r *Runner) composeMarkdownTransformers(transformers []sdk.MarkdownTransformer) interactive.MarkdownTransformer {
	if len(transformers) == 0 {
		return nil
	}
	return func(markdown string, ctx interactive.MarkdownTransformContext) string {
		current := markdown
		for _, transformer := range transformers {
			if transformer == nil {
				continue
			}
			current = r.applyMarkdownTransformer(transformer, current, ctx)
		}
		return current
	}
}

func (r *Runner) applyMarkdownTransformer(transformer sdk.MarkdownTransformer, markdown string, ctx interactive.MarkdownTransformContext) (result string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.reportExtensionPanic("markdown-transformer", recovered)
			result = markdown
		}
	}()
	transformed := transformer(markdown, sdk.MarkdownTransformContext{
		Kind:      ctx.Kind,
		Streaming: ctx.Streaming,
		Width:     ctx.Width,
	})
	return interactive.SanitizeDisplayText(transformed)
}

func (r *Runner) refreshExtensionWidgets(registry *extensionui.Registry) {
	order, factories, generations := registry.WidgetSnapshot()
	type removedWidget struct {
		key       string
		component tui.Component
	}
	var removed []removedWidget
	r.ext.mu.Lock()
	for key := range r.ext.widgetFactories {
		if _, ok := factories[key]; !ok {
			delete(r.ext.widgetFactories, key)
			delete(r.ext.widgetGenerations, key)
			removed = append(removed, removedWidget{key: key, component: r.ext.widgetComponents[key]})
			delete(r.ext.widgetComponents, key)
		}
	}
	r.ext.mu.Unlock()
	for _, entry := range removed {
		if r.surface != nil {
			r.surface.ClearWidgetComponent(entry.key)
		} else {
			disposeTUIComponent(entry.component)
		}
	}
	for _, key := range order {
		factory, ok := factories[key]
		if !ok {
			continue
		}
		r.ext.mu.Lock()
		generation, known := r.ext.widgetGenerations[key]
		_, installed := r.ext.widgetFactories[key]
		r.ext.mu.Unlock()
		if known && installed && generation == generations[key] {
			continue
		}
		if err := r.SetWidgetComponent(key, factory); err != nil {
			if errors.Is(err, ErrExtensionUIUnavailable) {
				return
			}
			r.reportFactoryFailure("widget", err)
			continue
		}
		r.ext.mu.Lock()
		r.ext.widgetGenerations[key] = generations[key]
		r.ext.mu.Unlock()
	}
}

func (r *Runner) reportFactoryFailure(kind string, err error) {
	r.reportExtensionWarning(kind+"-factory", fmt.Sprintf("%s factory failed: %v", kind, err))
}

func (r *Runner) reportExtensionWarning(kind string, message string) {
	if r.surface == nil {
		return
	}
	key := kind + ":" + message
	r.ext.mu.Lock()
	if _, seen := r.ext.warned[key]; seen {
		r.ext.mu.Unlock()
		return
	}
	r.ext.warned[key] = struct{}{}
	r.ext.mu.Unlock()
	r.surface.AddNotice(interactive.NoticeWarning, "extensions: "+message)
}

func (r *Runner) sdkRenderContext(ctx interactive.CustomRenderContext) sdk.RenderContext {
	metadata := r.surface.ExtensionMetadata()
	return sdk.RenderContext{
		Width:         ctx.Width,
		Streaming:     ctx.Streaming,
		Theme:         sdkTheme{theme: ctx.Theme},
		Cwd:           metadata.Cwd,
		SessionID:     metadata.SessionID,
		Model:         metadata.Model,
		ThinkingLevel: sdk.ThinkingLevel(metadata.ThinkingLevel),
	}
}

func (r *Runner) extensionMessageRenderer(customType string) interactive.CustomRenderer {
	return func(ctx interactive.CustomRenderContext, entry interactive.CustomEntryView) tui.Component {
		registry := r.extensionRegistry()
		if registry == nil {
			return nil
		}
		renderer, ok := registry.MessageRenderer(customType)
		if !ok || renderer == nil {
			return nil
		}
		var component sdk.Component
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					r.reportExtensionPanic("message-renderer", recovered)
				}
			}()
			component = renderer(r.sdkRenderContext(ctx), sdk.CustomMessage{
				Type:    entry.CustomType,
				Content: entry.Text,
				Display: true,
				Details: entry.Data,
			})
		}()
		if component == nil {
			return nil
		}
		return r.wrapComponent(component)
	}
}

func (r *Runner) extensionEntryRenderer(customType string) interactive.CustomRenderer {
	return func(ctx interactive.CustomRenderContext, entry interactive.CustomEntryView) tui.Component {
		registry := r.extensionRegistry()
		if registry == nil {
			return nil
		}
		renderer, ok := registry.EntryRenderer(customType)
		if !ok || renderer == nil {
			return nil
		}
		var component sdk.Component
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					r.reportExtensionPanic("entry-renderer", recovered)
				}
			}()
			component = renderer(r.sdkRenderContext(ctx), sdk.Entry{CustomType: entry.CustomType, Data: entry.Data})
		}()
		if component == nil {
			return nil
		}
		return r.wrapComponent(component)
	}
}

func (r *Runner) extensionInputHooks() []sdk.TerminalInputHandler {
	r.ext.mu.Lock()
	hooks := make([]sdk.TerminalInputHandler, 0, len(r.ext.inputHooks))
	for _, registered := range r.ext.inputHooks {
		if registered.handler != nil {
			hooks = append(hooks, registered.handler)
		}
	}
	registry := r.ext.registry
	r.ext.mu.Unlock()
	if registry == nil {
		return hooks
	}
	registered := registry.TerminalInputHooks()
	return append(registered, hooks...)
}

func (r *Runner) applyInputHooks(data string) (string, bool, bool) {
	replaced := false
	for _, hook := range r.extensionInputHooks() {
		result, ok := r.runInputHook(hook, data)
		if !ok {
			continue
		}
		if result.Consume {
			return data, true, replaced
		}
		if result.Replace {
			data = result.Data
			replaced = true
		}
	}
	return data, data == "", replaced
}

func (r *Runner) runInputHook(hook sdk.TerminalInputHandler, data string) (result sdk.TerminalInputResult, ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			ok = false
			r.reportExtensionPanic("terminal-input-hook", recovered)
		}
	}()
	return hook(data), true
}

func (r *Runner) SetHeader(factory sdk.ComponentFactory) {
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		return
	}
	r.ext.headerFactory = factory
	r.ext.mu.Unlock()
	if !r.Active() || r.surface == nil {
		return
	}
	var raw sdk.Component
	if factory != nil {
		built, err := buildExtensionComponent(factory)
		if err != nil {
			r.reportFactoryFailure("header", err)
			return
		}
		raw = built
	}
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		disposeTUIComponent(raw)
		return
	}
	var component tui.Component
	if raw != nil {
		component = r.wrappedComponent(r.ext.headerComponent, raw)
	}
	previous := r.surface.SetHeaderComponent(component)
	r.ext.headerComponent = component
	r.ext.mu.Unlock()
	if !tui.SameComponent(previous, component) {
		disposeTUIComponent(previous)
	}
}

func (r *Runner) SetFooter(factory sdk.ComponentFactory) {
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		return
	}
	r.ext.footerFactory = factory
	r.ext.mu.Unlock()
	if !r.Active() || r.surface == nil {
		return
	}
	var raw sdk.Component
	if factory != nil {
		built, err := buildExtensionComponent(factory)
		if err != nil {
			r.reportFactoryFailure("footer", err)
			return
		}
		raw = built
	}
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		disposeTUIComponent(raw)
		return
	}
	var component tui.Component
	if raw != nil {
		component = r.wrappedComponent(r.ext.footerComponent, raw)
	}
	previous := r.surface.SetFooterComponent(component)
	r.ext.footerComponent = component
	r.ext.mu.Unlock()
	if !tui.SameComponent(previous, component) {
		disposeTUIComponent(previous)
	}
}

func (r *Runner) SetWidgetComponent(key string, factory sdk.ComponentFactory) error {
	if strings.TrimSpace(key) == "" {
		return ErrExtensionUIKeyEmpty
	}
	if factory == nil {
		r.ext.mu.Lock()
		if r.ext.cleaned {
			r.ext.mu.Unlock()
			return ErrExtensionUIUnavailable
		}
		delete(r.ext.widgetFactories, key)
		delete(r.ext.widgetGenerations, key)
		component := r.ext.widgetComponents[key]
		delete(r.ext.widgetComponents, key)
		surface := r.surface
		r.ext.mu.Unlock()
		if surface != nil {
			surface.ClearWidgetComponent(key)
		}
		disposeTUIComponent(component)
		return nil
	}
	component, err := r.buildWrappedComponent(factory)
	if err != nil {
		return err
	}
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		disposeTUIComponent(component)
		return ErrExtensionUIUnavailable
	}
	if previous, ok := r.ext.widgetComponents[key]; ok && !tui.SameComponent(previous, component) {
		if wrapped, ok := component.(*extensionComponent); ok {
			if prevWrapped, ok := previous.(*extensionComponent); ok && sameSDKComponent(prevWrapped.component, wrapped.component) {
				component = previous
			}
		}
	}
	r.ext.widgetFactories[key] = factory
	r.ext.widgetComponents[key] = component
	surface := r.surface
	r.ext.mu.Unlock()
	if surface != nil {
		surface.SetWidgetComponent(key, component)
	}
	r.ext.mu.Lock()
	cleaned := r.ext.cleaned
	r.ext.mu.Unlock()
	if cleaned {
		if surface != nil {
			surface.ClearWidgetComponent(key)
		}
		disposeTUIComponent(component)
	}
	return nil
}

func (r *Runner) SetWorkingVisible(visible bool) {
	if r.surface == nil {
		return
	}
	r.surface.SetWorkingVisible(visible)
}

func (r *Runner) SetWorkingIndicator(indicator *sdk.WorkingIndicator) {
	if r.surface == nil {
		return
	}
	if indicator == nil {
		r.surface.SetWorkingIndicator(nil, 0)
		return
	}
	r.surface.SetWorkingIndicator(indicator.Frames, time.Duration(indicator.Interval)*time.Millisecond)
}

func (r *Runner) SetHiddenThinkingLabel(label string) {
	if r.surface == nil {
		return
	}
	r.surface.SetHiddenThinkingLabel(label)
}

func (r *Runner) OnTerminalInput(handler sdk.TerminalInputHandler) (func(), error) {
	if !r.Active() {
		return nil, sdk.ErrModeUnsupported
	}
	if handler == nil {
		return nil, ErrExtensionUINilHandler
	}
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		return nil, ErrExtensionUIUnavailable
	}
	r.ext.nextInputHookID++
	id := r.ext.nextInputHookID
	r.ext.inputHooks = append(r.ext.inputHooks, registeredInputHook{id: id, handler: handler})
	r.ext.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.ext.mu.Lock()
			for index, registered := range r.ext.inputHooks {
				if registered.id == id {
					r.ext.inputHooks = append(r.ext.inputHooks[:index], r.ext.inputHooks[index+1:]...)
					break
				}
			}
			r.ext.mu.Unlock()
		})
	}, nil
}

func (r *Runner) AllThemes() []sdk.ThemeInfo {
	if r.themes == nil {
		return nil
	}
	sources := r.themes.AvailableThemes()
	out := make([]sdk.ThemeInfo, 0, len(sources))
	for _, source := range sources {
		out = append(out, sdk.ThemeInfo{Name: source.Name, Path: source.Path})
	}
	return out
}

func (r *Runner) GetTheme(name string) (sdk.Theme, bool) {
	if r.themes == nil || strings.TrimSpace(name) == "" {
		return nil, false
	}
	theme, err := r.themes.Load(name)
	if err != nil || theme == nil {
		return nil, false
	}
	return sdkTheme{theme: theme}, true
}

func (r *Runner) ActiveTheme() sdk.Theme {
	if r.themes == nil {
		return sdkTheme{}
	}
	return sdkTheme{theme: r.themes.Active()}
}

func (r *Runner) SetTheme(name string) error {
	return r.ApplyTheme(name)
}

func (r *Runner) ToolsExpanded() bool {
	if r.surface == nil {
		return false
	}
	return r.surface.ToolsExpanded()
}

func (r *Runner) SetToolsExpanded(expanded bool) {
	if r.surface == nil {
		return
	}
	r.surface.SetToolsExpanded(expanded)
}

func (r *Runner) DeliverCustomMessage(entry interactive.CustomEntryView) {
	if !r.Active() || r.surface == nil {
		return
	}
	r.surface.AddCustomMessage(entry)
}

func (r *Runner) DeliverCustomEntry(entry interactive.CustomEntryView) {
	if !r.Active() || r.surface == nil {
		return
	}
	r.surface.AddCustomEntry(entry)
}

func disposeTUIComponent(component tui.Component) {
	if component == nil {
		return
	}
	if disposable, ok := component.(interface{ Dispose() }); ok {
		disposable.Dispose()
	}
}

func (x *runnerExtensions) cleanup() {
	x.mu.Lock()
	if x.cleaned {
		x.mu.Unlock()
		return
	}
	x.cleaned = true
	registry := x.registry
	x.registry = nil
	header := x.headerComponent
	footer := x.footerComponent
	widgets := x.widgetComponents
	editor := x.editorComponent
	x.headerComponent = nil
	x.footerComponent = nil
	x.widgetComponents = map[string]tui.Component{}
	x.widgetFactories = map[string]sdk.ComponentFactory{}
	x.widgetGenerations = map[string]uint64{}
	x.editorComponent = nil
	x.editorFactory = nil
	x.inputHooks = nil
	x.autocomplete = nil
	stopAutocomplete := x.autocompleteStop
	x.autocompleteStop = nil
	x.autocompleteRegistered = false
	x.mu.Unlock()

	if registry != nil {
		registry.SetOnChange(nil)
	}
	if stopAutocomplete != nil {
		stopAutocomplete()
	}
	if x.runner == nil {
		disposeTUIComponent(editor)
		disposeTUIComponent(header)
		disposeTUIComponent(footer)
		for _, component := range widgets {
			disposeTUIComponent(component)
		}
		return
	}
	runner := x.runner
	if runner.surface != nil {
		x.publishMu.Lock()
		runner.surface.SetMarkdownTransformer(nil)
		runner.surface.ClearCustomRenderers()
		x.publishMu.Unlock()
		runner.surface.DisposeExtensionComponents()
		runner.surface.SetHeaderComponent(nil)
		runner.surface.SetFooterComponent(nil)
		for key := range widgets {
			runner.surface.ClearWidgetComponent(key)
		}
		runner.surface.SetWorkingIndicator(nil, 0)
		runner.surface.SetWorkingVisible(true)
		runner.surface.SetHiddenThinkingLabel("")
	}
	if editor != nil && runner.surface != nil {
		runner.restoreDefaultEditor()
	} else {
		disposeTUIComponent(editor)
	}
	disposeTUIComponent(header)
	disposeTUIComponent(footer)
	for _, component := range widgets {
		disposeTUIComponent(component)
	}
}
