package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/internal/session"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type overflowClient struct {
	text  string
	calls int
}

func (c *overflowClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.calls++
	if c.calls == 1 {
		return nil, errors.New("openrouter: 400: prompt is too long")
	}
	if onText != nil {
		onText(c.text)
	}
	return textStop(c.text), nil
}

type alwaysOverflowClient struct{}

func (c *alwaysOverflowClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	return nil, errors.New("openrouter: 400: prompt is too long")
}

func testPreparer(t *testing.T) *contextPreparerAdapter {
	t.Helper()
	p, err := newContextPreparer(config.Config{
		Model:               "test/model",
		ContextEnabled:      true,
		ContextWindowTokens: 100_000,
	}, 100_000, nil)
	if err != nil {
		t.Fatalf("newContextPreparer: %v", err)
	}
	return p
}

func TestRunOnceOverflowRecovers(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      &overflowClient{text: "recovered answer"},
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		preparer:    testPreparer(t),
		isOverflow:  retry.IsContextOverflow,
	}
	if err := runOnce(context.Background(), d, "hello"); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !strings.Contains(stdout.String(), "recovered answer") {
		t.Errorf("stdout = %q, want the recovered response", stdout.String())
	}
	if !strings.Contains(stderr.String(), "compacting and retrying once") {
		t.Errorf("stderr = %q, want the recovery notice", stderr.String())
	}
}

func TestRunOnceOverflowTwiceFails(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      &alwaysOverflowClient{},
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		preparer:    testPreparer(t),
		isOverflow:  retry.IsContextOverflow,
	}
	err = runOnce(context.Background(), d, "hello")
	if err == nil {
		t.Fatal("second overflow: want error")
	}
	if !strings.Contains(err.Error(), "context still overflows the model window") {
		t.Errorf("error = %q, want the clear overflow message", err.Error())
	}
	if !strings.Contains(stderr.String(), "compacting and retrying once") {
		t.Errorf("stderr = %q, want the recovery notice", stderr.String())
	}
}

func forcedCompactFixture(t *testing.T) (*contextPreparerAdapter, []*agent.Message) {
	t.Helper()
	cfg := contextmanager.Config{
		Enabled:                true,
		ContextWindowTokens:    10_000,
		CacheMissAfter:         contextmanager.DefaultCacheMissAfter,
		PruneThreshold:         contextmanager.DefaultPruneThreshold,
		CompactThreshold:       contextmanager.DefaultCompactThreshold,
		SafetyCompactThreshold: contextmanager.DefaultSafetyCompactThreshold,
		CompactTarget:          contextmanager.DefaultCompactTarget,
		KeepRecentMessages:     2,
		SelectorChunkTokens:    contextmanager.DefaultSelectorChunkTokens,
		SelectorModel:          "test/model",
	}
	live, err := contextmanager.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := newContextPreparerAdapter(live, cfg)
	adapter.ObserveResponse(&agent.AssistantMessage{Usage: agent.Usage{Input: 1}})

	msgs := make([]*agent.Message, 12)
	for i := range msgs {
		content := strings.Repeat("x", 2500)
		if i%2 == 0 {
			msgs[i] = &agent.Message{User: &agent.UserMessage{
				Role: string(agent.RoleUser), Content: json.RawMessage(strconv.Quote(content)), Timestamp: int64(i),
			}}
		} else {
			msgs[i] = &agent.Message{Assistant: &agent.AssistantMessage{
				Role:      string(agent.RoleAssistant),
				Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: content}},
				Timestamp: int64(i),
			}}
		}
	}
	return adapter, msgs
}

func TestContextPreparerAdapterForcedCompaction(t *testing.T) {
	adapter, msgs := forcedCompactFixture(t)

	res, err := adapter.Prepare(context.Background(), agent.ContextRequest{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compacted || len(res.Pruned) != 0 {
		t.Fatalf("normal prepare with fresh cache: want no action, got compacted=%v pruned=%d", res.Compacted, len(res.Pruned))
	}

	adapter.forceSafety()
	res, err = adapter.Prepare(context.Background(), agent.ContextRequest{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted || res.Compaction == nil {
		t.Fatalf("forced prepare: want compaction, got compacted=%v", res.Compacted)
	}
	if len(res.Messages) >= len(msgs) {
		t.Errorf("forced prepare kept %d messages, want fewer than %d", len(res.Messages), len(msgs))
	}
	entries := adapter.drain()
	if len(entries) != 1 {
		t.Fatalf("drained %d entries, want 1", len(entries))
	}
	if !strings.Contains(string(entries[0].Summary), "smidja-fallback-v1") {
		t.Errorf("summary = %s, want the fallback strategy tag", entries[0].Summary)
	}
}

func TestPersistCompactions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	adapter, msgs := forcedCompactFixture(t)
	adapter.forceSafety()
	if _, err := adapter.Prepare(context.Background(), agent.ContextRequest{Messages: msgs}); err != nil {
		t.Fatal(err)
	}

	d := &runDeps{preparer: adapter, recorder: &sessionRecorder{sess}}
	if err := d.persistCompactions(); err != nil {
		t.Fatalf("persistCompactions: %v", err)
	}
	b, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"type":"compaction"`) {
		t.Errorf("session file lacks a compaction entry:\n%s", b)
	}
	if !strings.Contains(string(b), "smidja-fallback-v1") {
		t.Errorf("session file lacks the summary transcript:\n%s", b)
	}
}

func TestRetryAdapter(t *testing.T) {
	calls := 0
	var scheduled, finished []string
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("openrouter: provider returned error: upstream is overloaded")
		}
		return textStop("ok"), nil
	}
	callbacks := &agent.RetryCallbacks{
		Scheduled: func(attempt, maxAttempts int, delayMs int64, errorMessage string) {
			scheduled = append(scheduled, fmt.Sprintf("%d/%d", attempt, maxAttempts))
		},
		Finished: func(success bool, attempt int, finalError string) {
			finished = append(finished, fmt.Sprintf("success=%v attempt=%d", success, attempt))
		},
	}
	msg, err := retryAdapter(context.Background(), produce, agent.RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 1}, callbacks)
	if err != nil {
		t.Fatalf("retryAdapter: %v", err)
	}
	if msg == nil || calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", calls)
	}
	if len(scheduled) != 1 || scheduled[0] != "1/1" {
		t.Errorf("scheduled = %v, want [1/1]", scheduled)
	}
	if len(finished) != 1 || finished[0] != "success=true attempt=1" {
		t.Errorf("finished = %v, want the success event", finished)
	}
}

func TestLoopDetectorAdapter(t *testing.T) {
	cfg := loopdetector.Config{
		WindowSize:                    10,
		RepeatSequenceMinLength:       2,
		RepeatPatternMinReps:          1,
		EscalateAfter:                 2,
		EnableToolRepetitionDetection: true,
	}
	adapter := newLoopDetectorAdapter(loopdetector.New(cfg))
	args := json.RawMessage(`{"command":"echo hi"}`)
	turn := agent.Turn{
		TurnIndex: 1,
		ToolCalls: []agent.ToolCallObs{
			{ToolCallID: "c1", Name: "bash", Arguments: args, Result: &agent.ToolResultMessage{ToolCallID: "c1", ToolName: "bash", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "hi"}}}},
			{ToolCallID: "c2", Name: "bash", Arguments: args, Result: &agent.ToolResultMessage{ToolCallID: "c2", ToolName: "bash", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "hi"}}}},
		},
	}

	out1 := adapter.Observe(turn)
	if out1.Verdict != agent.VerdictWarn {
		t.Fatalf("first observe verdict = %v, want warn", out1.Verdict)
	}
	if out1.SteerCustomType != loopdetector.SteerTypeWarning || out1.SteerText == "" {
		t.Errorf("first steer = %q/%q, want the warning steer", out1.SteerCustomType, out1.SteerText)
	}
	out2 := adapter.Observe(turn)
	if out2.Verdict != agent.VerdictBlock {
		t.Fatalf("second observe verdict = %v, want block", out2.Verdict)
	}
	if out2.SteerCustomType != loopdetector.SteerTypeForceStop || out2.SteerText == "" {
		t.Errorf("second steer = %q/%q, want the force-stop steer", out2.SteerCustomType, out2.SteerText)
	}
}

type probeTool struct {
	calls *int
}

func (p *probeTool) Name() string        { return "probe" }
func (p *probeTool) Description() string { return "probe tool for injection tests" }
func (p *probeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"x":{"type":"integer"}}}`)
}
func (p *probeTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	*p.calls++
	return agent.TextResult("probe result")
}

func toolUse(id, name, args string) *agent.AssistantMessage {
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: id, Name: name, Arguments: json.RawMessage(args)}},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "test/model",
		StopReason: "toolUse",
		Timestamp:  1,
	}
}

func TestRunWithDepsInjection(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	cfg, err := config.Load(
		envFrom(map[string]string{
			"OPENROUTER_API_KEY": "sk-test",
			"SMIDJA_SESSION_DIR": sessDir,
		}),
		func() (string, error) { return cwd, nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.NewStore(sessDir)
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}

	var probeCalls int
	toolSet := []agent.Tool{&probeTool{calls: &probeCalls}}
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return "/home/tester" },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Config: cfg,
		Client: &fakeClient{script: []*agent.AssistantMessage{
			toolUse("call_1", "probe", `{"x":1}`),
			textStop("answer from injected fake"),
		}},
		Tools: toolSet,
		Store: store,
	}
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "answer from injected fake") {
		t.Errorf("stdout = %q, want the injected client's response", stdout.String())
	}
	if probeCalls != 1 {
		t.Errorf("injected tool executed %d times, want 1", probeCalls)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	var jsonls []string
	filepath.WalkDir(sessDir, func(p string, de fs.DirEntry, err error) error {
		if err == nil && !de.IsDir() && strings.HasSuffix(de.Name(), ".jsonl") {
			jsonls = append(jsonls, p)
		}
		return nil
	})
	if len(jsonls) != 1 {
		t.Fatalf("session files in the injected store = %d, want 1", len(jsonls))
	}
}

func TestRunWithDepsNilEqualsEmptyDeps(t *testing.T) {
	if err := RunWithDeps([]string{"version"}, nil); err != nil {
		t.Fatalf("RunWithDeps(version): %v", err)
	}
}
