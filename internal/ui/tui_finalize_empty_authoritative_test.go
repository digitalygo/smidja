package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func emptyErrorParts() []interactive.AssistantMessagePart {
	msg := &agent.Message{Assistant: &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		StopReason:   "error",
		ErrorMessage: "provider exploded",
	}}
	return AssistantParts(msg)
}

func emptyStopParts() []interactive.AssistantMessagePart {
	msg := &agent.Message{Assistant: &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		StopReason: "stop",
	}}
	return AssistantParts(msg)
}

func emptyAbortedParts() []interactive.AssistantMessagePart {
	msg := &agent.Message{Assistant: &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		StopReason: "aborted",
	}}
	return AssistantParts(msg)
}

func TestFinalizeAuthoritativeEmptyErrorClearsStreamedText(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	if _, err := scope.TextWriter().Write([]byte("stale streamed text")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	scope.FinalizeAuthoritative(true, emptyErrorParts(), "error", "provider exploded")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "stale streamed text") {
		t.Fatalf("stale streamed text survived empty authoritative reconcile:\n%s", text)
	}
	if got := strings.Count(text, "Error: provider exploded"); got != 1 {
		t.Fatalf("error rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeEmptyErrorClearsStreamedThinking(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.ThinkingCallback()("stale streamed thought")
	if _, err := scope.TextWriter().Write([]byte("stale streamed text")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	scope.FinalizeAuthoritative(true, emptyErrorParts(), "error", "provider exploded")
	surface.ToggleThinking()
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "stale streamed thought") {
		t.Fatalf("stale thinking survived empty authoritative reconcile:\n%s", text)
	}
	if strings.Contains(text, "stale streamed text") {
		t.Fatalf("stale text survived empty authoritative reconcile:\n%s", text)
	}
	if got := strings.Count(text, "Error: provider exploded"); got != 1 {
		t.Fatalf("error rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeEmptyErrorClearsThinkingOnly(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.ThinkingCallback()("only stale thought")
	scope.FinalizeAuthoritative(true, emptyErrorParts(), "error", "provider exploded")
	surface.ToggleThinking()
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "only stale thought") {
		t.Fatalf("stale thinking survived empty authoritative reconcile:\n%s", text)
	}
	if got := strings.Count(text, "Error: provider exploded"); got != 1 {
		t.Fatalf("error rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeExplicitEmptyStopCreatesNoBlock(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.FinalizeAuthoritative(true, emptyStopParts(), "stop", "")
	if scope.Opened() {
		t.Fatal("explicit empty stop without streaming must not open a block")
	}
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "Error:") || strings.Contains(text, "Operation aborted") {
		t.Fatalf("explicit empty stop added a stop line:\n%s", text)
	}
}

func TestFinalizeAuthoritativeExplicitEmptyAbortedCreatesNoBlock(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.FinalizeAuthoritative(true, emptyAbortedParts(), "aborted", "")
	if scope.Opened() {
		t.Fatal("explicit empty aborted without streaming must not open a block")
	}
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "Operation aborted") {
		t.Fatalf("explicit empty aborted should defer to bridge notice:\n%s", text)
	}
}

func TestFinalizeAuthoritativeExplicitEmptyAfterStreamedStopClearsOwned(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	if _, err := scope.TextWriter().Write([]byte("stale owned text")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	scope.FinalizeAuthoritative(true, emptyStopParts(), "stop", "")
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "stale owned text") {
		t.Fatalf("stale owned text survived explicit empty stop:\n%s", text)
	}
	if got := strings.Count(text, "stale owned text"); got != 0 {
		t.Fatalf("stale text count = %d, want 0:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeNoAuthoritativeAfterPriorTurnPreserves(t *testing.T) {
	surface := testHookSurface(t)
	first := NewTurnScope(surface, nil)
	if _, err := first.TextWriter().Write([]byte("prior answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	decorator := NewHookDecorator(&fakeHookDispatcher{}, first)
	if _, err := decorator.MessageEnd(context.Background(), assistantTextMessage("stop", "prior answer")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	first.FinalizeAuthoritative(true, authoritativeTextParts("prior answer"), "stop", "")
	before := plainSurfaceText(t, surface)
	if got := strings.Count(before, "prior answer"); got != 1 {
		t.Fatalf("prior answer rendered %d times, want 1:\n%s", got, before)
	}
	second := NewTurnScope(surface, nil)
	second.FinalizeAuthoritative(false, nil, "error", "boom")
	after := plainSurfaceText(t, surface)
	if got := strings.Count(after, "prior answer"); got != 1 {
		t.Fatalf("prior answer rendered %d times after no-authoritative turn, want 1:\n%s", got, after)
	}
	if strings.Contains(after, "stale") {
		t.Fatalf("unexpected stale content after no-authoritative turn:\n%s", after)
	}
	if second.Opened() {
		t.Fatal("no-authoritative turn must not open a block")
	}
}

func TestFinalizeAuthoritativeNoDuplicateEmptyBlocks(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	scope.FinalizeAuthoritative(true, emptyStopParts(), "stop", "")
	before := plainSurfaceText(t, surface)
	scope2 := NewTurnScope(surface, nil)
	scope2.FinalizeAuthoritative(false, nil, "stop", "")
	after := plainSurfaceText(t, surface)
	if before != after {
		t.Fatalf("empty finalizations changed the frame:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if scope.Opened() || scope2.Opened() {
		t.Fatal("empty turns must not open blocks")
	}
}

func TestFinalizeAuthoritativeEmptyAfterReconciledClears(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("kept answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "kept answer")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.FinalizeAuthoritative(true, emptyStopParts(), "stop", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "kept answer"); got != 0 {
		t.Fatalf("reconciled answer rendered %d times, want 0:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeEmptyAfterReconciledClearsThinking(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	scope.ThinkingCallback()("reconciled thought")
	if _, err := scope.TextWriter().Write([]byte("reconciled answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	authoritative := &agent.Message{Assistant: &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "reconciled thought"},
			{Type: agent.BlockTypeText, Text: "reconciled answer"},
		},
		StopReason: "stop",
	}}
	if _, err := decorator.MessageEnd(context.Background(), authoritative); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.FinalizeAuthoritative(true, emptyStopParts(), "stop", "")
	surface.ToggleThinking()
	text := plainSurfaceText(t, surface)
	if strings.Contains(text, "reconciled thought") {
		t.Fatalf("reconciled thinking survived explicit empty final:\n%s", text)
	}
	if got := strings.Count(text, "reconciled answer"); got != 0 {
		t.Fatalf("reconciled answer rendered %d times, want 0:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeAbsentAfterReconciledPreserves(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("kept answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "kept answer")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.FinalizeAuthoritative(false, nil, "stop", "")
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "kept answer"); got != 1 {
		t.Fatalf("reconciled answer rendered %d times, want 1:\n%s", got, text)
	}
}

func TestFinalizeAuthoritativeEmptyAfterReconciledIdempotent(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	ctx := context.Background()
	if _, err := scope.TextWriter().Write([]byte("kept answer")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := decorator.MessageEnd(ctx, assistantTextMessage("stop", "kept answer")); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	scope.FinalizeAuthoritative(true, emptyStopParts(), "stop", "")
	before := plainSurfaceText(t, surface)
	if got := strings.Count(before, "kept answer"); got != 0 {
		t.Fatalf("reconciled answer rendered %d times, want 0:\n%s", got, before)
	}
	scope.FinalizeAuthoritative(true, emptyStopParts(), "stop", "")
	after := plainSurfaceText(t, surface)
	if before != after {
		t.Fatalf("idempotent empty final changed the frame:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := strings.Count(after, "kept answer"); got != 0 {
		t.Fatalf("reconciled answer rendered %d times, want 0:\n%s", got, after)
	}
}

func TestFinalizeAuthoritativeToolUseEmptyPreservesToolOnly(t *testing.T) {
	surface := testHookSurface(t)
	scope := NewTurnScope(surface, nil)
	decorator := NewHookDecorator(&fakeHookDispatcher{}, scope)
	if _, err := decorator.MessageEnd(context.Background(), assistantToolMessage("toolUse", "", "call_1", "probe", `{}`)); err != nil {
		t.Fatalf("MessageEnd: %v", err)
	}
	runProbeTool(t, decorator, "call_1", "probe output")
	emptyTool := &agent.Message{Assistant: &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		StopReason: "toolUse",
	}}
	scope.FinalizeAuthoritative(true, AssistantParts(emptyTool), "toolUse", "")
	if scope.Opened() {
		t.Fatal("explicit empty toolUse must not open a block")
	}
	text := plainSurfaceText(t, surface)
	if got := strings.Count(text, "probe output"); got != 1 {
		t.Fatalf("tool output rendered %d times, want 1:\n%s", got, text)
	}
}
