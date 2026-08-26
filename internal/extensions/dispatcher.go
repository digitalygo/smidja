package extensions

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

type snapshot struct {
	entries []snapshotEntry
}

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

func (s snapshot) hasContextHandlers() bool {
	for _, e := range s.entries {
		if len(e.context) > 0 {
			return true
		}
	}
	return false
}

type Dispatcher struct {
	rt *Runtime
}

var _ agent.HookDispatcher = (*Dispatcher)(nil)

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
				continue
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
				continue
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
				continue
			}
			if dec != nil && dec.Block {
				return agent.ToolCallDecision{Block: true, Reason: dec.Reason, FinalArgs: current}, nil
			}
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
				continue
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

func (d *Dispatcher) call(extID, event string, fn func() error) bool {
	if err := runGuarded(fn); err == nil {
		return true
	} else if lg := d.log(); lg != nil {
		lg.Logf("extension %s: %s handler failed: %v", extID, event, err)
	}
	return false
}

func (d *Dispatcher) snapshot() snapshot {
	if d == nil || d.rt == nil || d.rt.registry == nil {
		return snapshot{}
	}
	return d.rt.registry.snapshot()
}

func (d *Dispatcher) handlerContext(ctx context.Context) sdk.HandlerContext {
	if d == nil || d.rt == nil {
		return &defaultContext{}
	}
	return d.rt.handlerContext(ctx)
}

func (d *Dispatcher) log() Logger {
	if d == nil || d.rt == nil {
		return nil
	}
	return d.rt.loggerOr()
}
