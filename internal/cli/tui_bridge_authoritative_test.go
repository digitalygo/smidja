package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

type partialNativeErrorClient struct {
	partial       string
	authoritative *agent.AssistantMessage
}

func (c *partialNativeErrorClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if onText != nil && c.partial != "" {
		onText(c.partial)
	}
	return c.authoritative, nil
}

type emptyAbortedClient struct{}

func (c *emptyAbortedClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		StopReason: "aborted",
	}, nil
}

type replaceMessageHook struct {
	agent.HookDispatcher
	replacement *agent.Message
}

func (h *replaceMessageHook) MessageEnd(ctx context.Context, m *agent.Message) (*agent.Message, error) {
	return h.replacement, nil
}

func errorAssistantWithUsage(text, stopReason, errorMessage string, input int64) *agent.AssistantMessage {
	msg := &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		Content:      []agent.ContentBlock{{Type: agent.BlockTypeText, Text: text}},
		StopReason:   stopReason,
		ErrorMessage: errorMessage,
		Usage:        agent.Usage{Input: input, Output: 5},
	}
	if text == "" {
		msg.Content = nil
	}
	return msg
}

func TestBridgeNativeErrorAfterPartialWithoutMessageEnd(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	authoritative := errorAssistantWithUsage("authoritative error text", "error", "provider exploded", 999)
	authoritative.Usage = agent.Usage{Input: 999, Output: 7}
	fixture.bridge.rd.client = &partialNativeErrorClient{
		partial:       "stale partial",
		authoritative: authoritative,
	}
	fixture.bridge.handle("trigger native error")
	text := bridgeFrameText(t, fixture)
	if strings.Contains(text, "stale partial") {
		t.Fatalf("stale streamed partial survived authoritative reconcile:\n%s", text)
	}
	for _, marker := range []string{"authoritative error text", "provider exploded"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
	if len(fixture.bridge.history) == 0 {
		t.Fatal("history must record the authoritative error turn")
	}
}

func TestBridgeEmptyAbortedNilErrorShowsNotice(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.client = &emptyAbortedClient{}
	fixture.bridge.handle("abort quietly")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "interrupted") {
		t.Fatalf("empty aborted turn must show an interruption notice:\n%s", text)
	}
	if strings.Contains(text, "Error:") {
		t.Fatalf("empty aborted must not render an error line:\n%s", text)
	}
}

func TestBridgeCurrentTurnNoAssistantAfterPrevious(t *testing.T) {
	first := textStop("first answer")
	first.Usage = agent.Usage{Input: 111, Output: 11}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{first}, nil)
	fixture.bridge.handle("first question")
	before := bridgeFrameText(t, fixture)
	if got := strings.Count(before, "first answer"); got != 1 {
		t.Fatalf("first answer rendered %d times, want 1:\n%s", got, before)
	}
	boundary := len(fixture.bridge.history)
	fixture.bridge.rd.client = &capturingClient{}
	fixture.bridge.handle("second question fails")
	text := bridgeFrameText(t, fixture)
	if got := strings.Count(text, "first answer"); got != 1 {
		t.Fatalf("previous assistant rendered %d times after failed turn, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, "script exhausted") {
		t.Fatalf("failed turn must show its own error, not reuse prior content:\n%s", text)
	}
	if asst, ok := authoritativeSince(fixture.bridge.history, boundary); ok {
		t.Fatalf("current turn should have no new assistant, got %+v", asst)
	}
	if _, _, ok := lastAssistantUsageSince(fixture.bridge.history, boundary); ok {
		t.Fatal("current turn usage must be absent when no assistant was produced")
	}
	if _, _, ok := lastAssistantUsageSince(fixture.bridge.history, 0); !ok {
		t.Fatal("full history must still contain the first usage")
	}
}

func TestBridgeMultiAssistantToolLoopUsesFinal(t *testing.T) {
	probeCalls := 0
	probe := &probeTool{calls: &probeCalls}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		toolUseWithText("call_1", "probe", `{"x":1}`, "first answer"),
		textStop("second answer"),
	}, []agent.Tool{probe})
	fixture.bridge.handle("loop through tools")
	text := bridgeFrameText(t, fixture)
	for _, marker := range []string{"first answer", "probe result", "second answer"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
	boundary := 0
	final, ok := authoritativeSince(fixture.bridge.history, boundary)
	if !ok || final.StopReason != "stop" {
		t.Fatalf("final authoritative = %+v %v, want stop", final, ok)
	}
	if len(final.Content) != 1 || final.Content[0].Text != "second answer" {
		t.Fatalf("final content = %+v, want second answer", final.Content)
	}
}

func TestBridgeHookReplacementUsesAuthoritative(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("alpha")}, nil)
	replacement := &agent.Message{Assistant: textStop("omega")}
	fixture.bridge.rd.hooks = &replaceMessageHook{
		HookDispatcher: fixture.bridge.rd.hooks,
		replacement:    replacement,
	}
	fixture.bridge.handle("replace me")
	text := bridgeFrameText(t, fixture)
	if strings.Contains(text, "alpha") {
		t.Fatalf("stale streamed text survived hook replacement:\n%s", text)
	}
	if got := strings.Count(text, "omega"); got != 1 {
		t.Fatalf("replacement rendered %d times, want 1:\n%s", got, text)
	}
}

func TestBridgeUsageBoundary(t *testing.T) {
	first := textStop("first answer")
	first.Usage = agent.Usage{Input: 111, Output: 11}
	second := textStop("second answer")
	second.Usage = agent.Usage{Input: 222, Output: 22}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{first, second}, nil)
	fixture.bridge.handle("first question")
	firstBoundary := 0
	if usage, _, ok := lastAssistantUsageSince(fixture.bridge.history, firstBoundary); !ok || usage.Input != 111 {
		t.Fatalf("first usage = %+v %v, want input 111", usage, ok)
	}
	secondBoundary := len(fixture.bridge.history)
	fixture.bridge.handle("second question")
	if usage, _, ok := lastAssistantUsageSince(fixture.bridge.history, secondBoundary); !ok || usage.Input != 222 {
		t.Fatalf("second usage = %+v %v, want input 222", usage, ok)
	}
	if usage, _, ok := lastAssistantUsageSince(fixture.bridge.history, 0); !ok || usage.Input != 222 {
		t.Fatalf("full history usage = %+v %v, want latest 222", usage, ok)
	}
	text := bridgeFrameText(t, fixture)
	for _, marker := range []string{"first answer", "second answer"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestBridgeNoDuplicateTextAfterFinalize(t *testing.T) {
	answer := textStop("exactly once")
	answer.Usage = agent.Usage{Input: 50, Output: 5}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{answer}, nil)
	fixture.bridge.handle("say it once")
	text := bridgeFrameText(t, fixture)
	if got := strings.Count(text, "exactly once"); got != 1 {
		t.Fatalf("answer rendered %d times, want 1:\n%s", got, text)
	}
}

func TestAuthoritativeSinceBoundaryAware(t *testing.T) {
	prev := &agent.Message{Assistant: textStop("previous")}
	prev.Assistant.Usage = agent.Usage{Input: 10}
	current := &agent.Message{Assistant: textStop("current")}
	current.Assistant.Usage = agent.Usage{Input: 20}
	history := []*agent.Message{prev, current}
	if asst, ok := authoritativeSince(history, 1); !ok || asst.Usage.Input != 20 {
		t.Fatalf("boundary 1 = %+v %v, want current", asst, ok)
	}
	if asst, ok := authoritativeSince(history, 0); !ok || asst.Usage.Input != 20 {
		t.Fatalf("boundary 0 = %+v %v, want latest current", asst, ok)
	}
	if _, ok := authoritativeSince(history, 2); ok {
		t.Fatal("boundary beyond history must report no assistant")
	}
	if _, ok := authoritativeSince(nil, 0); ok {
		t.Fatal("nil history must report no assistant")
	}
	if _, ok := authoritativeSince(history, -5); !ok {
		t.Fatal("negative boundary must clamp to zero")
	}
	withNil := []*agent.Message{nil, {User: &agent.UserMessage{Role: "user"}}, current}
	if asst, ok := authoritativeSince(withNil, 0); !ok || asst.Usage.Input != 20 {
		t.Fatalf("skipping nil/user = %+v %v", asst, ok)
	}
}

func TestLastAssistantUsageSinceBoundaryAware(t *testing.T) {
	prev := &agent.Message{Assistant: textStop("previous")}
	prev.Assistant.Usage = agent.Usage{Input: 10}
	prev.Assistant.StopReason = "stop"
	current := &agent.Message{Assistant: textStop("current")}
	current.Assistant.Usage = agent.Usage{Input: 20}
	current.Assistant.StopReason = "length"
	history := []*agent.Message{prev, current}
	usage, reason, ok := lastAssistantUsageSince(history, 1)
	if !ok || usage.Input != 20 || reason != "length" {
		t.Fatalf("boundary 1 = %+v %q %v, want 20 length", usage, reason, ok)
	}
	if _, _, ok := lastAssistantUsageSince(history, 2); ok {
		t.Fatal("boundary beyond history must report no usage")
	}
	if _, _, ok := lastAssistantUsageSince(nil, 0); ok {
		t.Fatal("nil history must report no usage")
	}
}
