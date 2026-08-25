package extensions

import (
	"errors"
	"fmt"
	"sync"

	"github.com/digitalygo/smidja/sdk"
)

// Sentinel errors returned by Registry methods. Compare with errors.Is.
var (
	// ErrDuplicateExtension is returned by Register when an extension ID
	// is already registered.
	ErrDuplicateExtension = errors.New("extensions: duplicate extension id")

	// ErrNilExtension is returned by Register for a nil extension.
	ErrNilExtension = errors.New("extensions: nil extension")

	// ErrEmptyExtensionID is returned by Register for an extension whose
	// ID is empty.
	ErrEmptyExtensionID = errors.New("extensions: empty extension id")

	// ErrSetupAlreadyRun is returned by Setup when it is called more than
	// once. The setup phase runs exactly once per registry.
	ErrSetupAlreadyRun = errors.New("extensions: setup already run")
)

// Registry holds the extensions loaded into one harness run and the hook
// handlers they registered. Extensions register in load order; the
// dispatcher runs handlers in extension registration order, then in the
// order each extension registered them.
//
// The registry is safe for concurrent use: Register serializes with Setup
// and with dispatch snapshots. No extension code ever runs while the
// registry lock is held: the Register*Hooks methods are called outside the
// lock during Register, Setup hooks run outside the lock during Setup, and
// dispatch handlers run outside the lock over a snapshot.
type Registry struct {
	mu         sync.RWMutex
	extensions []*entry
	byID       map[string]*entry
	setupDone  bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]*entry)}
}

// entry is one registered extension and its collected handlers. The
// handler slices are written once at registration and never mutated after
// publication (except by disable, which nils them all under the write
// lock), so dispatch snapshots can read entries safely.
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

// collected is the set of handlers one extension registered, gathered by
// the registrar adapters during Register.
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

// Register loads one extension into the registry, in call order. The
// extension's Register*Hooks methods run here, at registration time, and
// collect its handlers in the order the methods are called. Registering an
// ID that is already present returns ErrDuplicateExtension and leaves the
// registry unchanged.
//
// Register is safe to call while a dispatch is in flight: the dispatch
// already snapshotted its handler slices, so the new extension's handlers
// apply to the next event. Extensions registered after Setup has run do
// not receive the setup phase.
func (r *Registry) Register(ext sdk.Extension) error {
	if ext == nil {
		return ErrNilExtension
	}
	id := ext.ID()
	if id == "" {
		return ErrEmptyExtensionID
	}

	// Fast duplicate check so a duplicate does not run the extension's
	// registration code; the authoritative check happens again at commit.
	r.mu.RLock()
	_, dup := r.byID[id]
	r.mu.RUnlock()
	if dup {
		return fmt.Errorf("%w: %s", ErrDuplicateExtension, id)
	}

	// Collect the handlers outside the lock: the Register*Hooks methods
	// are extension code and must never run while the registry lock is
	// held. The registrar adapters only touch the local collected value,
	// so concurrent Register calls cannot interfere.
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

// set stores one extension's collected handlers. It is called once at
// registration, under the registry lock.
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

// Setup runs the setup phase: every registered extension implementing
// sdk.SetupHook receives Setup(api) exactly once, in registration order,
// before any session starts. The API is the host-provided extension API
// (see Runtime.SetAPI). A failing extension is logged and skipped: its
// Setup error or panic is logged once through logger, the extension's
// collected handlers are disabled, and the remaining extensions still run,
// matching Pi's per-extension error isolation.
//
// Calling Setup twice returns ErrSetupAlreadyRun. Extensions registered
// after Setup has run never receive it.
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

// disable drops every collected handler of one extension after a failed
// setup, so a setup-failed extension never contributes hooks. It runs
// under the write lock; a dispatch that already snapshotted keeps its
// snapshot, matching the snapshot semantics.
func (r *Registry) disable(e *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.set(collected{})
}

// snapshot returns an immutable copy of the registered handler chains at
// one point in time. Dispatch builds one at dispatch start, so handler
// registrations made during a dispatch only apply to the next event.
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

// collectHooks calls the extension's Register*Hooks methods, in the
// LLMHook, ToolHook, SessionHook group order, and gathers every handler it
// registers. Handler order within each group is the order the extension's
// registration methods call the registrar.
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

// llmRegistrar collects the LLM-cycle handlers of one extension into its
// collected set, preserving registration order.
type llmRegistrar struct{ c *collected }

// OnContext appends one context-assembly handler.
func (r *llmRegistrar) OnContext(h sdk.ContextHandler) {
	r.c.context = append(r.c.context, h)
}

// OnMessageEnd appends one finalized-message handler.
func (r *llmRegistrar) OnMessageEnd(h sdk.MessageEndHandler) {
	r.c.messageEnd = append(r.c.messageEnd, h)
}

// OnAutoRetryStart appends one retry-scheduling handler.
func (r *llmRegistrar) OnAutoRetryStart(h sdk.AutoRetryStartHandler) {
	r.c.autoRetryStart = append(r.c.autoRetryStart, h)
}

// OnAutoRetryEnd appends one retry-settling handler.
func (r *llmRegistrar) OnAutoRetryEnd(h sdk.AutoRetryEndHandler) {
	r.c.autoRetryEnd = append(r.c.autoRetryEnd, h)
}

// toolRegistrar collects the tool hooks of one extension into its
// collected set, preserving registration order.
type toolRegistrar struct{ c *collected }

// OnToolCall appends one pre-execution gate.
func (r *toolRegistrar) OnToolCall(h sdk.ToolCallHandler) {
	r.c.toolCall = append(r.c.toolCall, h)
}

// OnToolResult appends one result-patching handler.
func (r *toolRegistrar) OnToolResult(h sdk.ToolResultHandler) {
	r.c.toolResult = append(r.c.toolResult, h)
}

// sessionRegistrar collects the session-lifecycle hooks of one extension
// into its collected set, preserving registration order.
type sessionRegistrar struct{ c *collected }

// OnSessionStart appends one session-start handler.
func (r *sessionRegistrar) OnSessionStart(h sdk.SessionStartHandler) {
	r.c.sessionStart = append(r.c.sessionStart, h)
}

// OnSessionShutdown appends one session-shutdown handler.
func (r *sessionRegistrar) OnSessionShutdown(h sdk.SessionShutdownHandler) {
	r.c.sessionShutdown = append(r.c.sessionShutdown, h)
}

// Compile-time assertions that the registrar adapters satisfy the SDK
// registration interfaces.
var (
	_ sdk.LLMHookRegistry     = (*llmRegistrar)(nil)
	_ sdk.ToolHookRegistry    = (*toolRegistrar)(nil)
	_ sdk.SessionHookRegistry = (*sessionRegistrar)(nil)
)

// runGuarded runs fn, converting a panic into an error so callers can
// treat panics and returned errors uniformly. The panic value is wrapped
// so the message includes it; the goroutine never escapes.
func runGuarded(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn()
}
