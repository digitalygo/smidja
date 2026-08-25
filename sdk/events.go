package sdk

import "encoding/json"

// Event type constants for the phase 1 hooks. The strings match Pi's
// extension event type names exactly, so extensions and tooling that know
// Pi can port without renaming. The typed registries (LLMHookRegistry,
// ToolHookRegistry, SessionHookRegistry) do not need them; they are
// exported for diagnostics, logging, and future dynamic registration.
const (
	// EventContext is the context-assembly event ("context" in Pi).
	EventContext = "context"
	// EventMessageEnd is the finalized-message event ("message_end" in
	// Pi).
	EventMessageEnd = "message_end"
	// EventAutoRetryStart is the retry-scheduling event
	// ("auto_retry_start"; an agent-session event in Pi, a first-class
	// extension hook in smidja).
	EventAutoRetryStart = "auto_retry_start"
	// EventAutoRetryEnd is the retry-settling event ("auto_retry_end").
	EventAutoRetryEnd = "auto_retry_end"
	// EventToolCall is the tool gate event ("tool_call" in Pi).
	EventToolCall = "tool_call"
	// EventToolResult is the tool result event ("tool_result" in Pi).
	EventToolResult = "tool_result"
	// EventSessionStart is the session start event ("session_start" in
	// Pi).
	EventSessionStart = "session_start"
	// EventSessionShutdown is the session shutdown event
	// ("session_shutdown" in Pi).
	EventSessionShutdown = "session_shutdown"
)

// ContextEvent carries the outgoing message list of one LLM call. The
// Messages field is a deep copy of the session context; handlers inspect
// it and return a replacement through ContextEventResult.
type ContextEvent struct {
	// Messages is the assembled message list as a deep copy, safe to
	// inspect but not to rely on for mutation; return a replacement
	// instead.
	Messages []Message
}

// ContextEventResult is the optional outcome of a context handler.
// Returning nil keeps the current message list; returning a result
// replaces it for later handlers and for the actual request.
type ContextEventResult struct {
	// Messages is the replacement message list.
	Messages []Message
}

// MessageEndEvent carries one finalized message (user, assistant, or
// toolResult). Handlers may return a replacement through
// MessageEndEventResult.
type MessageEndEvent struct {
	// Message is the finalized message.
	Message Message
}

// MessageEndEventResult is the optional outcome of a message_end handler.
// Returning nil keeps the message; returning a result replaces it. The
// replacement must keep the original message's role.
type MessageEndEventResult struct {
	// Message is the replacement message.
	Message Message
}

// AutoRetryStartEvent describes one scheduled automatic retry, mirroring
// Pi's auto_retry_start agent-session event.
type AutoRetryStartEvent struct {
	// Attempt is the 1-based retry attempt about to run.
	Attempt int
	// MaxAttempts is the configured retry budget.
	MaxAttempts int
	// DelayMs is the exponential backoff delay before the retry.
	DelayMs int64
	// ErrorMessage describes the failure that triggered the retry.
	ErrorMessage string
}

// AutoRetryEndEvent describes the settling of an automatic retry,
// mirroring Pi's auto_retry_end agent-session event.
type AutoRetryEndEvent struct {
	// Success reports whether the retried turn completed.
	Success bool
	// Attempt is the attempt number that settled.
	Attempt int
	// FinalError describes the final failure when Success is false.
	FinalError string
}

// ToolCallEvent gates one tool execution. The Args field is the raw JSON
// of the tool arguments; handlers replace it to patch arguments before
// execution, mirroring Pi's mutable event.input.
type ToolCallEvent struct {
	// ToolCallID is the identifier of the tool call, matching the
	// toolCall block ID in the assistant message.
	ToolCallID string
	// Name is the tool name, for example "read" or "exec".
	Name string
	// Args is the raw JSON object of the tool arguments. Replacing it
	// patches the arguments seen by later handlers and by the execution.
	Args json.RawMessage
}

// ToolCallDecision is the outcome of a tool_call handler chain. The zero
// value allows the call; Block true denies it.
type ToolCallDecision struct {
	// Block denies the tool execution.
	Block bool
	// Reason is the denial reason, shown to the model and the user.
	Reason string
}

// ToolResultEvent describes one finished tool execution. Handlers may
// patch the result through ToolResultEventResult.
type ToolResultEvent struct {
	// ToolCallID is the identifier of the tool call.
	ToolCallID string
	// Name is the executed tool name.
	Name string
	// Args is the raw JSON of the arguments the tool ran with.
	Args json.RawMessage
	// Content is the output content blocks of the execution.
	Content []Block
	// Details carries extension-specific structured data for rendering
	// and state reconstruction; it is never sent to the model.
	Details any
	// IsError marks the execution as failed.
	IsError bool
	// Usage reports nested model usage when the tool made LLM calls; nil
	// when none.
	Usage *Usage
}

// ToolResultEventResult is the optional outcome of a tool_result handler.
// Only the non-nil fields are applied on top of the current result,
// matching Pi's partial-patch middleware semantics.
type ToolResultEventResult struct {
	// Content replaces the output content blocks when non-nil.
	Content []Block
	// Details replaces the structured data when non-nil.
	Details any
	// IsError replaces the error flag when non-nil.
	IsError *bool
	// Usage replaces the nested usage when non-nil.
	Usage *Usage
}

// SessionStartReason identifies why a session started, mirroring Pi's
// session_start reasons.
type SessionStartReason string

// Session start reasons.
const (
	// SessionStartStartup is a session started when the harness boots.
	SessionStartStartup SessionStartReason = "startup"
	// SessionStartReload is a session restarted by a reload.
	SessionStartReload SessionStartReason = "reload"
	// SessionStartNew is a new session started by the user.
	SessionStartNew SessionStartReason = "new"
	// SessionStartResume is a resumed session.
	SessionStartResume SessionStartReason = "resume"
	// SessionStartFork is a session created by forking or cloning.
	SessionStartFork SessionStartReason = "fork"
)

// SessionStartEvent describes the start of a session.
type SessionStartEvent struct {
	// Reason is why the session started.
	Reason SessionStartReason
	// PreviousSessionFile is the previously active session file; empty
	// for a startup session.
	PreviousSessionFile string
}

// SessionShutdownReason identifies why a session runtime is torn down,
// mirroring Pi's session_shutdown reasons.
type SessionShutdownReason string

// Session shutdown reasons.
const (
	// SessionShutdownQuit is a shutdown on harness exit.
	SessionShutdownQuit SessionShutdownReason = "quit"
	// SessionShutdownReload is a shutdown before a reload.
	SessionShutdownReload SessionShutdownReason = "reload"
	// SessionShutdownNew is a shutdown before starting a new session.
	SessionShutdownNew SessionShutdownReason = "new"
	// SessionShutdownResume is a shutdown before switching sessions.
	SessionShutdownResume SessionShutdownReason = "resume"
	// SessionShutdownFork is a shutdown before forking or cloning.
	SessionShutdownFork SessionShutdownReason = "fork"
)

// SessionShutdownEvent describes the shutdown of a session runtime.
type SessionShutdownEvent struct {
	// Reason is why the session is shutting down.
	Reason SessionShutdownReason
	// TargetSessionFile is the destination session file in session
	// replacement flows; empty otherwise.
	TargetSessionFile string
}
