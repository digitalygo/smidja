package interactive

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type MarkdownTransformContext struct {
	Kind      string
	Streaming bool
	Width     int
}

type MarkdownTransformer func(markdown string, ctx MarkdownTransformContext) string

type CustomRenderContext struct {
	Width     int
	Streaming bool
	Theme     *tui.Theme
}

type CustomEntryView struct {
	CustomType string
	Text       string
	Data       any
	Label      string
}

type CustomRenderer func(ctx CustomRenderContext, entry CustomEntryView) tui.Component

type RenderMetadata struct {
	Cwd           string
	SessionID     string
	Model         string
	ThinkingLevel string
}

type surfaceExtensionState struct {
	surface *Surface

	headerSlot *tui.Container
	footerSlot *tui.Container
	editorSlot *tui.Container
	activeEdit tui.Component
	widgets    *componentWidgetPanel

	workingVisible      bool
	workingFrames       []string
	workingInterval     time.Duration
	hiddenThinkingLabel string

	markdownTransformer MarkdownTransformer

	mu                  sync.Mutex
	customMessageRender map[string]CustomRenderer
	customEntryRender   map[string]CustomRenderer
	customSlots         []*customRenderSlot
	prepared            map[tui.Component]struct{}
	panicReporter       func(kind string, recovered any)

	metadata RenderMetadata
	width    atomic.Int64
}

func newSurfaceExtensionState(surface *Surface) *surfaceExtensionState {
	return &surfaceExtensionState{
		surface:             surface,
		headerSlot:          &tui.Container{},
		footerSlot:          &tui.Container{},
		editorSlot:          &tui.Container{},
		widgets:             newComponentWidgetPanel(),
		workingVisible:      true,
		customMessageRender: map[string]CustomRenderer{},
		customEntryRender:   map[string]CustomRenderer{},
		prepared:            map[tui.Component]struct{}{},
	}
}

func (s *Surface) SetHeaderComponent(component tui.Component) tui.Component {
	if s.ext == nil {
		return nil
	}
	var previous tui.Component
	s.runtime.Run(func() {
		if children := s.ext.headerSlot.Children(); len(children) > 0 {
			previous = children[0]
		}
		s.ext.headerSlot.Clear()
		if component != nil {
			s.ext.headerSlot.AddChild(component)
		}
	})
	s.ext.trackPrepared(component)
	if !sameComponent(previous, component) {
		s.ext.untrackPrepared(previous)
	}
	s.invalidateChat()
	return previous
}

func (s *Surface) SetFooterComponent(component tui.Component) tui.Component {
	if s.ext == nil {
		return nil
	}
	var previous tui.Component
	s.runtime.Run(func() {
		if children := s.ext.footerSlot.Children(); len(children) > 0 {
			previous = children[0]
		}
		s.ext.footerSlot.Clear()
		if component != nil {
			s.ext.footerSlot.AddChild(component)
			return
		}
		s.ext.footerSlot.AddChild(s.footer)
	})
	s.ext.trackPrepared(component)
	if !sameComponent(previous, component) {
		s.ext.untrackPrepared(previous)
	}
	s.requestRender()
	return previous
}

func (s *Surface) SetWidgetComponent(key string, component tui.Component) {
	if s.ext == nil || key == "" {
		return
	}
	previous := s.ext.widgets.Set(key, component)
	s.ext.trackPrepared(component)
	if !sameComponent(previous, component) {
		s.ext.untrackPrepared(previous)
		disposeComponent(previous)
	}
	s.requestRender()
}

func (s *Surface) ClearWidgetComponent(key string) {
	if s.ext == nil {
		return
	}
	previous := s.ext.widgets.Clear(key)
	s.ext.untrackPrepared(previous)
	disposeComponent(previous)
	s.requestRender()
}

func (s *Surface) SetWorkingVisible(visible bool) {
	if s.ext == nil {
		return
	}
	s.runtime.Run(func() {
		s.ext.workingVisible = visible
		s.status.SetVisible(visible)
	})
	s.requestRender()
}

func (s *Surface) SetWorkingIndicator(frames []string, interval time.Duration) {
	if s.ext == nil {
		return
	}
	if frames == nil {
		s.ext.workingFrames = nil
	} else {
		s.ext.workingFrames = append([]string{}, frames...)
	}
	s.ext.workingInterval = interval
	s.status.SetIndicator(frames, interval)
	s.requestRender()
}

func (s *Surface) SetHiddenThinkingLabel(label string) {
	if s.ext == nil {
		return
	}
	label = SanitizeSingleLine(label)
	s.ext.mu.Lock()
	s.ext.hiddenThinkingLabel = label
	s.ext.mu.Unlock()
	s.runtime.Run(func() {
		for _, assistant := range s.assistants {
			assistant.SetHiddenThinkingLabel(label)
		}
	})
	s.invalidateChat()
}

func (s *Surface) ReplaceActiveEditor(component tui.Component) tui.Component {
	if s.ext == nil {
		return s.editor
	}
	var previous tui.Component
	s.runtime.Run(func() {
		s.ext.editorSlot.Clear()
		if component == nil {
			component = s.editor
		}
		previous = s.ext.activeEdit
		s.ext.activeEdit = component
		s.ext.editorSlot.AddChild(component)
	})
	s.ext.trackPrepared(component)
	if !sameComponent(previous, component) {
		s.ext.untrackPrepared(previous)
	}
	s.invalidateChat()
	return previous
}

func (s *Surface) EditorWidth() int {
	if s.ext == nil {
		return 0
	}
	return int(s.ext.width.Load())
}

func (s *Surface) ActiveEditor() tui.Component {
	if s.ext == nil || s.ext.activeEdit == nil {
		return s.editor
	}
	return s.ext.activeEdit
}

func (s *Surface) SetMarkdownTransformer(transformer MarkdownTransformer) {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	s.ext.markdownTransformer = transformer
	s.ext.mu.Unlock()
	s.runtime.Run(func() {
		for _, child := range s.chat.Children() {
			switch typed := child.(type) {
			case *UserMessage:
				typed.SetMarkdownTransformer(transformer)
			case *AssistantMessage:
				typed.SetMarkdownTransformer(transformer)
			case *SkillBlock:
				typed.SetMarkdownTransformer(transformer)
			case *customRenderSlot:
				typed.setMarkdownTransformer(transformer)
			}
		}
	})
	s.invalidateChat()
}

func (s *Surface) transformMarkdown(text string, ctx MarkdownTransformContext) string {
	transformer := s.MarkdownTransformer()
	if transformer == nil {
		return text
	}
	return transformer(text, ctx)
}

func (s *Surface) SetCustomMessageRenderer(customType string, renderer CustomRenderer) {
	if s.ext == nil || customType == "" {
		return
	}
	s.ext.mu.Lock()
	s.ext.customMessageRender[customType] = renderer
	s.ext.mu.Unlock()
	s.RefreshCustomRenderers()
}

func (s *Surface) RemoveCustomMessageRenderer(customType string) {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	delete(s.ext.customMessageRender, customType)
	s.ext.mu.Unlock()
	s.RefreshCustomRenderers()
}

func (s *Surface) SetCustomEntryRenderer(customType string, renderer CustomRenderer) {
	if s.ext == nil || customType == "" {
		return
	}
	s.ext.mu.Lock()
	s.ext.customEntryRender[customType] = renderer
	s.ext.mu.Unlock()
	s.RefreshCustomRenderers()
}

func (s *Surface) RemoveCustomEntryRenderer(customType string) {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	delete(s.ext.customEntryRender, customType)
	s.ext.mu.Unlock()
	s.RefreshCustomRenderers()
}

func (s *Surface) ReplaceCustomRenderers(messageRenderers, entryRenderers map[string]CustomRenderer) {
	s.storeCustomRenderers(messageRenderers, entryRenderers)
	s.RefreshCustomRenderers()
}

func (s *Surface) SwapCustomRenderers(messageRenderers, entryRenderers map[string]CustomRenderer) []tui.Component {
	s.storeCustomRenderers(messageRenderers, entryRenderers)
	discarded := s.rebuildCustomRenderers()
	s.invalidateChat()
	return discarded
}

func (s *Surface) storeCustomRenderers(messageRenderers, entryRenderers map[string]CustomRenderer) {
	if s.ext == nil {
		return
	}
	message := make(map[string]CustomRenderer, len(messageRenderers))
	for customType, renderer := range messageRenderers {
		message[customType] = renderer
	}
	entry := make(map[string]CustomRenderer, len(entryRenderers))
	for customType, renderer := range entryRenderers {
		entry[customType] = renderer
	}
	s.ext.mu.Lock()
	s.ext.customMessageRender = message
	s.ext.customEntryRender = entry
	s.ext.mu.Unlock()
}

func (s *Surface) ClearCustomRenderers() {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	s.ext.customMessageRender = map[string]CustomRenderer{}
	s.ext.customEntryRender = map[string]CustomRenderer{}
	s.ext.mu.Unlock()
	s.invalidateChat()
}

func (s *Surface) SetRenderMetadata(metadata RenderMetadata) {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	s.ext.metadata = metadata
	s.ext.mu.Unlock()
}

func (s *Surface) updateRenderMetadata(update func(metadata *RenderMetadata)) {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	update(&s.ext.metadata)
	s.ext.mu.Unlock()
}

func (s *Surface) renderContext(streaming bool) CustomRenderContext {
	width := 0
	if s.ext != nil {
		width = int(s.ext.width.Load())
	}
	return CustomRenderContext{Width: width, Streaming: streaming, Theme: s.currentTheme()}
}

func (s *Surface) ExtensionMetadata() RenderMetadata {
	if s.ext == nil {
		return RenderMetadata{}
	}
	s.ext.mu.Lock()
	defer s.ext.mu.Unlock()
	return s.ext.metadata
}

func (s *Surface) NotifyEditorChange(text string) {
	s.handleEditorChange(text)
}

func (s *Surface) NotifyEditorSubmit(text string) {
	s.handleEditorSubmit(text)
}

func (s *Surface) customRenderer(kind, customType string) (CustomRenderer, bool) {
	if s.ext == nil || customType == "" {
		return nil, false
	}
	s.ext.mu.Lock()
	defer s.ext.mu.Unlock()
	if kind == CustomKindMessage {
		renderer, ok := s.ext.customMessageRender[customType]
		return renderer, ok
	}
	renderer, ok := s.ext.customEntryRender[customType]
	return renderer, ok
}

type customRenderSlot struct {
	mu          sync.Mutex
	kind        string
	entry       CustomEntryView
	inner       tui.Component
	disposeOnce sync.Once
}

func newCustomRenderSlot(kind string, entry CustomEntryView, inner tui.Component) *customRenderSlot {
	return &customRenderSlot{kind: kind, entry: entry, inner: inner}
}

func (c *customRenderSlot) Render(width int) []string {
	c.mu.Lock()
	inner := c.inner
	c.mu.Unlock()
	if inner == nil {
		return nil
	}
	return inner.Render(width)
}

func (c *customRenderSlot) Invalidate() {
	c.mu.Lock()
	inner := c.inner
	c.mu.Unlock()
	if inner != nil {
		inner.Invalidate()
	}
}

func (c *customRenderSlot) PrepareExternalFrame(width int) {
	c.mu.Lock()
	inner := c.inner
	c.mu.Unlock()
	if inner == nil {
		return
	}
	if preparer, ok := inner.(tui.ExternalFramePreparer); ok {
		preparer.PrepareExternalFrame(width)
	}
}

func (c *customRenderSlot) setMarkdownTransformer(transformer MarkdownTransformer) {
	c.mu.Lock()
	inner := c.inner
	c.mu.Unlock()
	if setter, ok := inner.(interface {
		SetMarkdownTransformer(MarkdownTransformer)
	}); ok {
		setter.SetMarkdownTransformer(transformer)
	}
}

func (c *customRenderSlot) snapshot() (string, CustomEntryView, tui.Component) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.kind, c.entry, c.inner
}

func (c *customRenderSlot) swap(inner tui.Component) tui.Component {
	c.mu.Lock()
	previous := c.inner
	c.inner = inner
	c.mu.Unlock()
	return previous
}

func (c *customRenderSlot) Dispose() {
	c.disposeOnce.Do(func() {
		disposeComponent(c.swap(nil))
	})
}

func fallbackCustomText(entry CustomEntryView) string {
	if entry.Text != "" {
		return entry.Text
	}
	if data, ok := entry.Data.(string); ok {
		return data
	}
	return ""
}

func (s *Surface) newCustomBlock(entry CustomEntryView, text string) *SkillBlock {
	label := entry.Label
	if label == "" {
		label = entry.CustomType
	}
	block := NewCustomBlock(label, text, s.currentTheme(), s.hyperlinks, s.expandKey)
	s.applyBlockTransformer(block)
	return block
}

func (s *Surface) prepareCustomComponent(kind string, entry CustomEntryView) tui.Component {
	renderer, ok := s.customRenderer(kind, entry.CustomType)
	if !ok || renderer == nil {
		return nil
	}
	var component tui.Component
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.reportPanic("renderer", recovered)
			}
		}()
		component = renderer(s.renderContext(false), entry)
	}()
	if component == nil {
		return nil
	}
	value := reflect.ValueOf(component)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return component
}

func (s *Surface) addCustomEntry(kind string, entry CustomEntryView) {
	component := s.prepareCustomComponent(kind, entry)
	text := entry.Text
	if kind == CustomKindEntry {
		text = fallbackCustomText(entry)
	}
	slot := newCustomRenderSlot(kind, entry, component)
	s.runtime.Run(func() {
		if component == nil {
			block := s.newCustomBlock(entry, text)
			block.SetExpanded(s.toolsExpanded)
			slot.swap(block)
			s.collapsibles = append(s.collapsibles, block)
		}
		s.chat.AddChild(slot)
		s.ext.addCustomSlot(slot)
		s.ext.trackPrepared(slot)
	})
	s.invalidateChat()
}

func (s *Surface) AddCustomMessage(entry CustomEntryView) {
	s.addCustomEntry(CustomKindMessage, entry)
}

func (s *Surface) AddCustomEntry(entry CustomEntryView) {
	s.addCustomEntry(CustomKindEntry, entry)
}

func (s *Surface) RefreshCustomRenderers() {
	discarded := s.rebuildCustomRenderers()
	for _, component := range discarded {
		disposeComponent(component)
	}
	s.invalidateChat()
}

func (s *Surface) rebuildCustomRenderers() []tui.Component {
	if s.ext == nil {
		return nil
	}
	slots := s.ext.snapshotCustomSlots()
	type pendingSwap struct {
		slot  *customRenderSlot
		inner tui.Component
	}
	swaps := make([]pendingSwap, 0, len(slots))
	for _, slot := range slots {
		kind, entry, _ := slot.snapshot()
		component := s.prepareCustomComponent(kind, entry)
		if component == nil {
			text := entry.Text
			if kind == CustomKindEntry {
				text = fallbackCustomText(entry)
			}
			component = s.newCustomBlock(entry, text)
		}
		swaps = append(swaps, pendingSwap{slot: slot, inner: component})
	}
	var discarded []tui.Component
	s.runtime.Run(func() {
		for _, pending := range swaps {
			previous := pending.slot.swap(pending.inner)
			if !sameComponent(previous, pending.inner) {
				discarded = append(discarded, previous)
			}
			s.removeCollapsible(previous)
			if block, ok := pending.inner.(*SkillBlock); ok {
				block.SetExpanded(s.toolsExpanded)
				s.collapsibles = append(s.collapsibles, block)
			}
		}
	})
	return discarded
}

func (s *Surface) removeCollapsible(target tui.Component) {
	for index, candidate := range s.collapsibles {
		component, ok := candidate.(tui.Component)
		if !ok || !sameComponent(component, target) {
			continue
		}
		s.collapsibles = append(s.collapsibles[:index], s.collapsibles[index+1:]...)
		return
	}
}

func (s *Surface) DisposeExtensionComponents() {
	if s.ext == nil {
		return
	}
	slots := s.ext.takeCustomSlots()
	if len(slots) == 0 {
		return
	}
	s.runtime.Run(func() {
		for _, slot := range slots {
			s.chat.RemoveChild(slot)
			s.ext.untrackPrepared(slot)
		}
	})
	for _, slot := range slots {
		slot.Dispose()
	}
}

func (s *Surface) SetPanicReporter(reporter func(kind string, recovered any)) {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	s.ext.panicReporter = reporter
	s.ext.mu.Unlock()
}

func (s *Surface) reportPanic(kind string, recovered any) {
	if s.ext == nil {
		return
	}
	s.ext.mu.Lock()
	reporter := s.ext.panicReporter
	s.ext.mu.Unlock()
	if reporter != nil {
		reporter(kind, recovered)
	}
}

func (s *Surface) prepareExternalFrames(width int) {
	if s.ext == nil {
		return
	}
	for _, component := range s.ext.preparedComponents() {
		preparer, ok := component.(tui.ExternalFramePreparer)
		if !ok {
			continue
		}
		s.prepareExternalFrame(preparer, width)
	}
}

func (s *Surface) prepareExternalFrame(preparer tui.ExternalFramePreparer, width int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.reportPanic("component", recovered)
		}
	}()
	preparer.PrepareExternalFrame(width)
}

func (x *surfaceExtensionState) addCustomSlot(slot *customRenderSlot) {
	x.mu.Lock()
	x.customSlots = append(x.customSlots, slot)
	x.mu.Unlock()
}

func (x *surfaceExtensionState) snapshotCustomSlots() []*customRenderSlot {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]*customRenderSlot(nil), x.customSlots...)
}

func (x *surfaceExtensionState) takeCustomSlots() []*customRenderSlot {
	x.mu.Lock()
	defer x.mu.Unlock()
	slots := x.customSlots
	x.customSlots = nil
	return slots
}

func (x *surfaceExtensionState) trackPrepared(component tui.Component) {
	if component == nil {
		return
	}
	x.mu.Lock()
	x.prepared[component] = struct{}{}
	x.mu.Unlock()
}

func (x *surfaceExtensionState) untrackPrepared(component tui.Component) {
	if component == nil {
		return
	}
	x.mu.Lock()
	delete(x.prepared, component)
	x.mu.Unlock()
}

func (x *surfaceExtensionState) preparedComponents() []tui.Component {
	x.mu.Lock()
	defer x.mu.Unlock()
	components := make([]tui.Component, 0, len(x.prepared))
	for component := range x.prepared {
		components = append(components, component)
	}
	return components
}

func (s *Surface) applyBlockTransformer(block *SkillBlock) {
	if transformer := s.MarkdownTransformer(); transformer != nil {
		block.SetMarkdownTransformer(transformer)
	}
}

func (s *Surface) applyUserTransformer(block *UserMessage) {
	if transformer := s.MarkdownTransformer(); transformer != nil {
		block.SetMarkdownTransformer(transformer)
	}
}

func (s *Surface) applyAssistantTransformer(block *AssistantMessage) {
	block.SetMarkdownTransformer(s.MarkdownTransformer())
	if s.ext != nil {
		s.ext.mu.Lock()
		label := s.ext.hiddenThinkingLabel
		s.ext.mu.Unlock()
		block.SetHiddenThinkingLabel(label)
	}
}

func (s *Surface) MarkdownTransformer() MarkdownTransformer {
	if s.ext == nil {
		return nil
	}
	s.ext.mu.Lock()
	defer s.ext.mu.Unlock()
	return s.ext.markdownTransformer
}

type componentWidgetPanel struct {
	mu      sync.Mutex
	order   []string
	widgets map[string]tui.Component
}

func newComponentWidgetPanel() *componentWidgetPanel {
	return &componentWidgetPanel{widgets: map[string]tui.Component{}}
}

func (p *componentWidgetPanel) Set(key string, component tui.Component) tui.Component {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.widgets[key]
	if previous == nil {
		p.order = append(p.order, key)
	}
	if component == nil {
		delete(p.widgets, key)
		for i, candidate := range p.order {
			if candidate == key {
				p.order = append(p.order[:i], p.order[i+1:]...)
				break
			}
		}
		return previous
	}
	p.widgets[key] = component
	return previous
}

func (p *componentWidgetPanel) Clear(key string) tui.Component {
	return p.Set(key, nil)
}

func (p *componentWidgetPanel) Has(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.widgets[key]
	return ok
}

func (p *componentWidgetPanel) Invalidate() {
	p.mu.Lock()
	components := make([]tui.Component, 0, len(p.order))
	for _, key := range p.order {
		if component, ok := p.widgets[key]; ok {
			components = append(components, component)
		}
	}
	p.mu.Unlock()
	for _, component := range components {
		component.Invalidate()
	}
}

func (p *componentWidgetPanel) Render(width int) []string {
	p.mu.Lock()
	components := make([]tui.Component, 0, len(p.order))
	for _, key := range p.order {
		if component, ok := p.widgets[key]; ok {
			components = append(components, component)
		}
	}
	p.mu.Unlock()
	var lines []string
	for _, component := range components {
		for _, line := range component.Render(width) {
			lines = append(lines, padLineToWidth(tui.TruncateToWidth(line, width, "...", false), width))
		}
	}
	return lines
}

func disposeComponent(component tui.Component) {
	if component == nil {
		return
	}
	disposable, ok := component.(interface{ Dispose() })
	if !ok {
		return
	}
	disposable.Dispose()
}

func sameComponent(first, second tui.Component) bool {
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
