package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

type thinkingEmptyNativeErrorClient struct {
	thinking      string
	partial       string
	authoritative *agent.AssistantMessage
}

func (c *thinkingEmptyNativeErrorClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if onThinking != nil && c.thinking != "" {
		onThinking(c.thinking)
	}
	if onText != nil && c.partial != "" {
		onText(c.partial)
	}
	return c.authoritative, nil
}

func emptyNativeErrorAssistant(message string) *agent.AssistantMessage {
	return &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		StopReason:   "error",
		ErrorMessage: message,
	}
}

func emptyStopAssistant() *agent.AssistantMessage {
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		StopReason: "stop",
	}
}

func TestBridgeEmptyNativeErrorClearsStreamedText(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.client = &partialNativeErrorClient{
		partial:       "stale partial text",
		authoritative: emptyNativeErrorAssistant("provider exploded"),
	}
	fixture.bridge.handle("trigger empty native error")
	text := bridgeFrameText(t, fixture)
	if strings.Contains(text, "stale partial text") {
		t.Fatalf("stale streamed text survived empty authoritative reconcile:\n%s", text)
	}
	if got := strings.Count(text, "provider exploded"); got != 1 {
		t.Fatalf("error rendered %d times, want 1:\n%s", got, text)
	}
}

func TestBridgeEmptyNativeErrorClearsStreamedThinking(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.client = &thinkingEmptyNativeErrorClient{
		thinking:      "stale streamed thought",
		authoritative: emptyNativeErrorAssistant("provider exploded"),
	}
	fixture.bridge.handle("trigger thinking empty error")
	text := bridgeFrameText(t, fixture)
	if strings.Contains(text, "stale streamed thought") {
		t.Fatalf("stale thinking survived empty authoritative reconcile:\n%s", text)
	}
	if got := strings.Count(text, "provider exploded"); got != 1 {
		t.Fatalf("error rendered %d times, want 1:\n%s", got, text)
	}
}

func TestBridgeEmptyNativeErrorClearsTextAndThinking(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.client = &thinkingEmptyNativeErrorClient{
		thinking:      "stale thought body",
		partial:       "stale text body",
		authoritative: emptyNativeErrorAssistant("provider exploded"),
	}
	fixture.bridge.handle("trigger combined empty error")
	text := bridgeFrameText(t, fixture)
	if strings.Contains(text, "stale thought body") {
		t.Fatalf("stale thought survived:\n%s", text)
	}
	if strings.Contains(text, "stale text body") {
		t.Fatalf("stale text survived:\n%s", text)
	}
	if got := strings.Count(text, "provider exploded"); got != 1 {
		t.Fatalf("error rendered %d times, want 1:\n%s", got, text)
	}
}

func TestBridgeExplicitEmptyStopCreatesNoDuplicate(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{emptyStopAssistant()}, nil)
	fixture.bridge.handle("explicit empty stop")
	text := bridgeFrameText(t, fixture)
	if strings.Contains(text, "Error:") || strings.Contains(text, "Operation aborted") {
		t.Fatalf("explicit empty stop added a stop line:\n%s", text)
	}
	before := text
	fixture.bridge.rd.client = &capturingClient{script: []*agent.AssistantMessage{emptyStopAssistant()}}
	fixture.bridge.handle("second empty stop")
	after := bridgeFrameText(t, fixture)
	if strings.Contains(after, "Error:") {
		t.Fatalf("second empty stop added an error:\n%s", after)
	}
	if got := strings.Count(after, "second empty stop"); got != 1 {
		t.Fatalf("second prompt rendered %d times, want 1:\n%s", got, after)
	}
	_ = before
}

func TestBridgeExplicitEmptyAbortedAfterPriorPreserves(t *testing.T) {
	first := textStop("prior answer")
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{first}, nil)
	fixture.bridge.handle("first question")
	before := bridgeFrameText(t, fixture)
	if got := strings.Count(before, "prior answer"); got != 1 {
		t.Fatalf("prior answer rendered %d times, want 1:\n%s", got, before)
	}
	fixture.bridge.rd.client = &emptyAbortedClient{}
	fixture.bridge.handle("abort explicitly")
	text := bridgeFrameText(t, fixture)
	if got := strings.Count(text, "prior answer"); got != 1 {
		t.Fatalf("prior answer rendered %d times after explicit empty abort, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, "interrupted") {
		t.Fatalf("explicit empty abort must show interruption notice:\n%s", text)
	}
	if strings.Contains(text, "Error:") {
		t.Fatalf("explicit empty abort must not render error:\n%s", text)
	}
}

func TestBridgeNoAuthoritativeAfterPriorPreserves(t *testing.T) {
	first := textStop("kept answer")
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{first}, nil)
	fixture.bridge.handle("first question")
	before := bridgeFrameText(t, fixture)
	if got := strings.Count(before, "kept answer"); got != 1 {
		t.Fatalf("kept answer rendered %d times, want 1:\n%s", got, before)
	}
	boundary := len(fixture.bridge.history)
	fixture.bridge.rd.client = &capturingClient{}
	fixture.bridge.handle("failing turn")
	text := bridgeFrameText(t, fixture)
	if got := strings.Count(text, "kept answer"); got != 1 {
		t.Fatalf("kept answer rendered %d times after no-authoritative turn, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, "script exhausted") {
		t.Fatalf("failed turn must show its own error:\n%s", text)
	}
	if asst, ok := authoritativeSince(fixture.bridge.history, boundary); ok {
		t.Fatalf("current turn should have no assistant, got %+v", asst)
	}
}

func TestBridgeNoDuplicateEmptyBlocksAcrossTurns(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{emptyStopAssistant()}, nil)
	fixture.bridge.handle("first empty")
	first := bridgeFrameText(t, fixture)
	fixture.bridge.rd.client = &capturingClient{script: []*agent.AssistantMessage{emptyStopAssistant()}}
	fixture.bridge.handle("second empty")
	second := bridgeFrameText(t, fixture)
	if strings.Contains(second, "Error:") || strings.Contains(second, "Operation aborted") {
		t.Fatalf("empty turns added a stop line:\n%s", second)
	}
	if !strings.Contains(second, "first empty") || !strings.Contains(second, "second empty") {
		t.Fatalf("prompts missing after empty turns:\n%s", second)
	}
	if len(fixture.bridge.history) != 4 {
		t.Fatalf("history length = %d, want 4 (two user plus two empty assistants)", len(fixture.bridge.history))
	}
	_ = first
}
