package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/gateway"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/summary"
)

type gatewayFakeClient struct {
	script  []*agent.AssistantMessage
	calls   int
	mu      sync.Mutex
	reqs    []*agent.TurnRequest
	block   chan struct{}
	entered chan struct{}
}

func (c *gatewayFakeClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if c.entered != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
	}
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c.mu.Lock()
	if c.calls >= len(c.script) {
		c.mu.Unlock()
		return nil, errors.New("gatewayFakeClient: script exhausted")
	}
	c.reqs = append(c.reqs, req)
	m := c.script[c.calls]
	c.calls++
	c.mu.Unlock()
	for _, b := range m.Content {
		switch b.Type {
		case agent.BlockTypeText:
			if onText != nil {
				onText(b.Text)
			}
		case agent.BlockTypeThinking:
			if onThinking != nil {
				onThinking(b.Thinking)
			}
		}
	}
	return m, nil
}

func (c *gatewayFakeClient) turnCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

type cliDeliverySpy struct {
	ch chan gateway.Delivery
}

func newCLIDeliverySpy(buffer int) *cliDeliverySpy {
	return &cliDeliverySpy{ch: make(chan gateway.Delivery, buffer)}
}

func (s *cliDeliverySpy) Deliver(ctx context.Context, d gateway.Delivery) error {
	s.ch <- d
	return nil
}

func (s *cliDeliverySpy) next(t *testing.T) gateway.Delivery {
	t.Helper()
	select {
	case d := <-s.ch:
		return d
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for delivery")
		return gateway.Delivery{}
	}
}

func (s *cliDeliverySpy) wait(t *testing.T, n int) []gateway.Delivery {
	t.Helper()
	out := make([]gateway.Delivery, 0, n)
	deadline := time.After(3 * time.Second)
	for len(out) < n {
		select {
		case d := <-s.ch:
			out = append(out, d)
		case <-deadline:
			t.Fatalf("timed out waiting for %d deliveries, got %d", n, len(out))
		}
	}
	return out
}

func runnerTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{Model: "test/model", WorkspaceRoot: t.TempDir()}
}

func newTestGatewayRunner(t *testing.T, cfg *config.Config, client agent.Client, mutate func(*gatewayRunnerConfig)) (*gatewayRunner, *session.Store, *bindingStore) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := loadBindings(filepath.Join(t.TempDir(), "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := extensions.NewToolCatalog()
	calls := new(int)
	toolSet := []agent.Tool{&probeTool{calls: calls}}
	for _, tl := range toolSet {
		if err := catalog.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	rc := gatewayRunnerConfig{
		cfg:                cfg,
		providerID:         "openrouter",
		model:              cfg.Model,
		system:             "be terse",
		home:               t.TempDir(),
		store:              store,
		bindings:           bindings,
		workspace:          func(string) string { return cfg.WorkspaceRoot },
		client:             client,
		tools:              toolSet,
		catalog:            catalog,
		contentFingerprint: func(string) string { return "fp-1" },
		stdout:             io.Discard,
		stderr:             io.Discard,
	}
	if mutate != nil {
		mutate(&rc)
	}
	return newGatewayRunner(rc), store, bindings
}

func TestGatewayRunnerDefaults(t *testing.T) {
	cfg := runnerTestConfig(t)
	r := newGatewayRunner(gatewayRunnerConfig{cfg: cfg})
	if r.providerID != "openrouter" {
		t.Errorf("providerID = %q, want openrouter", r.providerID)
	}
	if r.model != cfg.Model {
		t.Errorf("model = %q, want %q", r.model, cfg.Model)
	}
	if r.system != defaultSystemPrompt {
		t.Errorf("system = %q, want the default prompt", r.system)
	}
	if got := r.workspace("telegram:1"); got != cfg.WorkspaceRoot {
		t.Errorf("default workspace = %q, want %q", got, cfg.WorkspaceRoot)
	}
	if got := r.contentFingerprint("any"); got != "" {
		t.Errorf("default content fingerprint = %q, want empty", got)
	}
}

func TestGatewayRunnerBindingWriteFailureFailsTurn(t *testing.T) {
	cfg := runnerTestConfig(t)
	client := &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("ok")}}
	r, _, _ := newTestGatewayRunner(t, cfg, client, func(rc *gatewayRunnerConfig) {
		dir := t.TempDir()
		b, err := loadBindings(filepath.Join(dir, "bindings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		rc.bindings = b
	})
	if _, err := r.Run(context.Background(), gateway.WorkItem{Key: "k", Text: "hello"}); err == nil {
		t.Fatal("binding write failure: want turn error")
	}
}

func TestGatewayRunnerCreateStoreFailureFailsTurn(t *testing.T) {
	cfg := runnerTestConfig(t)
	client := &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("ok")}}
	r, store, _ := newTestGatewayRunner(t, cfg, client, nil)
	root := store.Root()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), gateway.WorkItem{Key: "k", Text: "hello"}); err == nil {
		t.Fatal("broken store root: want turn error")
	}
}

func writeRawSessionFile(t *testing.T, path string, entries []string) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString(`{"type":"session","version":3,"id":"11111111-2222-3333-4444-555555555555","timestamp":"2026-01-01T00:00:00Z","cwd":"/ws"}` + "\n")
	for _, e := range entries {
		b.WriteString(e + "\n")
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayRunnerContextCycleFailsTurn(t *testing.T) {
	cfg := runnerTestConfig(t)
	client := &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("ok")}}
	r, _, _ := newTestGatewayRunner(t, cfg, client, nil)
	path := filepath.Join(t.TempDir(), "cycle.jsonl")
	writeRawSessionFile(t, path, []string{
		`{"type":"message","id":"e1","parentId":"e2","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"\"a\"","timestamp":1}}`,
		`{"type":"message","id":"e2","parentId":"e1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":"\"b\"","timestamp":2}}`,
	})
	if _, err := r.Run(context.Background(), gateway.WorkItem{Key: "k", SessionPath: path, Text: "hello"}); err == nil {
		t.Fatal("cyclic parent chain: want turn error")
	}
}

func TestGatewayRunnerSkipsUndecodableMessages(t *testing.T) {
	cfg := runnerTestConfig(t)
	client := &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("ok")}}
	r, _, _ := newTestGatewayRunner(t, cfg, client, nil)
	path := filepath.Join(t.TempDir(), "mixed.jsonl")
	writeRawSessionFile(t, path, []string{
		`{"type":"message","id":"e1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"bogus","content":"\"x\"","timestamp":1}}`,
		`{"type":"message","id":"e2","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":"\"real\"","timestamp":2}}`,
	})
	res := runRunnerOnce(t, r, gateway.WorkItem{Key: "k", SessionPath: path, Text: "hello"})
	if res.Summary == "" {
		t.Fatal("resume with a bogus entry must still summarize")
	}
}

func TestGatewayRunnerToolsetFingerprintStableAndNilCatalog(t *testing.T) {
	toolSet := []agent.Tool{&probeTool{calls: new(int)}}
	a := toolsetFingerprint(nil, toolSet)
	if a == "" {
		t.Fatal("fingerprint of a tool set must be non-empty")
	}
	b := toolsetFingerprint(nil, toolSet)
	if a != b {
		t.Fatal("fingerprint must be deterministic")
	}
	c := toolsetFingerprint(nil, []agent.Tool{nil, &probeTool{calls: new(int)}})
	if c == "" {
		t.Fatal("nil tools must be skipped, not fail")
	}
}

func TestRenderResumeSummaryEmptyDigest(t *testing.T) {
	got := renderResumeSummary(summary.Digest{})
	if !strings.Contains(got, "Resumed session") {
		t.Errorf("empty digest summary = %q", got)
	}
}

func runRunnerOnce(t *testing.T, r *gatewayRunner, work gateway.WorkItem) gateway.RunResult {
	t.Helper()
	res, err := r.Run(context.Background(), work)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	return res
}

func countProfileEntries(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(line, []byte(session.RuntimeProfileCustomType)) {
			continue
		}
		var env struct {
			CustomType string `json:"customType"`
		}
		if json.Unmarshal(line, &env) == nil && env.CustomType == session.RuntimeProfileCustomType {
			count++
		}
	}
	return count
}

func TestGatewayRunnerFreshSessionCreatesFileAndBinding(t *testing.T) {
	cfg := runnerTestConfig(t)
	client := &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("hello back")}}
	r, _, bindings := newTestGatewayRunner(t, cfg, client, nil)

	res := runRunnerOnce(t, r, gateway.WorkItem{Key: "telegram:1", Text: "hello"})
	if res.SessionID == "" {
		t.Fatal("run result has no session id")
	}
	if res.Summary != "" {
		t.Fatalf("fresh session summary = %q, want empty", res.Summary)
	}
	if res.ProfileReset {
		t.Fatal("fresh session must not reset the runtime profile")
	}
	path, ok := bindings.lookup("telegram:1")
	if !ok {
		t.Fatal("no binding persisted for the chat key")
	}
	if !fileExists(path) {
		t.Fatalf("bound session file %q does not exist", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"hello"`, `"hello back"`, session.RuntimeProfileCustomType} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session file missing %q:\n%s", want, data)
		}
	}
	if client.turnCount() != 1 {
		t.Fatalf("client turns = %d, want 1", client.turnCount())
	}
}

func TestGatewayRunnerResumeAppendsSameFilePreservingPrefix(t *testing.T) {
	cfg := runnerTestConfig(t)
	client := &gatewayFakeClient{script: []*agent.AssistantMessage{
		textStop("first reply"),
		textStop("second reply"),
	}}
	r, _, bindings := newTestGatewayRunner(t, cfg, client, nil)

	runRunnerOnce(t, r, gateway.WorkItem{Key: "k", Text: "first"})
	path, ok := bindings.lookup("k")
	if !ok {
		t.Fatal("no binding after first run")
	}
	prefix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	res := runRunnerOnce(t, r, gateway.WorkItem{Key: "k", SessionPath: path, Text: "second"})
	if res.Summary == "" {
		t.Fatal("resumed run returned no summary")
	}
	if !strings.Contains(res.Summary, "Resumed session") {
		t.Errorf("summary = %q, want the resume marker", res.Summary)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, prefix) {
		t.Error("resume did not preserve the prefix bytes of the session file")
	}
	if len(data) <= len(prefix) {
		t.Error("resume did not append new entries")
	}
	for _, want := range []string{`"first"`, `"first reply"`, `"second"`, `"second reply"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session file missing %q", want)
		}
	}
}

func TestGatewayRunnerProfileMismatchResetsExactlyOnce(t *testing.T) {
	cfg := runnerTestConfig(t)
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := store.Create(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	foreign := session.CurrentProfile{
		ProviderID:                     "openrouter",
		ModelID:                        "other/model",
		SystemPromptSHA256:             "sp-other",
		ToolSchemasCanonicalJSONSHA256: "ts-other",
		OrderingVersion:                1,
		AffinityKey:                    "workspace:" + cfg.WorkspaceRoot,
	}
	if _, err := seeded.PersistRuntimeProfile(foreign, func() string { return "fp-other" }); err != nil {
		t.Fatal(err)
	}
	if err := seeded.Close(); err != nil {
		t.Fatal(err)
	}

	client := &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("ok"), textStop("ok again")}}
	r, _, _ := newTestGatewayRunner(t, cfg, client, nil)

	work := gateway.WorkItem{Key: "k", SessionPath: seeded.Path(), Text: "hello"}
	res := runRunnerOnce(t, r, work)
	if !res.ProfileReset {
		t.Fatal("profile mismatch did not trigger a reset")
	}
	if n := countProfileEntries(t, seeded.Path()); n != 2 {
		t.Fatalf("profile entries after reset = %d, want 2 (seeded + reset)", n)
	}

	res2 := runRunnerOnce(t, r, work)
	if res2.ProfileReset {
		t.Fatal("matching profile triggered another reset")
	}
	if n := countProfileEntries(t, seeded.Path()); n != 2 {
		t.Fatalf("profile entries after matching run = %d, want still 2", n)
	}
}

func TestGatewayRunnerSummaryDeliveredBeforeResponseOnResume(t *testing.T) {
	cfg := runnerTestConfig(t)
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := store.Create(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := seeded.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"prior question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := seeded.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "prior answer"}},
		API: "openai-completions", Provider: "openrouter", Model: "test/model", StopReason: "stop", Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := seeded.Close(); err != nil {
		t.Fatal(err)
	}

	client := &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("new answer")}}
	r, _, bindings := newTestGatewayRunner(t, cfg, client, nil)

	sink := newCLIDeliverySpy(4)
	g, err := gateway.New(gateway.Options{
		Dir: t.TempDir(),
		Resolver: func(key string) (string, string) {
			return cfg.WorkspaceRoot, seeded.Path()
		},
		Runner: r,
	})
	if err != nil {
		t.Fatal(err)
	}
	g.RegisterSink("telegram", sink)
	gctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := g.Start(gctx); err != nil {
		t.Fatal(err)
	}
	defer g.Shutdown(context.Background())

	msg := gateway.InboundMessage{
		ID: "m1", Transport: "telegram", ExternalChatKey: "chat-1",
		UserIDHash: gateway.HashUserIdentity("user-1"), Text: "new question",
	}
	if _, err := g.Submit(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	deliveries := sink.wait(t, 2)
	if deliveries[0].Kind != gateway.DeliveryKindSummary {
		t.Fatalf("first delivery kind = %q, want summary", deliveries[0].Kind)
	}
	if !strings.Contains(deliveries[0].Result.Text, "Resumed session") {
		t.Errorf("summary text = %q, want the resume marker", deliveries[0].Result.Text)
	}
	if deliveries[1].Kind != gateway.DeliveryKindResponse {
		t.Fatalf("second delivery kind = %q, want response", deliveries[1].Kind)
	}
	if deliveries[1].Result.Text != "new answer" {
		t.Errorf("response text = %q, want the fake reply", deliveries[1].Result.Text)
	}
	if deliveries[0].Result.SessionID != deliveries[1].Result.SessionID {
		t.Errorf("summary and response session ids differ: %q vs %q", deliveries[0].Result.SessionID, deliveries[1].Result.SessionID)
	}
	if _, ok := bindings.lookup("telegram:chat-1"); ok {
		t.Error("resumed chat must not create a fresh binding")
	}
}

func TestGatewayRunnerCancelEndToEndViaFacade(t *testing.T) {
	cfg := runnerTestConfig(t)
	client := &gatewayFakeClient{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	r, _, _ := newTestGatewayRunner(t, cfg, client, nil)

	sink := newCLIDeliverySpy(4)
	g, err := gateway.New(gateway.Options{
		Dir:      t.TempDir(),
		Resolver: func(key string) (string, string) { return cfg.WorkspaceRoot, "" },
		Runner:   r,
	})
	if err != nil {
		t.Fatal(err)
	}
	g.RegisterSink("telegram", sink)
	gctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := g.Start(gctx); err != nil {
		t.Fatal(err)
	}
	defer g.Shutdown(context.Background())

	msg := gateway.InboundMessage{
		ID: "m1", Transport: "telegram", ExternalChatKey: "chat-1",
		UserIDHash: gateway.HashUserIdentity("user-1"), Text: "hello",
	}
	if _, err := g.Submit(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("turn never reached the client")
	}
	if !g.Cancel(msg.Transport, msg.ExternalChatKey) {
		t.Fatal("cancel of the active turn returned false")
	}
	d := sink.next(t)
	if d.Err == nil {
		t.Fatal("cancel delivery has no error")
	}
	rec, ok := g.Journal().Get("m1")
	if !ok || rec.Status != gateway.StatusOutcomeUnknown {
		t.Fatalf("journal record after cancel = %+v ok=%v, want outcome_unknown", rec, ok)
	}
	if g.Cancel(msg.Transport, msg.ExternalChatKey) {
		t.Fatal("cancel after the turn ended returned true")
	}
}
