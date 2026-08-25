package extensions

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

// snapshot is an immutable view of the registered handler chains at one
// point in time. Dispatch builds one at dispatch start, so handler
// registrations made during a dispatch only apply to the next event.
type snapshot struct {
	entries []snapshotEntry
}

// snapshotEntry is one extension's handlers in the snapshot.
type snapshotEntry struct {
	id              string
	context         []sdk.ContextHandler
	messageEnd      []sdk.MessageEndHandler
	autoRetryStart  []sdk.AutoRetryStartHandler
	autoRetryEnd    []sdk.AutoRetryEndHandler
	toolCall        []sdk.ToolCallHandler
	toolResult      []sdk.ToolResultHandler
	sessionStart    []sdk.SessionStartHandler
	sessionShutdown []sdk.SessionShutdownHandler
}

// hasContextHandlers reports whether any snapshot entry registered
// context-assembly handlers. The Context method uses it to return the
// request unchanged, without converting, when no handler exists.
func (s snapshot) hasContextHandlers() bool {
	for _, e := range s.entries {
		if len(e.context) > 0 {
			return true
		}
	}
	return false
}

// Dispatcher runs the phase 1 hook chains over the handlers the registry
// collected. It implements internal/agent.HookDispatcher exactly, so the
// loop and the CLI call it at the corresponding points.
//
// Dispatch semantics, matching Pi's extension runner:
//
//   - Extensions are visited in registration order; within an extension,
//     handlers run in the order the extension registered them.
//   - Every handler invocation is guarded: a returned error or a recovered
//     panic is logged once, with the extension id and the event name, and
//     the handler's outcome is skipped. Subsequent handlers still run.
//   - The mutating chains (Context, MessageEnd, ToolResult) retain the
//     last valid value when a handler fails, and ToolCall never denies a
//     call because a handler failed.
//   - The handler slices are snapshotted at dispatch start; registrations
//     made during a dispatch apply to the next event.
//   - No locks are held while extension code runs: the registry lock is
//     released before the first handler is invoked.
//
// Every method is safe to call with a nil receiver: it behaves as a
// dispatcher with no handlers and returns the input unchanged.
type Dispatcher struct {
	rt *Runtime
}

// Compile-time assertion that the dispatcher satisfies the exact agent
// port method set. If internal/agent.HookDispatcher changes, this file
// stops compiling, which is the point.
var _ agent.HookDispatcher = (*Dispatcher)(nil)

// Context runs the context-assembly hook chain over the request and
// returns the assembled context. The messages handed to handlers are a
// deep copy of the request, so the input slice is never mutated; handlers
// return a replacement through ContextEventResult. The system prompt
// passes through unchanged. Handler failures are logged and skipped; the
// chain retains the last valid message list.
func (d *Dispatcher) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	snap := d.snapshot()
	if !snap.hasContextHandlers() {
		return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
	}
	hc := d.handlerContext(ctx)

	ev := sdk.ContextEvent{Messages: make([]sdk.Message, 0, len(req.Messages))}
	for _, m := range req.Messages {
		ev.Messages = append(ev.Messages, messageToSDK(m))
	}

	replaced := false
	for _, e := range snap.entries {
		for _, h := range e.context {
			var res *sdk.ContextEventResult
			if !d.call(e.id, sdk.EventContext, func() (err error) {
				res, err = h(hc, ev)
				return err
			}) {
				continue // keep the last valid message list
			}
			if res != nil && res.Messages != nil {
				ev.Messages = res.Messages
				replaced = true
			}
		}
	}

	if !replaced {
		return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
	}
	out := make([]*agent.Message, 0, len(ev.Messages))
	for i := range ev.Messages {
		out = append(out, messageFromSDK(&ev.Messages[i]))
	}
	return agent.ContextResult{Messages: out, System: req.System}, nil
}

// MessageEnd runs the message_end hook chain for a finalized message and
// returns the replacement message, or the original when no handler
// replaced it. A replacement whose role differs from the original is
// logged and ignored, keeping the current message. Handler failures are
// logged and skipped; the last valid message is retained.
func (d *Dispatcher) MessageEnd(ctx context.Context, m *agent.Message) (*agent.Message, error) {
	if m == nil {
		return nil, nil
	}
	snap := d.snapshot()
	hc := d.handlerContext(ctx)

	ev := sdk.MessageEndEvent{Message: messageToSDK(m)}
	originalRole := ev.Message.Role

	replaced := false
	for _, e := range snap.entries {
		for _, h := range e.messageEnd {
			var res *sdk.MessageEndEventResult
			if !d.call(e.id, sdk.EventMessageEnd, func() (err error) {
				res, err = h(hc, ev)
				return err
			}) {
				continue // keep the current message
			}
			if res == nil {
				continue
			}
			if res.Message.Role != originalRole {
				if lg := d.log(); lg != nil {
					lg.Logf("extension %s: %s handler returned a message with role %q, want %q; keeping the current message", e.id, sdk.EventMessageEnd, res.Message.Role, originalRole)
				}
				continue
			}
			ev.Message = res.Message
			replaced = true
		}
	}

	if !replaced {
		return m, nil
	}
	return messageFromSDK(&ev.Message), nil
}

// AutoRetryStart runs the auto_retry_start hook chain when a failed turn
// is scheduled for automatic retry, before the backoff delay. Handler
// failures are logged and skipped.
func (d *Dispatcher) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	snap := d.snapshot()
	hc := d.handlerContext(ctx)
	ev := sdk.AutoRetryStartEvent{
		Attempt:      attempt,
		MaxAttempts:  maxAttempts,
		DelayMs:      delayMs,
		ErrorMessage: errorMessage,
	}
	for _, e := range snap.entries {
		for _, h := range e.autoRetryStart {
			d.call(e.id, sdk.EventAutoRetryStart, func() error { return h(hc, ev) })
		}
	}
	return nil
}

// AutoRetryEnd runs the auto_retry_end hook chain when an automatic retry
// settles, whether it succeeded or exhausted its attempts. Handler
// failures are logged and skipped.
func (d *Dispatcher) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	snap := d.snapshot()
	hc := d.handlerContext(ctx)
	ev := sdk.AutoRetryEndEvent{
		Success:    success,
		Attempt:    attempt,
		FinalError: finalError,
	}
	for _, e := range snap.entries {
		for _, h := range e.autoRetryEnd {
			d.call(e.id, sdk.EventAutoRetryEnd, func() error { return h(hc, ev) })
		}
	}
	return nil
}

// ToolCall runs the tool_call hook chain before a tool executes and
// returns the decision plus the chain's final arguments. The first
// handler that returns a decision with Block true denies the call and
// short-circuits the chain; its reason surfaces to the model and the
// user. Handler errors and panics are logged and never deny the call:
// the chain continues and the call is allowed, matching Pi's fail-safe
// tool_call policy.
//
// Handlers patch the arguments two ways: in-place byte writes within the
// event's Args buffer (same-length patches) and, for full replacements,
// the returned decision's FinalArgs field. Each handler receives a
// private copy of the chain's current arguments; a successful handler's
// patches are committed and visible to later handlers and to the
// execution, while a failed handler's patches are discarded. Every
// committed value must be strict JSON: an invalid patch is logged and
// the last valid arguments are kept. The final arguments are returned in
// the decision's FinalArgs field, and the loop executes exactly those
// bytes.
func (d *Dispatcher) ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (agent.ToolCallDecision, error) {
	snap := d.snapshot()
	hc := d.handlerContext(ctx)

	current := cloneRaw(args)
	for _, e := range snap.entries {
		for _, h := range e.toolCall {
			patched := cloneRaw(current)
			var dec *sdk.ToolCallDecision
			if !d.call(e.id, sdk.EventToolCall, func() (err error) {
				dec, err = h(hc, sdk.ToolCallEvent{ToolCallID: callID, Name: name, Args: patched})
				return err
			}) {
				continue // panics and errors never deny; patches are discarded
			}
			if dec != nil && dec.Block {
				return agent.ToolCallDecision{Block: true, Reason: dec.Reason, FinalArgs: current}, nil
			}
			// Commit the handler's patch: a FinalArgs replacement wins
			// over in-place byte writes, and only strict JSON is
			// committed. Handlers that left the arguments untouched are
			// not validated.
			var candidate json.RawMessage
			if dec != nil && dec.FinalArgs != nil {
				candidate = dec.FinalArgs
			} else if !bytes.Equal(patched, current) {
				candidate = patched
			}
			if candidate != nil {
				if json.Valid(candidate) {
					current = cloneRaw(candidate)
				} else if lg := d.log(); lg != nil {
					lg.Logf("extension %s: %s handler produced invalid JSON tool arguments; keeping the last valid arguments", e.id, sdk.EventToolCall)
				}
			}
		}
	}
	return agent.ToolCallDecision{FinalArgs: current}, nil
}

// ToolResult runs the tool_result hook chain after a tool executes,
// letting handlers patch the result. Patches are partial: only the non-nil
// fields of each ToolResultEventResult apply on top of the current result.
// Handler failures are logged and skipped; the last valid result is
// retained. When no handler patched anything, the input result is returned
// unchanged.
func (d *Dispatcher) ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res agent.Result) (agent.Result, error) {
	snap := d.snapshot()
	hc := d.handlerContext(ctx)

	ev := sdk.ToolResultEvent{
		ToolCallID: callID,
		Name:       name,
		Args:       cloneRaw(args),
		Content:    blocksToSDK(res.Content),
		IsError:    res.IsError,
	}

	patched := false
	for _, e := range snap.entries {
		for _, h := range e.toolResult {
			var patch *sdk.ToolResultEventResult
			if !d.call(e.id, sdk.EventToolResult, func() (err error) {
				patch, err = h(hc, ev)
				return err
			}) {
				continue // keep the current result
			}
			if patch == nil {
				continue
			}
			if patch.Content != nil {
				ev.Content = patch.Content
				patched = true
			}
			if patch.Details != nil {
				ev.Details = patch.Details
				patched = true
			}
			if patch.IsError != nil {
				ev.IsError = *patch.IsError
				patched = true
			}
			if patch.Usage != nil {
				ev.Usage = patch.Usage
				patched = true
			}
		}
	}

	if !patched {
		return res, nil
	}
	return agent.Result{Content: blocksFromSDK(ev.Content), IsError: ev.IsError}, nil
}

// SessionStart runs the session_start hook chain when a session starts.
// reason is one of "startup", "reload", "new", "resume", or "fork".
// Handler failures are logged and skipped.
func (d *Dispatcher) SessionStart(ctx context.Context, reason string) error {
	snap := d.snapshot()
	hc := d.handlerContext(ctx)
	ev := sdk.SessionStartEvent{Reason: sdk.SessionStartReason(reason)}
	for _, e := range snap.entries {
		for _, h := range e.sessionStart {
			d.call(e.id, sdk.EventSessionStart, func() error { return h(hc, ev) })
		}
	}
	return nil
}

// SessionShutdown runs the session_shutdown hook chain when a session
// runtime is torn down. reason is one of "quit", "reload", "new",
// "resume", or "fork". Handler failures are logged and skipped.
func (d *Dispatcher) SessionShutdown(ctx context.Context, reason string) error {
	snap := d.snapshot()
	hc := d.handlerContext(ctx)
	ev := sdk.SessionShutdownEvent{Reason: sdk.SessionShutdownReason(reason)}
	for _, e := range snap.entries {
		for _, h := range e.sessionShutdown {
			d.call(e.id, sdk.EventSessionShutdown, func() error { return h(hc, ev) })
		}
	}
	return nil
}

// call runs one handler and reports whether it completed without an error
// or a panic. A failure is logged exactly once, with the extension id and
// the event name, and the handler's outcome is discarded by the caller.
func (d *Dispatcher) call(extID, event string, fn func() error) bool {
	if err := runGuarded(fn); err == nil {
		return true
	} else if lg := d.log(); lg != nil {
		lg.Logf("extension %s: %s handler failed: %v", extID, event, err)
	}
	return false
}

// snapshot returns the registry's handler snapshot, or an empty one when
// the dispatcher has no runtime or registry (nil-receiver safety).
func (d *Dispatcher) snapshot() snapshot {
	if d == nil || d.rt == nil || d.rt.registry == nil {
		return snapshot{}
	}
	return d.rt.registry.snapshot()
}

// handlerContext builds the sdk.HandlerContext handed to handlers: the
// host-provided context when one is set, otherwise the default context
// wrapping the host API. The dispatch signal is the request context.
func (d *Dispatcher) handlerContext(ctx context.Context) sdk.HandlerContext {
	if d == nil || d.rt == nil {
		return &defaultContext{}
	}
	return d.rt.handlerContext(ctx)
}

// log returns the runtime's logger, or nil when none is available.
func (d *Dispatcher) log() Logger {
	if d == nil || d.rt == nil {
		return nil
	}
	return d.rt.loggerOr()
}
