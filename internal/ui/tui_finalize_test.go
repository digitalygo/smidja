package ui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func assistantTextMessage(stopReason, text string) *agent.Message {
	return &agent.Message{Assistant: &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: text}},
		StopReason: stopReason,
	}}
}

func assistantToolMessage(stopReason, text, callID, name, args string) *agent.Message {
	content := make([]agent.ContentBlock, 0, 2)
	if text != "" {
		content = append(content, agent.ContentBlock{Type: agent.BlockTypeText, Text: text})
	}
	content = append(content, agent.ContentBlock{Type: agent.BlockTypeToolCall, ID: callID, Name: name, Arguments: json.RawMessage(args)})
	return &agent.Message{Assistant: &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    content,
		StopReason: stopReason,
	}}
}

func runProbeTool(t *testing.T, decorator *HookDecorator, callID string, result string) {
	t.Helper()
	ctx := context.Background()
	if _, err := decorator.ToolCall(ctx, "probe", callID, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("ToolCall(%s): %v", callID, err)
	}
	if _, err := decorator.ToolResult(ctx, "probe", callID, nil, agent.TextResult(result)); err != nil {
		t.Fatalf("ToolResult(%s): %v", callID, err)
	}
}

func TestTurnScopeNormalStreamingRendersOnce(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("normal streamed answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "normal streamed answer")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "normal streamed answer"); got != 1 {
		t.Fatalf("streamed answer rendered %d times, want 1:\n%s", got, text)
	}
	for _, unwanted := range []string{"Error:", "truncated"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("normal stop rendered %q:\n%s", unwanted, text)
		}
	}
}

func TestTurnScopeToolOnlyResponseAddsNoAssistantBlock(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	if _, err := decorator.MessageEnd(context.Background(), assistantToolMessage("toolUse", "", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	scope.Finish("toolUse", "")
	if scope.Opened() {
		t.Fatal("tool-only response must not open an assistant block")
	}
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "probe output"); got != 1 {
		t.Fatalf("tool output rendered %d times, want 1:\n%s", got, text)
	}
	if strings.Contains(text, "Error:") || strings.Contains(text, "truncated") {
		t.Fatalf("tool-only finalization added a stop line:\n%s", text)
	}
}

func TestTurnScopeAssistantToolAssistantKeepsBlocksSeparate(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("first answer")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantToolMessage("toolUse", "first answer", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("first MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	if _, err := scope.TextWriter().Write([]byte("second answer")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "second answer")); err != nil {
		t.Fatalf("second MessageEnd: %v", err)
	}
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	first := strings.Index(text, "first answer")
	tool := strings.Index(text, "probe output")
	second := strings.Index(text, "second answer")
	if first < 0 || tool < 0 || second < 0 {
		t.Fatalf("frame missing a block marker (%d, %d, %d):\n%s", first, tool, second, text)
	}
	if first > tool || tool > second {
		t.Fatalf("blocks out of order: first=%d tool=%d second=%d\n%s", first, tool, second, text)
	}
	if strings.Contains(text, "first answersecond answer") {
		t.Fatalf("post-tool text merged into the pre-tool block:\n%s", text)
	}
	for _, marker := range []string{"first answer", "probe output", "second answer"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestTurnScopeLengthStopSurvivesMessageEnd(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	if _, err := scope.TextWriter().Write([]byte("partial answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(context.Background(), assistantTextMessage("length", "partial answer")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.Finish("length", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "partial answer"); got != 1 {
		t.Fatalf("partial answer rendered %d times, want 1:\n%s", got, text)
	}
	if got := strings.Count(text, "Response was truncated before completion."); got != 1 {
		t.Fatalf("truncation notice rendered %d times, want 1:\n%s", got, text)
	}
}

func TestTurnScopeProviderErrorAfterMessageEndIsVisible(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("step one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantToolMessage("toolUse", "step one", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	scope.Finish("error", "provider exploded")
	text := plainSurfaceText(t, surface)
	for _, marker := range []string{"step one", "probe output", "Error: provider exploded"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestTurnScopeHookReplacementShorterThanStreamed(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	next := &fakeHookDispatcher{messageOut: assistantTextMessage("stop", "short")}
	decorator := NewHookDecorator(next, scope)
	if _, err := scope.TextWriter().Write([]byte("the full streamed answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := decorator.MessageEnd(context.Background(), assistantTextMessage("stop", "the full streamed answer"))
	if err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	if got == nil || got.Assistant == nil || got.Assistant.Content[0].Text != "short" {
		t.Fatalf("replacement not preserved: %+v", got)
	}
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "full streamed answer") {
		t.Fatalf("stale streamed text survived the replacement:\n%s", text)
	}
	if got := strings.Count(text, "short"); got != 1 {
		t.Fatalf("replacement rendered %d times, want 1:\n%s", got, text)
	}
	next.mu.Lock()
	calls := next.messageCalls
	next.mu.Unlock()
	if calls != 1 {
		t.Fatalf("forwarded MessageEnd calls = %d, want 1", calls)
	}
}

func TestTurnScopeHookReplacementLongerThanStreamed(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	next := &fakeHookDispatcher{messageOut: assistantTextMessage("stop", "a much longer replacement answer")}
	decorator := NewHookDecorator(next, scope)
	if _, err := scope.TextWriter().Write([]byte("short")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(context.Background(), assistantTextMessage("stop", "short")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "short") {
		t.Fatalf("stale streamed text survived the replacement:\n%s", text)
	}
	if got := strings.Count(text, "a much longer replacement answer"); got != 1 {
		t.Fatalf("replacement rendered %d times, want 1:\n%s", got, text)
	}
}

func TestTurnScopeHookReplacementDifferentFromStreamed(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	next := &fakeHookDispatcher{messageOut: assistantTextMessage("stop", "omega")}
	decorator := NewHookDecorator(next, scope)
	if _, err := scope.TextWriter().Write([]byte("alpha")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(context.Background(), assistantTextMessage("stop", "alpha")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "alpha") {
		t.Fatalf("stale streamed text survived the replacement:\n%s", text)
	}
	if got := strings.Count(text, "omega"); got != 1 {
		t.Fatalf("replacement rendered %d times, want 1:\n%s", got, text)
	}
}

func TestTurnScopeMessageEndCreatesBlockWithoutStreaming(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	if _, err := decorator.MessageEnd(context.Background(), assistantTextMessage("stop", "authoritative only")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	if !scope.Opened() {
		t.Fatal("authoritative content must open a block even without streaming")
	}
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "authoritative only"); got != 1 {
		t.Fatalf("authoritative content rendered %d times, want 1:\n%s", got, text)
	}
}

func TestTurnScopeMessageEndReconcilesThinking(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	if _, err := scope.TextWriter().Write([]byte("streamed thinking body")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	authoritative := &agent.Message{Assistant: &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "authoritative thought"},
			{Type: agent.BlockTypeText, Text: "authoritative answer"},
		},
		StopReason: "stop",
	}}
	if _, err := decorator.MessageEnd(context.Background(), authoritative); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	surface.ToggleThinking()
	scope.Finish("stop", "")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "streamed thinking body") {
		t.Fatalf("stale streamed text survived the thinking reconcile:\n%s", text)
	}
	if !strings.Contains(text, "authoritative thought") || !strings.Contains(text, "authoritative answer") {
		t.Fatalf("reconciled thinking or text missing:\n%s", text)
	}
}

func TestTurnScopeAbortWithoutBlockStaysEmpty(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.Finish("aborted", "")
	if scope.Opened() {
		t.Fatal("abort without streamed content must not open an assistant block")
	}
	if text := plainSurfaceText(t, surface); strings.Contains(text, "Operation aborted") {
		t.Fatalf("abort without a block should defer to the bridge notice:\n%s", text)
	}
}

func TestTurnScopeLengthStopAfterToolRound(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("step one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantToolMessage("toolUse", "step one", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	scope.Finish("length", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "Response was truncated before completion."); got != 1 {
		t.Fatalf("truncation notice rendered %d times, want 1:\n%s", got, text)
	}
	if got := strings.Count(text, "step one"); got != 1 {
		t.Fatalf("pre-tool text rendered %d times, want 1:\n%s", got, text)
	}
}

func TestTurnScopeNilGuards(t *testing.T) {
	var scope *TurnScope
	scope.toolBoundary()
	scope.reconcileMessage([]interactive.AssistantMessagePart{{Text: "x"}}, "stop")
	scope.endAssistant("error", "boom")
	if scope.Opened() {
		t.Fatal("nil scope must not report an opened block")
	}
	if parts := assistantParts(nil); parts != nil {
		t.Fatalf("assistantParts(nil) = %#v, want nil", parts)
	}
	if parts := assistantParts(&agent.Message{}); parts != nil {
		t.Fatalf("assistantParts without an assistant = %#v, want nil", parts)
	}
}

func TestTurnScopeAbortAfterMessageEndIsVisible(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("step one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantToolMessage("toolUse", "step one", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	scope.Finish("aborted", "")
	text := plainSurfaceText(t, surface)
	for _, marker := range []string{"step one", "probe output", "Operation aborted"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestTurnScopeEmptyFinalMessageAddsNoBlock(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("step one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantToolMessage("toolUse", "step one", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	before := plainSurfaceText(t, surface)
	empty := &agent.Message{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), StopReason: "stop"}}
	if _, err := decorator.MessageEnd(ctx, empty); err != nil {
		t.Fatalf("empty MessageEnd: %v", err)
	}
	scope.Finish("stop", "")
	after := plainSurfaceText(t, surface)
	if before != after {
		t.Fatalf("empty final message changed the frame:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := strings.Count(after, "step one"); got != 1 {
		t.Fatalf("pre-tool text rendered %d times, want 1:\n%s", got, after)
	}
}

func TestTurnScopeFinalizationIsIdempotent(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("keep me")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "keep me")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.Finish("stop", "")
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "keep me")); err != nil {
		t.Fatalf("late MessageEnd: %v", err)
	}
	scope.Finish("error", "late error")
	if _, err := decorator.ToolResult(ctx, "probe", "late", nil, agent.TextResult("late output")); err != nil {
		t.Fatalf("late ToolResult: %v", err)
	}
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "keep me"); got != 1 {
		t.Fatalf("final text rendered %d times, want 1:\n%s", got, text)
	}
	if strings.Contains(text, "Error:") {
		t.Fatalf("late finish replaced the stop reason:\n%s", text)
	}
}
