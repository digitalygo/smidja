package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

type fakeClient struct {
	mu         sync.Mutex
	script     []*AssistantMessage
	calls      int
	attempts   int
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

type copyingRecorder struct {
	*fakeRecorder
}

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

func (t *fakeTool) lastArgs() json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.args) == 0 {
		return nil
	}
	return t.args[len(t.args)-1]
}

func (t *fakeTool) allArgs() []json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]json.RawMessage(nil), t.args...)
}

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

func reqMessages(req *TurnRequest) int {
	if req == nil {
		return -1
	}
	return len(req.Messages)
}

func firstContent(users []*UserMessage) []byte {
	if len(users) == 0 {
		return nil
	}
	return users[0].Content
}

func TestRunTurnWave2NilDepsPreserveLegacy(t *testing.T) {
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
