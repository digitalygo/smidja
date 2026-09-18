package ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

type fakeHookDispatcher struct {
	mu sync.Mutex

	contextCalls  int
	messageCalls  int
	retryStart    int
	retryEnd      int
	toolCalls     [][3]string
	toolResults   int
	sessionStarts int
	sessionStops  int

	contextResult agent.ContextResult
	contextErr    error
	messageOut    *agent.Message
	messageErr    error
	retryErr      error
	toolDecision  agent.ToolCallDecision
	toolErr       error
	toolResult    agent.Result
	toolResultErr error
	sessionErr    error
}

var _ agent.HookDispatcher = (*fakeHookDispatcher)(nil)

func (f *fakeHookDispatcher) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contextCalls++
	if f.contextErr != nil {
		return agent.ContextResult{}, f.contextErr
	}
	return f.contextResult, nil
}

func (f *fakeHookDispatcher) MessageEnd(ctx context.Context, m *agent.Message) (*agent.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messageCalls++
	if f.messageErr != nil {
		return nil, f.messageErr
	}
	if f.messageOut != nil {
		return f.messageOut, nil
	}
	return m, nil
}

func (f *fakeHookDispatcher) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retryStart++
	return f.retryErr
}

func (f *fakeHookDispatcher) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retryEnd++
	return f.retryErr
}

func (f *fakeHookDispatcher) ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (agent.ToolCallDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolCalls = append(f.toolCalls, [3]string{name, callID, string(args)})
	if f.toolErr != nil {
		return agent.ToolCallDecision{}, f.toolErr
	}
	return f.toolDecision, nil
}

func (f *fakeHookDispatcher) ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res agent.Result) (agent.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolResults++
	if f.toolResultErr != nil {
		return agent.Result{}, f.toolResultErr
	}
	if f.toolResult.Content != nil {
		return f.toolResult, nil
	}
	return res, nil
}

func (f *fakeHookDispatcher) SessionStart(ctx context.Context, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionStarts++
	return f.sessionErr
}

func (f *fakeHookDispatcher) SessionShutdown(ctx context.Context, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionStops++
	return f.sessionErr
}

func testHookTheme(t *testing.T) *tui.Theme {
	t.Helper()
	registry := tui.NewThemeRegistry("", "", tui.ColorModeTrueColor)
	theme, err := registry.SetTheme("dark")
	if err != nil {
		t.Fatalf("load dark theme: %v", err)
	}
	return theme
}

func testHookSurface(t *testing.T) *interactive.Surface {
	t.Helper()
	return interactive.NewSurface(interactive.SurfaceOptions{Theme: testHookTheme(t), Home: t.TempDir()})
}

func plainSurfaceText(t *testing.T, surface *interactive.Surface) string {
	t.Helper()
	frame := surface.RenderFrame(80, 24)
	var builder strings.Builder
	for _, line := range frame.Lines {
		builder.WriteString(tui.StripTerminalSequences(line))
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestTurnScopeStreamsTextAndThinking(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	writer := scope.TextWriter()
	if n, err := writer.Write([]byte("hello ")); err != nil || n != 6 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if _, err := writer.Write([]byte("world")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	scope.ThinkingCallback()("quiet reasoning")
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	if !strings.Contains(text, "hello world") {
		t.Errorf("frame missing streamed text:\n%s", text)
	}
	if !strings.Contains(text, "Thinking") {
		t.Errorf("frame missing thinking block:\n%s", text)
	}
}

func TestTurnScopeDropsWritesWhenInactive(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, func() bool { return false })
	if n, err := scope.TextWriter().Write([]byte("hidden")); err != nil || n != 6 {
		t.Fatalf("Write() = %d, %v, want 6 and nil while inactive", n, err)
	}
	scope.ThinkingCallback()("hidden thought")
	scope.Finish("stop", "")
	if text := plainSurfaceText(t, surface); strings.Contains(text, "hidden") {
		t.Errorf("inactive scope wrote to the surface:\n%s", text)
	}
}

func TestTurnScopeFinishWithoutBlock(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.Finish("stop", "")
	scope.Finish("error", "boom")
}

func TestHookDecoratorForwardsEveryEvent(t *testing.T) {
	surface := testHookSurface(t)
	next := &fakeHookDispatcher{
		contextResult: agent.ContextResult{System: "kept"},
		toolDecision:  agent.ToolCallDecision{FinalArgs: json.RawMessage(`{"a":1}`)},
		toolResult:    agent.TextResult("patched"),
	}
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(next, scope)
	ctx := context.Background()
	if _, err := decorator.Context(ctx, agent.ContextRequest{System: "in"}); err != nil {
		t.Fatalf("Context: %v", err)
	}
	original := &agent.Message{User: &agent.UserMessage{Role: "user"}}
	replacement := &agent.Message{User: &agent.UserMessage{Role: "user", Content: json.RawMessage(`"new"`)}}
	next.messageOut = replacement
	if got, err := decorator.MessageEnd(ctx, original); err != nil || got != replacement {
		t.Fatalf("MessageEnd() = %v, %v, want the replacement", got, err)
	}
	if err := decorator.AutoRetryStart(ctx, 1, 3, 50, "flaky"); err != nil {
		t.Fatalf("AutoRetryStart: %v", err)
	}
	if err := decorator.AutoRetryEnd(ctx, true, 1, ""); err != nil {
		t.Fatalf("AutoRetryEnd: %v", err)
	}
	decision, err := decorator.ToolCall(ctx, "read", "call_1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ToolCall: %v", err)
	}
	if string(decision.FinalArgs) != `{"a":1}` {
		t.Fatalf("ToolCall decision = %q, want the patched args", decision.FinalArgs)
	}
	result, err := decorator.ToolResult(ctx, "read", "call_1", json.RawMessage(`{}`), agent.TextResult("raw"))
	if err != nil {
		t.Fatalf("ToolResult: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "patched" {
		t.Fatalf("ToolResult = %+v, want the patched content", result)
	}
	if err := decorator.SessionStart(ctx, "startup"); err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if err := decorator.SessionShutdown(ctx, "quit"); err != nil {
		t.Fatalf("SessionShutdown: %v", err)
	}
	next.mu.Lock()
	defer next.mu.Unlock()
	if next.contextCalls != 1 || next.messageCalls != 1 || next.retryStart != 1 || next.retryEnd != 1 {
		t.Errorf("forward counts = %+v, want one call each", next)
	}
	if len(next.toolCalls) != 1 || next.toolResults != 1 || next.sessionStarts != 1 || next.sessionStops != 1 {
		t.Errorf("forward counts = %+v, want one call each", next)
	}
}

func TestHookDecoratorDedupsToolCallsByID(t *testing.T) {
	surface := testHookSurface(t)
	next := &fakeHookDispatcher{}
	decorator := NewHookDecorator(next, NewTurnScope(surface, nil))
	ctx := context.Background()
	args := json.RawMessage(`{"path":"a.go"}`)
	if _, err := decorator.ToolCall(ctx, "read", "call_9", args); err != nil {
		t.Fatalf("first ToolCall: %v", err)
	}
	next.toolDecision = agent.ToolCallDecision{FinalArgs: args}
	second, err := decorator.ToolCall(ctx, "read", "call_9", args)
	if err != nil {
		t.Fatalf("second ToolCall: %v", err)
	}
	if string(second.FinalArgs) != string(args) {
		t.Fatalf("second decision = %q, want revalidation preserved", second.FinalArgs)
	}
	next.mu.Lock()
	forwards := len(next.toolCalls)
	next.mu.Unlock()
	if forwards != 2 {
		t.Fatalf("forwarded ToolCall events = %d, want 2 (revalidation preserved)", forwards)
	}
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "read"); got != 1 {
		t.Errorf("tool name appears %d times, want exactly one block:\n%s", got, text)
	}
}

func TestHookDecoratorFinalizesToolResult(t *testing.T) {
	surface := testHookSurface(t)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, NewTurnScope(surface, nil))
	ctx := context.Background()
	if _, err := decorator.ToolCall(ctx, "exec", "call_2", json.RawMessage(`{"cmd":"go build ./..."}`)); err != nil {
		t.Fatalf("ToolCall: %v", err)
	}
	if _, err := decorator.ToolResult(ctx, "exec", "call_2", nil, agent.TextResult("build ok")); err != nil {
		t.Fatalf("ToolResult: %v", err)
	}
	if _, err := decorator.ToolResult(ctx, "exec", "call_3", nil, agent.ErrorResult("boom")); err != nil {
		t.Fatalf("ToolResult without prior call: %v", err)
	}
	text := plainSurfaceText(t, surface)
	if !strings.Contains(text, "build ok") {
		t.Errorf("frame missing tool output:\n%s", text)
	}
	if !strings.Contains(text, "boom") {
		t.Errorf("frame missing error output:\n%s", text)
	}
}

func TestHookDecoratorMapsRetries(t *testing.T) {
	surface := testHookSurface(t)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, NewTurnScope(surface, nil))
	ctx := context.Background()
	if err := decorator.AutoRetryStart(ctx, 2, 5, 1500, "flaky"); err != nil {
		t.Fatalf("AutoRetryStart: %v", err)
	}
	if text := plainSurfaceText(t, surface); !strings.Contains(text, "Retrying (2/5") {
		t.Errorf("frame missing retry countdown:\n%s", text)
	}
	if err := decorator.AutoRetryEnd(ctx, true, 2, ""); err != nil {
		t.Fatalf("AutoRetryEnd: %v", err)
	}
	if text := plainSurfaceText(t, surface); strings.Contains(text, "Retrying (2/5") {
		t.Errorf("retry state should clear after AutoRetryEnd:\n%s", text)
	}
}

func TestHookDecoratorDeniesAndErrors(t *testing.T) {
	surface := testHookSurface(t)
	next := &fakeHookDispatcher{toolDecision: agent.ToolCallDecision{Block: true, Reason: "needs approval"}}
	decorator := NewHookDecorator(next, NewTurnScope(surface, nil))
	ctx := context.Background()
	decision, err := decorator.ToolCall(ctx, "exec", "call_d", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ToolCall: %v", err)
	}
	if !decision.Block || decision.Reason != "needs approval" {
		t.Fatalf("decision = %+v, want the denial preserved", decision)
	}
	next.toolErr = errors.New("hook failed")
	if _, err := decorator.ToolCall(ctx, "exec", "call_e", json.RawMessage(`{}`)); err == nil {
		t.Fatal("ToolCall should propagate hook errors")
	}
	next.toolErr = nil
	next.toolResultErr = errors.New("result hook failed")
	if _, err := decorator.ToolResult(ctx, "exec", "call_d", nil, agent.TextResult("x")); err == nil {
		t.Fatal("ToolResult should propagate hook errors")
	}
}

func TestHookDecoratorNilNext(t *testing.T) {
	surface := testHookSurface(t)
	decorator := NewHookDecorator(nil, NewTurnScope(surface, nil))
	ctx := context.Background()
	if _, err := decorator.Context(ctx, agent.ContextRequest{System: "s"}); err != nil {
		t.Fatalf("Context: %v", err)
	}
	original := &agent.Message{}
	if got, err := decorator.MessageEnd(ctx, original); err != nil || got != original {
		t.Fatalf("MessageEnd() = %v, %v, want identity", got, err)
	}
	decision, err := decorator.ToolCall(ctx, "read", "call_n", json.RawMessage(`{}`))
	if err != nil || decision.Block {
		t.Fatalf("ToolCall() = %+v, %v, want allow by default", decision, err)
	}
	result, err := decorator.ToolResult(ctx, "read", "call_n", nil, agent.TextResult("kept"))
	if err != nil || result.Content[0].Text != "kept" {
		t.Fatalf("ToolResult() = %+v, %v", result, err)
	}
	if err := decorator.SessionStart(ctx, "x"); err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if err := decorator.SessionShutdown(ctx, "x"); err != nil {
		t.Fatalf("SessionShutdown: %v", err)
	}
	if err := decorator.AutoRetryStart(ctx, 1, 1, 1, ""); err != nil {
		t.Fatalf("AutoRetryStart: %v", err)
	}
	if err := decorator.AutoRetryEnd(ctx, true, 1, ""); err != nil {
		t.Fatalf("AutoRetryEnd: %v", err)
	}
	if text := plainSurfaceText(t, surface); !strings.Contains(text, "kept") {
		t.Errorf("frame missing nil-next tool output:\n%s", text)
	}
}

func TestHookDecoratorNilScope(t *testing.T) {
	next := &fakeHookDispatcher{}
	decorator := NewHookDecorator(next, nil)
	ctx := context.Background()
	if _, err := decorator.ToolCall(ctx, "read", "call_s", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("ToolCall: %v", err)
	}
	if _, err := decorator.ToolResult(ctx, "read", "call_s", nil, agent.TextResult("x")); err != nil {
		t.Fatalf("ToolResult: %v", err)
	}
	if err := decorator.AutoRetryStart(ctx, 1, 1, 1, ""); err != nil {
		t.Fatalf("AutoRetryStart: %v", err)
	}
}

func TestTurnScopeOpenedTracksBlocks(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	if scope.Opened() {
		t.Fatal("fresh scope should report no block")
	}
	if _, err := scope.TextWriter().Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !scope.Opened() {
		t.Fatal("scope should report a block after streaming")
	}
	scope.Finish("stop", "")
	if !scope.Opened() {
		t.Fatal("scope should remember the block after finish")
	}
	var nilScope *TurnScope
	if nilScope.Opened() {
		t.Fatal("nil scope should report no block")
	}
}

func TestTurnScopeEmptyDeltas(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	if _, err := scope.TextWriter().Write(nil); err != nil {
		t.Fatalf("empty Write: %v", err)
	}
	scope.ThinkingCallback()("")
	if scope.Opened() {
		t.Fatal("empty deltas must not open a block")
	}
}

func TestTurnScopeNilSurface(t *testing.T) {
	scope := NewTurnScope(nil, nil)
	if _, err := scope.TextWriter().Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	scope.ThinkingCallback()("y")
	scope.Finish("stop", "")
	if scope.Opened() {
		t.Fatal("nil surface must never open a block")
	}
}

func TestHookDecoratorPropagatesRetryErrors(t *testing.T) {
	surface := testHookSurface(t)
	next := &fakeHookDispatcher{retryErr: errors.New("retry hook failed")}
	decorator := NewHookDecorator(next, NewTurnScope(surface, nil))
	ctx := context.Background()
	if err := decorator.AutoRetryStart(ctx, 1, 1, 1, ""); err == nil {
		t.Fatal("AutoRetryStart should propagate hook errors")
	}
	if err := decorator.AutoRetryEnd(ctx, true, 1, ""); err == nil {
		t.Fatal("AutoRetryEnd should propagate hook errors")
	}
}

func TestHookDecoratorPropagatesMessageError(t *testing.T) {
	surface := testHookSurface(t)
	next := &fakeHookDispatcher{messageErr: errors.New("message hook failed")}
	decorator := NewHookDecorator(next, NewTurnScope(surface, nil))
	if _, err := decorator.MessageEnd(context.Background(), &agent.Message{}); err == nil {
		t.Fatal("MessageEnd should propagate hook errors")
	}
}

func TestIsTerminalFileNil(t *testing.T) {
	if isTerminalFile(nil) {
		t.Fatal("nil file must never be a terminal")
	}
}

func TestHookDecoratorNilReceiver(t *testing.T) {
	var decorator *HookDecorator
	if decorator.live() {
		t.Fatal("nil decorator should not be live")
	}
	if decorator.surface() != nil {
		t.Fatal("nil decorator should have no surface")
	}
}

func TestHookDecoratorToolResultFirst(t *testing.T) {
	surface := testHookSurface(t)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, NewTurnScope(surface, nil))
	ctx := context.Background()
	if _, err := decorator.ToolResult(ctx, "read", "lonely", nil, agent.TextResult("found")); err != nil {
		t.Fatalf("ToolResult: %v", err)
	}
	if text := plainSurfaceText(t, surface); !strings.Contains(text, "found") {
		t.Errorf("frame missing the orphan result:\n%s", text)
	}
}
