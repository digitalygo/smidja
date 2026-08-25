package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeClient is a scripted agent.Client: each StreamTurn call returns the
// next scripted assistant message and delivers its text and thinking
// blocks to the callbacks it receives, so the loop's streaming forwarding
// is exercised end to end. When failFirst is positive, the first failFirst
// calls fail with a transient error before the script is consumed.
type fakeClient struct {
	mu         sync.Mutex
	script     []*AssistantMessage
	calls      int // successful scripted calls only
	attempts   int // every StreamTurn invocation, including failures
	err        error
	failFirst  int
	lastReq    *TurnRequest
	onText     func(string)
	onThinking func(string)
}

func (f *fakeClient) StreamTurn(ctx context.Context, req *TurnRequest, onText func(string), onThinking func(string)) (*AssistantMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	f.lastReq = req
	f.onText = onText
	f.onThinking = onThinking
	if f.failFirst > 0 {
		f.failFirst--
		return nil, errors.New("fakeClient: transient failure")
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.calls >= len(f.script) {
		return nil, errors.New("fakeClient: script exhausted")
	}
	m := f.script[f.calls]
	f.calls++
	for _, b := range m.Content {
		switch b.Type {
		case BlockTypeText:
			if onText != nil {
				onText(b.Text)
			}
		case BlockTypeThinking:
			if onThinking != nil {
				onThinking(b.Thinking)
			}
		}
	}
	return m, nil
}

// fakeRecorder records the events and messages it receives, in order.
type fakeRecorder struct {
	mu          sync.Mutex
	events      []string
	users       []*UserMessage
	assistants  []*AssistantMessage
	toolResults []*ToolResultMessage
}

func (r *fakeRecorder) AppendUser(m *UserMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "user")
	r.users = append(r.users, m)
	return nil
}

func (r *fakeRecorder) AppendAssistant(m *AssistantMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "assistant")
	r.assistants = append(r.assistants, m)
	return nil
}

func (r *fakeRecorder) AppendToolResult(m *ToolResultMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "toolResult")
	r.toolResults = append(r.toolResults, m)
	return nil
}

func (r *fakeRecorder) joinEvents() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.events, ",")
}

// copyingRecorder wraps fakeRecorder but deep-copies every assistant
// message at AppendAssistant time (a JSON round-trip, the same way the
// session package persists entries), so mutations to the in-memory
// message after the call cannot leak into the captured record. Tests
// that assert the durable record's contents use this recorder, because
// the aliasing fakeRecorder would hide a record-after-mutation bug.
type copyingRecorder struct {
	*fakeRecorder
}

// AppendAssistant deep-copies the message before recording it, mirroring
// the session's marshal-at-append semantics.
func (r *copyingRecorder) AppendAssistant(m *AssistantMessage) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var copy AssistantMessage
	if err := json.Unmarshal(payload, &copy); err != nil {
		return err
	}
	return r.fakeRecorder.AppendAssistant(&copy)
}

// fakeTool is a scripted agent.Tool that counts its executions and
// records the raw arguments of every call.
type fakeTool struct {
	mu     sync.Mutex
	name   string
	result Result
	calls  int
	args   []json.RawMessage
}

func (t *fakeTool) Name() string            { return t.name }
func (t *fakeTool) Description() string     { return "fake tool" }
func (t *fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *fakeTool) Exec(ctx context.Context, args json.RawMessage) Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	t.args = append(t.args, append(json.RawMessage(nil), args...))
	return t.result
}

func (t *fakeTool) executed() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// lastArgs returns the raw arguments of the most recent execution, or
// nil when the tool never executed.
func (t *fakeTool) lastArgs() json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.args) == 0 {
		return nil
	}
	return t.args[len(t.args)-1]
}

// allArgs returns the raw arguments of every execution, in execution
// order.
func (t *fakeTool) allArgs() []json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]json.RawMessage(nil), t.args...)
}

// textStop builds a scripted assistant message that stops with text.
func textStop(text string) *AssistantMessage {
	return &AssistantMessage{
		Role:       string(RoleAssistant),
		Content:    []ContentBlock{{Type: BlockTypeText, Text: text}},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "test/model",
		StopReason: "stop",
		Timestamp:  1,
	}
}

// toolUseMsg builds a scripted assistant message that stops requesting
// tool calls, with the given content blocks.
func toolUseMsg(blocks ...ContentBlock) *AssistantMessage {
	return &AssistantMessage{
		Role:       string(RoleAssistant),
		Content:    blocks,
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "test/model",
		StopReason: "toolUse",
		Timestamp:  1,
	}
}

// toolCallBlock builds a toolCall content block.
func toolCallBlock(id, name, args string) ContentBlock {
	return ContentBlock{Type: BlockTypeToolCall, ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func TestRunTurnHappyTextTurn(t *testing.T) {
	var stdout bytes.Buffer
	rec := &fakeRecorder{}
	client := &fakeClient{script: []*AssistantMessage{textStop("hello there")}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Stdout:   &stdout,
	}, "test/model", "be terse", nil, "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if len(history) != 2 {
		t.Fatalf("history = %d messages, want 2 (user, assistant)", len(history))
	}
	if got := history[0].User; got == nil || string(got.Content) != `"hi"` {
		t.Errorf("history[0] = %+v, want user message with JSON string content %q", history[0], `"hi"`)
	}
	if got := history[1].Assistant; got == nil || got.StopReason != "stop" || len(got.Content) != 1 || got.Content[0].Text != "hello there" {
		t.Errorf("history[1] = %+v, want the scripted assistant message", history[1])
	}
	if got := stdout.String(); got != "hello there" {
		t.Errorf("stdout = %q, want the streamed text", got)
	}
	if got := rec.joinEvents(); got != "user,assistant" {
		t.Errorf("recorder events = %q, want user,assistant", got)
	}
	// The client saw the model, system, and the single user message.
	req := client.lastReq
	if req == nil || len(req.Messages) != 1 {
		t.Fatalf("client saw %d messages, want 1", reqMessages(req))
	}
	if req.Model != "test/model" || req.System != "be terse" {
		t.Errorf("client request model/system = %q/%q, want test/model/be terse", req.Model, req.System)
	}
}

func TestRunTurnToolRoundTrip(t *testing.T) {
	var stdout bytes.Buffer
	rec := &fakeRecorder{}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(
			ContentBlock{Type: BlockTypeText, Text: "let me check "},
			toolCallBlock("call_1", "read", `{"path":"a.go"}`),
		),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Stdout:   &stdout,
	}, "test/model", "", nil, "read a.go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if len(history) != 4 {
		t.Fatalf("history = %d messages, want 4 (user, assistant, toolResult, assistant)", len(history))
	}
	tr := history[2].ToolResult
	if tr == nil {
		t.Fatal("history[2] is not a tool result")
	}
	if tr.ToolCallID != "call_1" || tr.ToolName != "read" || tr.IsError {
		t.Errorf("tool result = %+v, want success for call_1/read", tr)
	}
	if len(tr.Content) != 1 || tr.Content[0].Text != "file body" {
		t.Errorf("tool result content = %+v, want the tool output", tr.Content)
	}
	if tool.executed() != 1 {
		t.Errorf("tool executed %d times, want 1", tool.executed())
	}
	if got := rec.joinEvents(); got != "user,assistant,toolResult,assistant" {
		t.Errorf("recorder events = %q, want user,assistant,toolResult,assistant", got)
	}
	if got := stdout.String(); got != "let me check done" {
		t.Errorf("stdout = %q, want the streamed text of both rounds", got)
	}
	// The second round carried the full history up to that point: user,
	// assistant, tool result.
	if client.calls != 2 || reqMessages(client.lastReq) != 3 {
		t.Errorf("second round saw %d messages over %d calls, want 3 over 2", reqMessages(client.lastReq), client.calls)
	}
}

func TestRunTurnUnknownTool(t *testing.T) {
	var stdout bytes.Buffer
	rec := &fakeRecorder{}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "nope", `{}`)),
		textStop("ok"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Stdout:   &stdout,
	}, "m", "", nil, "do it")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("history = %d messages, want 4", len(history))
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError {
		t.Fatalf("history[2] = %+v, want an error tool result", history[2])
	}
	if len(tr.Content) != 1 || !strings.Contains(tr.Content[0].Text, `unknown tool "nope"`) {
		t.Errorf("tool result text = %q, want unknown-tool description", tr.Content[0].Text)
	}
	if got := rec.joinEvents(); got != "user,assistant,toolResult,assistant" {
		t.Errorf("recorder events = %q", got)
	}
}

func TestRunTurnInvalidArgumentsNotExecuted(t *testing.T) {
	var stdout bytes.Buffer
	tool := &fakeTool{name: "read", result: TextResult("ok")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `not-json`)),
		textStop("fine"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client: client,
		Tools:  []Tool{tool},
		Stdout: &stdout,
	}, "m", "", nil, "read it")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError {
		t.Fatalf("history[2] = %+v, want an error tool result", history[2])
	}
	if !strings.Contains(tr.Content[0].Text, "invalid arguments") {
		t.Errorf("tool result text = %q, want invalid-arguments description", tr.Content[0].Text)
	}
	if tool.executed() != 0 {
		t.Errorf("tool executed %d times, want 0 (invalid arguments must not execute)", tool.executed())
	}
}

func TestRunTurnUnboundedRounds(t *testing.T) {
	// The loop has no round or tool-call budget: 50 consecutive toolUse
	// turns must keep going until the scripted client finally stops.
	const rounds = 50
	var stdout bytes.Buffer
	tool := &fakeTool{name: "t", result: TextResult("ok")}
	script := make([]*AssistantMessage, rounds+1)
	for i := 0; i < rounds; i++ {
		script[i] = toolUseMsg(toolCallBlock("c", "t", `{}`))
	}
	script[rounds] = textStop("done")

	rec := &fakeRecorder{}
	client := &fakeClient{script: script}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Stdout:   &stdout,
	}, "m", "", nil, "go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	// user + 50 * (assistant + tool result) + final assistant.
	if want := 1 + rounds*2 + 1; len(history) != want {
		t.Errorf("history = %d messages, want %d", len(history), want)
	}
	if tool.executed() != rounds {
		t.Errorf("tool executed %d times, want %d", tool.executed(), rounds)
	}
	if got := stdout.String(); got != "done" {
		t.Errorf("stdout = %q, want the final answer only", got)
	}
	if got := rec.joinEvents(); got != "user,"+strings.Repeat("assistant,toolResult,", rounds)+"assistant" {
		t.Errorf("recorder events = %q, want %d full rounds then a final assistant", got, rounds)
	}
	// The last client request carried the full accumulated history.
	if want := 1 + rounds*2; client.lastReq == nil || reqMessages(client.lastReq) != want {
		t.Errorf("final request saw %d messages, want %d", reqMessages(client.lastReq), want)
	}
	if client.calls != rounds+1 {
		t.Errorf("client called %d times, want %d", client.calls, rounds+1)
	}
}

func TestRunTurnForwardsThinking(t *testing.T) {
	var stdout bytes.Buffer
	var thinking []string
	asst := &AssistantMessage{
		Role:       string(RoleAssistant),
		Content:    []ContentBlock{{Type: BlockTypeThinking, Thinking: "hmm, let me think"}, {Type: BlockTypeText, Text: "answer"}},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "test/model",
		StopReason: "stop",
		Timestamp:  1,
	}
	_, err := RunTurn(context.Background(), &LoopDeps{
		Client: &fakeClient{script: []*AssistantMessage{asst}},
		Stdout: &stdout,
		OnThinking: func(delta string) {
			thinking = append(thinking, delta)
		},
	}, "m", "", nil, "q")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got := strings.Join(thinking, ""); got != "hmm, let me think" {
		t.Errorf("thinking deltas = %q, want the full thinking block", got)
	}
	if got := stdout.String(); got != "answer" {
		t.Errorf("stdout = %q, want the text only", got)
	}
}

func TestRunTurnMarshalSafeUserContent(t *testing.T) {
	// The user message content must be the exact JSON encoding of the
	// input string (quoting, escaping), so session persistence and the
	// provider both see a well-formed JSON document.
	input := "line one\n\"quoted\" \\ backslash \t tab \u00e9\u65e5\u672c\u8a9e"
	want, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	rec := &fakeRecorder{}
	client := &fakeClient{script: []*AssistantMessage{textStop("ok")}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
	}, "m", "", nil, input)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	user := history[0].User
	if user == nil {
		t.Fatal("history[0] is not a user message")
	}
	if !bytes.Equal(user.Content, want) {
		t.Errorf("user content = %s, want the JSON encoding %s", user.Content, want)
	}
	var back string
	if err := json.Unmarshal(user.Content, &back); err != nil {
		t.Fatalf("user content does not round-trip: %v", err)
	}
	if back != input {
		t.Errorf("round-trip = %q, want %q", back, input)
	}
	if got := rec.users; len(got) != 1 || !bytes.Equal(got[0].Content, want) {
		t.Errorf("recorder captured %d user messages, first content = %s, want the same JSON", len(got), firstContent(got))
	}

	// Every message in the returned history marshals without error, the
	// property session persistence depends on.
	for i, m := range history {
		var inner any
		switch {
		case m.User != nil:
			inner = m.User
		case m.Assistant != nil:
			inner = m.Assistant
		case m.ToolResult != nil:
			inner = m.ToolResult
		}
		if _, err := json.Marshal(inner); err != nil {
			t.Errorf("history[%d] (%s) does not marshal: %v", i, m.Role(), err)
		}
	}
}

func TestRunTurnContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	history, err := RunTurn(ctx, &LoopDeps{Client: &fakeClient{}}, "m", "", nil, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(history) != 1 || history[0].User == nil {
		t.Errorf("history = %+v, want just the recorded user message", history)
	}
}

func TestRunTurnClientError(t *testing.T) {
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client: &fakeClient{err: errors.New("boom")},
	}, "m", "", nil, "hi")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the client error wrapped", err)
	}
	if len(history) != 1 || history[0].User == nil {
		t.Errorf("history = %+v, want the user message recorded before the failure", history)
	}
}

func TestRunTurnNilDeps(t *testing.T) {
	if _, err := RunTurn(context.Background(), nil, "m", "", nil, "hi"); err == nil {
		t.Error("nil deps: want error")
	}
	if _, err := RunTurn(context.Background(), &LoopDeps{}, "m", "", nil, "hi"); err == nil {
		t.Error("nil client: want error")
	}
}

func TestRunTurnNilStdoutDiscards(t *testing.T) {
	client := &fakeClient{script: []*AssistantMessage{textStop("hi")}}
	if _, err := RunTurn(context.Background(), &LoopDeps{Client: client}, "m", "", nil, "q"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
}

// reqMessages returns the number of messages the client last saw, or -1
// when it never saw a request.
func reqMessages(req *TurnRequest) int {
	if req == nil {
		return -1
	}
	return len(req.Messages)
}

// firstContent renders the first captured user message's content, for
// diagnostics.
func firstContent(users []*UserMessage) []byte {
	if len(users) == 0 {
		return nil
	}
	return users[0].Content
}

// ---------------------------------------------------------------------------
// Wave 2 fakes

// fakeHooks is a scripted agent.HookDispatcher: it records every event it
// receives in order and delegates each event to an optional override
// function. The default overrides return the input unchanged (or allow the
// call), so a bare fakeHooks behaves like a dispatcher with no handlers.
type fakeHooks struct {
	mu     sync.Mutex
	events []string

	contextFn      func(ctx context.Context, req ContextRequest) (ContextResult, error)
	messageEndFn   func(ctx context.Context, m *Message) (*Message, error)
	retryStartFn   func(ctx context.Context, attempt, maxAttempts int, delayMs int64, errorMessage string) error
	retryEndFn     func(ctx context.Context, success bool, attempt int, finalError string) error
	toolCallFn     func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error)
	toolResultFn   func(ctx context.Context, name, callID string, args json.RawMessage, res Result) (Result, error)
	sessionStartFn func(ctx context.Context, reason string) error
	sessionEndFn   func(ctx context.Context, reason string) error
}

func (h *fakeHooks) record(event string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
}

func (h *fakeHooks) joinEvents() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.events, ",")
}

// Context implements agent.HookDispatcher.
func (h *fakeHooks) Context(ctx context.Context, req ContextRequest) (ContextResult, error) {
	h.record("context")
	if h.contextFn != nil {
		return h.contextFn(ctx, req)
	}
	return ContextResult{Messages: req.Messages, System: req.System}, nil
}

// MessageEnd implements agent.HookDispatcher.
func (h *fakeHooks) MessageEnd(ctx context.Context, m *Message) (*Message, error) {
	h.record("message_end")
	if h.messageEndFn != nil {
		return h.messageEndFn(ctx, m)
	}
	return m, nil
}

// AutoRetryStart implements agent.HookDispatcher.
func (h *fakeHooks) AutoRetryStart(ctx context.Context, attempt, maxAttempts int, delayMs int64, errorMessage string) error {
	h.record("auto_retry_start")
	if h.retryStartFn != nil {
		return h.retryStartFn(ctx, attempt, maxAttempts, delayMs, errorMessage)
	}
	return nil
}

// AutoRetryEnd implements agent.HookDispatcher.
func (h *fakeHooks) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	h.record("auto_retry_end")
	if h.retryEndFn != nil {
		return h.retryEndFn(ctx, success, attempt, finalError)
	}
	return nil
}

// ToolCall implements agent.HookDispatcher.
func (h *fakeHooks) ToolCall(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
	h.record("tool_call")
	if h.toolCallFn != nil {
		return h.toolCallFn(ctx, name, callID, args)
	}
	return ToolCallDecision{}, nil
}

// ToolResult implements agent.HookDispatcher.
func (h *fakeHooks) ToolResult(ctx context.Context, name, callID string, args json.RawMessage, res Result) (Result, error) {
	h.record("tool_result")
	if h.toolResultFn != nil {
		return h.toolResultFn(ctx, name, callID, args, res)
	}
	return res, nil
}

// SessionStart implements agent.HookDispatcher. The loop never dispatches
// session events; the methods exist to satisfy the interface.
func (h *fakeHooks) SessionStart(ctx context.Context, reason string) error {
	if h.sessionStartFn != nil {
		return h.sessionStartFn(ctx, reason)
	}
	return nil
}

// SessionShutdown implements agent.HookDispatcher. The loop never
// dispatches session events; the methods exist to satisfy the interface.
func (h *fakeHooks) SessionShutdown(ctx context.Context, reason string) error {
	if h.sessionEndFn != nil {
		return h.sessionEndFn(ctx, reason)
	}
	return nil
}

// fakeDetector is a scripted agent.LoopDetector: each Observe call consumes
// the next scripted outcome, falling back to VerdictNone when the script is
// exhausted, and records every observation it receives.
type fakeDetector struct {
	mu       sync.Mutex
	outcomes []Outcome
	observed []Turn
}

func (d *fakeDetector) Observe(turn Turn) Outcome {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.observed = append(d.observed, turn)
	if len(d.outcomes) == 0 {
		return Outcome{}
	}
	o := d.outcomes[0]
	d.outcomes = d.outcomes[1:]
	return o
}

func (d *fakeDetector) observations() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.observed)
}

// runRetries is a scripted retrier matching the LoopDeps.Retry shape: it
// calls produce until it succeeds or the policy budget is exhausted,
// emitting the retry lifecycle callbacks around each attempt. It never
// sleeps, so tests run without real backoff timers.
func runRetries(ctx context.Context, produce func(context.Context) (*AssistantMessage, error), policy RetryPolicy, callbacks *RetryCallbacks) (*AssistantMessage, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		msg, err := produce(ctx)
		if err == nil {
			if callbacks != nil && callbacks.Finished != nil {
				callbacks.Finished(true, attempt-1, "")
			}
			return msg, nil
		}
		lastErr = err
		if attempt > policy.MaxRetries {
			if callbacks != nil && callbacks.Finished != nil {
				callbacks.Finished(false, attempt-1, lastErr.Error())
			}
			return nil, lastErr
		}
		if callbacks != nil && callbacks.Scheduled != nil {
			callbacks.Scheduled(attempt, policy.MaxRetries, policy.BaseDelayMs, lastErr.Error())
		}
		if callbacks != nil && callbacks.AttemptStart != nil {
			callbacks.AttemptStart()
		}
	}
}

// ---------------------------------------------------------------------------
// Wave 2 tests

func TestRunTurnRetryConsumedAndEventsOrdered(t *testing.T) {
	// The client fails twice, then succeeds. The retrier must be consumed
	// (three produce attempts), the policy zero value must default, the
	// retry lifecycle events must reach both the hooks and the LoopDeps
	// callbacks, and only the successful assistant message is recorded.
	client := &fakeClient{failFirst: 2, script: []*AssistantMessage{textStop("recovered")}}
	rec := &fakeRecorder{}
	hooks := &fakeHooks{}
	var policySeen RetryPolicy
	var scheduled []string
	var finished []string
	_, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Hooks:    hooks,
		Retry: func(ctx context.Context, produce func(context.Context) (*AssistantMessage, error), policy RetryPolicy, callbacks *RetryCallbacks) (*AssistantMessage, error) {
			policySeen = policy
			return runRetries(ctx, produce, policy, callbacks)
		},
		OnRetryScheduled: func(attempt, maxAttempts int, delayMs int64, errorMessage string) {
			scheduled = append(scheduled, fmt.Sprintf("%d/%d", attempt, maxAttempts))
		},
		OnRetryFinished: func(success bool, attempt int, finalError string) {
			finished = append(finished, fmt.Sprintf("%v/%d", success, attempt))
		},
	}, "m", "", nil, "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if client.attempts != 3 {
		t.Errorf("client invoked %d times, want 3 (2 failures + 1 success)", client.attempts)
	}
	if policySeen != DefaultRetryPolicy() {
		t.Errorf("retrier saw policy %+v, want DefaultRetryPolicy %+v", policySeen, DefaultRetryPolicy())
	}
	if got := strings.Join(scheduled, ","); got != "1/10,2/10" {
		t.Errorf("OnRetryScheduled = %q, want 1/10,2/10", got)
	}
	if got := strings.Join(finished, ","); got != "true/2" {
		t.Errorf("OnRetryFinished = %q, want true/2", got)
	}
	// Hook events in dispatch order: context once per round, then the
	// retry lifecycle, then the finalized message.
	if got := hooks.joinEvents(); got != "context,auto_retry_start,auto_retry_start,auto_retry_end,message_end" {
		t.Errorf("hook events = %q, want context,auto_retry_start,auto_retry_start,auto_retry_end,message_end", got)
	}
	// Only the successful assistant message is persisted.
	if got := rec.joinEvents(); got != "user,assistant" {
		t.Errorf("recorder events = %q, want user,assistant", got)
	}
}

func TestRunTurnContextOverflowSentinelWithoutRetry(t *testing.T) {
	classify := func(s string) bool { return strings.Contains(s, "too long") }

	// Transport path: the client fails with an overflow-shaped error and
	// no retrier is wired, so nothing retries and the sentinel carries no
	// assistant message.
	client := &fakeClient{err: errors.New("provider: prompt is too long for requested model")}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:            client,
		IsContextOverflow: classify,
	}, "m", "", nil, "hi")
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("err = %v, want ErrContextOverflow", err)
	}
	var oe *ContextOverflowError
	if !errors.As(err, &oe) || oe.Assistant != nil {
		t.Errorf("overflow error = %+v, want typed error with nil assistant for a transport failure", err)
	}
	if client.attempts != 1 {
		t.Errorf("client invoked %d times, want 1 (no retries consumed)", client.attempts)
	}
	if len(history) != 1 || history[0].User == nil {
		t.Errorf("history = %+v, want just the recorded user message", history)
	}

	// Provider-message path: the client returns a failed assistant message
	// whose error text is an overflow marker. The sentinel wraps that
	// message, and the message itself stays recorded in the history.
	client2 := &fakeClient{script: []*AssistantMessage{{
		Role:         string(RoleAssistant),
		StopReason:   "error",
		ErrorMessage: "input is too long for requested model",
		Timestamp:    1,
	}}}
	rec := &fakeRecorder{}
	history2, err := RunTurn(context.Background(), &LoopDeps{
		Client:            client2,
		Recorder:          rec,
		IsContextOverflow: classify,
	}, "m", "", nil, "hi")
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("err = %v, want ErrContextOverflow", err)
	}
	if !errors.As(err, &oe) || oe.Assistant == nil || oe.Assistant.ErrorMessage != "input is too long for requested model" {
		t.Errorf("overflow error = %+v, want typed error wrapping the failed assistant message", err)
	}
	if len(history2) != 2 || history2[1].Assistant == nil || history2[1].Assistant.StopReason != "error" {
		t.Errorf("history = %+v, want the failed assistant message recorded", history2)
	}
	if got := rec.joinEvents(); got != "user,assistant" {
		t.Errorf("recorder events = %q, want user,assistant", got)
	}
}

func TestRunTurnToolCallDenied(t *testing.T) {
	// The tool_call gate denies the call: nothing executes, the recorded
	// result is an error carrying the denial reason, and the loop keeps
	// going so the model can react.
	var stdout bytes.Buffer
	rec := &fakeRecorder{}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		return ToolCallDecision{Block: true, Reason: "denied by policy"}, nil
	}}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Stdout:   &stdout,
		Hooks:    hooks,
	}, "m", "", nil, "read a.go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if tool.executed() != 0 {
		t.Errorf("tool executed %d times, want 0 (denied call must not execute)", tool.executed())
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError {
		t.Fatalf("history[2] = %+v, want an error tool result", history[2])
	}
	if len(tr.Content) != 1 || !strings.Contains(tr.Content[0].Text, "denied by policy") {
		t.Errorf("tool result text = %q, want the denial reason", tr.Content[0].Text)
	}
	if len(history) != 4 || history[3].Assistant == nil {
		t.Errorf("history = %d messages, want 4 (user, assistant, denied result, assistant)", len(history))
	}
	if got := rec.joinEvents(); got != "user,assistant,toolResult,assistant" {
		t.Errorf("recorder events = %q, want user,assistant,toolResult,assistant", got)
	}
}

func TestRunTurnDetectorWarnInjectsSteeringMessage(t *testing.T) {
	// The loop detector warns on the first observation: the steering user
	// message is recorded and appended to history, and the loop continues
	// to the next assistant round. The recorded text is the detector's
	// fixed host-owned template, prefixed with "[smidja] " so it is
	// distinguishable from real user input and carries no finding
	// details.
	rec := &fakeRecorder{}
	tool := &fakeTool{name: "read", result: TextResult("ok")}
	const steerText = "[smidja] You are repeating the same actions with the same results. Stop, summarize the current state briefly, and choose a different approach."
	det := &fakeDetector{outcomes: []Outcome{{
		Verdict:         VerdictWarn,
		Findings:        []Finding{{Type: "tool-repetition", Message: "Tool call repetition: \"read(a.go)\" repeated 3 times in a row with identical output."}},
		SteerCustomType: "loop-detector-warning",
		SteerText:       steerText,
	}}}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Detector: det,
	}, "m", "", nil, "do it")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(history) != 5 {
		t.Fatalf("history = %d messages, want 5 (user, assistant, toolResult, steer user, assistant)", len(history))
	}
	steer := history[3].User
	if steer == nil {
		t.Fatalf("history[3] = %+v, want the steering user message", history[3])
	}
	var text string
	if err := json.Unmarshal(steer.Content, &text); err != nil {
		t.Fatalf("steer content does not decode: %v", err)
	}
	if text != steerText {
		t.Errorf("steer text = %q, want the detector's fixed warning template", text)
	}
	// The steer text is a fixed host-owned template: it must not embed
	// the finding's model-controlled details.
	if strings.Contains(text, "read(a.go)") || strings.Contains(text, "tool-repetition") {
		t.Errorf("steer text embeds finding details: %q", text)
	}
	if !strings.HasPrefix(text, "[smidja] ") {
		t.Errorf("steer text = %q, want the [smidja] provenance prefix", text)
	}
	if history[4].Assistant == nil || history[4].Assistant.Content[0].Text != "done" {
		t.Errorf("history[4] = %+v, want the loop to continue with the final assistant turn", history[4])
	}
	if got := rec.joinEvents(); got != "user,assistant,toolResult,user,assistant" {
		t.Errorf("recorder events = %q, want the steer message recorded as a user message", got)
	}
	if got := det.observations(); got != 1 {
		t.Errorf("detector observed %d turns, want 1", got)
	}
}

func TestRunTurnDetectorBlockEndsRun(t *testing.T) {
	// The loop detector force-stops: the observed call's result is
	// replaced with a loop-detected error, recorded, and the run ends
	// with ErrLoopDetected.
	rec := &fakeRecorder{}
	tool := &fakeTool{name: "read", result: TextResult("ok")}
	const finding = "Tool call repetition: \"read(a.go)\" repeated 3 times in a row with identical output."
	det := &fakeDetector{outcomes: []Outcome{{
		Verdict:  VerdictBlock,
		Findings: []Finding{{Type: "tool-repetition", Message: finding}},
	}}}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Detector: det,
	}, "m", "", nil, "do it")
	if !errors.Is(err, ErrLoopDetected) {
		t.Fatalf("err = %v, want ErrLoopDetected", err)
	}
	var led *LoopDetectedError
	if !errors.As(err, &led) || led.Outcome.Verdict != VerdictBlock || led.Call.Name != "read" {
		t.Errorf("loop error = %+v, want the typed error with the block outcome and call", err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %d messages, want 3 (user, assistant, blocked toolResult)", len(history))
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError {
		t.Fatalf("history[2] = %+v, want the loop-detected error result", history[2])
	}
	if len(tr.Content) != 1 || !strings.Contains(tr.Content[0].Text, "loop detected: "+finding) {
		t.Errorf("tool result text = %q, want the loop-detected finding", tr.Content[0].Text)
	}
	if tool.executed() != 1 {
		t.Errorf("tool executed %d times, want 1 (observation happens after execution)", tool.executed())
	}
	if got := rec.joinEvents(); got != "user,assistant,toolResult" {
		t.Errorf("recorder events = %q, want user,assistant,toolResult", got)
	}
}

func TestRunTurnWave2NilDepsPreserveLegacy(t *testing.T) {
	// Every wave-2 seam left nil must reproduce the pre-wave-2 loop: no
	// retry, no hooks, no context management, no detection, single-attempt
	// model calls, and the same event order.
	var stdout bytes.Buffer
	rec := &fakeRecorder{}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Stdout:   &stdout,
	}, "m", "", nil, "read a.go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(history) != 4 {
		t.Errorf("history = %d messages, want 4 (user, assistant, toolResult, assistant)", len(history))
	}
	if tool.executed() != 1 || client.calls != 2 {
		t.Errorf("tool executed %d times over %d client calls, want 1 over 2", tool.executed(), client.calls)
	}
	if got := rec.joinEvents(); got != "user,assistant,toolResult,assistant" {
		t.Errorf("recorder events = %q, want user,assistant,toolResult,assistant", got)
	}
}

func TestRunTurnContextHookMutation(t *testing.T) {
	// The context hook receives the prepared messages as a copy it may
	// replace; the replacement feeds the provider call, while the loop's
	// own history and recorder stay untouched.
	client := &fakeClient{script: []*AssistantMessage{textStop("ok")}}
	rec := &fakeRecorder{}
	injected := &Message{User: &UserMessage{Role: string(RoleUser), Content: json.RawMessage(`"injected by hook"`), Timestamp: 1}}
	hooks := &fakeHooks{contextFn: func(ctx context.Context, req ContextRequest) (ContextResult, error) {
		mutated := append(append([]*Message(nil), req.Messages...), injected)
		return ContextResult{Messages: mutated, System: req.System}, nil
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Hooks:    hooks,
	}, "m", "", nil, "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	req := client.lastReq
	if req == nil || len(req.Messages) != 2 {
		t.Fatalf("client saw %d messages, want 2 (user + hook-injected)", reqMessages(req))
	}
	got := req.Messages[1].User
	if got == nil || string(got.Content) != `"injected by hook"` {
		t.Errorf("client message[1] = %+v, want the hook-injected user message", req.Messages[1])
	}
	// The loop's history and the recorder hold only the real messages.
	if len(history) != 2 || history[1].Assistant == nil {
		t.Errorf("loop history = %d messages, want 2 (user, assistant) without the injected copy", len(history))
	}
	if got := rec.joinEvents(); got != "user,assistant" {
		t.Errorf("recorder events = %q, want user,assistant", got)
	}
}

func TestRunTurnMessageEndReplacement(t *testing.T) {
	// The message_end hook chain may replace the finalized assistant
	// message; the replacement is what gets recorded and appended.
	client := &fakeClient{script: []*AssistantMessage{textStop("original")}}
	rec := &fakeRecorder{}
	hooks := &fakeHooks{messageEndFn: func(ctx context.Context, m *Message) (*Message, error) {
		replaced := *m.Assistant
		replaced.Content = []ContentBlock{{Type: BlockTypeText, Text: "replaced"}}
		return &Message{Assistant: &replaced}, nil
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Hooks:    hooks,
	}, "m", "", nil, "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(history) != 2 || history[1].Assistant == nil || history[1].Assistant.Content[0].Text != "replaced" {
		t.Errorf("history = %+v, want the replaced assistant message", history)
	}
	if len(rec.assistants) != 1 || rec.assistants[0].Content[0].Text != "replaced" {
		t.Errorf("recorder captured %d assistants, first text = %q, want the replacement recorded", len(rec.assistants), firstAssistantText(rec))
	}
}

// firstAssistantText renders the text of the first captured assistant
// message, for diagnostics.
func firstAssistantText(rec *fakeRecorder) string {
	if len(rec.assistants) == 0 {
		return ""
	}
	if len(rec.assistants[0].Content) == 0 {
		return ""
	}
	return rec.assistants[0].Content[0].Text
}

func TestRunTurnToolCallFinalArgsUsed(t *testing.T) {
	// The tool_call gate's final arguments are exactly what executes,
	// what gets recorded in the assistant toolCall block, what the
	// tool_result hook sees, and what the loop detector observes. The
	// two-pass model runs the chain twice (pass 1 before the durable
	// record, pass 2 before execution); this sanitizer patches the same
	// bytes both times, so the record, the execution, the detector, and
	// the next provider request all agree.
	rec := &copyingRecorder{fakeRecorder: &fakeRecorder{}}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	dispatches := 0
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		dispatches++
		if dispatches == 1 {
			// Pass 1 sees the model's original arguments.
			if !strings.Contains(string(args), "a.go") {
				t.Errorf("gate saw %s, want the original model arguments", args)
			}
		} else {
			// Pass 2 re-validates against the pass-1 final state: it
			// sees the sanitized arguments, never the model's originals.
			if !strings.Contains(string(args), "sanitized.go") || strings.Contains(string(args), "a.go") {
				t.Errorf("re-validation saw %s, want the pass-1 final (sanitized) arguments", args)
			}
		}
		return ToolCallDecision{FinalArgs: json.RawMessage(`{"path":"sanitized.go"}`)}, nil
	}}
	det := &fakeDetector{}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Hooks:    hooks,
		Detector: det,
	}, "m", "", nil, "read a.go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	// The tool executed with the final arguments, not the original ones.
	if got := tool.lastArgs(); !strings.Contains(string(got), "sanitized.go") || strings.Contains(string(got), "a.go") {
		t.Errorf("tool executed with %s, want the sanitized final arguments", got)
	}
	// The recorded assistant toolCall block carries the final arguments.
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 1 || asst.Content[0].Type != BlockTypeToolCall {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block", history[1])
	}
	if got := string(asst.Content[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("recorded assistant block arguments = %s, want the final arguments", got)
	}
	// The durable recorder copy (deep-copied at AppendAssistant time,
	// before any execution) shows the same block.
	if len(rec.assistants) != 2 || len(rec.assistants[0].Content) != 1 {
		t.Fatalf("recorder captured %d assistants, want 2 with the first holding the toolCall block", len(rec.assistants))
	}
	if got := string(rec.assistants[0].Content[0].Arguments); !strings.Contains(got, "sanitized.go") {
		t.Errorf("recorded assistant block arguments = %s, want the final arguments", got)
	}
	// The loop detector observed the final arguments.
	obs := det.observed
	if len(obs) != 1 || len(obs[0].ToolCalls) != 1 {
		t.Fatalf("detector observed %+v, want one observation with one call", obs)
	}
	if got := string(obs[0].ToolCalls[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("detector observed arguments %s, want the final arguments", got)
	}
	// The next model call sees the toolCall block with the final
	// arguments, so the provider and the tool agree on what ran.
	req := client.lastReq
	if req == nil || len(req.Messages) < 2 {
		t.Fatalf("client saw %+v, want the full history", req)
	}
	for _, m := range req.Messages {
		if m.Assistant != nil {
			for _, b := range m.Assistant.Content {
				if b.Type == BlockTypeToolCall && strings.Contains(string(b.Arguments), "a.go") {
					t.Errorf("provider request carries the unsanitized arguments: %s", b.Arguments)
				}
			}
		}
	}
}

func TestRunTurnToolCallDenyAfterPatchRecordsPatchedArgs(t *testing.T) {
	// A denied call never executes, but when the tool_call gate patched
	// the arguments before denying, the durable record carries the
	// patched arguments: the audit reflects the last validated state
	// before the denial. The denial reason still becomes the error
	// result.
	rec := &copyingRecorder{fakeRecorder: &fakeRecorder{}}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		return ToolCallDecision{Block: true, Reason: "denied by policy", FinalArgs: json.RawMessage(`{"path":"sanitized.go"}`)}, nil
	}}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Hooks:    hooks,
	}, "m", "", nil, "read a.go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if tool.executed() != 0 {
		t.Errorf("tool executed %d times, want 0 (denied call must not execute)", tool.executed())
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError || !strings.Contains(tr.Content[0].Text, "denied by policy") {
		t.Fatalf("history[2] = %+v, want the denial error result", history[2])
	}
	// The recorded assistant block carries the patched (final) args, the
	// last validated state before the denial.
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 1 || asst.Content[0].Type != BlockTypeToolCall {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block", history[1])
	}
	if got := string(asst.Content[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("recorded assistant block arguments = %s, want the patched arguments", got)
	}
	// The durable recorder copy agrees: it was deep-copied at
	// AppendAssistant time, after the patch was applied.
	if len(rec.assistants) != 2 || len(rec.assistants[0].Content) != 1 {
		t.Fatalf("recorder captured %d assistants, want 2 with the first holding the toolCall block", len(rec.assistants))
	}
	if got := string(rec.assistants[0].Content[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("durable assistant block arguments = %s, want the patched arguments", got)
	}
}

func TestRunTurnToolCallDenyRecordsOriginalArgs(t *testing.T) {
	// A plain deny (no FinalArgs patch) records the model's original
	// arguments: the model requested that call, the policy rejected it,
	// and nothing sensitive ran, so the audit keeps the fidelity of what
	// was requested.
	rec := &copyingRecorder{fakeRecorder: &fakeRecorder{}}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		return ToolCallDecision{Block: true, Reason: "denied by policy"}, nil
	}}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Hooks:    hooks,
	}, "m", "", nil, "read a.go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if tool.executed() != 0 {
		t.Errorf("tool executed %d times, want 0 (denied call must not execute)", tool.executed())
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError || !strings.Contains(tr.Content[0].Text, "denied by policy") {
		t.Fatalf("history[2] = %+v, want the denial error result", history[2])
	}
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 1 || asst.Content[0].Type != BlockTypeToolCall {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block", history[1])
	}
	if got := string(asst.Content[0].Arguments); !strings.Contains(got, "a.go") {
		t.Errorf("recorded assistant block arguments = %s, want the original (never-executed) arguments", got)
	}
	if len(rec.assistants) != 2 || len(rec.assistants[0].Content) != 1 {
		t.Fatalf("recorder captured %d assistants, want 2 with the first holding the toolCall block", len(rec.assistants))
	}
	if got := string(rec.assistants[0].Content[0].Arguments); !strings.Contains(got, "a.go") {
		t.Errorf("durable assistant block arguments = %s, want the original (never-executed) arguments", got)
	}
}

func TestRunTurnNoHooksRecordsOriginalArgs(t *testing.T) {
	// Without a hook chain there is nothing to patch: the durable
	// assistant record carries the model's original arguments, which are
	// also the ones that execute.
	rec := &copyingRecorder{fakeRecorder: &fakeRecorder{}}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "read", `{"path":"a.go"}`)),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
	}, "m", "", nil, "read a.go")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got := tool.lastArgs(); !strings.Contains(string(got), "a.go") {
		t.Errorf("tool executed with %s, want the original arguments", got)
	}
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 1 || asst.Content[0].Type != BlockTypeToolCall {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block", history[1])
	}
	if got := string(asst.Content[0].Arguments); !strings.Contains(got, "a.go") {
		t.Errorf("recorded assistant block arguments = %s, want the original arguments", got)
	}
	if len(rec.assistants) != 2 || len(rec.assistants[0].Content) != 1 {
		t.Fatalf("recorder captured %d assistants, want 2 with the first holding the toolCall block", len(rec.assistants))
	}
	if got := string(rec.assistants[0].Content[0].Arguments); !strings.Contains(got, "a.go") {
		t.Errorf("durable assistant block arguments = %s, want the original arguments", got)
	}
}

func TestRunTurnToolCallRevalidationDeniesAfterExecution(t *testing.T) {
	// A stateful one-success quota is evaluated against stale state in
	// pass 1: both calls of the batch pass, because no execution has
	// happened yet. Pass 2 re-validates each call right before it
	// executes; the second call sees the first execution's outcome, the
	// quota is exhausted, and the call is denied with a
	// "blocked on re-validation" error result instead of executing.
	rec := &fakeRecorder{}
	tool := &fakeTool{name: "read", result: TextResult("ok")}
	var mu sync.Mutex
	executed := 0 // successful executions observed through the tool_result hook
	hooks := &fakeHooks{
		toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
			mu.Lock()
			defer mu.Unlock()
			if executed >= 1 {
				return ToolCallDecision{Block: true, Reason: "one-success quota exhausted"}, nil
			}
			return ToolCallDecision{}, nil
		},
		toolResultFn: func(ctx context.Context, name, callID string, args json.RawMessage, res Result) (Result, error) {
			mu.Lock()
			defer mu.Unlock()
			if !res.IsError {
				executed++
			}
			return res, nil
		},
	}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(
			toolCallBlock("c1", "read", `{"path":"a.go"}`),
			toolCallBlock("c2", "read", `{"path":"a.go"}`),
		),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Hooks:    hooks,
	}, "m", "", nil, "read it")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if tool.executed() != 1 {
		t.Errorf("tool executed %d times, want 1 (one-success quota)", tool.executed())
	}
	// c1 executed normally...
	tr1 := history[2].ToolResult
	if tr1 == nil || tr1.IsError || tr1.ToolCallID != "c1" {
		t.Errorf("history[2] = %+v, want the successful c1 result", history[2])
	}
	// ...c2 was blocked on re-validation: an error result, never
	// executed, carrying the re-validation reason.
	tr2 := history[3].ToolResult
	if tr2 == nil || !tr2.IsError || tr2.ToolCallID != "c2" {
		t.Fatalf("history[3] = %+v, want the c2 error result", history[3])
	}
	if len(tr2.Content) != 1 || !strings.Contains(tr2.Content[0].Text, "blocked on re-validation: one-success quota exhausted") {
		t.Errorf("c2 result text = %q, want the re-validation denial reason", tr2.Content[0].Text)
	}
	// Both calls were authorized in pass 1 (the batch-start record holds
	// them), then the chain dispatched again per execution: pass 2 for
	// c1, pass 2 for c2.
	if got := hooks.joinEvents(); got != "context,message_end,tool_call,tool_call,tool_call,tool_result,tool_call,tool_result,context,message_end" {
		t.Errorf("hook events = %q, want the two-pass dispatch order", got)
	}
	if len(history) != 5 || history[4].Assistant == nil {
		t.Errorf("history = %d messages, want 5 (user, assistant, c1 result, c2 result, assistant)", len(history))
	}
	if got := rec.joinEvents(); got != "user,assistant,toolResult,toolResult,assistant" {
		t.Errorf("recorder events = %q, want both tool results recorded", got)
	}
}

func TestRunTurnToolCallRevalidationTransformation(t *testing.T) {
	// Re-validation may transform the arguments immediately before
	// execution: the second dispatch of the second call patches them,
	// and the patched bytes are exactly what executes and what the loop
	// detector observes. The in-memory assistant block follows the
	// latest validated state, while the durable record keeps the
	// batch-start (pass 1) bytes: append-only, the audit reflects
	// batch-start authorization.
	rec := &copyingRecorder{fakeRecorder: &fakeRecorder{}}
	tool := &fakeTool{name: "read", result: TextResult("ok")}
	det := &fakeDetector{}
	dispatches := map[string]int{}
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		dispatches[callID]++
		if callID == "c2" && dispatches[callID] == 2 {
			// Pass 2 of c2: transform the arguments before execution.
			return ToolCallDecision{FinalArgs: json.RawMessage(`{"path":"revalidated.go"}`)}, nil
		}
		return ToolCallDecision{}, nil
	}}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(
			toolCallBlock("c1", "read", `{"path":"a.go"}`),
			toolCallBlock("c2", "read", `{"path":"a.go"}`),
		),
		textStop("done"),
	}}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Tools:    []Tool{tool},
		Hooks:    hooks,
		Detector: det,
	}, "m", "", nil, "read it")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if tool.executed() != 2 {
		t.Fatalf("tool executed %d times, want 2", tool.executed())
	}
	args := tool.allArgs()
	if got := string(args[0]); !strings.Contains(got, "a.go") {
		t.Errorf("first execution args = %s, want the original arguments (no re-validation patch)", got)
	}
	if got := string(args[1]); !strings.Contains(got, "revalidated.go") || strings.Contains(got, "a.go") {
		t.Errorf("second execution args = %s, want the re-validated arguments", got)
	}
	// The loop detector observed the re-validated arguments of the
	// second execution.
	obs := det.observed
	if len(obs) != 2 {
		t.Fatalf("detector observed %d calls, want 2", len(obs))
	}
	if got := string(obs[1].ToolCalls[0].Arguments); !strings.Contains(got, "revalidated.go") {
		t.Errorf("detector observed second-call args %s, want the re-validated arguments", got)
	}
	// The in-memory assistant block follows the latest validated state.
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 2 {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block with two calls", history[1])
	}
	if got := string(asst.Content[1].Arguments); !strings.Contains(got, "revalidated.go") {
		t.Errorf("in-memory assistant block args = %s, want the re-validated arguments", got)
	}
	// The durable record keeps the batch-start bytes: it was deep-copied
	// at AppendAssistant time, before the re-validation patched the
	// second call, and is never rewritten.
	if len(rec.assistants) != 2 {
		t.Fatalf("recorder captured %d assistants, want 2", len(rec.assistants))
	}
	durable := rec.assistants[0]
	if len(durable.Content) != 2 {
		t.Fatalf("durable assistant has %d blocks, want 2", len(durable.Content))
	}
	if got := string(durable.Content[1].Arguments); !strings.Contains(got, "a.go") {
		t.Errorf("durable assistant block args = %s, want the batch-start original arguments", got)
	}
}
