package sdk

// LLMHookRegistry collects the LLM-cycle hooks of one extension. The
// harness calls each registration method once per handler the extension
// wants, at extension load time, in the order the methods are called.
// Handlers run in registration order: extension load order first, then
// the order registered within each extension, matching Pi's handler
// ordering (no explicit priorities).
type LLMHookRegistry interface {
	// OnContext registers a context-assembly handler. Handlers receive a
	// deep copy of the outgoing message list and may return a replacement
	// (pruning, pinning, or injecting messages). Returning nil keeps the
	// list unchanged.
	OnContext(handler ContextHandler)

	// OnMessageEnd registers a handler for finalized messages. Handlers
	// may return a replacement message; the replacement must keep the
	// original role, or the harness logs the violation and keeps the
	// current message.
	OnMessageEnd(handler MessageEndHandler)

	// OnAutoRetryStart registers a handler fired when a failed turn is
	// scheduled for automatic retry, before the backoff delay.
	OnAutoRetryStart(handler AutoRetryStartHandler)

	// OnAutoRetryEnd registers a handler fired when an automatic retry
	// settles, whether it succeeded or exhausted its attempts.
	OnAutoRetryEnd(handler AutoRetryEndHandler)
}

// ToolHookRegistry collects the tool hooks of one extension.
type ToolHookRegistry interface {
	// OnToolCall registers a pre-execution gate. Return nil to allow the
	// call, or a decision with Block true to deny it, with an optional
	// reason shown to the model and the user. Handler errors are logged
	// and the call is allowed, matching Pi's fail-safe tool_call policy.
	OnToolCall(handler ToolCallHandler)

	// OnToolResult registers a result-patching handler. Returning nil
	// keeps the result unchanged; returning a result applies the non-nil
	// fields of the patch on top of the current result (partial patch
	// semantics, matching Pi's tool_result middleware chaining).
	OnToolResult(handler ToolResultHandler)
}

// SessionHookRegistry collects the session-lifecycle hooks of one
// extension.
type SessionHookRegistry interface {
	// OnSessionStart registers a handler fired when a session starts
	// (startup, reload, new, resume, or fork).
	OnSessionStart(handler SessionStartHandler)

	// OnSessionShutdown registers a handler fired before a session
	// runtime is torn down (quit, reload, new, resume, or fork). Use it
	// to clean up resources opened from OnSessionStart or other
	// session-scoped hooks.
	OnSessionShutdown(handler SessionShutdownHandler)
}

// ContextHandler handles the context-assembly event. The event carries the
// outgoing message list as a deep copy; mutating it in place is safe but
// has no effect on the request, only the returned result does.
type ContextHandler func(ctx HandlerContext, event ContextEvent) (*ContextEventResult, error)

// MessageEndHandler handles a finalized message. Returning nil keeps the
// message; returning a result replaces it (the replacement must keep the
// original role, mirroring Pi's message_end contract).
type MessageEndHandler func(ctx HandlerContext, event MessageEndEvent) (*MessageEndEventResult, error)

// AutoRetryStartHandler handles the scheduling of an automatic retry.
type AutoRetryStartHandler func(ctx HandlerContext, event AutoRetryStartEvent) error

// AutoRetryEndHandler handles the settling of an automatic retry.
type AutoRetryEndHandler func(ctx HandlerContext, event AutoRetryEndEvent) error

// ToolCallHandler gates one tool execution. Return nil to allow the call,
// or a decision with Block true to deny it. Handler errors are logged and
// the call is allowed, matching Pi's fail-safe behavior.
type ToolCallHandler func(ctx HandlerContext, event ToolCallEvent) (*ToolCallDecision, error)

// ToolResultHandler patches a finished tool result. Returning nil keeps
// the result; returning a result applies the patch's non-nil fields.
type ToolResultHandler func(ctx HandlerContext, event ToolResultEvent) (*ToolResultEventResult, error)

// SessionStartHandler handles the start of a session.
type SessionStartHandler func(ctx HandlerContext, event SessionStartEvent) error

// SessionShutdownHandler handles the shutdown of a session.
type SessionShutdownHandler func(ctx HandlerContext, event SessionShutdownEvent) error
