package extensions

import (
	"errors"
	"fmt"
	"sync"

	"github.com/digitalygo/smidja/sdk"
)

var (
	ErrDuplicateExtension = errors.New("extensions: duplicate extension id")

	ErrNilExtension = errors.New("extensions: nil extension")

	ErrEmptyExtensionID = errors.New("extensions: empty extension id")

	ErrSetupAlreadyRun = errors.New("extensions: setup already run")
)

type Registry struct {
	mu         sync.RWMutex
	extensions []*entry
	byID       map[string]*entry
	setupDone  bool
}

func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]*entry)}
}

type entry struct {
	id              string
	ext             sdk.Extension
	context         []sdk.ContextHandler
	messageEnd      []sdk.MessageEndHandler
	autoRetryStart  []sdk.AutoRetryStartHandler
	autoRetryEnd    []sdk.AutoRetryEndHandler
	toolCall        []sdk.ToolCallHandler
	toolResult      []sdk.ToolResultHandler
	sessionStart    []sdk.SessionStartHandler
	sessionShutdown []sdk.SessionShutdownHandler
}

type collected struct {
	context         []sdk.ContextHandler
	messageEnd      []sdk.MessageEndHandler
	autoRetryStart  []sdk.AutoRetryStartHandler
	autoRetryEnd    []sdk.AutoRetryEndHandler
	toolCall        []sdk.ToolCallHandler
	toolResult      []sdk.ToolResultHandler
	sessionStart    []sdk.SessionStartHandler
	sessionShutdown []sdk.SessionShutdownHandler
}

func (r *Registry) Register(ext sdk.Extension) error {
	if ext == nil {
		return ErrNilExtension
	}
	id := ext.ID()
	if id == "" {
		return ErrEmptyExtensionID
	}

	r.mu.RLock()
	_, dup := r.byID[id]
	r.mu.RUnlock()
	if dup {
		return fmt.Errorf("%w: %s", ErrDuplicateExtension, id)
	}

	c := collectHooks(ext)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID == nil {
		r.byID = make(map[string]*entry)
	}
	if _, dup := r.byID[id]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicateExtension, id)
	}
	e := &entry{id: id, ext: ext}
	e.set(c)
	r.byID[id] = e
	r.extensions = append(r.extensions, e)
	return nil
}

func (e *entry) set(c collected) {
	e.context = c.context
	e.messageEnd = c.messageEnd
	e.autoRetryStart = c.autoRetryStart
	e.autoRetryEnd = c.autoRetryEnd
	e.toolCall = c.toolCall
	e.toolResult = c.toolResult
	e.sessionStart = c.sessionStart
	e.sessionShutdown = c.sessionShutdown
}

func (r *Registry) Setup(api sdk.API, logger Logger) error {
	r.mu.Lock()
	if r.setupDone {
		r.mu.Unlock()
		return ErrSetupAlreadyRun
	}
	r.setupDone = true
	exts := append([]*entry(nil), r.extensions...)
	r.mu.Unlock()

	for _, e := range exts {
		sh, ok := e.ext.(sdk.SetupHook)
		if !ok {
			continue
		}
		err := runGuarded(func() error { return sh.Setup(api) })
		if err == nil {
			continue
		}
		if logger != nil {
			logger.Logf("extension %s: setup failed: %v", e.id, err)
		}
		r.disable(e)
	}
	return nil
}

func (r *Registry) disable(e *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.set(collected{})
}

func (r *Registry) snapshot() snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := snapshot{entries: make([]snapshotEntry, 0, len(r.extensions))}
	for _, e := range r.extensions {
		s.entries = append(s.entries, snapshotEntry{
			id:              e.id,
			context:         append([]sdk.ContextHandler(nil), e.context...),
			messageEnd:      append([]sdk.MessageEndHandler(nil), e.messageEnd...),
			autoRetryStart:  append([]sdk.AutoRetryStartHandler(nil), e.autoRetryStart...),
			autoRetryEnd:    append([]sdk.AutoRetryEndHandler(nil), e.autoRetryEnd...),
			toolCall:        append([]sdk.ToolCallHandler(nil), e.toolCall...),
			toolResult:      append([]sdk.ToolResultHandler(nil), e.toolResult...),
			sessionStart:    append([]sdk.SessionStartHandler(nil), e.sessionStart...),
			sessionShutdown: append([]sdk.SessionShutdownHandler(nil), e.sessionShutdown...),
		})
	}
	return s
}

func collectHooks(ext sdk.Extension) collected {
	var c collected
	if lh, ok := ext.(sdk.LLMHook); ok {
		lh.RegisterLLMHooks(&llmRegistrar{c: &c})
	}
	if th, ok := ext.(sdk.ToolHook); ok {
		th.RegisterToolHooks(&toolRegistrar{c: &c})
	}
	if sh, ok := ext.(sdk.SessionHook); ok {
		sh.RegisterSessionHooks(&sessionRegistrar{c: &c})
	}
	return c
}

type llmRegistrar struct{ c *collected }

func (r *llmRegistrar) OnContext(h sdk.ContextHandler) {
	r.c.context = append(r.c.context, h)
}

func (r *llmRegistrar) OnMessageEnd(h sdk.MessageEndHandler) {
	r.c.messageEnd = append(r.c.messageEnd, h)
}

func (r *llmRegistrar) OnAutoRetryStart(h sdk.AutoRetryStartHandler) {
	r.c.autoRetryStart = append(r.c.autoRetryStart, h)
}

func (r *llmRegistrar) OnAutoRetryEnd(h sdk.AutoRetryEndHandler) {
	r.c.autoRetryEnd = append(r.c.autoRetryEnd, h)
}

type toolRegistrar struct{ c *collected }

func (r *toolRegistrar) OnToolCall(h sdk.ToolCallHandler) {
	r.c.toolCall = append(r.c.toolCall, h)
}

func (r *toolRegistrar) OnToolResult(h sdk.ToolResultHandler) {
	r.c.toolResult = append(r.c.toolResult, h)
}

type sessionRegistrar struct{ c *collected }

func (r *sessionRegistrar) OnSessionStart(h sdk.SessionStartHandler) {
	r.c.sessionStart = append(r.c.sessionStart, h)
}

func (r *sessionRegistrar) OnSessionShutdown(h sdk.SessionShutdownHandler) {
	r.c.sessionShutdown = append(r.c.sessionShutdown, h)
}

var (
	_ sdk.LLMHookRegistry     = (*llmRegistrar)(nil)
	_ sdk.ToolHookRegistry    = (*toolRegistrar)(nil)
	_ sdk.SessionHookRegistry = (*sessionRegistrar)(nil)
)

func runGuarded(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn()
}
