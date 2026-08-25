package extensions

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

// reqWith builds a ContextRequest whose first user message carries the
// given text, used to observe which handler saw which request.
func reqWith(text string) agent.ContextRequest {
	return agent.ContextRequest{
		Messages: []*agent.Message{{
			User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"` + text + `"`)},
		}},
		System: "system prompt",
	}
}

// TestContextOrdering verifies that context handlers run in extension
// registration order, then in in-extension registration order.
func TestContextOrdering(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	var order []string

	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			order = append(order, "a1")
			return nil, nil
		}).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			order = append(order, "a2")
			return nil, nil
		}).
		build())
	reg.Register(ext("b").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			order = append(order, "b1")
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	req := reqWith("x")
	res, err := d.Context(t.Context(), req)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	want := []string{"a1", "a2", "b1"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("handler order = %v, want %v", order, want)
	}
	// No handler replaced anything: the input passes through unchanged.
	if len(res.Messages) != 1 || res.Messages[0] != req.Messages[0] {
		t.Fatal("context result messages must be the input unchanged")
	}
	if res.System != "system prompt" {
		t.Fatalf("system = %q, want passthrough", res.System)
	}
	if log.count() != 0 {
		t.Fatalf("logs = %d, want 0", log.count())
	}
}

// TestContextChainReplacesMessages verifies the mutating chain: handlers
// see the previous handler's replacement and the final value is returned.
func TestContextChainReplacesMessages(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			return &sdk.ContextEventResult{Messages: []sdk.Message{
				{Role: string(agent.RoleUser), Content: []sdk.Block{{Type: "text", Text: "replaced"}}},
			}}, nil
		}).
		build())
	reg.Register(ext("b").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			// The previous handler's replacement is visible here.
			if len(e.Messages) != 1 || e.Messages[0].Content[0].Text != "replaced" {
				t.Errorf("handler b saw %v, want the replacement from a", e.Messages)
			}
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	res, err := d.Context(t.Context(), reqWith("original"))
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Role() != string(agent.RoleUser) ||
		len(res.Messages[0].User.Content) == 0 || !strings.Contains(string(res.Messages[0].User.Content), "replaced") {
		t.Fatalf("result messages = %+v, want the replacement", res.Messages)
	}
}

// TestContextPanicAndErrorIsolation verifies that a failing handler is
// logged exactly once and does not stop the remaining handlers.
func TestContextPanicAndErrorIsolation(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	var order []string

	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			order = append(order, "a1-error")
			return nil, errors.New("boom")
		}).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			order = append(order, "a2-panic")
			panic("kaboom")
		}).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			order = append(order, "a3")
			return &sdk.ContextEventResult{Messages: []sdk.Message{
				{Role: string(agent.RoleUser), Content: []sdk.Block{{Type: "text", Text: "survivor"}}},
			}}, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	res, err := d.Context(t.Context(), reqWith("x"))
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"a1-error", "a2-panic", "a3"}) {
		t.Fatalf("handler order = %v, want all three handlers to run", order)
	}
	// The surviving handler's replacement is the chain result.
	if len(res.Messages) != 1 || !strings.Contains(string(res.Messages[0].User.Content), "survivor") {
		t.Fatalf("result = %+v, want the survivor replacement", res.Messages)
	}
	lines := log.all()
	if len(lines) != 2 {
		t.Fatalf("logs = %v, want exactly 2 lines", lines)
	}
	if !strings.Contains(lines[0], "a") || !strings.Contains(lines[0], "context") || !strings.Contains(lines[0], "boom") {
		t.Fatalf("log line 0 = %q, want extension id, event name, and error", lines[0])
	}
	if !strings.Contains(lines[1], "a") || !strings.Contains(lines[1], "context") || !strings.Contains(lines[1], "panic: kaboom") {
		t.Fatalf("log line 1 = %q, want extension id, event name, and panic", lines[1])
	}
}

// TestContextRetainsLastValidValue verifies that a failed handler does
// not discard the previous handler's replacement.
func TestContextRetainsLastValidValue(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			return &sdk.ContextEventResult{Messages: []sdk.Message{
				{Role: string(agent.RoleUser), Content: []sdk.Block{{Type: "text", Text: "kept"}}},
			}}, nil
		}).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			return nil, errors.New("boom")
		}).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			if len(e.Messages) != 1 || e.Messages[0].Content[0].Text != "kept" {
				t.Errorf("handler 3 saw %v, want the retained replacement", e.Messages)
			}
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(&recLogger{}).Dispatcher()
	res, err := d.Context(t.Context(), reqWith("x"))
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(res.Messages) != 1 || !strings.Contains(string(res.Messages[0].User.Content), "kept") {
		t.Fatalf("result = %+v, want the retained replacement", res.Messages)
	}
}

// TestMessageEndChaining verifies ordering and replacement on the
// message_end chain.
func TestMessageEndChaining(t *testing.T) {
	reg := NewRegistry()
	var order []string

	original := &agent.Message{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant)}}
	reg.Register(ext("a").
		messageEnd(func(ctx sdk.HandlerContext, e sdk.MessageEndEvent) (*sdk.MessageEndEventResult, error) {
			order = append(order, "a1")
			return &sdk.MessageEndEventResult{Message: sdk.Message{
				Role:    string(agent.RoleAssistant),
				Content: []sdk.Block{{Type: "text", Text: "patched"}},
			}}, nil
		}).
		build())
	reg.Register(ext("b").
		messageEnd(func(ctx sdk.HandlerContext, e sdk.MessageEndEvent) (*sdk.MessageEndEventResult, error) {
			order = append(order, "b1")
			if e.Message.Content[0].Text != "patched" {
				t.Errorf("handler b saw %+v, want the previous replacement", e.Message)
			}
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	got, err := d.MessageEnd(t.Context(), original)
	if err != nil {
		t.Fatalf("message end: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"a1", "b1"}) {
		t.Fatalf("handler order = %v, want [a1 b1]", order)
	}
	if got.Role() != string(agent.RoleAssistant) || len(got.Assistant.Content) != 1 || got.Assistant.Content[0].Text != "patched" {
		t.Fatalf("result = %+v, want the patched assistant message", got)
	}
}

// TestMessageEndRoleViolationKeepsCurrent verifies that a replacement
// with the wrong role is logged and ignored.
func TestMessageEndRoleViolationKeepsCurrent(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	original := &agent.Message{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant)}}

	reg.Register(ext("a").
		messageEnd(func(ctx sdk.HandlerContext, e sdk.MessageEndEvent) (*sdk.MessageEndEventResult, error) {
			return &sdk.MessageEndEventResult{Message: sdk.Message{Role: string(agent.RoleUser)}}, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	got, err := d.MessageEnd(t.Context(), original)
	if err != nil {
		t.Fatalf("message end: %v", err)
	}
	if got.Role() != string(agent.RoleAssistant) {
		t.Fatalf("result role = %q, want the original assistant role kept", got.Role())
	}
	lines := log.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "role") {
		t.Fatalf("logs = %v, want one role-violation line", lines)
	}
}

// TestToolCallOrderingAndDeny verifies the tool_call chain order and the
// first-Block-wins short circuit.
func TestToolCallOrderingAndDeny(t *testing.T) {
	reg := NewRegistry()
	var order []string

	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "a1")
			return nil, nil // allow
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "a2-deny")
			return &sdk.ToolCallDecision{Block: true, Reason: "denied by a"}, nil
		}).
		build())
	reg.Register(ext("b").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "b1")
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if !dec.Block || dec.Reason != "denied by a" {
		t.Fatalf("decision = %+v, want the first Block=true with its reason", dec)
	}
	if !reflect.DeepEqual(order, []string{"a1", "a2-deny"}) {
		t.Fatalf("handler order = %v, want [a1 a2-deny] (b must be short-circuited)", order)
	}
}

// TestToolCallAllowsWhenNothingBlocks verifies the allow path.
func TestToolCallAllowsWhenNothingBlocks(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			return &sdk.ToolCallDecision{}, nil // zero decision: allow
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if dec.Block {
		t.Fatalf("decision = %+v, want the zero (allow) decision", dec)
	}
}

// TestToolCallErrorsAndPanicsNeverDeny verifies the fail-safe policy: a
// failing handler is logged, the chain continues, and the call is only
// denied by a handler that explicitly blocks.
func TestToolCallErrorsAndPanicsNeverDeny(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	var order []string

	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "a1-error")
			return &sdk.ToolCallDecision{Block: true, Reason: "must not surface"}, errors.New("boom")
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "a2-panic")
			panic("kaboom")
		}).
		build())
	reg.Register(ext("b").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "b1-deny")
			return &sdk.ToolCallDecision{Block: true, Reason: "denied by b"}, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if !dec.Block || dec.Reason != "denied by b" {
		t.Fatalf("decision = %+v, want b's explicit deny (errors and panics never deny)", dec)
	}
	if !reflect.DeepEqual(order, []string{"a1-error", "a2-panic", "b1-deny"}) {
		t.Fatalf("handler order = %v, want the full chain after failures", order)
	}
	if log.count() != 2 {
		t.Fatalf("logs = %d, want exactly 2", log.count())
	}
}

// TestToolCallPanicAloneAllows verifies that a chain whose only failure
// is a panic still allows the call.
func TestToolCallPanicAloneAllows(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			panic("kaboom")
		}).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", nil)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if dec.Block {
		t.Fatalf("decision = %+v, want allow after a panic", dec)
	}
	if log.count() != 1 {
		t.Fatalf("logs = %d, want exactly 1", log.count())
	}
}

// TestToolCallFinalArgsReplacement verifies that a handler patching the
// arguments through the decision's FinalArgs commits the replacement:
// later handlers see it and the returned decision carries it.
func TestToolCallFinalArgsReplacement(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			return &sdk.ToolCallDecision{FinalArgs: json.RawMessage(`{"path":"sanitized.go"}`)}, nil
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			if !strings.Contains(string(e.Args), "sanitized.go") {
				t.Errorf("handler b saw %s, want the committed replacement", e.Args)
			}
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if dec.Block {
		t.Fatalf("decision = %+v, want allow", dec)
	}
	if !strings.Contains(string(dec.FinalArgs), "sanitized.go") {
		t.Errorf("FinalArgs = %s, want the sanitized replacement", dec.FinalArgs)
	}
}

// TestToolCallInPlaceMutationCommitted verifies that a same-length
// in-place mutation of the event's Args is committed on success: later
// handlers see it and the returned decision carries it.
func TestToolCallInPlaceMutationCommitted(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			copy(e.Args, `{"path":"x.go"}`) // same length as the input
			return nil, nil
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			if !strings.Contains(string(e.Args), "x.go") {
				t.Errorf("handler b saw %s, want the committed in-place patch", e.Args)
			}
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if !strings.Contains(string(dec.FinalArgs), "x.go") || strings.Contains(string(dec.FinalArgs), "a.go") {
		t.Errorf("FinalArgs = %s, want the in-place patched arguments", dec.FinalArgs)
	}
}

// TestToolCallInvalidPatchRejected verifies that a handler returning an
// invalid-JSON FinalArgs is treated as failed for the mutation: the
// patch is logged, the last valid arguments are kept, later handlers see
// them, and the chain still allows the call.
func TestToolCallInvalidPatchRejected(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			return &sdk.ToolCallDecision{FinalArgs: json.RawMessage(`not-json`)}, nil
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			if !strings.Contains(string(e.Args), "a.go") {
				t.Errorf("handler b saw %s, want the last valid arguments kept", e.Args)
			}
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if dec.Block {
		t.Fatalf("decision = %+v, want allow", dec)
	}
	if !strings.Contains(string(dec.FinalArgs), "a.go") {
		t.Errorf("FinalArgs = %s, want the last valid arguments", dec.FinalArgs)
	}
	lines := log.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "invalid JSON tool arguments") {
		t.Fatalf("logs = %v, want exactly one invalid-JSON line", lines)
	}
}

// TestToolCallInvalidInPlacePatchRejected verifies that a same-length
// in-place mutation producing invalid JSON is rejected the same way: it
// is logged and the last valid arguments are kept.
func TestToolCallInvalidInPlacePatchRejected(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			for i := range e.Args {
				e.Args[i] = 'x'
			}
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if !strings.Contains(string(dec.FinalArgs), "a.go") {
		t.Errorf("FinalArgs = %s, want the last valid arguments", dec.FinalArgs)
	}
	lines := log.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "invalid JSON tool arguments") {
		t.Fatalf("logs = %v, want exactly one invalid-JSON line", lines)
	}
}

// TestToolCallFailedHandlerPatchDiscarded verifies that a failing
// handler's patch is discarded: its FinalArgs is never committed, the
// last valid arguments are kept, and a later handler still denies.
func TestToolCallFailedHandlerPatchDiscarded(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			return &sdk.ToolCallDecision{FinalArgs: json.RawMessage(`{"path":"must-not-commit"}`)}, errors.New("boom")
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			if !strings.Contains(string(e.Args), "a.go") {
				t.Errorf("handler b saw %s, want the last valid arguments kept after a failure", e.Args)
			}
			return &sdk.ToolCallDecision{Block: true, Reason: "denied by b"}, nil
		}).
		build())

	d := NewRuntime(reg).SetLogger(&recLogger{}).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if !dec.Block || dec.Reason != "denied by b" {
		t.Fatalf("decision = %+v, want b's deny (failed patch discarded)", dec)
	}
	if !strings.Contains(string(dec.FinalArgs), "a.go") || strings.Contains(string(dec.FinalArgs), "must-not-commit") {
		t.Errorf("FinalArgs = %s, want the last valid arguments, not the failed patch", dec.FinalArgs)
	}
}

// TestToolCallDenyAfterPatchShortCircuits verifies that a deny still
// short-circuits the chain after earlier handlers patched the
// arguments: the decision carries the patched arguments as FinalArgs and
// no later handler runs.
func TestToolCallDenyAfterPatchShortCircuits(t *testing.T) {
	reg := NewRegistry()
	var order []string
	reg.Register(ext("a").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "a1")
			return &sdk.ToolCallDecision{FinalArgs: json.RawMessage(`{"path":"patched.go"}`)}, nil
		}).
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "a2-deny")
			return &sdk.ToolCallDecision{Block: true, Reason: "denied after patch"}, nil
		}).
		build())
	reg.Register(ext("b").
		toolCall(func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			order = append(order, "b1")
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if !dec.Block || dec.Reason != "denied after patch" {
		t.Fatalf("decision = %+v, want the deny with its reason", dec)
	}
	if !strings.Contains(string(dec.FinalArgs), "patched.go") {
		t.Errorf("FinalArgs = %s, want the patched arguments carried on deny", dec.FinalArgs)
	}
	if !reflect.DeepEqual(order, []string{"a1", "a2-deny"}) {
		t.Fatalf("handler order = %v, want [a1 a2-deny] (b must be short-circuited)", order)
	}
}

// TestToolResultPartialPatch verifies the partial-patch semantics: only
// non-nil patch fields apply, in chain order.
func TestToolResultPartialPatch(t *testing.T) {
	reg := NewRegistry()
	res := agent.Result{Content: []agent.ContentBlock{{Type: "text", Text: "original"}}, IsError: true}

	reg.Register(ext("a").
		toolResult(func(ctx sdk.HandlerContext, e sdk.ToolResultEvent) (*sdk.ToolResultEventResult, error) {
			return &sdk.ToolResultEventResult{Content: []sdk.Block{{Type: "text", Text: "patched"}}}, nil
		}).
		build())
	reg.Register(ext("b").
		toolResult(func(ctx sdk.HandlerContext, e sdk.ToolResultEvent) (*sdk.ToolResultEventResult, error) {
			if e.Content[0].Text != "patched" {
				t.Errorf("handler b saw %+v, want a's patch", e.Content)
			}
			f := false
			return &sdk.ToolResultEventResult{IsError: &f}, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	got, err := d.ToolResult(t.Context(), "read", "call_1", json.RawMessage(`{}`), res)
	if err != nil {
		t.Fatalf("tool result: %v", err)
	}
	if got.IsError {
		t.Fatal("IsError = true, want the patch to false")
	}
	if len(got.Content) != 1 || got.Content[0].Text != "patched" {
		t.Fatalf("content = %+v, want a's content patch", got.Content)
	}
}

// TestToolResultRetainsLastValidValue verifies that a failed handler does
// not discard earlier patches.
func TestToolResultRetainsLastValidValue(t *testing.T) {
	reg := NewRegistry()
	res := agent.Result{Content: []agent.ContentBlock{{Type: "text", Text: "original"}}}

	reg.Register(ext("a").
		toolResult(func(ctx sdk.HandlerContext, e sdk.ToolResultEvent) (*sdk.ToolResultEventResult, error) {
			return &sdk.ToolResultEventResult{Content: []sdk.Block{{Type: "text", Text: "kept"}}}, nil
		}).
		build())
	reg.Register(ext("b").
		toolResult(func(ctx sdk.HandlerContext, e sdk.ToolResultEvent) (*sdk.ToolResultEventResult, error) {
			return nil, errors.New("boom")
		}).
		build())

	d := NewRuntime(reg).SetLogger(&recLogger{}).Dispatcher()
	got, err := d.ToolResult(t.Context(), "read", "call_1", json.RawMessage(`{}`), res)
	if err != nil {
		t.Fatalf("tool result: %v", err)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "kept" {
		t.Fatalf("content = %+v, want the retained patch", got.Content)
	}
}

// TestRetryAndSessionEvents verifies the event fields reach the handlers
// and the events dispatch in order.
func TestRetryAndSessionEvents(t *testing.T) {
	reg := NewRegistry()
	var start *sdk.AutoRetryStartEvent
	var end *sdk.AutoRetryEndEvent
	var sessStart sdk.SessionStartReason
	var sessEnd sdk.SessionShutdownReason
	var order []string

	reg.Register(ext("a").
		retryStart(func(ctx sdk.HandlerContext, e sdk.AutoRetryStartEvent) error {
			order = append(order, "start")
			e2 := e
			start = &e2
			return nil
		}).
		retryEnd(func(ctx sdk.HandlerContext, e sdk.AutoRetryEndEvent) error {
			order = append(order, "end")
			e2 := e
			end = &e2
			return nil
		}).
		sessionStart(func(ctx sdk.HandlerContext, e sdk.SessionStartEvent) error {
			sessStart = e.Reason
			return nil
		}).
		sessionShutdown(func(ctx sdk.HandlerContext, e sdk.SessionShutdownEvent) error {
			sessEnd = e.Reason
			return nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	if err := d.AutoRetryStart(t.Context(), 2, 5, 1500, "timeout"); err != nil {
		t.Fatalf("auto retry start: %v", err)
	}
	if start == nil || start.Attempt != 2 || start.MaxAttempts != 5 || start.DelayMs != 1500 || start.ErrorMessage != "timeout" {
		t.Fatalf("start event = %+v, want the retry fields", start)
	}
	if err := d.AutoRetryEnd(t.Context(), false, 2, "timeout"); err != nil {
		t.Fatalf("auto retry end: %v", err)
	}
	if end == nil || end.Success || end.Attempt != 2 || end.FinalError != "timeout" {
		t.Fatalf("end event = %+v, want the settling fields", end)
	}
	if err := d.SessionStart(t.Context(), "new"); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if sessStart != sdk.SessionStartNew {
		t.Fatalf("session start reason = %q, want %q", sessStart, sdk.SessionStartNew)
	}
	if err := d.SessionShutdown(t.Context(), "quit"); err != nil {
		t.Fatalf("session shutdown: %v", err)
	}
	if sessEnd != sdk.SessionShutdownQuit {
		t.Fatalf("session shutdown reason = %q, want %q", sessEnd, sdk.SessionShutdownQuit)
	}
	if !reflect.DeepEqual(order, []string{"start", "end"}) {
		t.Fatalf("order = %v, want [start end]", order)
	}
}

// TestRetryEventIsolation verifies failing retry and session handlers
// are logged and skipped.
func TestRetryEventIsolation(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	reg.Register(ext("a").
		retryStart(func(ctx sdk.HandlerContext, e sdk.AutoRetryStartEvent) error {
			return errors.New("boom")
		}).
		sessionStart(func(ctx sdk.HandlerContext, e sdk.SessionStartEvent) error {
			panic("kaboom")
		}).
		build())
	reg.Register(ext("b").
		retryStart(func(ctx sdk.HandlerContext, e sdk.AutoRetryStartEvent) error { return nil }).
		build())

	d := NewRuntime(reg).SetLogger(log).Dispatcher()
	if err := d.AutoRetryStart(t.Context(), 1, 3, 100, "x"); err != nil {
		t.Fatalf("auto retry start: %v", err)
	}
	if err := d.SessionStart(t.Context(), "new"); err != nil {
		t.Fatalf("session start: %v", err)
	}
	lines := log.all()
	if len(lines) != 2 {
		t.Fatalf("logs = %v, want exactly 2 lines", lines)
	}
	if !strings.Contains(lines[0], "auto_retry_start") || !strings.Contains(lines[1], "session_start") {
		t.Fatalf("logs = %v, want the event names in the lines", lines)
	}
}

// TestNoHandlersPassThrough verifies the no-handlers behavior: every
// dispatch returns the input unchanged.
func TestNoHandlersPassThrough(t *testing.T) {
	reg := NewRegistry()
	d := NewRuntime(reg).Dispatcher()

	req := reqWith("x")
	res, err := d.Context(t.Context(), req)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(res.Messages) != len(req.Messages) || res.Messages[0] != req.Messages[0] {
		t.Fatal("context must return the input messages unchanged")
	}
	if res.System != req.System {
		t.Fatal("context must return the system prompt unchanged")
	}

	m := &agent.Message{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant)}}
	got, err := d.MessageEnd(t.Context(), m)
	if err != nil || got != m {
		t.Fatalf("message end = %v, %v; want the input message unchanged", got, err)
	}

	dec, err := d.ToolCall(t.Context(), "read", "call_1", json.RawMessage(`{"a":1}`))
	if err != nil || dec.Block {
		t.Fatalf("tool call = %+v, %v; want the zero allow decision", dec, err)
	}

	toolRes := agent.Result{Content: []agent.ContentBlock{{Type: "text", Text: "out"}}, IsError: true}
	gotRes, err := d.ToolResult(t.Context(), "read", "call_1", json.RawMessage(`{}`), toolRes)
	if err != nil || !reflect.DeepEqual(gotRes, toolRes) {
		t.Fatalf("tool result = %+v, %v; want the input result unchanged", gotRes, err)
	}

	if err := d.AutoRetryStart(t.Context(), 1, 3, 0, ""); err != nil {
		t.Fatalf("auto retry start: %v", err)
	}
	if err := d.AutoRetryEnd(t.Context(), true, 1, ""); err != nil {
		t.Fatalf("auto retry end: %v", err)
	}
	if err := d.SessionStart(t.Context(), "startup"); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := d.SessionShutdown(t.Context(), "quit"); err != nil {
		t.Fatalf("session shutdown: %v", err)
	}
}

// TestSnapshotDuringDispatch verifies that handlers registered while a
// dispatch runs apply to the next event, not the current one.
func TestSnapshotDuringDispatch(t *testing.T) {
	reg := NewRegistry()
	var order []string
	var registered sync.Once
	ctx := t.Context()

	a := ext("a").
		context(func(hc sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			order = append(order, "a1")
			// Register a second extension mid-dispatch, exactly once.
			registered.Do(func() {
				if err := reg.Register(ext("b").
					context(func(hc sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
						order = append(order, "b1")
						return nil, nil
					}).
					build()); err != nil {
					t.Errorf("register b mid-dispatch: %v", err)
				}
			})
			return nil, nil
		}).
		build()
	if err := reg.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}

	d := NewRuntime(reg).Dispatcher()
	_, _ = d.Context(ctx, reqWith("first"))
	if !reflect.DeepEqual(order, []string{"a1"}) {
		t.Fatalf("first dispatch order = %v, want [a1] (b must not run in the current dispatch)", order)
	}
	_, _ = d.Context(ctx, reqWith("second"))
	if !reflect.DeepEqual(order, []string{"a1", "a1", "b1"}) {
		t.Fatalf("second dispatch order = %v, want [a1 a1 b1]", order)
	}
}

// TestNilDispatcherIsSafe verifies the nil-receiver equivalent: a nil
// dispatcher behaves as one with no handlers.
func TestNilDispatcherIsSafe(t *testing.T) {
	var d *Dispatcher
	req := reqWith("x")
	res, err := d.Context(t.Context(), req)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(res.Messages) != len(req.Messages) {
		t.Fatal("nil dispatcher must pass the request through")
	}
	dec, err := d.ToolCall(t.Context(), "read", "call_1", nil)
	if err != nil || dec.Block {
		t.Fatalf("nil dispatcher tool call = %+v, %v; want allow", dec, err)
	}
	if err := d.SessionStart(t.Context(), "startup"); err != nil {
		t.Fatalf("nil dispatcher session start: %v", err)
	}
}

// TestNilMessageEndIsSafe verifies MessageEnd with a nil message returns
// nil without panicking.
func TestNilMessageEndIsSafe(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		messageEnd(func(ctx sdk.HandlerContext, e sdk.MessageEndEvent) (*sdk.MessageEndEventResult, error) {
			return &sdk.MessageEndEventResult{Message: e.Message}, nil
		}).
		build())
	d := NewRuntime(reg).Dispatcher()
	got, err := d.MessageEnd(t.Context(), nil)
	if err != nil || got != nil {
		t.Fatalf("message end(nil) = %v, %v; want nil, nil", got, err)
	}
}

// TestInputNeverMutated verifies the deep-copy boundary: handlers cannot
// mutate the caller's request messages.
func TestInputNeverMutated(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			// Aggressively mutate the event in place; the input must not
			// observe any of it.
			for i := range e.Messages {
				e.Messages[i].Role = "hacker"
				for j := range e.Messages[i].Content {
					e.Messages[i].Content[j].Text = "mutated"
				}
			}
			return nil, nil
		}).
		build())

	original := &agent.Message{
		Assistant: &agent.AssistantMessage{
			Role:      string(agent.RoleAssistant),
			Content:   []agent.ContentBlock{{Type: "text", Text: "keep me"}},
			Timestamp: 42,
		},
	}
	d := NewRuntime(reg).Dispatcher()
	_, err := d.Context(t.Context(), agent.ContextRequest{Messages: []*agent.Message{original}})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if original.Role() != string(agent.RoleAssistant) || original.Assistant.Content[0].Text != "keep me" {
		t.Fatal("the input message was mutated through the event")
	}
}
