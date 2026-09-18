package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func authoritativeTextParts(text string) []interactive.AssistantMessagePart {
	return AssistantParts(assistantTextMessage("stop", text))
}

func TestFinalizeAuthoritativeNativeErrorAfterPartial(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	if _, err := scope.TextWriter().Write([]byte("stale partial")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	authoritative := &agent.Message{Assistant: &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		Content:      []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "authoritative error text"}},
		StopReason:   "error",
		ErrorMessage: "provider exploded",
	}}
	scope.FinalizeAuthoritative(true, AssistantParts(authoritative), "error", "provider exploded")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "stale partial") {
		t.Fatalf("stale streamed text survived authoritative reconcile:\n%s", text)
	}
	for _, marker := range []string{"authoritative error text", "Error: provider exploded"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestFinalizeAuthoritativeEmptyAbortedWithoutBlock(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.FinalizeAuthoritative(false, nil, "aborted", "")
	if scope.Opened() {
		t.Fatal("empty aborted without content must not open a block")
	}
	if text := plainSurfaceText(t, surface); strings.Contains(text, "Operation aborted") {
		t.Fatalf("empty aborted should defer to bridge notice:\n%s", text)
	}
}

func TestFinalizeAuthoritativeEmptyAbortedAfterTool(t *testing.T) {
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
	scope.FinalizeAuthoritative(false, nil, "aborted", "")
	text := plainSurfaceText(t, surface)
	for _, marker := range []string{"step one", "probe output", "Operation aborted"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestFinalizeAuthoritativeEmptyErrorWithoutBlock(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.FinalizeAuthoritative(false, nil, "error", "boom")
	if scope.Opened() {
		t.Fatal("empty error without prior content must not open a block")
	}
}

func TestFinalizeAuthoritativeEmptyErrorAfterTool(t *testing.T) {
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
	authoritative := &agent.Message{Assistant: &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		StopReason:   "error",
		ErrorMessage: "provider exploded",
	}}
	scope.FinalizeAuthoritative(true, AssistantParts(authoritative), "error", "provider exploded")
	text := plainSurfaceText(t, surface)
	for _, marker := range []string{"step one", "probe output", "Error: provider exploded"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestFinalizeAuthoritativeMultiAssistantToolLoop(t *testing.T) {
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
	if _, err := scope.TextWriter().Write([]byte("stale second")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	final := assistantTextMessage("stop", "second authoritative")
	scope.FinalizeAuthoritative(true, AssistantParts(final), "stop", "")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "stale second") {
		t.Fatalf("stale second-attempt text survived:\n%s", text)
	}
	first := strings.Index(text, "first answer")
	tool := strings.Index(text, "probe output")
	second := strings.Index(text, "second authoritative")
	if first < 0 || tool < 0 || second < 0 {
		t.Fatalf("frame missing a block marker (%d, %d, %d):\n%s", first, tool, second, text)
	}
	if first > tool || tool > second {
		t.Fatalf("blocks out of order: first=%d tool=%d second=%d\n%s", first, tool, second, text)
	}
	for _, marker := range []string{"first answer", "probe output", "second authoritative"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestFinalizeAuthoritativeHookReplacement(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	if _, err := scope.TextWriter().Write([]byte("alpha")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	replacement := assistantTextMessage("stop", "omega")
	scope.FinalizeAuthoritative(true, AssistantParts(replacement), "stop", "")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "alpha") {
		t.Fatalf("stale streamed text survived replacement:\n%s", text)
	}
	if got := strings.Count(text, "omega"); got != 1 {
		t.Fatalf("replacement rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeNoDuplicateWhenAlreadyReconciled(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("same text")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "same text")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.FinalizeAuthoritative(true, authoritativeTextParts("same text"), "stop", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "same text"); got != 1 {
		t.Fatalf("text rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeToolOnlyRemainsEmpty(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	if _, err := decorator.MessageEnd(context.Background(), assistantToolMessage("toolUse", "", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	scope.FinalizeAuthoritative(false, nil, "toolUse", "")
	if scope.Opened() {
		t.Fatal("tool-only finalize must not open a block")
	}
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "probe output"); got != 1 {
		t.Fatalf("tool output rendered %d times, want 1:\n%s", got, text)
	}
	if strings.Contains(text, "Error:") || strings.Contains(text, "Operation aborted") {
		t.Fatalf("tool-only finalize added a stop line:\n%s", text)
	}
}

func TestFinalizeAuthoritativeToolUseWithTextDoesNotDuplicate(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("tool text")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	msg := assistantToolMessage("toolUse", "tool text", "call_7", "probe", `{}`)
	if _, err := decorator.MessageEnd(ctx, msg); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.FinalizeAuthoritative(true, AssistantParts(msg), "toolUse", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "tool text"); got != 1 {
		t.Fatalf("tool text rendered %d times, want 1:\n%s", got, text)
	}
	if scope.Opened() && strings.Contains(text, "Error:") {
		t.Fatalf("toolUse finalize added an error line:\n%s", text)
	}
}

func TestFinalizeAuthoritativeThinkingPreserved(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.ThinkingCallback()("stale thought")
	final := &agent.Message{Assistant: &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "authoritative thought"},
			{Type: agent.BlockTypeText, Text: "authoritative answer"},
		},
		StopReason: "stop",
	}}
	scope.FinalizeAuthoritative(true, AssistantParts(final), "stop", "")
	surface.ToggleThinking()
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "stale thought") {
		t.Fatalf("stale thought survived:\n%s", text)
	}
	if !strings.Contains(text, "authoritative thought") || !strings.Contains(text, "authoritative answer") {
		t.Fatalf("authoritative thinking or text missing:\n%s", text)
	}
	if got := strings.Count(text, "authoritative answer"); got != 1 {
		t.Fatalf("answer rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeLengthWithoutStreaming(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	final := assistantTextMessage("length", "cut off here")
	scope.FinalizeAuthoritative(true, AssistantParts(final), "length", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "cut off here"); got != 1 {
		t.Fatalf("truncated text rendered %d times, want 1:\n%s", got, text)
	}
	if got := strings.Count(text, "Response was truncated before completion."); got != 1 {
		t.Fatalf("truncation notice rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeCreatesBlockWithoutStreaming(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.FinalizeAuthoritative(true, authoritativeTextParts("authoritative only"), "stop", "")
	if !scope.Opened() {
		t.Fatal("authoritative content must open a block even without streaming")
	}
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "authoritative only"); got != 1 {
		t.Fatalf("authoritative content rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeIsIdempotent(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.FinalizeAuthoritative(true, authoritativeTextParts("keep me"), "stop", "")
	scope.FinalizeAuthoritative(true, authoritativeTextParts("keep me"), "error", "late error")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "keep me"); got != 1 {
		t.Fatalf("text rendered %d times, want 1:\n%s", got, text)
	}
	if strings.Contains(text, "Error:") {
		t.Fatalf("late finalize replaced the stop reason:\n%s", text)
	}
}

func TestFinalizeAuthoritativeNilGuards(t *testing.T) {
	var scope *TurnScope
	scope.FinalizeAuthoritative(false, nil, "stop", "")
	scope.FinalizeAuthoritative(true, []interactive.AssistantMessagePart{{Text: "x"}}, "stop", "")
	if scope.Opened() {
		t.Fatal("nil scope must not report a block")
	}
	inactive := NewTurnScope(testHookSurface(t), func() bool { return false })
	inactive.FinalizeAuthoritative(true, authoritativeTextParts("hidden"), "stop", "")
	if inactive.Opened() {
		t.Fatal("inactive scope must not open a block")
	}
	nilSurface := NewTurnScope(nil, nil)
	nilSurface.FinalizeAuthoritative(true, authoritativeTextParts("hidden"), "stop", "")
	if nilSurface.Opened() {
		t.Fatal("nil surface must never open a block")
	}
}

func TestAssistantPartsExportedMatchesInternal(t *testing.T) {
	msg := assistantToolMessage("toolUse", "hello", "call_1", "probe", `{"x":1}`)
	got := AssistantParts(msg)
	want := assistantParts(msg)
	if len(got) != len(want) {
		t.Fatalf("AssistantParts length = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("part %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if parts := AssistantParts(nil); parts != nil {
		t.Fatalf("AssistantParts(nil) = %#v, want nil", parts)
	}
	empty := &agent.Message{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), StopReason: "stop"}}
	if parts := AssistantParts(empty); len(parts) != 0 {
		t.Fatalf("empty assistant parts = %#v, want empty", parts)
	}
	toolOnly := assistantToolMessage("toolUse", "", "call_1", "probe", `{}`)
	if parts := AssistantParts(toolOnly); len(parts) != 0 {
		t.Fatalf("tool-only parts = %#v, want empty", parts)
	}
}
