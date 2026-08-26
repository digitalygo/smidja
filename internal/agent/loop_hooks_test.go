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

func (h *fakeHooks) Context(ctx context.Context, req ContextRequest) (ContextResult, error) {
	h.record("context")
	if h.contextFn != nil {
		return h.contextFn(ctx, req)
	}
	return ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (h *fakeHooks) MessageEnd(ctx context.Context, m *Message) (*Message, error) {
	h.record("message_end")
	if h.messageEndFn != nil {
		return h.messageEndFn(ctx, m)
	}
	return m, nil
}

func (h *fakeHooks) AutoRetryStart(ctx context.Context, attempt, maxAttempts int, delayMs int64, errorMessage string) error {
	h.record("auto_retry_start")
	if h.retryStartFn != nil {
		return h.retryStartFn(ctx, attempt, maxAttempts, delayMs, errorMessage)
	}
	return nil
}

func (h *fakeHooks) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	h.record("auto_retry_end")
	if h.retryEndFn != nil {
		return h.retryEndFn(ctx, success, attempt, finalError)
	}
	return nil
}

func (h *fakeHooks) ToolCall(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
	h.record("tool_call")
	if h.toolCallFn != nil {
		return h.toolCallFn(ctx, name, callID, args)
	}
	return ToolCallDecision{}, nil
}

func (h *fakeHooks) ToolResult(ctx context.Context, name, callID string, args json.RawMessage, res Result) (Result, error) {
	h.record("tool_result")
	if h.toolResultFn != nil {
		return h.toolResultFn(ctx, name, callID, args, res)
	}
	return res, nil
}

func (h *fakeHooks) SessionStart(ctx context.Context, reason string) error {
	if h.sessionStartFn != nil {
		return h.sessionStartFn(ctx, reason)
	}
	return nil
}

func (h *fakeHooks) SessionShutdown(ctx context.Context, reason string) error {
	if h.sessionEndFn != nil {
		return h.sessionEndFn(ctx, reason)
	}
	return nil
}

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

func TestRunTurnToolCallDenied(t *testing.T) {
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

func TestRunTurnContextHookMutation(t *testing.T) {
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
	if len(history) != 2 || history[1].Assistant == nil {
		t.Errorf("loop history = %d messages, want 2 (user, assistant) without the injected copy", len(history))
	}
	if got := rec.joinEvents(); got != "user,assistant" {
		t.Errorf("recorder events = %q, want user,assistant", got)
	}
}

func TestRunTurnMessageEndReplacement(t *testing.T) {
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
	rec := &copyingRecorder{fakeRecorder: &fakeRecorder{}}
	tool := &fakeTool{name: "read", result: TextResult("file body")}
	dispatches := 0
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		dispatches++
		if dispatches == 1 {
			if !strings.Contains(string(args), "a.go") {
				t.Errorf("gate saw %s, want the original model arguments", args)
			}
		} else {
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
	if got := tool.lastArgs(); !strings.Contains(string(got), "sanitized.go") || strings.Contains(string(got), "a.go") {
		t.Errorf("tool executed with %s, want the sanitized final arguments", got)
	}
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 1 || asst.Content[0].Type != BlockTypeToolCall {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block", history[1])
	}
	if got := string(asst.Content[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("recorded assistant block arguments = %s, want the final arguments", got)
	}
	if len(rec.assistants) != 2 || len(rec.assistants[0].Content) != 1 {
		t.Fatalf("recorder captured %d assistants, want 2 with the first holding the toolCall block", len(rec.assistants))
	}
	if got := string(rec.assistants[0].Content[0].Arguments); !strings.Contains(got, "sanitized.go") {
		t.Errorf("recorded assistant block arguments = %s, want the final arguments", got)
	}
	obs := det.observed
	if len(obs) != 1 || len(obs[0].ToolCalls) != 1 {
		t.Fatalf("detector observed %+v, want one observation with one call", obs)
	}
	if got := string(obs[0].ToolCalls[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("detector observed arguments %s, want the final arguments", got)
	}
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
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 1 || asst.Content[0].Type != BlockTypeToolCall {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block", history[1])
	}
	if got := string(asst.Content[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("recorded assistant block arguments = %s, want the patched arguments", got)
	}
	if len(rec.assistants) != 2 || len(rec.assistants[0].Content) != 1 {
		t.Fatalf("recorder captured %d assistants, want 2 with the first holding the toolCall block", len(rec.assistants))
	}
	if got := string(rec.assistants[0].Content[0].Arguments); !strings.Contains(got, "sanitized.go") || strings.Contains(got, "a.go") {
		t.Errorf("durable assistant block arguments = %s, want the patched arguments", got)
	}
}

func TestRunTurnToolCallDenyRecordsOriginalArgs(t *testing.T) {
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
	rec := &fakeRecorder{}
	tool := &fakeTool{name: "read", result: TextResult("ok")}
	var mu sync.Mutex
	executed := 0
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
	tr1 := history[2].ToolResult
	if tr1 == nil || tr1.IsError || tr1.ToolCallID != "c1" {
		t.Errorf("history[2] = %+v, want the successful c1 result", history[2])
	}
	tr2 := history[3].ToolResult
	if tr2 == nil || !tr2.IsError || tr2.ToolCallID != "c2" {
		t.Fatalf("history[3] = %+v, want the c2 error result", history[3])
	}
	if len(tr2.Content) != 1 || !strings.Contains(tr2.Content[0].Text, "blocked on re-validation: one-success quota exhausted") {
		t.Errorf("c2 result text = %q, want the re-validation denial reason", tr2.Content[0].Text)
	}
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
	rec := &copyingRecorder{fakeRecorder: &fakeRecorder{}}
	tool := &fakeTool{name: "read", result: TextResult("ok")}
	det := &fakeDetector{}
	dispatches := map[string]int{}
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		dispatches[callID]++
		if callID == "c2" && dispatches[callID] == 2 {
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
	obs := det.observed
	if len(obs) != 2 {
		t.Fatalf("detector observed %d calls, want 2", len(obs))
	}
	if got := string(obs[1].ToolCalls[0].Arguments); !strings.Contains(got, "revalidated.go") {
		t.Errorf("detector observed second-call args %s, want the re-validated arguments", got)
	}
	asst := history[1].Assistant
	if asst == nil || len(asst.Content) != 2 {
		t.Fatalf("history[1] = %+v, want the toolCall assistant block with two calls", history[1])
	}
	if got := string(asst.Content[1].Arguments); !strings.Contains(got, "revalidated.go") {
		t.Errorf("in-memory assistant block args = %s, want the re-validated arguments", got)
	}
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
