package extensions

import (
	"context"
	"fmt"
	"sync"

	"github.com/digitalygo/smidja/sdk"
)

// Test helpers shared by the extensions package tests: a builder for
// test extensions, a recording logger, a stub API, and a fake handler
// context. They live in the package so tests can also exercise
// unexported internals (snapshot, defaultContext, noopUI).

// builder accumulates the capability groups of one test extension. Call
// the group methods in the order the handlers should be registered, then
// build.
type builder struct {
	id          string
	setupFn     func(sdk.API) error
	contexts    []sdk.ContextHandler
	messageEnds []sdk.MessageEndHandler
	retryStarts []sdk.AutoRetryStartHandler
	retryEnds   []sdk.AutoRetryEndHandler
	toolCalls   []sdk.ToolCallHandler
	toolResults []sdk.ToolResultHandler
	sessionOns  []sdk.SessionStartHandler
	sessionOffs []sdk.SessionShutdownHandler
}

// ext starts a test extension builder with the given id.
func ext(id string) *builder { return &builder{id: id} }

func (b *builder) setup(fn func(sdk.API) error) *builder { b.setupFn = fn; return b }
func (b *builder) context(fn sdk.ContextHandler) *builder {
	b.contexts = append(b.contexts, fn)
	return b
}
func (b *builder) messageEnd(fn sdk.MessageEndHandler) *builder {
	b.messageEnds = append(b.messageEnds, fn)
	return b
}
func (b *builder) retryStart(fn sdk.AutoRetryStartHandler) *builder {
	b.retryStarts = append(b.retryStarts, fn)
	return b
}
func (b *builder) retryEnd(fn sdk.AutoRetryEndHandler) *builder {
	b.retryEnds = append(b.retryEnds, fn)
	return b
}
func (b *builder) toolCall(fn sdk.ToolCallHandler) *builder {
	b.toolCalls = append(b.toolCalls, fn)
	return b
}
func (b *builder) toolResult(fn sdk.ToolResultHandler) *builder {
	b.toolResults = append(b.toolResults, fn)
	return b
}
func (b *builder) sessionStart(fn sdk.SessionStartHandler) *builder {
	b.sessionOns = append(b.sessionOns, fn)
	return b
}
func (b *builder) sessionShutdown(fn sdk.SessionShutdownHandler) *builder {
	b.sessionOffs = append(b.sessionOffs, fn)
	return b
}

// build returns the extension; every registered handler type is present
// so the compile-time capability assertions hold.
func (b *builder) build() sdk.Extension {
	return &hookExtension{b: b}
}

// hookExtension is the concrete test extension behind builder.
type hookExtension struct{ b *builder }

var (
	_ sdk.Extension   = (*hookExtension)(nil)
	_ sdk.SetupHook   = (*hookExtension)(nil)
	_ sdk.LLMHook     = (*hookExtension)(nil)
	_ sdk.ToolHook    = (*hookExtension)(nil)
	_ sdk.SessionHook = (*hookExtension)(nil)
)

func (e *hookExtension) ID() string { return e.b.id }

func (e *hookExtension) Setup(api sdk.API) error {
	if e.b.setupFn == nil {
		return nil
	}
	return e.b.setupFn(api)
}

func (e *hookExtension) RegisterLLMHooks(r sdk.LLMHookRegistry) {
	for _, h := range e.b.contexts {
		r.OnContext(h)
	}
	for _, h := range e.b.messageEnds {
		r.OnMessageEnd(h)
	}
	for _, h := range e.b.retryStarts {
		r.OnAutoRetryStart(h)
	}
	for _, h := range e.b.retryEnds {
		r.OnAutoRetryEnd(h)
	}
}

func (e *hookExtension) RegisterToolHooks(r sdk.ToolHookRegistry) {
	for _, h := range e.b.toolCalls {
		r.OnToolCall(h)
	}
	for _, h := range e.b.toolResults {
		r.OnToolResult(h)
	}
}

func (e *hookExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	for _, h := range e.b.sessionOns {
		r.OnSessionStart(h)
	}
	for _, h := range e.b.sessionOffs {
		r.OnSessionShutdown(h)
	}
}

// recLogger records formatted lines for assertions. It is safe for
// concurrent use.
type recLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func (l *recLogger) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.lines))
	copy(out, l.lines)
	return out
}

// stubAPI is a minimal sdk.API implementation that records the calls
// tests care about.
type stubAPI struct {
	calls []string
}

var _ sdk.API = (*stubAPI)(nil)

func (a *stubAPI) RegisterTool(t sdk.Tool) error { return nil }
func (a *stubAPI) UnregisterTool(name string) error {
	a.calls = append(a.calls, "UnregisterTool:"+name)
	return nil
}
func (a *stubAPI) ActiveTools() []string {
	a.calls = append(a.calls, "ActiveTools")
	return nil
}
func (a *stubAPI) SetActiveTools(names []string) error { return nil }
func (a *stubAPI) AllTools() []sdk.ToolInfo            { return nil }
func (a *stubAPI) RegisterCommand(name string, cmd sdk.Command) error {
	a.calls = append(a.calls, "RegisterCommand:"+name)
	return nil
}
func (a *stubAPI) Commands() []sdk.CommandInfo { return nil }
func (a *stubAPI) SendMessage(msg sdk.CustomMessage, opts sdk.SendOptions) error {
	return nil
}
func (a *stubAPI) SendUserMessage(text string, opts sdk.SendOptions) error {
	a.calls = append(a.calls, "SendUserMessage:"+text)
	return nil
}
func (a *stubAPI) AppendEntry(customType string, data any) error {
	a.calls = append(a.calls, "AppendEntry:"+customType)
	return nil
}
func (a *stubAPI) SetSessionName(name string) error {
	a.calls = append(a.calls, "SetSessionName:"+name)
	return nil
}
func (a *stubAPI) LabelEntry(entryID, label string) error { return nil }
func (a *stubAPI) SetModel(m sdk.Model) error             { return nil }
func (a *stubAPI) SetThinkingLevel(level sdk.ThinkingLevel) error {
	return nil
}
func (a *stubAPI) RegisterProvider(name string, cfg sdk.ProviderConfig) error {
	return nil
}
func (a *stubAPI) RemoveProvider(name string) error { return nil }
func (a *stubAPI) RegisterFlag(name string, opts sdk.FlagOptions) error {
	return nil
}
func (a *stubAPI) Flags() map[string]any { return nil }
func (a *stubAPI) Exec(command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error) {
	a.calls = append(a.calls, "Exec:"+command)
	return &sdk.ExecResult{}, nil
}
func (a *stubAPI) EmitCustomEvent(name string, data any) error {
	a.calls = append(a.calls, "EmitCustomEvent:"+name)
	return nil
}

// fakeContext is a host-provided handler context used to verify that the
// runtime passes the host context through to handlers.
type fakeContext struct {
	sdk.API
	mode sdk.Mode
}

var _ sdk.HandlerContext = (*fakeContext)(nil)

func (c *fakeContext) UI() sdk.UI                       { return nil }
func (c *fakeContext) Mode() sdk.Mode                   { return c.mode }
func (c *fakeContext) HasUI() bool                      { return true }
func (c *fakeContext) Cwd() string                      { return "/work" }
func (c *fakeContext) SessionManager() sdk.SessionView  { return nil }
func (c *fakeContext) ModelRegistry() sdk.ModelRegistry { return nil }
func (c *fakeContext) Model() *sdk.Model                { return nil }
func (c *fakeContext) ThinkingLevel() sdk.ThinkingLevel { return sdk.ThinkingOff }
func (c *fakeContext) Signal() context.Context          { return context.Background() }
func (c *fakeContext) Abort()                           {}
func (c *fakeContext) Shutdown()                        {}
func (c *fakeContext) ContextUsage() *sdk.ContextUsage  { return nil }
func (c *fakeContext) Compact(sdk.CompactOptions)       {}
func (c *fakeContext) SystemPrompt() string             { return "" }
