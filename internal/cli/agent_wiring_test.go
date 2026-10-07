package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/agents"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func agentBundle(definitions map[string]string) sdk.Bundle {
	files := fstest.MapFS{}
	for name, body := range definitions {
		files["content/agents/"+name+".md"] = &fstest.MapFile{Data: []byte(body)}
	}
	return sdk.Bundle{ID: "test-bundle", FS: files}
}

type agentWireClient struct {
	mu         sync.Mutex
	requests   []*agent.TurnRequest
	childCalls int
}

func (c *agentWireClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	child := strings.Contains(req.System, "You are the child agent.")
	if child {
		c.childCalls++
	}
	c.mu.Unlock()
	if child {
		if lastToolText(req, "read") == "" {
			return toolUse("child_read", "read", `{"path":"note.txt"}`), nil
		}
		text := "child answer with " + strings.TrimSpace(lastToolText(req, "read"))
		if onText != nil {
			onText(text)
		}
		return textStop(text), nil
	}
	if lastToolText(req, "subagent") == "" {
		return toolUse("parent_delegate", "subagent", `{"name":"reader","task":"read note.txt"}`), nil
	}
	if onText != nil {
		onText("parent final answer")
	}
	return textStop("parent final answer"), nil
}

func (c *agentWireClient) childRequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.childCalls
}

func (c *agentWireClient) parentRequests() []*agent.TurnRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*agent.TurnRequest, 0, len(c.requests))
	for _, req := range c.requests {
		if !strings.Contains(req.System, "You are the child agent.") {
			out = append(out, req)
		}
	}
	return out
}

func lastToolText(req *agent.TurnRequest, name string) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		message := req.Messages[i]
		if message == nil || message.ToolResult == nil || message.ToolResult.ToolName != name {
			continue
		}
		var out strings.Builder
		for _, block := range message.ToolResult.Content {
			out.WriteString(block.Text)
		}
		return out.String()
	}
	return ""
}

func childSessionPaths(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, "subagent-sessions")
	var paths []string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

func parentSessionPath(t *testing.T, root string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"subagent-sessions"+string(filepath.Separator)) {
			return nil
		}
		found = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatalf("no parent session file under %s", root)
	}
	return found
}

func readSessionFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPrintModeSubagentToolWireFlow(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "note.txt"), []byte("file contents here"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &agentWireClient{}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "---\ndescription: reads files\n---\nYou are the child agent."})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if err := RunWithDeps([]string{"-p", "ask the reader"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "parent final answer") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if client.childRequestCount() != 2 {
		t.Fatalf("child calls = %d, want 2", client.childRequestCount())
	}
	parentRequests := client.parentRequests()
	if len(parentRequests) != 2 {
		t.Fatalf("parent requests = %d, want 2", len(parentRequests))
	}
	foundSubagent := false
	for _, tool := range parentRequests[0].Tools {
		if tool.Name() == "subagent" {
			foundSubagent = true
		}
	}
	if !foundSubagent {
		t.Fatalf("parent tools = %v, want subagent", parentRequests[0].Tools)
	}
	childPaths := childSessionPaths(t, deps.Store.Root())
	if len(childPaths) != 1 {
		t.Fatalf("child sessions = %v", childPaths)
	}
	loader, err := session.LoadWithOptions(childPaths[0], session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	var childUser, childAssistant string
	for _, entry := range loader.Entries() {
		message, ok := entry.(*session.MessageEntry)
		if !ok {
			continue
		}
		decoded, decodeErr := message.DecodeMessage()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if decoded.User != nil {
			childUser = strings.TrimSpace(string(decoded.User.Content))
		}
		if decoded.Assistant != nil {
			childAssistant = strings.TrimSpace(blocksText(decoded.Assistant.Content))
		}
	}
	if childUser != `"read note.txt"` {
		t.Fatalf("child user = %q", childUser)
	}
	if !strings.Contains(childAssistant, "child answer with") || !strings.Contains(childAssistant, "file contents here") {
		t.Fatalf("child assistant = %q", childAssistant)
	}
	parentTranscript := readSessionFile(t, parentSessionPath(t, deps.Store.Root()))
	if !strings.Contains(parentTranscript, "child answer with") || !strings.Contains(parentTranscript, "file contents here") {
		t.Fatalf("parent transcript missing the bounded child answer:\n%s", parentTranscript)
	}
	if strings.Contains(parentTranscript, "child_read") {
		t.Fatalf("parent transcript leaked child-only tool calls:\n%s", parentTranscript)
	}
}

func TestReplAgentCommandRunsChildAndPersists(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("child answer")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/agent reader say \"hi\" $(touch pwned)\n/quit\n")
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "---\ndescription: reader\n---\nYou are the child agent."})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 1 {
		t.Fatalf("child model calls = %d, want 1", client.calls)
	}
	if !strings.Contains(stdout.String(), "child answer") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "agent reader") {
		t.Fatalf("stdout lacks subagent progress: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, "pwned")); !os.IsNotExist(err) {
		t.Fatal("task text was executed as a shell command")
	}
	wantTask := `say "hi" $(touch pwned)`
	if client.lastUserText() != wantTask {
		t.Fatalf("child task wire = %q, want %q", client.lastUserText(), wantTask)
	}
	parentLoader, err := session.LoadWithOptions(parentSessionPath(t, deps.Store.Root()), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	messages := 0
	for _, entry := range parentLoader.Entries() {
		if _, ok := entry.(*session.MessageEntry); ok {
			messages++
		}
		custom, ok := entry.(*session.CustomEntry)
		if !ok || custom.CustomType != subagentResultCustomType {
			continue
		}
		var payload subagentResultEntry
		if err := json.Unmarshal(custom.Data, &payload); err != nil {
			t.Fatalf("decode subagent entry: %v", err)
		}
		if payload.Agent != "reader" || payload.Result != "child answer" || payload.Status != "ok" {
			t.Fatalf("subagent entry = %+v", payload)
		}
		if payload.Session == "" || payload.SessionID == "" {
			t.Fatalf("subagent entry lacks child session identity: %+v", payload)
		}
		found = true
	}
	if !found {
		t.Fatal("parent session lacks the subagent result entry")
	}
	if messages != 0 {
		t.Fatalf("direct /agent wrote %d message entries into the parent history", messages)
	}
	if len(childSessionPaths(t, deps.Store.Root())) != 1 {
		t.Fatalf("child sessions = %v", childSessionPaths(t, deps.Store.Root()))
	}
	listed, err := deps.Store.List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || strings.Contains(listed[0], "subagent-sessions") {
		t.Fatalf("session browser list = %v", listed)
	}
}

func TestPrintModeKeepsAgentInputLiteral(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("literal answer")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "child body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if err := RunWithDeps([]string{"-p", "/agent reader hello"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.lastUserText() != "/agent reader hello" {
		t.Fatalf("wire prompt = %q, want the literal input", client.lastUserText())
	}
	if paths := childSessionPaths(t, deps.Store.Root()); len(paths) != 0 {
		t.Fatalf("print mode executed the agent command: %v", paths)
	}
}

func TestReplAgentCommandListsUnknownAndArgumentErrors(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/agent\n/agent missing task\n/agent reader\n/quit\n")
	deps.Client = &capturingClient{}
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "child body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "reader") {
		t.Fatalf("listing = %q, want the agent name", stdout.String())
	}
	errorsText := stderr.String()
	if !strings.Contains(errorsText, `no agent named "missing"`) {
		t.Fatalf("stderr = %q", errorsText)
	}
	if !strings.Contains(errorsText, "a task is required") {
		t.Fatalf("stderr = %q", errorsText)
	}
}

func TestReplAgentCommandRejectsMalformedDefinition(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/agent broken task\n/quit\n")
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"broken": "---\nname: x\n---\n   "})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 0 {
		t.Fatal("malformed definition reached the model")
	}
	if !strings.Contains(stderr.String(), `agent "broken"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAgentCatalogTierTrustThroughBuildSnapshot(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".smidja", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".smidja", "agents", "local.md"), []byte("local body"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := wiringTestDeps(t.TempDir())
	untrusted, err := buildContentSnapshot(deps, ws, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := untrusted.Agents["local"]; ok {
		t.Fatal("untrusted workspace agent must be excluded")
	}
	trusted, err := buildContentSnapshot(deps, ws, true)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := trusted.Agents["local"]
	if !ok || ref.Content != "local body" || ref.Tier != "workspace" {
		t.Fatalf("trusted workspace agent = %+v ok=%v", ref, ok)
	}
}

type agentCollidingExtension struct {
	invocations int
}

func (e *agentCollidingExtension) ID() string { return "agent-collision" }

func (e *agentCollidingExtension) Setup(api sdk.API) error {
	return api.RegisterCommand("agent", sdk.Command{
		Description: "extension agent",
		Handler: func(ctx sdk.CommandContext, args string) error {
			e.invocations++
			return nil
		},
	})
}

func TestCanonicalAgentCommandPrecedesCollidingExtension(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/agent\n/agent2 tail\n/quit\n")
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "child body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	ext := &agentCollidingExtension{}
	deps.ExtensionRuntime = promptExtensionRuntime(t, ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if ext.invocations != 1 {
		t.Fatalf("colliding extension invocations = %d, want 1", ext.invocations)
	}
	if !strings.Contains(stdout.String(), "reader") {
		t.Fatalf("canonical /agent did not list: %q", stdout.String())
	}
	if client.calls != 0 {
		t.Fatal("listing /agent must not call the model")
	}
}

type namedTool struct {
	name        string
	description string
	calls       int
}

func (t *namedTool) Name() string            { return t.name }
func (t *namedTool) Description() string     { return t.description }
func (t *namedTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *namedTool) Exec(ctx context.Context, args json.RawMessage) sdk.Result {
	t.calls++
	return sdk.Result{Content: []sdk.Block{{Type: agent.BlockTypeText, Text: "override result"}}}
}

type subagentOverrideExtension struct {
	tool *namedTool
}

func (e *subagentOverrideExtension) ID() string { return "subagent-override" }

func (e *subagentOverrideExtension) Setup(api sdk.API) error {
	return api.RegisterTool(e.tool)
}

func TestSubagentToolOverridePreserved(t *testing.T) {
	cwd := t.TempDir()
	tool := &namedTool{name: "subagent", description: "extension subagent tool"}
	client := &capturingClient{script: []*agent.AssistantMessage{
		toolUse("call_override", "subagent", `{"name":"anything","task":"anything"}`),
		textStop("override done"),
	}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "child body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.ExtensionRuntime = promptExtensionRuntime(t, &subagentOverrideExtension{tool: tool})
	if err := RunWithDeps([]string{"-p", "hello"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if tool.calls != 1 {
		t.Fatalf("override tool calls = %d, want 1", tool.calls)
	}
	advertised := false
	for _, req := range client.reqs {
		for _, candidate := range req.Tools {
			if candidate.Name() == "subagent" && candidate.Description() == "extension subagent tool" {
				advertised = true
			}
		}
	}
	if !advertised {
		t.Fatal("extension subagent tool was replaced by the host tool")
	}
}

func TestBuiltinSubagentToolOverridePreserved(t *testing.T) {
	cwd := t.TempDir()
	tool := &subagentOverrideTool{}
	client := &capturingClient{script: []*agent.AssistantMessage{
		toolUse("call_builtin", "subagent", `{"name":"reader","task":"t"}`),
		textStop("override done"),
	}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Tools = []agent.Tool{tool}
	deps.Bundle = agentBundle(map[string]string{"reader": "child body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if err := RunWithDeps([]string{"-p", "hello"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if tool.calls != 1 {
		t.Fatalf("builtin override calls = %d, want 1", tool.calls)
	}
	if paths := childSessionPaths(t, deps.Store.Root()); len(paths) != 0 {
		t.Fatalf("host executor ran despite the override: %v", paths)
	}
}

type subagentActiveDisableExtension struct {
	targets []string
	once    sync.Once
	fired   chan struct{}
}

func (e *subagentActiveDisableExtension) ID() string { return "subagent-active-disable" }

func (e *subagentActiveDisableExtension) RegisterToolHooks(r sdk.ToolHookRegistry) {
	r.OnToolCall(func(ctx sdk.HandlerContext, ev sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
		if ev.Name == agents.SubagentToolName {
			e.once.Do(func() { close(e.fired) })
			if err := ctx.SetActiveTools(e.targets); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

type activeGateClient struct {
	mu           sync.Mutex
	childCalls   int
	parentResult string
	childRead    string
}

func (c *activeGateClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if strings.Contains(req.System, "child body") {
		c.mu.Lock()
		c.childCalls++
		c.mu.Unlock()
		if last := lastToolText(req, "read"); last != "" {
			answer := "child read " + strings.TrimSpace(last)
			c.mu.Lock()
			c.childRead = strings.TrimSpace(last)
			c.mu.Unlock()
			return textStop(answer), nil
		}
		return toolUse("child_read", "read", `{"path":"note.txt"}`), nil
	}
	if last := lastToolText(req, agents.SubagentToolName); last != "" {
		c.mu.Lock()
		c.parentResult = strings.TrimSpace(last)
		c.mu.Unlock()
		return textStop("parent done"), nil
	}
	return toolUse("parent_delegate", agents.SubagentToolName, `{"name":"runner","task":"run"}`), nil
}

func (c *activeGateClient) counts() (int, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.childCalls, c.parentResult, c.childRead
}

func TestChildAllowlistRejectsParentDisabledToolBeforeChildStart(t *testing.T) {
	cwd := t.TempDir()
	routing := &activeGateClient{}
	disable := &subagentActiveDisableExtension{targets: []string{"read", agents.SubagentToolName}, fired: make(chan struct{})}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = routing
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"runner": "---\ntools: [exec]\n---\nchild body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.ExtensionRuntime = promptExtensionRuntime(t, disable)
	if err := RunWithDeps([]string{"-p", "ask"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	select {
	case <-disable.fired:
	case <-time.After(5 * time.Second):
		t.Fatal("the parent active-tool hook never fired")
	}
	childCalls, parentResult, _ := routing.counts()
	if !strings.Contains(parentResult, "tool \"exec\" is not active for the parent") {
		t.Fatalf("parent result = %q, want the inactive-tool error", parentResult)
	}
	if childCalls != 0 {
		t.Fatalf("child model calls = %d, want 0 before revalidation", childCalls)
	}
	if paths := childSessionPaths(t, deps.Store.Root()); len(paths) != 0 {
		t.Fatalf("rejected child left session files: %v", paths)
	}
	if _, err := os.Stat(filepath.Join(cwd, "pwned-child")); !os.IsNotExist(err) {
		t.Fatal("disabled exec tool still ran")
	}
}

func TestChildAllowlistKeepsParentActiveTool(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "note.txt"), []byte("file contents here"), 0o644); err != nil {
		t.Fatal(err)
	}
	routing := &activeGateClient{}
	disable := &subagentActiveDisableExtension{targets: []string{"read", agents.SubagentToolName}, fired: make(chan struct{})}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = routing
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"runner": "---\ntools: [read]\n---\nchild body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.ExtensionRuntime = promptExtensionRuntime(t, disable)
	if err := RunWithDeps([]string{"-p", "ask"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	childCalls, _, childRead := routing.counts()
	if childCalls != 2 {
		t.Fatalf("child model calls = %d, want 2", childCalls)
	}
	if !strings.Contains(childRead, "file contents here") {
		t.Fatalf("child read result = %q", childRead)
	}
	if len(childSessionPaths(t, deps.Store.Root())) != 1 {
		t.Fatal("allowed child session was not created")
	}
}

type apiCaptureExtension struct {
	api sdk.API
}

func (e *apiCaptureExtension) ID() string { return "api-capture" }

func (e *apiCaptureExtension) Setup(api sdk.API) error {
	e.api = api
	return nil
}

type barrierReadTool struct {
	mu    sync.Mutex
	calls int
}

func (t *barrierReadTool) Name() string        { return "read" }
func (t *barrierReadTool) Description() string { return "read a workspace file" }
func (t *barrierReadTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}

func (t *barrierReadTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return agent.TextResult("file contents here")
}

func (t *barrierReadTool) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

type childBarrierReadClient struct {
	mu           sync.Mutex
	started      chan struct{}
	release      chan struct{}
	childResults []string
}

func (c *childBarrierReadClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if strings.Contains(req.System, "child body") {
		if last := lastToolText(req, "read"); last != "" {
			c.mu.Lock()
			c.childResults = append(c.childResults, strings.TrimSpace(last))
			c.mu.Unlock()
			return textStop("child done"), nil
		}
		select {
		case <-c.started:
		default:
			close(c.started)
		}
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return toolUse("child_read", "read", `{"path":"note.txt"}`), nil
	}
	if last := lastToolText(req, agents.SubagentToolName); last != "" {
		return textStop("parent done"), nil
	}
	return toolUse("parent_delegate", agents.SubagentToolName, `{"name":"runner","task":"run"}`), nil
}

func (c *childBarrierReadClient) observedResults() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.childResults...)
}

func TestChildToolDisabledWhileChildRequestBlocked(t *testing.T) {
	cwd := t.TempDir()
	read := &barrierReadTool{}
	client := &childBarrierReadClient{started: make(chan struct{}), release: make(chan struct{})}
	capture := &apiCaptureExtension{}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"runner": "---\ntools: [read]\n---\nchild body"})
	deps.Tools = []agent.Tool{read}
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.ExtensionRuntime = promptExtensionRuntime(t, capture)
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps([]string{"-p", "ask"}, deps)
	}()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatalf("the child model request never started; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if capture.api == nil {
		t.Fatal("the extension did not capture the host API")
	}
	if err := capture.api.SetActiveTools([]string{agents.SubagentToolName}); err != nil {
		t.Fatalf("SetActiveTools: %v", err)
	}
	close(client.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the blocked child request never finished")
	}
	if read.callCount() != 0 {
		t.Fatalf("the disabled read tool executed %d times", read.callCount())
	}
	results := client.observedResults()
	if len(results) != 1 {
		t.Fatalf("child model results = %v, want one disabled-tool result", results)
	}
	if !strings.Contains(results[0], "unknown tool") {
		t.Fatalf("child model result = %q, want the disabled-tool rejection", results[0])
	}
	if strings.Contains(results[0], "file contents here") {
		t.Fatalf("the disabled tool leaked into the child model result: %q", results[0])
	}
}

type bootstrapActiveExtension struct {
	api     sdk.API
	targets []string
}

func (e *bootstrapActiveExtension) ID() string { return "bootstrap-active" }

func (e *bootstrapActiveExtension) Setup(api sdk.API) error {
	e.api = api
	return api.SetActiveTools(e.targets)
}

type namedAgentTool struct {
	name  string
	calls int
}

func (t *namedAgentTool) Name() string            { return t.name }
func (t *namedAgentTool) Description() string     { return "agent tool " + t.name }
func (t *namedAgentTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *namedAgentTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	t.calls++
	return agent.TextResult("agent tool result")
}

type reactivationCommandClient struct {
	mu         sync.Mutex
	turns      map[string]int
	results    map[string][]string
	advertised map[string][][]string
	deepCalls  int
	reenable   func(bool) error
}

func newReactivationCommandClient(reenable func(bool) error) *reactivationCommandClient {
	return &reactivationCommandClient{
		turns:      map[string]int{},
		results:    map[string][]string{},
		advertised: map[string][][]string{},
		reenable:   reenable,
	}
}

func (c *reactivationCommandClient) reactivationAgent(req *agent.TurnRequest) string {
	switch {
	case strings.Contains(req.System, "deep exec body"):
		return "deep-exec"
	case strings.Contains(req.System, "deep read body"):
		return "deep-read"
	case strings.Contains(req.System, "mid body"):
		return "mid"
	default:
		return "top"
	}
}

func (c *reactivationCommandClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	agentName := c.reactivationAgent(req)
	if agentName != "mid" && agentName != "top" {
		if agentName == "deep-exec" {
			c.mu.Lock()
			c.deepCalls++
			c.mu.Unlock()
		}
		return textStop("deep done"), nil
	}
	c.mu.Lock()
	call := c.turns[agentName]
	c.turns[agentName]++
	c.results[agentName] = append(c.results[agentName], lastToolText(req, agents.SubagentToolName))
	names := make([]string, 0, len(req.Tools))
	for _, tool := range req.Tools {
		names = append(names, tool.Name())
	}
	c.advertised[agentName] = append(c.advertised[agentName], names)
	c.mu.Unlock()
	if agentName == "mid" {
		switch call {
		case 0:
			return toolUse("gate_mid_read", agents.SubagentToolName, `{"name":"deep-read","task":"continue"}`), nil
		case 1:
			return toolUse("gate_mid_exec", agents.SubagentToolName, `{"name":"deep-exec","task":"continue"}`), nil
		default:
			return textStop("mid done"), nil
		}
	}
	switch call {
	case 0:
		return toolUse("gate_top_blocked", agents.SubagentToolName, `{"name":"top","task":"self"}`), nil
	case 1:
		for i := 0; i < 5; i++ {
			if err := c.reenable(false); err != nil {
				return nil, err
			}
			if err := c.reenable(true); err != nil {
				return nil, err
			}
		}
		return toolUse("gate_top_cycle", agents.SubagentToolName, `{"name":"top","task":"self"}`), nil
	case 2:
		return toolUse("gate_top_mid", agents.SubagentToolName, `{"name":"mid","task":"continue"}`), nil
	default:
		return textStop("top done"), nil
	}
}

func (c *reactivationCommandClient) snapshot(agentName string) ([]string, [][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	results := append([]string(nil), c.results[agentName]...)
	rows := make([][]string, len(c.advertised[agentName]))
	for i, names := range c.advertised[agentName] {
		rows[i] = append([]string(nil), names...)
	}
	return results, rows
}

func (c *reactivationCommandClient) deepCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deepCalls
}

func TestChildReactivationNeverFallsThroughToRootDelegation(t *testing.T) {
	cwd := t.TempDir()
	read := &barrierReadTool{}
	execTool := &namedAgentTool{name: "exec"}
	gate := &bootstrapActiveExtension{targets: []string{"read"}}
	client := newReactivationCommandClient(func(on bool) error {
		names := []string{"read"}
		if on {
			names = append(names, agents.SubagentToolName, "exec")
		}
		return gate.api.SetActiveTools(names)
	})
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/agent top run\n/quit\n")
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{
		"top":       "top body",
		"mid":       "---\ntools: [read, subagent]\n---\nmid body",
		"deep-read": "---\ntools: [read]\n---\ndeep read body",
		"deep-exec": "---\ntools: [exec]\n---\ndeep exec body",
	})
	deps.Tools = []agent.Tool{read, execTool}
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.ExtensionRuntime = promptExtensionRuntime(t, gate)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	topResults, topAdvertised := client.snapshot("top")
	if len(topResults) != 4 {
		t.Fatalf("top turns = %d, want 4", len(topResults))
	}
	if !strings.Contains(topResults[1], `unknown tool "subagent"`) {
		t.Fatalf("inactive delegation = %q, want the blocked tool", topResults[1])
	}
	if !strings.Contains(topResults[2], "delegation cycle detected") {
		t.Fatalf("reactivated self delegation = %q, want the cycle denial", topResults[2])
	}
	if !strings.Contains(topResults[3], "mid done") {
		t.Fatalf("intermediate delegation = %q", topResults[3])
	}
	if strings.Join(topAdvertised[0], ",") != "read" {
		t.Fatalf("inactive child advertised = %v, want only the active read tool", topAdvertised[0])
	}
	if !strings.Contains(strings.Join(topAdvertised[2], ","), agents.SubagentToolName) {
		t.Fatalf("reactivated child did not advertise a delegation tool: %v", topAdvertised[2])
	}
	midResults, midAdvertised := client.snapshot("mid")
	if len(midResults) != 3 {
		t.Fatalf("mid turns = %d, want 3", len(midResults))
	}
	if !strings.Contains(midResults[1], "deep done") {
		t.Fatalf("deep read delegation = %q", midResults[1])
	}
	if !strings.Contains(midResults[2], `tool "exec" is not active for the parent`) {
		t.Fatalf("restricted delegation = %q, want the intermediate-view rejection", midResults[2])
	}
	if strings.Join(midAdvertised[0], ",") != "read,subagent" {
		t.Fatalf("mid tools = %v, want the restricted intermediate view", midAdvertised[0])
	}
	if client.deepCount() != 0 {
		t.Fatalf("deep exec child ran %d times", client.deepCount())
	}
	if execTool.calls != 0 {
		t.Fatalf("deep exec tool ran %d times", execTool.calls)
	}
	if paths := childSessionPaths(t, deps.Store.Root()); len(paths) != 3 {
		t.Fatalf("child sessions = %v, want top, mid and deep read only", paths)
	}
}

type parentHookRecorderExtension struct {
	mu       sync.Mutex
	events   []string
	models   []string
	sessions []string
}

func (e *parentHookRecorderExtension) ID() string { return "parent-hook-recorder" }

func (e *parentHookRecorderExtension) RegisterLLMHooks(r sdk.LLMHookRegistry) {
	r.OnMessageEnd(func(ctx sdk.HandlerContext, ev sdk.MessageEndEvent) (*sdk.MessageEndEventResult, error) {
		e.record(ctx, "message_end")
		return nil, nil
	})
}

func (e *parentHookRecorderExtension) RegisterToolHooks(r sdk.ToolHookRegistry) {
	r.OnToolCall(func(ctx sdk.HandlerContext, ev sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
		e.record(ctx, "call:"+ev.Name)
		return nil, nil
	})
	r.OnToolResult(func(ctx sdk.HandlerContext, ev sdk.ToolResultEvent) (*sdk.ToolResultEventResult, error) {
		e.record(ctx, "result:"+ev.Name)
		return nil, nil
	})
}

func (e *parentHookRecorderExtension) record(ctx sdk.HandlerContext, event string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
	if model := ctx.Model(); model != nil {
		e.models = append(e.models, model.ID)
	} else {
		e.models = append(e.models, "")
	}
	if session := ctx.SessionManager(); session != nil {
		e.sessions = append(e.sessions, session.ID())
	} else {
		e.sessions = append(e.sessions, "")
	}
}

func (e *parentHookRecorderExtension) snapshot() ([]string, []string, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...), append([]string(nil), e.models...), append([]string(nil), e.sessions...)
}

func TestChildLoopDoesNotDispatchParentHooks(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "note.txt"), []byte("file contents here"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &agentWireClient{}
	recorder := &parentHookRecorderExtension{}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "---\ndescription: reads files\n---\nYou are the child agent."})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.ExtensionRuntime = promptExtensionRuntime(t, recorder)
	if err := RunWithDeps([]string{"-p", "ask the reader"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	events, models, sessions := recorder.snapshot()
	counts := map[string]int{}
	for _, event := range events {
		counts[event]++
	}
	if counts["call:subagent"] < 1 || counts["result:subagent"] != 1 {
		t.Fatalf("parent subagent events = %v", events)
	}
	if counts["call:read"] != 0 || counts["result:read"] != 0 {
		t.Fatalf("child tool events leaked into the parent dispatcher: %v", events)
	}
	if counts["message_end"] != 2 {
		t.Fatalf("message_end count = %d, want 2 parent calls only: %v", counts["message_end"], events)
	}
	if len(models) != len(events) || len(sessions) != len(events) {
		t.Fatalf("hook records misaligned: %v %v %v", events, models, sessions)
	}
	parentWire := client.parentRequests()[0].Model
	parentPath := parentSessionPath(t, deps.Store.Root())
	for i := range events {
		if models[i] != parentWire {
			t.Fatalf("hook model = %q, want parent %q", models[i], parentWire)
		}
		if sessions[i] == "" || !strings.Contains(parentPath, sessions[i]) {
			t.Fatalf("hook session = %q, want the parent session %q", sessions[i], parentPath)
		}
	}
	transcript := readSessionFile(t, parentPath)
	if strings.Contains(transcript, "child_read") {
		t.Fatalf("child tool entries leaked into the parent session:\n%s", transcript)
	}
	if count := strings.Count(transcript, `"type":"message"`); count != 4 {
		t.Fatalf("parent message entries = %d, want the parent's own four:\n%s", count, transcript)
	}
}

func TestSubagentResultEntryRejectsStaleGeneration(t *testing.T) {
	store := wiringStore(t)
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	host := newHostRuntime(context.Background(), t.TempDir(), nil, nil)
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), t.TempDir(), "")
	handle := host.snapshot()
	next, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	host.bindSession(next, &sessionRecorder{next}, next.ID(), next.Path(), t.TempDir(), "")
	if err := persistAgentResult(host, handle, agents.Result{Name: "reader", Answer: "child answer"}, nil); err == nil {
		t.Fatal("stale generation accepted a subagent result")
	}
	current := host.snapshot()
	if err := persistAgentResult(host, current, agents.Result{Name: "reader", Answer: "child answer"}, nil); err != nil {
		t.Fatalf("current generation rejected: %v", err)
	}
	loader, err := session.LoadWithOptions(next.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomEntry); ok && custom.CustomType == subagentResultCustomType {
			found = true
		}
	}
	if !found {
		t.Fatal("current generation did not persist the subagent result")
	}
}
