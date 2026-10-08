package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/agents"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

func testDefinitionCatalog() agents.Catalog {
	return agents.NewCatalog(content.Snapshot{Agents: map[string]content.AgentRef{
		"reader": {Name: "reader", Content: "---\ndescription: reads files\n---\nYou are the child agent.", Tier: content.TierBundle, Package: "test", Path: "reader.md", Origin: "bundle:agents"},
		"plain":  {Name: "plain", Content: "plain body", Tier: content.TierUser, Package: "user", Path: "plain.md", Origin: "user:agents"},
		"broken": {Name: "broken", Content: "---\nname: x\n---\n   "},
	}})
}

type cliChildPreparer struct {
	mu      sync.Mutex
	forced  bool
	drained []*agent.CompactionEntry
}

func (p *cliChildPreparer) Prepare(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (p *cliChildPreparer) ObserveRequest(time.Time) {}

func (p *cliChildPreparer) ObserveResponse(*agent.AssistantMessage) {}

func (p *cliChildPreparer) ForceSafety() {
	p.mu.Lock()
	p.forced = true
	p.mu.Unlock()
}

func (p *cliChildPreparer) DrainCompactions() []*agent.CompactionEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.drained
	p.drained = nil
	return out
}

func newTestAgentExecutor(t *testing.T, catalog agents.Catalog, client agent.Client) (*agents.Executor, *cliChildPreparer) {
	t.Helper()
	preparer := &cliChildPreparer{}
	tools := extensions.NewToolCatalog()
	exec, err := agents.NewExecutor(agents.Dependencies{
		Catalog:     func() agents.Catalog { return catalog },
		ParentTools: func() agent.ToolCatalog { return tools },
		Client: func(definition agents.Definition, parent agents.Parent) (agents.Client, error) {
			return agents.Client{Client: client, Model: "test/model", Wire: "test/model", Provider: "openrouter"}, nil
		},
		Preparer:     func(string, string, agent.Client) (agents.Preparer, error) { return preparer, nil },
		SessionsRoot: t.TempDir(),
		Cwd:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return exec, preparer
}

func boundTestHost(t *testing.T, cwd string) (*hostRuntime, *session.Session) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	catalog := extensions.NewToolCatalog()
	host := newHostRuntime(context.Background(), cwd, nil, catalog)
	host.setModel(models.NewRegistry(), "test/model", "test/model", "openrouter")
	host.setSystem("parent system")
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	return host, sess
}

type subagentOverrideTool struct {
	calls int
}

func (t *subagentOverrideTool) Name() string            { return agents.SubagentToolName }
func (t *subagentOverrideTool) Description() string     { return "override" }
func (t *subagentOverrideTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *subagentOverrideTool) Exec(context.Context, json.RawMessage) agent.Result {
	t.calls++
	return agent.TextResult("override result")
}

type signalCommandContext struct {
	fakeCommandContext
}

func (c *signalCommandContext) Signal() context.Context { return context.Background() }

func TestAgentCommandSlotLifecycle(t *testing.T) {
	var nilSlot *agentCommandSlot
	if registered := nilSlot.bind(extensions.NewCommandCatalog()); registered != "" {
		t.Fatalf("nil slot bind = %q", registered)
	}
	if err := nilSlot.dispatch(nil, ""); err == nil {
		t.Fatal("nil slot dispatch succeeded")
	}
	if registered := newAgentCommandSlot().bind(nil); registered != "" {
		t.Fatalf("nil catalog bind = %q", registered)
	}
	commands := extensions.NewCommandCatalog()
	slot := newAgentCommandSlot()
	registered := slot.bind(commands)
	if registered != agentCommandName {
		t.Fatalf("registered = %q", registered)
	}
	if again := slot.bind(commands); again != registered {
		t.Fatalf("second bind = %q", again)
	}
	command, ok := commands.Get(agentCommandName)
	if !ok {
		t.Fatal("canonical command missing")
	}
	if err := command.Handler(&fakeCommandContext{}, ""); err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatalf("unset dispatch = %v", err)
	}
	called := false
	slot.set(func(ctx sdk.CommandContext, args string) error {
		called = args == "tail"
		return nil
	})
	if err := command.Handler(&fakeCommandContext{}, "tail"); err != nil || !called {
		t.Fatalf("bound dispatch: %v called=%v", err, called)
	}
}

func TestSubagentToolSlotLifecycle(t *testing.T) {
	var nilSlot *subagentToolSlot
	nilSlot.bind(nil)
	nilSlot.set(nil)
	nilSlot.set(func(context.Context, string, string) agent.Result { return agent.Result{} })
	if nilSlot.toolRef() != nil {
		t.Fatal("nil slot exposed a tool")
	}
	slot := newSubagentToolSlot()
	slot.bind(nil)
	catalog := extensions.NewToolCatalog()
	slot.bind(catalog)
	if slot.toolRef() == nil {
		t.Fatal("slot tool missing")
	}
	if _, ok := catalog.Get(agents.SubagentToolName); !ok {
		t.Fatal("subagent tool not registered")
	}
	override := &subagentOverrideTool{}
	catalog2 := extensions.NewToolCatalog()
	if err := catalog2.Register(override); err != nil {
		t.Fatal(err)
	}
	slot.bind(catalog2)
	got, _ := catalog2.Get(agents.SubagentToolName)
	if got != override {
		t.Fatal("existing override was replaced")
	}
}

func TestListAgentCatalogFormats(t *testing.T) {
	var out bytes.Buffer
	if err := listAgentCatalog(&out, testDefinitionCatalog()); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "reader\treads files") {
		t.Fatalf("listing = %q", text)
	}
	if !strings.Contains(text, "plain\tplain body") {
		t.Fatalf("listing = %q", text)
	}
	if strings.Contains(text, "broken") {
		t.Fatalf("malformed definition listed: %q", text)
	}
	if err := listAgentCatalog(nil, testDefinitionCatalog()); err != nil {
		t.Fatal(err)
	}
	if err := listAgentCatalog(failingWriter{}, testDefinitionCatalog()); err == nil {
		t.Fatal("write failure was not reported")
	}
}

type recordingSubagentProgress struct {
	start    string
	events   []string
	result   agents.Result
	err      error
	finishes int
}

func (p *recordingSubagentProgress) Start(name, identity string) {
	p.start = name + "|" + identity
}

func (p *recordingSubagentProgress) Event(text string) {
	p.events = append(p.events, text)
}

func (p *recordingSubagentProgress) Finish(res agents.Result, err error) {
	p.result = res
	p.err = err
	p.finishes++
}

func TestWriterSubagentProgress(t *testing.T) {
	var out bytes.Buffer
	progress := &writerSubagentProgress{w: &out}
	progress.Start("reader", "tier=bundle")
	progress.Event("tool: read")
	progress.Finish(agents.Result{Name: "reader", Answer: "child answer"}, nil)
	progress.Finish(agents.Result{Name: "reader"}, errors.New("boom"))
	text := out.String()
	for _, want := range []string{"agent reader: tier=bundle", "tool: read", "child answer", "failed: boom"} {
		if !strings.Contains(text, want) {
			t.Fatalf("progress = %q, want %q", text, want)
		}
	}
	(&writerSubagentProgress{}).Start("a", "b")
	empty := &writerSubagentProgress{w: &bytes.Buffer{}}
	empty.Finish(agents.Result{Name: "reader"}, nil)
}

func TestTUISubagentProgress(t *testing.T) {
	block := interactive.NewSubagentBlock("reader", nil, "", nil)
	progress := &tuiSubagentProgress{block: block}
	progress.Start("reader", "tier=bundle")
	progress.Event("   ")
	progress.Event("tool: read")
	progress.Finish(agents.Result{Name: "reader", Answer: "child answer"}, nil)
	if progress.block == nil {
		t.Fatal("block removed")
	}
	progress.Finish(agents.Result{Name: "reader"}, errors.New("boom"))
	for i := 0; i < 260; i++ {
		progress.add("line")
	}
	if len(progress.lines) != 200 {
		t.Fatalf("progress lines = %d, want the cap", len(progress.lines))
	}
	bare := &tuiSubagentProgress{}
	bare.Event("x")
	bare.Finish(agents.Result{}, errors.New("boom"))
}

func TestEmitAgentProgress(t *testing.T) {
	emitAgentProgress(nil, agents.Event{Kind: agents.EventToolCall, Name: "read"})
	progress := &recordingSubagentProgress{}
	emitAgentProgress(progress, agents.Event{Kind: agents.EventDone})
	if len(progress.events) != 0 {
		t.Fatalf("done event became progress: %v", progress.events)
	}
	emitAgentProgress(progress, agents.Event{Kind: agents.EventToolCall, Name: "read"})
	if len(progress.events) != 1 || progress.events[0] != "tool: read" {
		t.Fatalf("events = %v", progress.events)
	}
}

func TestHandleAgentCommandFallbackContext(t *testing.T) {
	catalog := testDefinitionCatalog()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("child answer")}}
	exec, _ := newTestAgentExecutor(t, catalog, client)
	host, sess := boundTestHost(t, t.TempDir())
	var out bytes.Buffer
	if err := handleAgentCommand(&signalCommandContext{}, catalog, exec, host, &out, "reader task text"); err != nil {
		t.Fatalf("handleAgentCommand: %v", err)
	}
	host.waitCallbacks()
	if !strings.Contains(out.String(), "child answer") {
		t.Fatalf("output = %q", out.String())
	}
	if client.lastUserText() != "task text" {
		t.Fatalf("child task = %q", client.lastUserText())
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
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
		t.Fatal("fallback command did not record the subagent result")
	}
	if err := handleAgentCommand(&signalCommandContext{}, catalog, nil, host, &out, "reader task"); err == nil {
		t.Fatal("nil executor accepted")
	}
	if err := handleAgentCommand(&signalCommandContext{}, catalog, exec, nil, &out, "reader task"); err == nil {
		t.Fatal("nil host accepted")
	}
}

type queuedDispatch struct {
	mu   sync.Mutex
	jobs []func()
}

func (q *queuedDispatch) enqueue(job func()) bool {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	q.mu.Unlock()
	return true
}

func (q *queuedDispatch) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

func (q *queuedDispatch) run() {
	for {
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.mu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.mu.Unlock()
		job()
	}
}

type progressRunnerCommandContext struct {
	fakeCommandContext
	signal   context.Context
	progress *recordingSubagentProgress
}

func (c *progressRunnerCommandContext) Signal() context.Context { return c.signal }

func (c *progressRunnerCommandContext) runSubagentCommand(_ string, run func(context.Context, subagentProgress) error) error {
	c.progress = &recordingSubagentProgress{}
	return run(c.signal, c.progress)
}

func TestDirectAgentFinishIsGenerationScoped(t *testing.T) {
	host, _, store := newOwnedTestHost(t)
	dispatch := &queuedDispatch{}
	host.setLifecycle(hostLifecycle{dispatch: dispatch.enqueue})
	catalog := testDefinitionCatalog()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("child answer")}}
	exec, _ := newTestAgentExecutor(t, catalog, client)
	command := &progressRunnerCommandContext{signal: context.Background()}
	if err := handleAgentCommand(command, catalog, exec, host, io.Discard, "reader hello"); err != nil {
		t.Fatalf("handleAgentCommand: %v", err)
	}
	if command.progress == nil {
		t.Fatal("the command did not expose progress")
	}
	if command.progress.finishes != 0 || dispatch.pending() == 0 {
		t.Fatalf("presentation ran outside owned delivery: finishes=%d pending=%d", command.progress.finishes, dispatch.pending())
	}
	next, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	host.bindSession(next, &sessionRecorder{next}, next.ID(), next.Path(), t.TempDir(), "")
	dispatch.run()
	if command.progress.finishes != 0 {
		t.Fatalf("stale finish presented on the new viewport: %+v", command.progress)
	}
}

func TestDirectAgentCancellationPresentsFailureOnOwnedViewport(t *testing.T) {
	host, _, _ := newOwnedTestHost(t)
	dispatch := &queuedDispatch{}
	host.setLifecycle(hostLifecycle{dispatch: dispatch.enqueue})
	catalog := testDefinitionCatalog()
	client := &ownershipTextClient{started: make(chan struct{})}
	exec, _ := newTestAgentExecutor(t, catalog, client)
	signal, cancelSignal := context.WithCancel(context.Background())
	command := &progressRunnerCommandContext{signal: signal}
	errs := make(chan error, 1)
	go func() {
		errs <- handleAgentCommand(command, catalog, exec, host, io.Discard, "reader hello")
	}()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("direct /agent never reached the child model")
	}
	cancelSignal()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled direct /agent did not return")
	}
	if command.progress == nil {
		t.Fatal("the command did not expose progress")
	}
	if command.progress.finishes != 0 || dispatch.pending() == 0 {
		t.Fatalf("presentation ran outside owned delivery: finishes=%d pending=%d", command.progress.finishes, dispatch.pending())
	}
	dispatch.run()
	if command.progress.finishes != 1 {
		t.Fatalf("canceled finish count = %d, want 1 on the owned viewport", command.progress.finishes)
	}
	if command.progress.err == nil {
		t.Fatal("canceled finish reported success")
	}
}

func TestPersistAgentResultErrorStatus(t *testing.T) {
	host, sess := boundTestHost(t, t.TempDir())
	handle := host.snapshot()
	if err := persistAgentResult(host, handle, agents.Result{Name: "reader", Answer: "partial"}, errors.New(strings.Repeat("e", 500))); err != nil {
		t.Fatalf("persistAgentResult: %v", err)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loader.Entries() {
		custom, ok := entry.(*session.CustomEntry)
		if !ok || custom.CustomType != subagentResultCustomType {
			continue
		}
		var payload subagentResultEntry
		if err := json.Unmarshal(custom.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Status != "error" {
			t.Fatalf("status = %q", payload.Status)
		}
		if len([]rune(payload.Result)) > 204 {
			t.Fatalf("result not bounded: %d", len([]rune(payload.Result)))
		}
	}
}

func TestRunHostSubagentUnavailable(t *testing.T) {
	if res := runHostSubagent(context.Background(), nil, nil, "reader", "task"); !res.IsError {
		t.Fatal("nil executor accepted")
	}
	host, _ := boundTestHost(t, t.TempDir())
	if res := runHostSubagent(context.Background(), nil, host, "reader", "task"); !res.IsError {
		t.Fatal("nil executor accepted with a host")
	}
	empty := newHostRuntime(context.Background(), t.TempDir(), nil, nil)
	exec, _ := newTestAgentExecutor(t, testDefinitionCatalog(), &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}})
	if res := runHostSubagent(context.Background(), exec, empty, "reader", "task"); !res.IsError || !strings.Contains(blockText(res.Content), "no active parent session") {
		t.Fatalf("sessionless result = %+v", res)
	}
	res := runHostSubagent(context.Background(), exec, host, "reader", "task")
	if res.IsError {
		t.Fatalf("hosted result = %+v", res)
	}
}

func TestChildPreparerAdapterAndFactory(t *testing.T) {
	adapter := childPreparerAdapter{testPreparer(t)}
	adapter.ForceSafety()
	if entries := adapter.DrainCompactions(); len(entries) != 0 {
		t.Fatalf("drained = %v", entries)
	}
	factory := childPreparerFactory(nil, nil)
	if _, err := factory("m", "m", &capturingClient{}); err == nil {
		t.Fatal("nil config accepted")
	}
}

func TestFixedReasoningClientNilBase(t *testing.T) {
	if client := newFixedReasoningClient(nil, openrouter.ReasoningDirective{}); client != nil {
		t.Fatalf("nil base produced %v", client)
	}
}

func TestChildReasoningDirectiveFallbacks(t *testing.T) {
	registry := models.NewRegistry()
	registry.Register("plain/model", models.ModelInfo{ID: "plain/model", Provider: "openrouter"})
	if _, ok := childReasoningDirective(registry, nil, "plain/model", "high"); ok {
		t.Fatal("effort applied to a model without reasoning")
	}
	if _, ok := childReasoningDirective(nil, nil, "missing/model", "high"); ok {
		t.Fatal("unknown model produced a directive")
	}
	registry.Register("reason/model", models.ModelInfo{ID: "reason/model", Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}})
	directive, ok := childReasoningDirective(registry, nil, "reason/model", "off")
	if !ok || !directive.Disable {
		t.Fatalf("off directive = %+v ok=%v", directive, ok)
	}
	directive, ok = childReasoningDirective(registry, nil, "reason/model", "max")
	if !ok || directive.Effort != "max" {
		t.Fatalf("effort directive = %+v ok=%v", directive, ok)
	}
}

func TestChildClientForNoModelOrClient(t *testing.T) {
	if _, err := childClientFor(nil, nil, nil, nil, &capturingClient{}, true, "openrouter", agents.Definition{Name: "reader", Body: "body"}, agents.Parent{}); err == nil {
		t.Fatal("empty parent model accepted")
	}
	registry := models.NewRegistry()
	_, err := childClientFor(nil, nil, registry, nil, nil, true, "openrouter", agents.Definition{Name: "reader", Body: "body"}, agents.Parent{Model: "m", WireModel: "m", Provider: "openrouter"})
	if err == nil || !strings.Contains(err.Error(), "no parent client") {
		t.Fatalf("missing client error = %v", err)
	}
}

func TestClampText(t *testing.T) {
	long := strings.Repeat("x", 300)
	done := clampText(long)
	if len(done) != 203 || !strings.HasSuffix(done, "...") {
		t.Fatalf("clamped length = %d", len(done))
	}
	if got := clampText("short"); got != "short" {
		t.Fatalf("short = %q", got)
	}
}

func TestHandleAgentCommandErrors(t *testing.T) {
	catalog := testDefinitionCatalog()
	exec, _ := newTestAgentExecutor(t, catalog, &capturingClient{})
	host, _ := boundTestHost(t, t.TempDir())
	var out bytes.Buffer
	if err := handleAgentCommand(&signalCommandContext{}, catalog, exec, host, &out, "missing task"); err == nil || !strings.Contains(err.Error(), "no agent named") {
		t.Fatalf("unknown error = %v", err)
	}
	if err := handleAgentCommand(&signalCommandContext{}, catalog, exec, host, &out, "broken task"); err == nil || !strings.Contains(err.Error(), `agent "broken"`) {
		t.Fatalf("malformed error = %v", err)
	}
	if err := handleAgentCommand(&signalCommandContext{}, catalog, exec, host, &out, "reader"); err == nil || !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("missing task error = %v", err)
	}
}
