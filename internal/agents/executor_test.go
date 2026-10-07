package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/session"
)

type scriptClient struct {
	mu    sync.Mutex
	reps  []string
	errs  []error
	calls []*agent.TurnRequest
}

func (c *scriptClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	call := len(c.calls)
	c.calls = append(c.calls, req)
	c.mu.Unlock()
	if call < len(c.errs) && c.errs[call] != nil {
		return nil, c.errs[call]
	}
	if call >= len(c.reps) {
		return nil, errors.New("scriptClient: script exhausted")
	}
	text := c.reps[call]
	if onText != nil {
		onText(text)
	}
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: text}},
		Model:      req.Model,
		StopReason: "stop",
		Timestamp:  agent.NowMillis(),
	}, nil
}

func (c *scriptClient) lastRequest() *agent.TurnRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return nil
	}
	return c.calls[len(c.calls)-1]
}

func (c *scriptClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

type fakePreparer struct {
	mu      sync.Mutex
	forced  atomic.Bool
	drained []*agent.CompactionEntry
}

func (p *fakePreparer) Prepare(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (p *fakePreparer) ObserveRequest(time.Time) {}

func (p *fakePreparer) ObserveResponse(*agent.AssistantMessage) {}

func (p *fakePreparer) ForceSafety() {
	p.forced.Store(true)
	p.mu.Lock()
	p.drained = append(p.drained, &agent.CompactionEntry{Summary: json.RawMessage(`"forced"`), TokensBefore: 42})
	p.mu.Unlock()
}

func (p *fakePreparer) DrainCompactions() []*agent.CompactionEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.drained
	p.drained = nil
	return out
}

type probeTool struct {
	name  string
	calls int
}

func (t *probeTool) Name() string            { return t.name }
func (t *probeTool) Description() string     { return "probe " + t.name }
func (t *probeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *probeTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	t.calls++
	return agent.TextResult("probe result")
}

type toggleCatalog struct {
	mu       sync.Mutex
	tools    []agent.Tool
	byName   map[string]agent.Tool
	disabled map[string]bool
}

func newToggleCatalog(tools ...agent.Tool) *toggleCatalog {
	c := &toggleCatalog{byName: map[string]agent.Tool{}, disabled: map[string]bool{}}
	for _, tool := range tools {
		c.tools = append(c.tools, tool)
		c.byName[tool.Name()] = tool
	}
	return c
}

func (c *toggleCatalog) Tools() []agent.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]agent.Tool, 0, len(c.tools))
	for _, tool := range c.tools {
		if !c.disabled[tool.Name()] {
			out = append(out, tool)
		}
	}
	return out
}

func (c *toggleCatalog) Get(name string) (agent.Tool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tool, ok := c.byName[name]
	return tool, ok
}

func (c *toggleCatalog) GetActive(name string) (agent.Tool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled[name] {
		return nil, false
	}
	tool, ok := c.byName[name]
	return tool, ok
}

func (c *toggleCatalog) disable(name string) {
	c.mu.Lock()
	c.disabled[name] = true
	c.mu.Unlock()
}

func (c *toggleCatalog) enable(name string) {
	c.mu.Lock()
	delete(c.disabled, name)
	c.mu.Unlock()
}

func executorCatalog(t *testing.T, bodies map[string]string) Catalog {
	t.Helper()
	snapshot := content.Snapshot{Agents: make(map[string]content.AgentRef, len(bodies))}
	for name, body := range bodies {
		snapshot.Agents[name] = content.AgentRef{Name: name, Content: body, Tier: content.TierBundle, Package: "test", Path: name + ".md", Origin: "bundle:agents"}
	}
	return NewCatalog(snapshot)
}

type executorFixture struct {
	exec     *Executor
	client   *scriptClient
	preparer *fakePreparer
	catalog  *toggleCatalog
	root     string
}

func defaultParents() Parent {
	return Parent{
		Generation: 7,
		SessionID:  "parent123",
		Model:      "test/model",
		WireModel:  "test/wire",
		Provider:   "openrouter",
		System:     "parent instructions",
	}
}

func newExecutorFixture(t *testing.T, bodies map[string]string, tools *toggleCatalog) *executorFixture {
	t.Helper()
	client := &scriptClient{reps: []string{"child answer"}}
	preparer := &fakePreparer{}
	root := t.TempDir()
	exec, err := NewExecutor(Dependencies{
		Catalog:     func() Catalog { return executorCatalog(t, bodies) },
		ParentTools: func() agent.ToolCatalog { return tools },
		Client: func(def Definition, parent Parent) (Client, error) {
			return Client{Client: client, Model: parent.Model, Wire: parent.WireModel, Provider: parent.Provider}, nil
		},
		Preparer: func(model, wire string, c agent.Client) (Preparer, error) {
			return preparer, nil
		},
		SessionsRoot: root,
		Cwd:          t.TempDir(),
		TempDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return &executorFixture{exec: exec, client: client, preparer: preparer, catalog: tools, root: root}
}

func loadChildSession(t *testing.T, path string) *session.Loader {
	t.Helper()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatalf("load child session %s: %v", path, err)
	}
	return loader
}

func TestExecutorRunsIsolatedChild(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{
		"reader": "---\ndescription: Reads things\n---\nYou are the child agent.",
	}, newToggleCatalog())
	res, err := fixture.exec.Run(context.Background(), Request{
		Name:   "reader",
		Task:   "read the notes\nwith a quote: \"x\" and $(no shell)",
		Parent: defaultParents(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "child answer" || res.Model != "test/model" || res.Depth != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Tier != string(content.TierBundle) || res.Origin != "bundle:agents" {
		t.Fatalf("identity = %+v", res)
	}
	wantDir := filepath.Join(fixture.root, subagentSessionsDir, "parent123")
	if !strings.HasPrefix(res.SessionPath, wantDir+string(os.PathSeparator)) {
		t.Fatalf("child session path = %q, want under %q", res.SessionPath, wantDir)
	}
	req := fixture.client.lastRequest()
	if req == nil || req.Model != "test/wire" {
		t.Fatalf("child request = %+v", req)
	}
	if res.Model != "test/model" || res.Wire != "test/wire" {
		t.Fatalf("result display/wire = %+v", res)
	}
	if !strings.Contains(req.System, "You are the child agent.") || !strings.Contains(req.System, "parent instructions") {
		t.Fatalf("child system = %q", req.System)
	}
	if len(req.Messages) != 1 || string(req.Messages[0].User.Content) != `"read the notes\nwith a quote: \"x\" and $(no shell)"` {
		t.Fatalf("child task wire = %+v", req.Messages)
	}
	loader := loadChildSession(t, res.SessionPath)
	entries := loader.Entries()
	if len(entries) != 3 {
		t.Fatalf("child entries = %d, want marker, user, assistant", len(entries))
	}
	marker, ok := entries[0].(*session.CustomEntry)
	if !ok || marker.CustomType != childDefinitionCustomType || !strings.Contains(string(marker.Data), `"agent":"reader"`) {
		t.Fatalf("child marker = %+v ok=%v", entries[0], ok)
	}
	if entries[1].EntryType() != session.EntryTypeMessage || entries[2].EntryType() != session.EntryTypeMessage {
		t.Fatalf("child entry types = %s %s", entries[1].EntryType(), entries[2].EntryType())
	}
}

func TestExecutorChildPathIsContained(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	parent := defaultParents()
	parent.SessionID = "../escape"
	res, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "task", Parent: parent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rel, err := filepath.Rel(fixture.root, res.SessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsLocal(rel) {
		t.Fatalf("child session escaped the root: %q", rel)
	}
}

func TestExecutorRejections(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{
		"reader":  "child body",
		"limited": "---\ntools: [read, exec]\n---\nchild body",
		"bad":     "---\nname: x\n---\n   ",
	}, newToggleCatalog())
	cases := []struct {
		name  string
		req   Request
		match string
	}{
		{"unknown", Request{Name: "missing", Task: "t", Parent: defaultParents()}, "no agent named"},
		{"malformed", Request{Name: "bad", Task: "t", Parent: defaultParents()}, "agent \"bad\""},
		{"empty task", Request{Name: "reader", Parent: defaultParents()}, "task is required"},
		{"depth", Request{Name: "reader", Task: "t", Parent: Parent{SessionID: "p", Model: "m", Depth: DefaultDepthLimit}}, "maximum nesting depth"},
		{"cycle", Request{Name: "reader", Task: "t", Parent: Parent{SessionID: "p", Model: "m", Depth: 1, Ancestry: []string{"reader"}}}, "delegation cycle"},
		{"unknown tool", Request{Name: "limited", Task: "t", Parent: defaultParents()}, "not active for the parent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fixture.exec.Run(context.Background(), tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("error = %v, want %q", err, tc.match)
			}
		})
	}
	disabled := newExecutorFixture(t, map[string]string{"limited": "---\ntools: [read, exec]\n---\nchild body"}, newToggleCatalog(&probeTool{name: "read"}, &probeTool{name: "exec"}))
	disabled.catalog.disable("exec")
	_, err := disabled.exec.Run(context.Background(), Request{Name: "limited", Task: "t", Parent: defaultParents()})
	if err == nil || !strings.Contains(err.Error(), `tool "exec" is not active`) {
		t.Fatalf("disabled tool error = %v", err)
	}
}

func TestExecutorChildToolsSubsetAndRevalidation(t *testing.T) {
	read := &probeTool{name: "read"}
	write := &probeTool{name: "write"}
	execTool := &probeTool{name: "exec"}
	tools := newToggleCatalog(read, write, execTool)
	fixture := newExecutorFixture(t, map[string]string{
		"limited": "---\ntools: [read]\n---\nchild body",
		"full":    "child body",
	}, tools)
	parent := defaultParents()
	definition, ok := executorCatalog(t, map[string]string{"limited": "---\ntools: [read]\n---\nchild body"}).Lookup("limited")
	if !ok {
		t.Fatal("limited definition missing")
	}
	client := Client{Client: fixture.client, Model: "test/model", Wire: "test/wire"}
	childToolsValue, err := fixture.exec.buildTools(definition, parent, client)
	if err != nil {
		t.Fatalf("buildTools: %v", err)
	}
	names := toolNames(childToolsValue.Tools())
	if strings.Join(names, ",") != "read" {
		t.Fatalf("child tools = %v", names)
	}
	if _, ok := childToolsValue.GetActive("exec"); ok {
		t.Fatal("child must not see tools outside its allowlist")
	}
	tools.disable("read")
	if _, ok := childToolsValue.GetActive("read"); ok {
		t.Fatal("parent-disabled read must fail child revalidation")
	}
	if strings.Join(toolNames(childToolsValue.Tools()), ",") != "" {
		t.Fatal("parent-disabled read must drop out of the child tool list")
	}
	full, _ := executorCatalog(t, map[string]string{"full": "child body"}).Lookup("full")
	inherited, err := fixture.exec.buildTools(full, parent, client)
	if err != nil {
		t.Fatalf("buildTools full: %v", err)
	}
	if strings.Join(toolNames(inherited.Tools()), ",") != "write,exec" {
		t.Fatalf("inherited tools = %v", toolNames(inherited.Tools()))
	}
}

func sessionFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

type subagentScriptClient struct {
	mu      sync.Mutex
	steps   []string
	results []string
	before  func(call int)
	done    string
}

func (c *subagentScriptClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	call := len(c.results)
	c.results = append(c.results, lastToolResultText(req))
	c.mu.Unlock()
	if c.before != nil {
		c.before(call)
	}
	if call < len(c.steps) {
		return subagentCall("nested_call", c.steps[call]), nil
	}
	return textAssistant(c.done), nil
}

func (c *subagentScriptClient) observed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.results...)
}

type reactivationScriptClient struct {
	mu         sync.Mutex
	steps      []string
	results    []string
	systems    []string
	advertised [][]string
	subagents  []agent.Tool
	before     func(call int)
	done       string
}

func (c *reactivationScriptClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	call := len(c.results)
	c.results = append(c.results, lastToolResultText(req))
	c.systems = append(c.systems, req.System)
	names := make([]string, 0, len(req.Tools))
	var subagentTool agent.Tool
	for _, tool := range req.Tools {
		names = append(names, tool.Name())
		if tool.Name() == SubagentToolName {
			subagentTool = tool
		}
	}
	c.advertised = append(c.advertised, names)
	c.subagents = append(c.subagents, subagentTool)
	c.mu.Unlock()
	if c.before != nil {
		c.before(call)
	}
	if call < len(c.steps) {
		return subagentCall("reactivated_call", c.steps[call]), nil
	}
	return textAssistant(c.done), nil
}

func (c *reactivationScriptClient) observed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.results...)
}

func (c *reactivationScriptClient) observedSystems() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.systems...)
}

func (c *reactivationScriptClient) observedSubagents() []agent.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agent.Tool(nil), c.subagents...)
}

func (c *reactivationScriptClient) observedAdvertised() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := make([][]string, len(c.advertised))
	for i, names := range c.advertised {
		rows[i] = append([]string(nil), names...)
	}
	return rows
}

type reactivationChainClient struct {
	mu      sync.Mutex
	next    string
	toggle  func()
	results []string
	turns   int
}

func (c *reactivationChainClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.turns++
	turn := c.turns
	c.results = append(c.results, lastToolResultText(req))
	c.mu.Unlock()
	if turn == 1 && c.next != "" {
		c.toggle()
		return subagentCall("reactivated_chain", c.next), nil
	}
	return textAssistant("chain done"), nil
}

func (c *reactivationChainClient) observed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.results...)
}

type readThenAnswerClient struct {
	answer string
}

func (c *readThenAnswerClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if lastToolResultText(req) != "" {
		return textAssistant(c.answer), nil
	}
	args, _ := json.Marshal(map[string]string{"path": "note.txt"})
	return &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{{
			Type:      agent.BlockTypeToolCall,
			ID:        "child_read_call",
			Name:      "read",
			Arguments: args,
		}},
		StopReason: "toolUse",
		Timestamp:  agent.NowMillis(),
	}, nil
}

func subagentCall(id, name string) *agent.AssistantMessage {
	args, _ := json.Marshal(map[string]string{"name": name, "task": "continue"})
	return &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{{
			Type:      agent.BlockTypeToolCall,
			ID:        id,
			Name:      SubagentToolName,
			Arguments: args,
		}},
		StopReason: "toolUse",
		Timestamp:  agent.NowMillis(),
	}
}

func TestExecutorNestedToolsUseImmediateParentView(t *testing.T) {
	read := &probeTool{name: "read"}
	execTool := &probeTool{name: "exec"}
	tools := newToggleCatalog(read, execTool, NewTool(nil))
	fixture := newExecutorFixture(t, map[string]string{
		"mid":       "---\ntools: [read, subagent]\n---\nmid body",
		"deep-exec": "---\ntools: [exec]\n---\ndeep exec body",
		"deep-read": "---\ntools: [read]\n---\ndeep read body",
	}, tools)
	mid := &subagentScriptClient{steps: []string{"deep-exec", "deep-read"}, done: "mid done"}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		if def.Name == "mid" {
			return Client{Client: mid, Model: "test/model", Wire: "test/wire"}, nil
		}
		return Client{Client: &readThenAnswerClient{answer: def.Name + " done"}, Model: "test/model", Wire: "test/wire"}, nil
	}
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "mid", Task: "go", Parent: defaultParents()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := mid.observed()
	if len(results) != 3 {
		t.Fatalf("mid calls = %d, want 3", len(results))
	}
	if !strings.Contains(results[1], `tool "exec" is not active for the parent`) {
		t.Fatalf("nested exec result = %q", results[1])
	}
	if !strings.Contains(results[2], "deep-read done") {
		t.Fatalf("nested read result = %q", results[2])
	}
	if execTool.calls != 0 {
		t.Fatalf("exec calls = %d, want 0", execTool.calls)
	}
	if read.calls == 0 {
		t.Fatal("allowed nested read never executed")
	}
	sessions := sessionFilesUnder(t, filepath.Join(fixture.root, subagentSessionsDir, "parent123"))
	if len(sessions) != 2 {
		t.Fatalf("child sessions = %v, want the mid and allowed deep sessions only", sessions)
	}
}

func TestExecutorNestedRevalidatesRootActiveGate(t *testing.T) {
	read := &probeTool{name: "read"}
	tools := newToggleCatalog(read, NewTool(nil))
	fixture := newExecutorFixture(t, map[string]string{
		"mid":       "---\ntools: [read, subagent]\n---\nmid body",
		"deep-read": "---\ntools: [read]\n---\ndeep read body",
	}, tools)
	mid := &subagentScriptClient{steps: []string{"deep-read"}, done: "mid done"}
	mid.before = func(call int) {
		if call == 0 {
			tools.disable("read")
		}
	}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		if def.Name == "mid" {
			return Client{Client: mid, Model: "test/model", Wire: "test/wire"}, nil
		}
		return Client{Client: &answerClient{answer: "deep done"}, Model: "test/model", Wire: "test/wire"}, nil
	}
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "mid", Task: "go", Parent: defaultParents()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := mid.observed()
	if len(results) != 2 {
		t.Fatalf("mid calls = %d, want 2", len(results))
	}
	if !strings.Contains(results[1], `tool "read" is not active for the parent`) {
		t.Fatalf("nested read result = %q, want the root gate rejection", results[1])
	}
	if read.calls != 0 {
		t.Fatalf("disabled root read still executed %d times", read.calls)
	}
	sessions := sessionFilesUnder(t, filepath.Join(fixture.root, subagentSessionsDir, "parent123"))
	if len(sessions) != 1 {
		t.Fatalf("child sessions = %v, want the mid session only", sessions)
	}
}

func toolNames(tools []agent.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func TestExecutorNestedDepthAndCycle(t *testing.T) {
	bodies := map[string]string{"a": "agent a body", "b": "agent b body", "c": "agent c body", "d": "agent d body", "e": "agent e body"}
	tools := newToggleCatalog(NewTool(nil))
	fixture := newExecutorFixture(t, bodies, tools)
	clients := map[int]*chainClient{}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		client := &chainClient{step: parent.Depth}
		clients[parent.Depth] = client
		return Client{Client: client, Model: "test/model", Wire: "test/wire"}, nil
	}
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "a", Task: "start", Parent: defaultParents()}); err != nil {
		t.Fatalf("depth run: %v", err)
	}
	deepest := clients[3]
	if deepest == nil || deepest.calls != 2 {
		t.Fatalf("deepest client = %+v", deepest)
	}
	if !deepest.sawDepth {
		t.Fatal("deepest child did not observe the explicit over-depth error")
	}
	for step := 0; step < 3; step++ {
		if clients[step] == nil || clients[step].calls != 2 {
			t.Fatalf("step %d client = %+v", step, clients[step])
		}
	}
	childFiles := sessionFilesUnder(t, filepath.Join(fixture.root, subagentSessionsDir, "parent123"))
	if len(childFiles) != 4 {
		t.Fatalf("nested child sessions = %d, want 4", len(childFiles))
	}
	cycle := &chainClient{step: -1}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: cycle, Model: "test/model", Wire: "test/wire"}, nil
	}
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "a", Task: "start", Parent: defaultParents()}); err != nil {
		t.Fatalf("cycle run: %v", err)
	}
	if !cycle.sawCycle || cycle.calls != 2 {
		t.Fatalf("cycle client = %+v", cycle)
	}
}

func TestExecutorInactiveRootBuiltinReactivation(t *testing.T) {
	rootCalls := 0
	var rootMu sync.Mutex
	root := NewTool(func(context.Context, string, string) agent.Result {
		rootMu.Lock()
		rootCalls++
		rootMu.Unlock()
		return agent.TextResult("root delegation reached")
	})
	read := &probeTool{name: "read"}
	execTool := &probeTool{name: "exec"}
	tools := newToggleCatalog(read, root, execTool)
	tools.disable(SubagentToolName)

	top := &reactivationScriptClient{steps: []string{"top", "top", "mid"}, done: "top done"}
	mid := &reactivationScriptClient{steps: []string{"deep-read", "deep-exec"}, done: "mid done"}
	deep := &reactivationScriptClient{done: "deep done"}
	parents := map[string]Parent{}
	fixture := newExecutorFixture(t, map[string]string{
		"top":       "top body",
		"mid":       "---\ntools: [read, subagent]\n---\nmid body",
		"deep-read": "---\ntools: [read]\n---\ndeep read body",
		"deep-exec": "---\ntools: [exec]\n---\ndeep exec body",
	}, tools)
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		parents[def.Name] = parent
		client := Client{Client: deep, Model: parent.Model, Wire: parent.WireModel, Provider: parent.Provider, ReasoningSeam: parent.ReasoningSeam}
		switch def.Name {
		case "top":
			client.Client = top
		case "mid":
			client.Client = mid
		}
		return client, nil
	}
	top.before = func(call int) {
		if call != 1 {
			return
		}
		for i := 0; i < 10; i++ {
			tools.disable(SubagentToolName)
			tools.enable(SubagentToolName)
		}
	}
	parent := defaultParents()
	parent.ReasoningSeam = true
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "top", Task: "go", Parent: parent}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	topResults := top.observed()
	if len(topResults) != 4 {
		t.Fatalf("top calls = %d, want 4", len(topResults))
	}
	if !strings.Contains(topResults[1], `unknown tool "subagent"`) {
		t.Fatalf("inactive child delegation = %q, want the blocked tool", topResults[1])
	}
	if !strings.Contains(topResults[2], "delegation cycle detected") {
		t.Fatalf("reactivated self delegation = %q, want the cycle denial", topResults[2])
	}
	if !strings.Contains(topResults[3], "mid done") {
		t.Fatalf("intermediate delegation = %q", topResults[3])
	}
	subagents := top.observedSubagents()
	if subagents[0] != nil || subagents[1] != nil {
		t.Fatalf("inactive child advertised the delegation tool: %v", subagents[:2])
	}
	if subagents[2] == nil || subagents[2] == agent.Tool(root) {
		t.Fatalf("reactivated child advertised the root builtin: %v", subagents[2])
	}
	if subagents[3] == nil || subagents[3] == agent.Tool(root) {
		t.Fatalf("post-toggle child advertised the root builtin: %v", subagents[3])
	}
	midResults := mid.observed()
	if len(midResults) != 3 {
		t.Fatalf("mid calls = %d, want 3", len(midResults))
	}
	if !strings.Contains(midResults[1], "deep done") {
		t.Fatalf("deep read delegation = %q", midResults[1])
	}
	if !strings.Contains(midResults[2], `tool "exec" is not active for the parent`) {
		t.Fatalf("deep exec delegation = %q, want the intermediate-view rejection", midResults[2])
	}
	midAdvertised := mid.observedAdvertised()
	if strings.Join(midAdvertised[0], ",") != "read,subagent" {
		t.Fatalf("mid tools = %v, want the restricted intermediate view", midAdvertised[0])
	}
	midParent, ok := parents["mid"]
	if !ok {
		t.Fatal("mid never received a client")
	}
	if midParent.Depth != 1 || strings.Join(midParent.Ancestry, ",") != "top" {
		t.Fatalf("mid parent = depth %d ancestry %v", midParent.Depth, midParent.Ancestry)
	}
	if !midParent.ReasoningSeam {
		t.Fatal("reconstructed nested wrapper dropped the reasoning seam")
	}
	deepParent, ok := parents["deep-read"]
	if !ok {
		t.Fatal("deep read never received a client")
	}
	if deepParent.Depth != 2 || strings.Join(deepParent.Ancestry, ",") != "top,mid" {
		t.Fatalf("deep parent = depth %d ancestry %v", deepParent.Depth, deepParent.Ancestry)
	}
	if !deepParent.ReasoningSeam {
		t.Fatal("intermediate nested wrapper dropped the reasoning seam")
	}
	if _, ok := activeTool(deepParent.Tools, "read"); !ok {
		t.Fatal("deep parent view lost the allowed read tool")
	}
	if _, ok := activeTool(deepParent.Tools, "exec"); ok {
		t.Fatal("deep parent view leaked a tool outside the intermediate allowlist")
	}
	execParent, ok := parents["deep-exec"]
	if !ok {
		t.Fatal("deep exec never received a client")
	}
	if _, ok := activeTool(execParent.Tools, "exec"); ok {
		t.Fatal("rejected deep exec view leaked exec")
	}
	systems := deep.observedSystems()
	if len(systems) != 1 {
		t.Fatalf("deep requests = %d, want 1", len(systems))
	}
	deepIndex := strings.Index(systems[0], "deep read body")
	midIndex := strings.Index(systems[0], "mid body")
	topIndex := strings.Index(systems[0], "top body")
	rootIndex := strings.Index(systems[0], "parent instructions")
	if deepIndex < 0 || midIndex < 0 || topIndex < 0 || rootIndex < 0 || !(deepIndex < midIndex && midIndex < topIndex && topIndex < rootIndex) {
		t.Fatalf("deep system chain = %q", systems[0])
	}
	if rootCalls != 0 {
		t.Fatalf("root builtin delegation ran %d times", rootCalls)
	}
	sessions := sessionFilesUnder(t, filepath.Join(fixture.root, subagentSessionsDir, "parent123"))
	if len(sessions) != 3 {
		t.Fatalf("child sessions = %v, want top, mid and deep read only", sessions)
	}
}

func TestExecutorReactivationMaintainsDepthLimit(t *testing.T) {
	rootCalls := 0
	var rootMu sync.Mutex
	root := NewTool(func(context.Context, string, string) agent.Result {
		rootMu.Lock()
		rootCalls++
		rootMu.Unlock()
		return agent.TextResult("root delegation reached")
	})
	tools := newToggleCatalog(root)
	tools.disable(SubagentToolName)
	bodies := map[string]string{}
	next := map[string]string{"a": "b", "b": "c", "c": "d", "d": "e"}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		bodies[name] = name + " body"
	}
	fixture := newExecutorFixture(t, bodies, tools)
	depths := map[string]int{}
	clients := map[string]*reactivationChainClient{}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		depths[def.Name] = parent.Depth
		client := &reactivationChainClient{next: next[def.Name]}
		client.toggle = func() {
			for i := 0; i < 10; i++ {
				tools.disable(SubagentToolName)
				tools.enable(SubagentToolName)
			}
		}
		clients[def.Name] = client
		return Client{Client: client, Model: parent.Model, Wire: parent.WireModel, Provider: parent.Provider}, nil
	}
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "a", Task: "go", Parent: defaultParents()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for name, want := range map[string]int{"a": 0, "b": 1, "c": 2, "d": 3} {
		if depths[name] != want {
			t.Fatalf("%s parent depth = %d, want %d", name, depths[name], want)
		}
	}
	if depth, ok := depths["e"]; ok {
		t.Fatalf("depth-limit child e received a client at parent depth %d", depth)
	}
	deepest := clients["d"].observed()
	if len(deepest) != 2 || !strings.Contains(deepest[1], "maximum nesting depth") {
		t.Fatalf("deepest results = %v, want the depth-limit result", deepest)
	}
	if rootCalls != 0 {
		t.Fatalf("root builtin delegation ran %d times", rootCalls)
	}
	sessions := sessionFilesUnder(t, filepath.Join(fixture.root, subagentSessionsDir, "parent123"))
	if len(sessions) != 4 {
		t.Fatalf("child sessions = %d, want 4", len(sessions))
	}
}

type chainClient struct {
	step     int
	calls    int
	sawCycle bool
	sawDepth bool
}

func (c *chainClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.calls++
	if last := lastToolResultText(req); last != "" {
		switch {
		case strings.Contains(last, "cycle detected"):
			c.sawCycle = true
			return textAssistant("cycle observed"), nil
		case strings.Contains(last, "maximum nesting depth"):
			c.sawDepth = true
			return textAssistant("depth observed"), nil
		default:
			return textAssistant("chain done"), nil
		}
	}
	name := "a"
	if c.step >= 0 {
		names := []string{"b", "c", "d", "e"}
		if c.step >= len(names) {
			return textAssistant("unexpected step"), nil
		}
		name = names[c.step]
	}
	args, _ := json.Marshal(map[string]string{"name": name, "task": "continue"})
	return &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{{
			Type:      agent.BlockTypeToolCall,
			ID:        "call_" + name,
			Name:      SubagentToolName,
			Arguments: args,
		}},
		StopReason: "toolUse",
		Timestamp:  agent.NowMillis(),
	}, nil
}

func textAssistant(text string) *agent.AssistantMessage {
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: text}},
		StopReason: "stop",
		Timestamp:  agent.NowMillis(),
	}
}

func lastToolResultText(req *agent.TurnRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		message := req.Messages[i]
		if message == nil || message.ToolResult == nil {
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

func TestExecutorOutputBudget(t *testing.T) {
	lines := make([]string, 3000)
	for i := range lines {
		lines[i] = strings.Repeat("x", 30)
	}
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	fixture.client.reps = []string{strings.Join(lines, "\n")}
	res, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated || res.FullOutputPath == "" {
		t.Fatalf("truncation = %+v", res)
	}
	if strings.Count(res.Answer, "\n")+1 > childMaxLines+2 {
		t.Fatalf("bounded answer lines = %d", strings.Count(res.Answer, "\n")+1)
	}
	info, err := os.Stat(res.FullOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode = %v, want 0600", info.Mode().Perm())
	}
	full, err := os.ReadFile(res.FullOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(full) != strings.Join(lines, "\n") {
		t.Fatal("artifact does not retain the full source")
	}
	if !strings.Contains(res.Answer, "Full output:") {
		t.Fatalf("answer lacks the artifact reference: %q", res.Answer)
	}
}

func TestExecutorConcurrentRunsAreIsolated(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"one": "one body", "two": "two body"}, newToggleCatalog())
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: &answerClient{answer: def.Name + " answer"}, Model: "test/model", Wire: "test/wire"}, nil
	}
	type outcome struct {
		res Result
		err error
	}
	results := make(chan outcome, 2)
	for _, name := range []string{"one", "two"} {
		name := name
		go func() {
			res, err := fixture.exec.Run(context.Background(), Request{Name: name, Task: "task " + name, Parent: defaultParents()})
			results <- outcome{res: res, err: err}
		}()
	}
	seen := map[string]string{}
	answers := map[string]string{}
	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatalf("Run: %v", out.err)
		}
		seen[out.res.Name] = out.res.SessionPath
		answers[out.res.Name] = out.res.Answer
	}
	if seen["one"] == seen["two"] || seen["one"] == "" || seen["two"] == "" {
		t.Fatalf("sessions = %v", seen)
	}
	if answers["one"] != "one answer" || answers["two"] != "two answer" {
		t.Fatalf("answers = %v", answers)
	}
	for _, path := range seen {
		loadChildSession(t, path)
	}
}

type answerClient struct {
	answer string
}

func (c *answerClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if onText != nil {
		onText(c.answer)
	}
	return textAssistant(c.answer), nil
}

func TestExecutorToolProgressWithoutDispatcher(t *testing.T) {
	tools := newToggleCatalog(&probeTool{name: "read"})
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, tools)
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: &readThenAnswerClient{answer: "done"}, Model: "test/model", Wire: "test/wire"}, nil
	}
	var events []Event
	if _, err := fixture.exec.Run(context.Background(), Request{
		Name:   "reader",
		Task:   "t",
		Parent: defaultParents(),
		OnEvent: func(event Event) {
			events = append(events, event)
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Kind+":"+event.Name]++
	}
	if counts[EventStart+":reader"] != 1 || counts[EventDone+":reader"] != 1 {
		t.Fatalf("lifecycle events = %v", events)
	}
	if counts[EventToolCall+":read"] == 0 || counts[EventToolResult+":read"] != 1 {
		t.Fatalf("tool progress events = %v", events)
	}
}

func TestExecutorCancellationClosesSession(t *testing.T) {
	started := make(chan struct{})
	blocking := &blockingClient{started: started}
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: blocking, Model: "test/model", Wire: "test/wire"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan Result, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := fixture.exec.Run(ctx, Request{Name: "reader", Task: "t", Parent: defaultParents()})
		result <- res
		errs <- err
	}()
	<-started
	cancel()
	res := <-result
	err := <-errs
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if res.SessionPath == "" {
		t.Fatal("canceled run must report its child session")
	}
	loader := loadChildSession(t, res.SessionPath)
	if len(loader.Entries()) == 0 {
		t.Fatal("canceled child session has no entries")
	}
	store, err := session.NewStore(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(res.SessionPath, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("canceled child session lock was not released: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

type blockingClient struct {
	once    sync.Once
	started chan struct{}
}

func (c *blockingClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestExecutorOverflowCompactsAndRetries(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	client := &scriptClient{
		errs: []error{errors.New("prompt is too long")},
		reps: []string{"", "recovered answer"},
	}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: client, Model: "test/model", Wire: "test/wire"}, nil
	}
	fixture.exec.deps.IsContextOverflow = retryMarker
	res, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !fixture.preparer.forced.Load() {
		t.Fatal("overflow did not force compaction")
	}
	if res.Answer != "recovered answer" {
		t.Fatalf("answer = %q", res.Answer)
	}
	if client.callCount() != 2 {
		t.Fatalf("client calls = %d, want 2", client.callCount())
	}
	loader := loadChildSession(t, res.SessionPath)
	found := false
	for _, entry := range loader.Entries() {
		if entry.EntryType() == session.EntryTypeCompaction {
			found = true
		}
	}
	if !found {
		t.Fatal("child session lacks the overflow compaction entry")
	}
}

func TestExecutorPersistsDrainedCompactions(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	summary := json.RawMessage(`{"strategy":"test"}`)
	fixture.preparer.drained = []*agent.CompactionEntry{{
		Summary:          summary,
		FirstKeptEntryID: "entry1",
		TokensBefore:     120,
	}}
	res, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	loader := loadChildSession(t, res.SessionPath)
	found := false
	for _, entry := range loader.Entries() {
		if compaction, ok := entry.(*session.CompactionEntry); ok {
			if compaction.Summary != string(summary) || compaction.FirstKeptEntryID != "entry1" || compaction.TokensBefore != 120 {
				t.Fatalf("compaction = %+v", compaction)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("drained compaction was not persisted")
	}
}

func TestExecutorRetryUsesInjectedRetryFunc(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	client := &flakyClient{}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: client, Model: "test/model", Wire: "test/wire"}, nil
	}
	fixture.exec.deps.Retry = func(ctx context.Context, produce func(context.Context) (*agent.AssistantMessage, error), policy agent.RetryPolicy, callbacks *agent.RetryCallbacks) (*agent.AssistantMessage, error) {
		message, err := produce(ctx)
		if err != nil {
			message, err = produce(ctx)
		}
		return message, err
	}
	res, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "retried answer" || client.attemptsValue() != 2 {
		t.Fatalf("answer = %q attempts = %d", res.Answer, client.attemptsValue())
	}
}

type flakyClient struct {
	mu       sync.Mutex
	attempts int
}

func (c *flakyClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.attempts++
	attempt := c.attempts
	c.mu.Unlock()
	if attempt == 1 {
		return nil, errors.New("transient failure")
	}
	if onText != nil {
		onText("retried answer")
	}
	return textAssistant("retried answer"), nil
}

func (c *flakyClient) attemptsValue() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}

func TestExecutorModelErrorIsBoundedAndHonest(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	client := &scriptClient{errs: []error{errors.New("provider exploded: " + strings.Repeat("x", 60*1024))}}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: client, Model: "test/model", Wire: "test/wire"}, nil
	}
	res, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()})
	if err == nil {
		t.Fatal("model error was swallowed")
	}
	if !res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Answer, "provider exploded") {
		t.Fatalf("answer = %q", res.Answer)
	}
	if !res.Truncated || res.FullOutputPath == "" {
		t.Fatalf("oversized error was not bounded: %+v", res)
	}
	if len(res.Answer) > childMaxBytes+1024 {
		t.Fatalf("answer is not bounded: %d bytes", len(res.Answer))
	}
	loadChildSession(t, res.SessionPath)
}

func TestExecutorEventsReportProgress(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	var events []Event
	_, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents(), OnEvent: func(event Event) {
		events = append(events, event)
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	if strings.Join(kinds, ",") != EventStart+","+EventDone {
		t.Fatalf("events = %v", kinds)
	}
}

func retryMarker(message string) bool {
	return strings.Contains(message, "too long")
}
