package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Recorder is the persistence seam RunTurn writes every message to as the
// turn progresses. The session package's *session.Session satisfies this
// interface directly; the CLI wraps it in an adapter so the loop stays
// decoupled from the session format. Implementations must accept
// sequential calls; RunTurn never calls them concurrently.
type Recorder interface {
	AppendUser(*UserMessage) error
	AppendAssistant(*AssistantMessage) error
	AppendToolResult(*ToolResultMessage) error
}

// LoopDeps carries the dependencies of one RunTurn invocation.
type LoopDeps struct {
	// Client is the provider seam used for every assistant turn. It is
	// required.
	Client Client

	// Tools is the registry of tools the model may call, looked up by
	// Name on each toolCall block. May be nil or empty; tool calls then
	// resolve to unknown-tool error results.
	Tools []Tool

	// Recorder persists every message produced by the turn, in order:
	// the user message, each assistant message, and each tool result.
	// May be nil to disable persistence.
	Recorder Recorder

	// Stdout is the streaming output target for text deltas. May be nil
	// to discard the streamed output.
	Stdout io.Writer

	// OnThinking receives thinking deltas for display, when the caller
	// wants them. It may be nil to discard thinking output; the loop
	// itself never decides what to show, it always forwards both text
	// and thinking deltas to the caller-provided sinks. The CLI wires
	// this only when SMIDJA_SHOW_THINKING is set.
	OnThinking func(string)

	// Preparer is the context-management seam consulted before every
	// model call: it may prune, compact, or pin messages and reports the
	// actions taken. May be nil to pass the history through unchanged.
	Preparer ContextPreparer

	// Hooks is the extension hook dispatcher. The loop dispatches the
	// context, message_end, auto_retry_start, auto_retry_end, tool_call,
	// and tool_result chains at the corresponding points. May be nil to
	// run without extension hooks.
	Hooks HookDispatcher

	// Detector is the loop-safety seam consulted after each tool
	// execution. May be nil to disable loop detection.
	Detector LoopDetector

	// SessionEntryIDs maps the initial history passed to RunTurn to its
	// session entry ids, parallel to history, so the preparer can build
	// compaction transcripts that reference real session entries. Best
	// effort: the list is passed through as-is, and the preparer derives
	// request-local refs for the messages the turn appends itself.
	SessionEntryIDs []string

	// RetryPolicy is the retry budget applied to every assistant call.
	// The zero value means DefaultRetryPolicy. It is only consulted when
	// Retry is wired.
	RetryPolicy RetryPolicy

	// Retry wraps one assistant-producing call with bounded retry. It is
	// the seam over internal/retry.Retry, which smidja's agent package
	// cannot import directly (retry depends on agent); hosts wire an
	// adapter that maps RetryPolicy and RetryCallbacks onto the retry
	// package's types. May be nil: the turn then runs without retries,
	// the pre-wave-2 single-attempt behavior.
	Retry func(ctx context.Context, produce func(context.Context) (*AssistantMessage, error), policy RetryPolicy, callbacks *RetryCallbacks) (*AssistantMessage, error)

	// IsContextOverflow classifies an error message as a context-overflow
	// marker, mirroring internal/retry.IsContextOverflow. When a failed
	// model call matches, RunTurn returns a *ContextOverflowError so the
	// host can recover by compacting. May be nil: overflow failures then
	// surface as ordinary errors (the pre-wave-2 behavior).
	IsContextOverflow func(errorMessage string) bool

	// OnRetryScheduled receives the retry-scheduling event of each
	// scheduled automatic retry (attempt is 1-indexed). May be nil.
	OnRetryScheduled func(attempt, maxAttempts int, delayMs int64, errorMessage string)

	// OnRetryFinished receives the settling event of the retry loop:
	// success if the last call completed normally, otherwise the final
	// error message (empty when success is true). May be nil.
	OnRetryFinished func(success bool, attempt int, finalError string)
}

// Sentinel errors returned by RunTurn. Compare with errors.Is.
var (
	// ErrContextOverflow is the root of the typed context-overflow error
	// RunTurn returns when the provider rejected the request because the
	// context exceeds the model's window. The host decides the recovery;
	// the compaction-aware re-entry path lands with session integration.
	ErrContextOverflow = errors.New("agent: context overflow")

	// ErrLoopDetected is the root of the typed loop-detection error
	// RunTurn returns when the loop detector force-stops a run.
	ErrLoopDetected = errors.New("agent: loop detected")
)

// ContextOverflowError wraps a context-overflow failure with the failed
// assistant turn, so the host can compact the session and re-run.
// Compare with errors.Is(err, ErrContextOverflow).
type ContextOverflowError struct {
	// Assistant is the failed assistant turn when the provider returned
	// one (a message with StopReason "error"); nil for transport-level
	// overflow errors.
	Assistant *AssistantMessage

	// Err is the underlying failure, wrapping ErrContextOverflow.
	Err error
}

// Error implements error.
func (e *ContextOverflowError) Error() string {
	if e == nil {
		return ErrContextOverflow.Error()
	}
	if e.Assistant != nil && e.Assistant.ErrorMessage != "" {
		return "agent: context overflow: " + e.Assistant.ErrorMessage
	}
	if e.Err != nil {
		return "agent: context overflow: " + e.Err.Error()
	}
	return ErrContextOverflow.Error()
}

// Unwrap exposes the wrapped error, which itself wraps ErrContextOverflow,
// so errors.Is(err, ErrContextOverflow) matches both error paths.
func (e *ContextOverflowError) Unwrap() error { return e.Err }

// LoopDetectedError wraps a loop-detector force-stop with the outcome and
// the blocked call. Compare with errors.Is(err, ErrLoopDetected).
type LoopDetectedError struct {
	// Outcome is the detector outcome that produced the force-stop.
	Outcome Outcome

	// Call is the tool call that was blocked.
	Call ContentBlock

	// Err is the underlying error, wrapping ErrLoopDetected.
	Err error
}

// Error implements error.
func (e *LoopDetectedError) Error() string {
	if e == nil {
		return ErrLoopDetected.Error()
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return ErrLoopDetected.Error()
}

// Unwrap exposes the wrapped error, which wraps ErrLoopDetected.
func (e *LoopDetectedError) Unwrap() error { return e.Err }

// RunTurn runs one user turn of the agentic loop: it records the user
// message, then alternates assistant turns with tool executions until the
// model stops requesting tools or the context is cancelled.
//
// The user message is built from userInput as a JSON string (so quoting is
// correct for the provider), recorded through the Recorder, and appended to
// history. Each assistant turn streams its text deltas to deps.Stdout and
// its thinking deltas to deps.OnThinking as they arrive, and the completed
// message is recorded and appended. When the model requests tool calls
// (StopReason "toolUse"), the loop executes them in block order, records
// each result immediately, and starts the next round with the full history.
// Unknown tools and tool calls whose arguments are not valid JSON produce
// error results without executing anything.
//
// Wave 2 integration, in order per round:
//
//  1. Context assembly: deps.Preparer.Prepare runs over a copy of the
//     history (with the system prompt, the last provider input-token count,
//     and the session entry ids when provided), then the context hook chain
//     runs over the prepared messages; handlers may replace the list, and
//     the final list feeds the provider call.
//  2. The model call is wrapped in deps.Retry with deps.RetryPolicy (the
//     zero policy means DefaultRetryPolicy). Retry lifecycle events
//     dispatch the auto_retry_start/auto_retry_end hooks and surface
//     through deps.OnRetryScheduled/deps.OnRetryFinished. An aborted
//     stopReason passes through untouched. A failed call whose error text
//     matches deps.IsContextOverflow returns a *ContextOverflowError
//     without consuming retries, so the host can compact and recover.
//  3. Successful assistant messages run the message_end hook chain (the
//     replacement, which must keep the assistant role, is what gets
//     recorded and appended).
//  4. Before the assistant message is recorded, each tool call runs the
//     tool_call gate: a deny short-circuits execution (an error result
//     with the denial reason is recorded instead). The chain's final
//     arguments (returned by the dispatcher after handler patches) are
//     always applied to the recorded assistant toolCall block, deny or
//     not, so the durable record reflects the last validated state: the
//     patched arguments when a sanitizer ran, the model's originals when
//     nothing patched. The same final bytes are what execute, what the
//     loop detector observes, and what the provider sees next. Unknown
//     tools and invalid arguments still produce error results without
//     executing anything.
//  5. After each tool execution the loop detector observes the call's raw
//     outcome. A warn verdict injects the detector's fixed steer message
//     (a host-owned template prefixed with "[smidja] ", containing no
//     model-controlled values) as a user message into history (recorded
//     through the Recorder) and the turn continues; a block verdict
//     replaces the call's result with a "loop detected" error, records
//     it, and ends the run with ErrLoopDetected.
//  6. The tool_result hook chain runs on every result message before it is
//     recorded.
//
// The loop has no round or tool-call budget: it keeps alternating assistant
// turns with tool executions until the model stops on its own ("stop",
// "aborted", or "toolUse" without tool calls), the client fails, the
// context is cancelled (wrapped so errors.Is matches
// context.Canceled/DeadlineExceeded), the context overflows, or the loop
// detector force-stops. RunTurn returns the accumulated history and nil
// when the model stopped on its own, and the history plus an error
// otherwise.
func RunTurn(ctx context.Context, deps *LoopDeps, model string, system string, history []*Message, userInput string) ([]*Message, error) {
	if deps == nil {
		return history, errors.New("agent: nil loop deps")
	}
	if deps.Client == nil {
		return history, errors.New("agent: nil client")
	}

	userContent, err := json.Marshal(userInput)
	if err != nil {
		return history, fmt.Errorf("agent: marshal user input: %w", err)
	}
	userMsg := &UserMessage{Role: string(RoleUser), Content: userContent, Timestamp: NowMillis()}
	if deps.Recorder != nil {
		if err := deps.Recorder.AppendUser(userMsg); err != nil {
			return history, fmt.Errorf("agent: record user message: %w", err)
		}
	}
	history = append(history, &Message{User: userMsg})

	onText := func(delta string) {
		if deps.Stdout != nil {
			io.WriteString(deps.Stdout, delta)
		}
	}
	onThinking := func(delta string) {
		if deps.OnThinking != nil {
			deps.OnThinking(delta)
		}
	}
	toolsByName := make(map[string]Tool, len(deps.Tools))
	for _, t := range deps.Tools {
		if t != nil {
			toolsByName[t.Name()] = t
		}
	}

	policy := deps.RetryPolicy
	if policy == (RetryPolicy{}) {
		policy = DefaultRetryPolicy()
	}
	overflow := deps.IsContextOverflow

	// lastUsageInput anchors the context estimate to the input-token
	// count of the most recent provider call, so a freshly started
	// session keeps the provider's real measurement.
	var lastUsageInput int64
	// turnIndex counts completed tool-using assistant turns, used by the
	// loop detector to identify turns in its detection messages.
	turnIndex := 0

	for {
		if err := ctx.Err(); err != nil {
			return history, fmt.Errorf("agent: %w", err)
		}

		// Context assembly: the preparer runs first over a copy of the
		// history, then the extension context hook chain runs over the
		// prepared messages. Handlers may replace the message list; the
		// final list feeds the provider call. The loop's own history is
		// never replaced, only the per-call request.
		req := ContextRequest{
			Messages:       append([]*Message(nil), history...),
			System:         system,
			LastUsageInput: lastUsageInput,
			EntryIDs:       deps.SessionEntryIDs,
		}
		cres := ContextResult{Messages: req.Messages, System: req.System}
		if deps.Preparer != nil {
			if cres, err = deps.Preparer.Prepare(ctx, req); err != nil {
				return history, fmt.Errorf("agent: prepare context: %w", err)
			}
		}
		if deps.Hooks != nil {
			hres, herr := deps.Hooks.Context(ctx, ContextRequest{
				Messages: cres.Messages,
				System:   cres.System,
				EntryIDs: req.EntryIDs,
			})
			if herr != nil {
				return history, fmt.Errorf("agent: context hook: %w", herr)
			}
			if hres.Messages != nil {
				cres.Messages = hres.Messages
				cres.System = hres.System
			}
		}

		// Model call with bounded retry. The produce closure records the
		// last assistant message it returned so the overflow path can
		// hand the failed turn to the host, and drives the preparer's
		// observation methods around each attempt.
		turnReq := &TurnRequest{
			Model:    model,
			System:   cres.System,
			Messages: cres.Messages,
			Tools:    deps.Tools,
		}
		var lastMsg *AssistantMessage
		produce := func(pctx context.Context) (*AssistantMessage, error) {
			if deps.Preparer != nil {
				deps.Preparer.ObserveRequest(time.Now())
			}
			msg, perr := deps.Client.StreamTurn(pctx, turnReq, onText, onThinking)
			if perr != nil {
				return nil, perr
			}
			if msg != nil {
				lastMsg = msg
				if deps.Preparer != nil {
					deps.Preparer.ObserveResponse(msg)
				}
			}
			return msg, nil
		}

		var asst *AssistantMessage
		if deps.Retry != nil {
			asst, err = deps.Retry(ctx, produce, policy, retryCallbacks(deps, ctx))
		} else {
			asst, err = produce(ctx)
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return history, fmt.Errorf("agent: %w", ctxErr)
			}
			if overflow != nil && overflow(err.Error()) {
				return history, &ContextOverflowError{
					Assistant: lastMsg,
					Err:       fmt.Errorf("%w: %v", ErrContextOverflow, err),
				}
			}
			return history, fmt.Errorf("agent: stream turn: %w", err)
		}
		if asst == nil {
			return history, errors.New("agent: client returned a nil assistant message")
		}
		lastUsageInput = asst.Usage.Input

		// Finalize the assistant message: the message_end hook chain may
		// replace it, and the replacement (which must keep the assistant
		// role) is what gets recorded and appended. Failed provider
		// messages (StopReason "error") skip the hook chain: they are
		// reported below, never finalized as successes.
		finalMsg := &Message{Assistant: asst}
		if asst.StopReason != "error" && deps.Hooks != nil {
			replaced, merr := deps.Hooks.MessageEnd(ctx, finalMsg)
			if merr != nil {
				return history, fmt.Errorf("agent: message_end hook: %w", merr)
			}
			if replaced != nil {
				if replaced.Assistant == nil {
					return history, errors.New("agent: message_end hook replaced the assistant message with another role")
				}
				finalMsg = replaced
			}
		}
		// Tool-call gating runs after message_end but before the durable
		// record, so the recorded assistant message carries the chain's
		// final arguments when a handler patches them. The rule is
		// consistent: FinalArgs, when present, is always applied to the
		// recorded message, deny or not. Only execution is
		// short-circuited by a deny. A plain deny (no patch) therefore
		// records the model's original arguments, the fidelity the audit
		// wants: the model requested that call, the policy rejected it,
		// and nothing sensitive ran. When a sanitizer patched the
		// arguments before a later handler denied, the record shows the
		// patched arguments, the last validated state before the denial.
		calls := toolCallBlocks(finalMsg.Assistant.Content)
		denied := make([]bool, len(calls))
		denyReason := make([]string, len(calls))
		if finalMsg.Assistant.StopReason == "toolUse" && deps.Hooks != nil {
			for i := range calls {
				call := &calls[i]
				dec, derr := deps.Hooks.ToolCall(ctx, call.Name, call.ID, call.Arguments)
				if derr != nil {
					return history, fmt.Errorf("agent: tool_call hook: %w", derr)
				}
				if dec.Block {
					denied[i] = true
					denyReason[i] = dec.Reason
				}
				if dec.FinalArgs != nil {
					// The chain's final arguments are the ones that
					// execute, get recorded, and get observed. The
					// recorded assistant toolCall block is updated so
					// history, the session, and the provider all see the
					// same arguments that ran.
					call.Arguments = dec.FinalArgs
					applyFinalArgs(finalMsg.Assistant, call.ID, call.Arguments)
				}
			}
		}

		if deps.Recorder != nil {
			if err := deps.Recorder.AppendAssistant(finalMsg.Assistant); err != nil {
				return history, fmt.Errorf("agent: record assistant message: %w", err)
			}
		}
		history = append(history, finalMsg)

		if finalMsg.Assistant.StopReason == "error" {
			if overflow != nil && overflow(finalMsg.Assistant.ErrorMessage) {
				return history, &ContextOverflowError{
					Assistant: finalMsg.Assistant,
					Err:       fmt.Errorf("%w: %s", ErrContextOverflow, finalMsg.Assistant.ErrorMessage),
				}
			}
			return history, fmt.Errorf("agent: provider error: %s", finalMsg.Assistant.ErrorMessage)
		}
		if finalMsg.Assistant.StopReason != "toolUse" {
			return history, nil
		}
		if len(calls) == 0 {
			return history, nil
		}
		turnIndex++

		// Steer messages from warn verdicts are injected after the turn's
		// tool activity completes, mirroring Pi's turn-end delivery of
		// the loop-detector warning.
		var steerMsgs []*Message
		for i, call := range calls {
			// The gate already ran for every call before the assistant
			// message was recorded: denied calls produce the denial error
			// result without executing, everything else executes with the
			// same final bytes the record carries.
			res := Result{}
			if denied[i] {
				res = ErrorResult(denyReason[i])
			} else {
				res = executeCall(ctx, call, toolsByName)
			}

			// Loop detector: one observation per executed call, over the
			// raw outcome (before the tool_result hook chain mutates what
			// gets recorded). The assistant text and thinking are
			// included on the first call of the turn only, so the
			// text-based detectors see each turn once.
			if deps.Detector != nil {
				outcome := deps.Detector.Observe(observationTurn(finalMsg.Assistant, call, res, turnIndex, i == 0))
				switch outcome.Verdict {
				case VerdictWarn:
					// The steer message is a fixed host-owned template
					// prefixed with "[smidja] " (rendered by the detector
					// adapter), so it carries no model-controlled values
					// and is distinguishable from real user input. It is
					// still recorded as a plain user message through the
					// Recorder.
					// TODO(session wave): persist the steering message as
					// a custom entry type instead of a plain user-role
					// message.
					if outcome.SteerText != "" {
						steerMsgs = append(steerMsgs, &Message{User: &UserMessage{
							Role:      string(RoleUser),
							Content:   mustMarshalString(outcome.SteerText),
							Timestamp: NowMillis(),
						}})
					}
				case VerdictBlock:
					// Force-stop: the observed call's result is replaced
					// with a loop-detected error, recorded, and the run
					// ends with the sentinel.
					res = ErrorResult("loop detected: " + loopFindingText(outcome))
					blocked, rerr := recordToolResult(deps, ctx, call, res)
					if rerr != nil {
						return history, rerr
					}
					history = append(history, &Message{ToolResult: blocked})
					return history, &LoopDetectedError{
						Outcome: outcome,
						Call:    call,
						Err:     fmt.Errorf("%w: %s", ErrLoopDetected, loopFindingText(outcome)),
					}
				}
			}

			// Tool_result hook chain, then record the final message.
			msg, rerr := recordToolResult(deps, ctx, call, res)
			if rerr != nil {
				return history, rerr
			}
			history = append(history, &Message{ToolResult: msg})
		}

		// Deliver the pending steer messages now that the turn's tool
		// activity is complete: record each through the Recorder and
		// append it to history so the next model call sees it.
		for _, sm := range steerMsgs {
			if deps.Recorder != nil {
				if err := deps.Recorder.AppendUser(sm.User); err != nil {
					return history, fmt.Errorf("agent: record steering message: %w", err)
				}
			}
			history = append(history, sm)
		}
	}
}

// retryCallbacks builds the retry lifecycle callbacks of one assistant
// call: each event dispatches the corresponding extension hook (when a
// dispatcher is wired) and then surfaces to the host through the LoopDeps
// callbacks. Hook dispatch errors are informational and ignored, matching
// the dispatcher's fail-safe semantics.
func retryCallbacks(deps *LoopDeps, ctx context.Context) *RetryCallbacks {
	return &RetryCallbacks{
		Scheduled: func(attempt, maxAttempts int, delayMs int64, errorMessage string) {
			if deps.Hooks != nil {
				deps.Hooks.AutoRetryStart(ctx, attempt, maxAttempts, delayMs, errorMessage)
			}
			if deps.OnRetryScheduled != nil {
				deps.OnRetryScheduled(attempt, maxAttempts, delayMs, errorMessage)
			}
		},
		Finished: func(success bool, attempt int, finalError string) {
			if deps.Hooks != nil {
				deps.Hooks.AutoRetryEnd(ctx, success, attempt, finalError)
			}
			if deps.OnRetryFinished != nil {
				deps.OnRetryFinished(success, attempt, finalError)
			}
		},
	}
}

// recordToolResult runs the tool_result hook chain over the result (when a
// dispatcher is wired), converts it into the tool result message, and
// records it through the Recorder. The returned message is what the caller
// appends to history, so the hook-mutated message is what both the session
// and the model see.
func recordToolResult(deps *LoopDeps, ctx context.Context, call ContentBlock, res Result) (*ToolResultMessage, error) {
	if deps.Hooks != nil {
		var err error
		if res, err = deps.Hooks.ToolResult(ctx, call.Name, call.ID, call.Arguments, res); err != nil {
			return nil, fmt.Errorf("agent: tool_result hook: %w", err)
		}
	}
	msg := resultToToolResult(call, res)
	if deps.Recorder != nil {
		if err := deps.Recorder.AppendToolResult(msg); err != nil {
			return nil, fmt.Errorf("agent: record tool result: %w", err)
		}
	}
	return msg, nil
}

// observationTurn builds the loop-detector observation for one executed
// tool call: the assistant turn's thinking and text (only when withText is
// true, so text-based detectors see each turn once), the call, and its raw
// result. The loop never computes fingerprints; the detector derives them
// from the raw arguments and the provisional result message.
func observationTurn(asst *AssistantMessage, call ContentBlock, res Result, turnIndex int, withText bool) Turn {
	obs := Turn{
		TurnIndex: turnIndex,
		ToolCalls: []ToolCallObs{{
			ToolCallID: call.ID,
			Name:       call.Name,
			Arguments:  call.Arguments,
			Result:     resultToToolResult(call, res),
		}},
	}
	if withText {
		var thinking, text strings.Builder
		for _, b := range asst.Content {
			switch b.Type {
			case BlockTypeThinking:
				thinking.WriteString(b.Thinking)
			case BlockTypeText:
				text.WriteString(b.Text)
			}
		}
		obs.ThinkingText = strings.TrimSpace(thinking.String())
		obs.TextContent = strings.TrimSpace(text.String())
	}
	return obs
}

// loopFindingText renders the block verdict's error text: the first
// finding's message when the outcome carries findings, otherwise the bare
// "loop detected" marker.
func loopFindingText(o Outcome) string {
	if len(o.Findings) > 0 {
		return o.Findings[0].Message
	}
	return ""
}

// mustMarshalString marshals s as a JSON string. Marshaling a string never
// fails, so the error is dropped.
func mustMarshalString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// executeCall runs one toolCall block and converts the outcome into the
// tool result message fed back to the model. Unknown tools and arguments
// that are not valid JSON produce error results without executing
// anything.
func executeCall(ctx context.Context, call ContentBlock, toolsByName map[string]Tool) Result {
	tool, ok := toolsByName[call.Name]
	if !ok {
		return ErrorResult(fmt.Sprintf("unknown tool %q", call.Name))
	}
	if !json.Valid(call.Arguments) {
		return ErrorResult(fmt.Sprintf("tool %q: invalid arguments: not valid JSON", call.Name))
	}
	return tool.Exec(ctx, call.Arguments)
}

// toolCallBlocks returns the content blocks of the given assistant content
// whose type is toolCall, in order.
func toolCallBlocks(content []ContentBlock) []ContentBlock {
	var out []ContentBlock
	for _, b := range content {
		if b.Type == BlockTypeToolCall {
			out = append(out, b)
		}
	}
	return out
}

// applyFinalArgs replaces the Arguments of the assistant content's
// toolCall block with the given id, so the recorded assistant message
// carries the exact arguments the tool executed with, or, for denied
// calls, the last validated state before the denial. It is a no-op when
// the block is absent (for example after a message_end replacement that
// dropped or re-id'd the block).
func applyFinalArgs(asst *AssistantMessage, callID string, args json.RawMessage) {
	if asst == nil {
		return
	}
	for i := range asst.Content {
		b := &asst.Content[i]
		if b.Type == BlockTypeToolCall && b.ID == callID {
			b.Arguments = args
			return
		}
	}
}

// resultToToolResult converts a tool's Result into the tool result message
// fed back to the model. Content blocks pass through as-is; when the
// result carries no text block at all, an empty text block is appended so
// the message always has a readable content for the provider.
func resultToToolResult(call ContentBlock, res Result) *ToolResultMessage {
	content := res.Content
	hasText := false
	for _, b := range content {
		if b.Type == BlockTypeText {
			hasText = true
			break
		}
	}
	if !hasText {
		content = append(append([]ContentBlock(nil), content...), ContentBlock{Type: BlockTypeText, Text: ""})
	}
	return &ToolResultMessage{
		Role:       string(RoleToolResult),
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    content,
		IsError:    res.IsError,
		Timestamp:  NowMillis(),
	}
}
