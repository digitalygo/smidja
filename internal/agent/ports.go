package agent

import (
	"context"
	"encoding/json"
	"time"
)

// ToolCallID identifies one tool invocation. It matches the ID of the
// assistant toolCall block (ContentBlock.ID) and the ToolCallID of its
// ToolResultMessage.
type ToolCallID string

// ContextRequest is the input of one context-assembly step: the
// conversation so far and the system prompt about to be sent to the
// provider.
type ContextRequest struct {
	// Messages is the conversation so far: user, assistant, and
	// toolResult messages in chronological order. The preparer must not
	// mutate the slice or its messages; it returns a new list.
	Messages []*Message

	// System is the system prompt. May be empty when the request
	// carries no system prompt.
	System string

	// LastUsageInput, when greater than zero, anchors the occupancy
	// estimate to the input-token count of the most recent provider
	// call (the Usage.Input of the last assistant message). The
	// preparer estimates the current context size as the anchor plus
	// the messages appended since, so a freshly started session keeps
	// the provider's real measurement instead of re-estimating the
	// whole context from scratch. Zero disables the anchor.
	LastUsageInput int64

	// EntryIDs, when present with len(EntryIDs) == len(Messages), maps
	// each message to its session entry id. The preparer uses entry ids
	// in compaction transcripts and in CompactionEntry.FirstKeptEntryID
	// so the caller can persist summaries that reference real session
	// entries. When absent or mismatched, the preparer derives
	// deterministic request-local refs instead.
	EntryIDs []string
}

// ContextResult is the output of context assembly: the message list and
// system prompt to send, plus a record of the context-management actions
// taken so the loop can report them.
type ContextResult struct {
	// Messages is the message list to send to the provider.
	Messages []*Message

	// System is the system prompt to send.
	System string

	// Pruned lists the ToolCallIDs of the tool-result messages the
	// preparer replaced with prune placeholders, in the order they
	// appear in Messages. Empty when nothing was pruned.
	Pruned []ToolCallID

	// Compacted reports whether the preparer applied a compaction entry
	// (a summary replacing older messages). It is true exactly when
	// Compaction is non-nil.
	Compacted bool

	// Compaction, when non-nil, records the compaction entry the caller
	// must persist in the session in place of the compacted messages.
	// The entry is never inserted into Messages: the summary transcript
	// is an audit record for the session, not model-visible prose.
	Compaction *CompactionEntry
}

// CompactionEntry records one compaction pass for the caller to persist
// in the session, mirroring Pi's compaction entry fields (summary,
// firstKeptEntryId, tokensBefore). The caller stores Summary in place of
// the compacted messages and uses FirstKeptEntryID to locate the boundary
// between the summary and the surviving messages.
type CompactionEntry struct {
	// Summary is the deterministic tagged JSON transcript of the
	// compaction: {"strategy":"smidja-verbatim-v1","kept":[...refs...]}
	// for selector-driven compaction or
	// {"strategy":"smidja-fallback-v1","dropped":[...refs...]} for the
	// deterministic fallback. It is a structured audit record, never
	// generated prose.
	Summary json.RawMessage

	// FirstKeptEntryID is the entry ref of the first message that
	// survived compaction, in the order the caller will send them. The
	// caller can resolve it to a session entry through EntryIDs when it
	// provided them.
	FirstKeptEntryID string

	// TokensBefore is the estimated context size in tokens at the time
	// of compaction, before the compacted messages were removed.
	TokensBefore int64
}

// ContextPreparer assembles the context for every LLM call. It is the
// seam the smart context management core implements: internal/contextmanager
// provides the prune/compact/pin policy and delegates verbatim selection
// to the internal/subagent selector. A no-op implementation passes the
// request through unchanged. Implementations normally combine the context
// policy with the extension context hook through HookDispatcher.Context.
type ContextPreparer interface {
	// Prepare assembles the context for one LLM call from the request.
	// It returns the context to send. Errors are fatal for the turn: the
	// loop aborts with the error.
	Prepare(ctx context.Context, req ContextRequest) (ContextResult, error)

	// ObserveRequest records the start time of a provider request, so
	// the preparer can detect likely cache misses (the double-criterion
	// prune/compact trigger needs the time since the last request).
	ObserveRequest(t time.Time)

	// ObserveResponse records the completed assistant message, so the
	// preparer can account tokens and update its usage state.
	ObserveResponse(m *AssistantMessage)
}

// ToolCallDecision is the outcome of the tool_call hook chain for one
// tool call. The zero value allows the call.
type ToolCallDecision struct {
	// Block denies the tool execution.
	Block bool

	// Reason is the denial reason, surfaced to the model and the user.
	Reason string
}

// HookDispatcher runs the extension hook chains for the phase 1 events.
// internal/extensions implements it over its handler registry; the loop
// and the CLI call the dispatch methods at the corresponding points. Every
// method is safe to call with a nil receiver equivalent (no handlers): it
// returns the input unchanged.
type HookDispatcher interface {
	// Context runs the context-assembly hook chain over the request and
	// returns the assembled context. The messages handed to handlers are
	// a deep copy, so the input slice is never mutated.
	Context(ctx context.Context, req ContextRequest) (ContextResult, error)

	// MessageEnd runs the message_end hook chain for a finalized
	// message. It returns the replacement message, or the original when
	// no handler replaced it. The replacement must keep the original
	// role.
	MessageEnd(ctx context.Context, m *Message) (*Message, error)

	// AutoRetryStart runs the auto_retry_start hook chain when a failed
	// turn is scheduled for automatic retry.
	AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error

	// AutoRetryEnd runs the auto_retry_end hook chain when an automatic
	// retry settles. finalError is empty when success is true.
	AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error

	// ToolCall runs the tool_call hook chain before a tool executes.
	// Handlers may patch the arguments (by returning a replaced request
	// through the context chain) or deny the call.
	ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (ToolCallDecision, error)

	// ToolResult runs the tool_result hook chain after a tool executes,
	// letting handlers patch the result content and error flag.
	ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res Result) (Result, error)

	// SessionStart runs the session_start hook chain when a session
	// starts. reason is one of "startup", "reload", "new", "resume", or
	// "fork".
	SessionStart(ctx context.Context, reason string) error

	// SessionShutdown runs the session_shutdown hook chain when a
	// session runtime is torn down. reason is one of "quit", "reload",
	// "new", "resume", or "fork".
	SessionShutdown(ctx context.Context, reason string) error
}

// ToolCatalog is the read side of the tool registry the loop uses.
// internal/extensions implements it over the registered tool set (built-in
// tools plus extension tools); the loop replaces its plain tool slice with
// this catalog in the extension wave.
type ToolCatalog interface {
	// Tools returns every registered tool in registration order.
	Tools() []Tool

	// Get returns the tool with the given name and whether it exists.
	Get(name string) (Tool, bool)
}

// ---------------------------------------------------------------------------
// Wave 2 loop seams: retry and loop detection.
//
// The types in this section mirror internal/retry and internal/loopdetector,
// which smidja's agent package cannot import directly: both packages depend
// on agent, and importing them would create a cycle. Hosts wire adapters
// that map between these mirror types and the real packages (for example
// the CLI maps agent.RetryPolicy onto retry.Policy and feeds the loop
// detector through a small adapter over *loopdetector.Detector). A nil seam
// disables the corresponding behavior, keeping the pre-wave-2 loop intact.

// RetryPolicy is the retry budget of one assistant-producing call, mirroring
// internal/retry.Policy: bounded attempts with exponential backoff
// (baseDelayMs * 2^(attempt-1)) before jitter. The zero value means
// DefaultRetryPolicy when a retrier is wired; to disable retries entirely,
// leave LoopDeps.Retry nil.
type RetryPolicy struct {
	// Enabled turns retry on. When false, the retrier returns the first
	// response unchanged, equivalent to calling produce directly.
	Enabled bool

	// MaxRetries is the maximum number of retry attempts after the
	// initial call. 0 disables retries. Default 10.
	MaxRetries int

	// BaseDelayMs is the first backoff delay in milliseconds; attempt n
	// waits baseDelayMs * 2^(n-1). Default 2000.
	BaseDelayMs int64
}

// DefaultRetryPolicy returns the smidja default retry policy, mirroring
// internal/retry.Default(): enabled, 10 retries, 2000ms base delay.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Enabled: true, MaxRetries: 10, BaseDelayMs: 2000}
}

// RetryCallbacks reports the retry lifecycle events of one assistant turn,
// mirroring internal/retry.Callbacks. Methods are invoked synchronously
// from the retrier in call order; nil fields are skipped. The loop builds
// the callbacks from LoopDeps.OnRetryScheduled/OnRetryFinished and the
// hook dispatcher's AutoRetryStart/AutoRetryEnd events.
type RetryCallbacks struct {
	// Scheduled is called before the backoff sleep of each retry attempt
	// (attempt is 1-indexed).
	Scheduled func(attempt, maxAttempts int, delayMs int64, errorMessage string)

	// AttemptStart is called after the backoff sleep, immediately before
	// the retried call starts.
	AttemptStart func()

	// Finished is called once when the retry loop ends after at least
	// one retry: success if the last call completed normally, otherwise
	// the final error message (empty when success is true).
	Finished func(success bool, attempt int, finalError string)
}

// Verdict is the combined loop-detector verdict for one observation,
// mirroring internal/loopdetector.Verdict: none (no detection, or the
// silent middle step of a longer escalation ladder), warn (first
// detection), or block (force-stop after consecutive detections).
type Verdict int

// Verdict values.
const (
	// VerdictNone means no detection, or the silent step of the
	// escalation ladder between warn and block.
	VerdictNone Verdict = iota

	// VerdictWarn is the first detection: the host delivers the warning
	// steer message and the turn continues.
	VerdictWarn

	// VerdictBlock is the escalated force-stop: the loop replaces the
	// observed call's result with a loop-detected error, records it, and
	// ends the run with ErrLoopDetected.
	VerdictBlock
)

// String returns the verdict's name.
func (v Verdict) String() string {
	switch v {
	case VerdictWarn:
		return "warning"
	case VerdictBlock:
		return "force-stop"
	default:
		return "none"
	}
}

// Finding is one loop detector's finding, mirroring
// internal/loopdetector.Finding: its type and the human-readable message
// the detector attaches.
type Finding struct {
	// Type is one of the loopdetector Finding* constants, for example
	// "tool-repetition" or "message-repetition".
	Type string

	// Message is the detection message text.
	Message string
}

// ToolCallObs is one tool invocation observed by the loop detector. The
// loop never computes hashes: it hands the raw arguments and the raw
// result message to the detector, and the concrete detector (the host's
// adapter over internal/loopdetector) derives its fingerprints from them,
// so the fingerprints stay in one place.
type ToolCallObs struct {
	// ToolCallID matches the toolCall block ID and the result's
	// ToolCallID.
	ToolCallID string

	// Name is the tool name, for example "read" or "bash".
	Name string

	// Arguments is the raw JSON object of the tool arguments.
	Arguments json.RawMessage

	// Result is the provisional tool result message of the execution:
	// the raw outcome, before the tool_result hook chain mutates the
	// recorded message. Nil when the call produced no result.
	Result *ToolResultMessage
}

// Turn is one tool-execution observation fed to the loop detector: the
// assistant turn that produced the call, the call itself, and its raw
// result. It mirrors internal/loopdetector.Turn; the host's adapter
// converts it. The loop feeds one observation per executed tool call, and
// includes the assistant thinking and text only on the first call of each
// assistant turn so the text-based detectors see each turn exactly once.
type Turn struct {
	// TurnIndex is the host's session turn counter of the assistant
	// turn that produced the call, used in detection messages.
	TurnIndex int

	// ThinkingText is the concatenated thinking blocks of the assistant
	// turn, trimmed. Empty when not included in this observation.
	ThinkingText string

	// TextContent is the concatenated text blocks of the assistant turn,
	// trimmed. Empty when not included in this observation.
	TextContent string

	// ToolCalls are the executed calls observed in this turn. The loop
	// observes one call per Turn.
	ToolCalls []ToolCallObs
}

// Outcome is the result of observing one turn: the combined verdict,
// every detector's findings, and the rendered steer intervention the loop
// should deliver for warn and block verdicts. The host's adapter fills
// SteerCustomType and SteerText from the underlying detector's steer
// message (loopdetector.Outcome.SteerMessage); other implementations set
// them directly. Both are empty when Verdict is VerdictNone.
type Outcome struct {
	// Verdict is the combined verdict.
	Verdict Verdict

	// Findings lists every detector finding of this observation, in
	// detector order. Empty when Verdict is VerdictNone.
	Findings []Finding

	// SteerCustomType is the custom type of the steer message to deliver
	// ("loop-detector-warning" or "loop-detector-force-stop"), or empty.
	SteerCustomType string

	// SteerText is the rendered markdown body of the steer message to
	// deliver, or empty.
	SteerText string
}

// LoopDetector is the loop-safety seam RunTurn consults after each tool
// execution. internal/loopdetector.Detector satisfies it through a thin
// host adapter (the agent package cannot import loopdetector because
// loopdetector depends on agent). A nil detector disables detection.
//
// The adapter converts the agent.Turn (which mirrors loopdetector.Turn
// field for field) back into an assistant message plus tool results, runs
// it through loopdetector.ExtractTurn and Detector.Observe, and maps the
// outcome back, rendering SteerCustomType/SteerText via
// loopdetector.Outcome.SteerMessage.
type LoopDetector interface {
	// Observe feeds one completed tool-execution observation and returns
	// the combined verdict plus every detector's findings.
	Observe(turn Turn) Outcome
}
