package extensionui

import (
	"errors"
	"sync"

	"github.com/digitalygo/smidja/sdk"
)

var (
	ErrEmptyKey = errors.New("extensionui: registration key is empty")
	ErrNilValue = errors.New("extensionui: registration value is nil")
	ErrNotFound = errors.New("extensionui: registration not found")
	ErrClosed   = errors.New("extensionui: registry is closed")
)

type Registry struct {
	mu sync.Mutex

	components   map[string]sdk.ComponentFactory
	componentOrd []string

	widgets   map[string]sdk.ComponentFactory
	widgetOrd []string
	widgetGen map[string]uint64
	widgetSeq uint64

	messageRenderers map[string]sdk.MessageRenderer
	messageOrd       []string

	entryRenderers map[string]sdk.EntryRenderer
	entryOrd       []string

	transformers   map[string]sdk.MarkdownTransformer
	transformerOrd []string

	inputHooks map[string]sdk.TerminalInputHandler
	inputOrd   []string

	onChange func()
}

func NewRegistry() *Registry {
	return &Registry{
		components:       map[string]sdk.ComponentFactory{},
		widgets:          map[string]sdk.ComponentFactory{},
		widgetGen:        map[string]uint64{},
		messageRenderers: map[string]sdk.MessageRenderer{},
		entryRenderers:   map[string]sdk.EntryRenderer{},
		transformers:     map[string]sdk.MarkdownTransformer{},
		inputHooks:       map[string]sdk.TerminalInputHandler{},
	}
}

func (r *Registry) SetOnChange(fn func()) {
	r.mu.Lock()
	r.onChange = fn
	r.mu.Unlock()
}

func (r *Registry) notifyLocked() {
	if r.onChange != nil {
		fn := r.onChange
		r.mu.Unlock()
		fn()
		r.mu.Lock()
	}
}

func replaceOrdered[T any](order []string, store map[string]T, key string, value T) []string {
	if _, ok := store[key]; !ok {
		order = append(order, key)
	}
	store[key] = value
	return order
}

func removeOrdered[T any](order []string, store map[string]T, key string) ([]string, bool) {
	if _, ok := store[key]; !ok {
		return order, false
	}
	delete(store, key)
	for i, candidate := range order {
		if candidate == key {
			return append(order[:i], order[i+1:]...), true
		}
	}
	return order, true
}

func (r *Registry) RegisterComponent(key string, factory sdk.ComponentFactory) error {
	if key == "" {
		return ErrEmptyKey
	}
	if factory == nil {
		return ErrNilValue
	}
	r.mu.Lock()
	r.componentOrd = replaceOrdered(r.componentOrd, r.components, key, factory)
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) UnregisterComponent(key string) error {
	r.mu.Lock()
	order, ok := removeOrdered(r.componentOrd, r.components, key)
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	r.componentOrd = order
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) ComponentKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.componentOrd...)
}

func (r *Registry) WidgetKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.widgetOrd...)
}

func (r *Registry) MessageRendererTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.messageOrd...)
}

func (r *Registry) EntryRendererTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.entryOrd...)
}

func (r *Registry) MarkdownTransformerNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.transformerOrd...)
}

func (r *Registry) TerminalInputHookKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.inputOrd...)
}

func (r *Registry) Component(key string) (sdk.ComponentFactory, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	factory, ok := r.components[key]
	return factory, ok
}

func (r *Registry) Components() []sdk.ComponentFactory {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sdk.ComponentFactory, 0, len(r.componentOrd))
	for _, key := range r.componentOrd {
		if factory, ok := r.components[key]; ok {
			out = append(out, factory)
		}
	}
	return out
}

func (r *Registry) RegisterWidget(key string, factory sdk.ComponentFactory) error {
	if key == "" {
		return ErrEmptyKey
	}
	if factory == nil {
		return ErrNilValue
	}
	r.mu.Lock()
	r.widgetOrd = replaceOrdered(r.widgetOrd, r.widgets, key, factory)
	r.widgetSeq++
	r.widgetGen[key] = r.widgetSeq
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) UnregisterWidget(key string) error {
	r.mu.Lock()
	order, ok := removeOrdered(r.widgetOrd, r.widgets, key)
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	r.widgetOrd = order
	delete(r.widgetGen, key)
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) Widgets() map[string]sdk.ComponentFactory {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]sdk.ComponentFactory, len(r.widgetOrd))
	for _, key := range r.widgetOrd {
		if factory, ok := r.widgets[key]; ok {
			out[key] = factory
		}
	}
	return out
}

func (r *Registry) WidgetSnapshot() ([]string, map[string]sdk.ComponentFactory, map[string]uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	order := append([]string(nil), r.widgetOrd...)
	factories := make(map[string]sdk.ComponentFactory, len(order))
	generations := make(map[string]uint64, len(order))
	for _, key := range order {
		if factory, ok := r.widgets[key]; ok {
			factories[key] = factory
		}
		generations[key] = r.widgetGen[key]
	}
	return order, factories, generations
}

func (r *Registry) RegisterMessageRenderer(customType string, renderer sdk.MessageRenderer) error {
	if customType == "" {
		return ErrEmptyKey
	}
	if renderer == nil {
		return ErrNilValue
	}
	r.mu.Lock()
	r.messageOrd = replaceOrdered(r.messageOrd, r.messageRenderers, customType, renderer)
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) UnregisterMessageRenderer(customType string) error {
	r.mu.Lock()
	order, ok := removeOrdered(r.messageOrd, r.messageRenderers, customType)
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	r.messageOrd = order
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) MessageRenderer(customType string) (sdk.MessageRenderer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	renderer, ok := r.messageRenderers[customType]
	return renderer, ok
}

func (r *Registry) MessageRenderers() []sdk.MessageRenderer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sdk.MessageRenderer, 0, len(r.messageOrd))
	for _, key := range r.messageOrd {
		if renderer, ok := r.messageRenderers[key]; ok {
			out = append(out, renderer)
		}
	}
	return out
}

func (r *Registry) RegisterEntryRenderer(customType string, renderer sdk.EntryRenderer) error {
	if customType == "" {
		return ErrEmptyKey
	}
	if renderer == nil {
		return ErrNilValue
	}
	r.mu.Lock()
	r.entryOrd = replaceOrdered(r.entryOrd, r.entryRenderers, customType, renderer)
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) UnregisterEntryRenderer(customType string) error {
	r.mu.Lock()
	order, ok := removeOrdered(r.entryOrd, r.entryRenderers, customType)
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	r.entryOrd = order
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) EntryRenderer(customType string) (sdk.EntryRenderer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	renderer, ok := r.entryRenderers[customType]
	return renderer, ok
}

func (r *Registry) EntryRenderers() []sdk.EntryRenderer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sdk.EntryRenderer, 0, len(r.entryOrd))
	for _, key := range r.entryOrd {
		if renderer, ok := r.entryRenderers[key]; ok {
			out = append(out, renderer)
		}
	}
	return out
}

func (r *Registry) RegisterMarkdownTransformer(name string, transformer sdk.MarkdownTransformer) error {
	if name == "" {
		return ErrEmptyKey
	}
	if transformer == nil {
		return ErrNilValue
	}
	r.mu.Lock()
	r.transformerOrd = replaceOrdered(r.transformerOrd, r.transformers, name, transformer)
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) UnregisterMarkdownTransformer(name string) error {
	r.mu.Lock()
	order, ok := removeOrdered(r.transformerOrd, r.transformers, name)
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	r.transformerOrd = order
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) MarkdownTransformers() []sdk.MarkdownTransformer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sdk.MarkdownTransformer, 0, len(r.transformerOrd))
	for _, key := range r.transformerOrd {
		if transformer, ok := r.transformers[key]; ok {
			out = append(out, transformer)
		}
	}
	return out
}

func (r *Registry) RegisterTerminalInputHook(key string, handler sdk.TerminalInputHandler) error {
	if key == "" {
		return ErrEmptyKey
	}
	if handler == nil {
		return ErrNilValue
	}
	r.mu.Lock()
	r.inputOrd = replaceOrdered(r.inputOrd, r.inputHooks, key, handler)
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) UnregisterTerminalInputHook(key string) error {
	r.mu.Lock()
	order, ok := removeOrdered(r.inputOrd, r.inputHooks, key)
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	r.inputOrd = order
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (r *Registry) TerminalInputHooks() []sdk.TerminalInputHandler {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sdk.TerminalInputHandler, 0, len(r.inputOrd))
	for _, key := range r.inputOrd {
		if handler, ok := r.inputHooks[key]; ok {
			out = append(out, handler)
		}
	}
	return out
}

type RegistrySnapshot struct {
	components   map[string]sdk.ComponentFactory
	componentOrd []string

	widgets   map[string]sdk.ComponentFactory
	widgetOrd []string
	widgetGen map[string]uint64
	widgetSeq uint64

	messageRenderers map[string]sdk.MessageRenderer
	messageOrd       []string

	entryRenderers map[string]sdk.EntryRenderer
	entryOrd       []string

	transformers   map[string]sdk.MarkdownTransformer
	transformerOrd []string

	inputHooks map[string]sdk.TerminalInputHandler
	inputOrd   []string
}

func (r *Registry) Snapshot() RegistrySnapshot {
	if r == nil {
		return RegistrySnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := RegistrySnapshot{
		components:       make(map[string]sdk.ComponentFactory, len(r.components)),
		componentOrd:     append([]string(nil), r.componentOrd...),
		widgets:          make(map[string]sdk.ComponentFactory, len(r.widgets)),
		widgetOrd:        append([]string(nil), r.widgetOrd...),
		widgetGen:        make(map[string]uint64, len(r.widgetGen)),
		widgetSeq:        r.widgetSeq,
		messageRenderers: make(map[string]sdk.MessageRenderer, len(r.messageRenderers)),
		messageOrd:       append([]string(nil), r.messageOrd...),
		entryRenderers:   make(map[string]sdk.EntryRenderer, len(r.entryRenderers)),
		entryOrd:         append([]string(nil), r.entryOrd...),
		transformers:     make(map[string]sdk.MarkdownTransformer, len(r.transformers)),
		transformerOrd:   append([]string(nil), r.transformerOrd...),
		inputHooks:       make(map[string]sdk.TerminalInputHandler, len(r.inputHooks)),
		inputOrd:         append([]string(nil), r.inputOrd...),
	}
	for key, value := range r.components {
		snap.components[key] = value
	}
	for key, value := range r.widgets {
		snap.widgets[key] = value
	}
	for key, value := range r.widgetGen {
		snap.widgetGen[key] = value
	}
	for key, value := range r.messageRenderers {
		snap.messageRenderers[key] = value
	}
	for key, value := range r.entryRenderers {
		snap.entryRenderers[key] = value
	}
	for key, value := range r.transformers {
		snap.transformers[key] = value
	}
	for key, value := range r.inputHooks {
		snap.inputHooks[key] = value
	}
	return snap
}

func (r *Registry) Restore(snapshot RegistrySnapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = snapshot.components
	if r.components == nil {
		r.components = map[string]sdk.ComponentFactory{}
	}
	r.componentOrd = snapshot.componentOrd
	r.widgets = snapshot.widgets
	if r.widgets == nil {
		r.widgets = map[string]sdk.ComponentFactory{}
	}
	r.widgetOrd = snapshot.widgetOrd
	r.widgetGen = snapshot.widgetGen
	if r.widgetGen == nil {
		r.widgetGen = map[string]uint64{}
	}
	r.widgetSeq = snapshot.widgetSeq
	r.messageRenderers = snapshot.messageRenderers
	if r.messageRenderers == nil {
		r.messageRenderers = map[string]sdk.MessageRenderer{}
	}
	r.messageOrd = snapshot.messageOrd
	r.entryRenderers = snapshot.entryRenderers
	if r.entryRenderers == nil {
		r.entryRenderers = map[string]sdk.EntryRenderer{}
	}
	r.entryOrd = snapshot.entryOrd
	r.transformers = snapshot.transformers
	if r.transformers == nil {
		r.transformers = map[string]sdk.MarkdownTransformer{}
	}
	r.transformerOrd = snapshot.transformerOrd
	r.inputHooks = snapshot.inputHooks
	if r.inputHooks == nil {
		r.inputHooks = map[string]sdk.TerminalInputHandler{}
	}
	r.inputOrd = snapshot.inputOrd
}
