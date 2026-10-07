package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type hostTuiFixture struct {
	cwd      string
	deps     *Deps
	rd       *runDeps
	lineUI   *ui.LineUI
	terminal *fakeBridgeTerminal
	runtime  *extensions.Runtime
	host     *hostRuntime
	sess     *session.Session
	store    *session.Store
}

func newHostTuiFixture(t *testing.T, client agent.Client, ext sdk.Extension) *hostTuiFixture {
	t.Helper()
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	registry := extensions.NewRegistry()
	if ext != nil {
		if err := registry.Register(ext); err != nil {
			t.Fatal(err)
		}
	}
	runtime := extensions.NewRuntime(registry)
	catalog := extensions.NewToolCatalog()
	uiRegistry := extensionui.NewRegistry()
	modelReg := models.NewRegistry()
	host := newHostRuntime(context.Background(), cwd, nil, catalog)
	api := extensions.NewAPI(extensions.APIOptions{Catalog: catalog, UI: uiRegistry, Host: host.hostOptions()})
	host.bindAPI(api)
	host.setModel(modelReg, "test/model", "test/model", "openrouter")
	host.setSystem("host system prompt")
	host.setWindow(128000)
	recorder := &sessionRecorder{sess}
	host.bindSession(sess, recorder, sess.ID(), sess.Path(), cwd, "")
	runtime.SetAPI(func() sdk.API { return api })
	runtime.SetUIRegistry(uiRegistry)
	runtime.SetContext(func() sdk.HandlerContext { return host.context() })
	if err := runtime.Start(); err != nil {
		t.Fatal(err)
	}
	terminal := newFakeBridgeTerminal()
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	rd := &runDeps{
		model:       "test/model",
		wireModel:   "test/model",
		system:      "host system prompt",
		sessionPath: sess.Path(),
		client:      client,
		recorder:    recorder,
		stdout:      &stdout,
		stderr:      &stderr,
		hooks:       runtime.Dispatcher(),
		commands:    extensions.NewCommandCatalog(),
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return runtime.HandlerContext(signal)
		},
		catalog:       catalog,
		host:          host,
		modelRegistry: modelReg,
		uiRegistry:    uiRegistry,
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	return &hostTuiFixture{
		cwd:      cwd,
		deps:     deps,
		rd:       rd,
		lineUI:   lineUI,
		terminal: terminal,
		runtime:  runtime,
		host:     host,
		sess:     sess,
		store:    store,
	}
}

func (f *hostTuiFixture) start(ctx context.Context) chan error {
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, f.deps, f.rd, f.lineUI, ui.TUIModeRegular, f.cwd, f.cwd, nil, bridgeTerminalFactory(f.terminal), f.runtime, nil)
	}()
	return done
}

func waitUntil(t *testing.T, timeout time.Duration, message string, probe func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probe() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(message)
}

func TestTuiHostLiveAppendUsesEntryRenderer(t *testing.T) {
	extension := &hostHookExtension{
		id:       "live-entry",
		contexts: []sdk.HandlerContext{},
		entryRenderer: func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component {
			return replayRendererComponent{text: "LIVE-NOTE:" + entry.CustomType}
		},
	}
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		if call == 0 {
			return nil, ctx.AppendEntry("note", map[string]string{"k": "v"})
		}
		return nil, nil
	}
	fixture := newHostTuiFixture(t, &gatedTurnClient{}, extension)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)
	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	fixture.terminal.SendInput("go")
	fixture.terminal.SendInput("\r")
	output := waitForOutputSettled(t, fixture.terminal, "LIVE-NOTE:note", 5*time.Second)
	if !strings.Contains(output, "LIVE-NOTE:note") {
		t.Fatalf("live entry renderer output missing:\n%s", output)
	}
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return")
	}
	loader, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	var persisted *session.CustomEntry
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomEntry); ok && custom.CustomType == "note" {
			persisted = custom
		}
	}
	if persisted == nil {
		t.Fatal("live custom entry was not persisted")
	}
	if string(persisted.Data) != `{"k":"v"}` {
		t.Fatalf("persisted data = %s", persisted.Data)
	}
	transcript, _ := projectTranscript(loader)
	for _, item := range transcript {
		if item.Kind == "custom" && item.CustomType == "note" {
			return
		}
	}
	t.Fatal("persisted custom entry is missing from the replay transcript")
}

type gatedTurnClient struct {
	mu       sync.Mutex
	calls    int
	gates    []chan struct{}
	canceled []bool
	returned []bool
}

func newGatedTurnClient(gates ...chan struct{}) *gatedTurnClient {
	return &gatedTurnClient{gates: gates}
}

func (c *gatedTurnClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	index := c.calls
	c.calls++
	c.canceled = append(c.canceled, false)
	c.returned = append(c.returned, false)
	var gate chan struct{}
	if index < len(c.gates) {
		gate = c.gates[index]
	}
	c.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			c.mu.Lock()
			c.canceled[index] = true
			c.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	c.mu.Lock()
	c.returned[index] = true
	c.mu.Unlock()
	return textStop("turn-" + itoaIndex(index)), nil
}

func itoaIndex(index int) string {
	return string(rune('0' + index))
}

func (c *gatedTurnClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *gatedTurnClient) wasCanceled(index int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return index < len(c.canceled) && c.canceled[index]
}

func (c *gatedTurnClient) wasReturned(index int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return index < len(c.returned) && c.returned[index]
}

func TestTuiHostAbortIgnoresStaleContextsAndCancelsCurrentTurn(t *testing.T) {
	second := make(chan struct{})
	third := make(chan struct{})
	client := newGatedTurnClient(nil, second, third)
	extension := &hostHookExtension{id: "abort-hooks", contexts: []sdk.HandlerContext{}}
	fixture := newHostTuiFixture(t, client, extension)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)
	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}

	fixture.terminal.SendInput("first")
	fixture.terminal.SendInput("\r")
	waitUntil(t, 5*time.Second, "first turn did not complete", func() bool {
		return extension.contextCount() >= 1 && client.wasReturned(0)
	})

	fixture.terminal.SendInput("second")
	fixture.terminal.SendInput("\r")
	waitUntil(t, 5*time.Second, "second turn did not reach the client", func() bool {
		return client.callCount() >= 2
	})
	stale := extension.contextAt(0)
	if stale == nil {
		t.Fatal("missing first-turn context")
	}
	stale.Abort()
	assertNever(t, 200*time.Millisecond, "a stale context aborted the new turn", func() bool {
		return client.wasCanceled(1) || client.wasReturned(1)
	})
	close(second)
	waitUntil(t, 5*time.Second, "second turn did not complete after release", func() bool {
		return client.wasReturned(1)
	})

	fixture.terminal.SendInput("third")
	fixture.terminal.SendInput("\r")
	waitUntil(t, 5*time.Second, "third turn did not reach the client", func() bool {
		return client.callCount() >= 3
	})
	current := extension.contextAt(extension.contextCount() - 1)
	if current == nil {
		t.Fatal("missing current context")
	}
	current.Abort()
	waitUntil(t, 5*time.Second, "current abort did not cancel the turn", func() bool {
		return client.wasCanceled(2)
	})
	close(third)

	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return")
	}
}

func TestTuiHostShutdownStopsRunnerAndCancelsTurn(t *testing.T) {
	gate := make(chan struct{})
	client := newGatedTurnClient(gate)
	extension := &hostHookExtension{
		id: "shutdown-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				ctx.Shutdown()
			}
			return nil, nil
		},
	}
	fixture := newHostTuiFixture(t, client, extension)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)
	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	fixture.terminal.SendInput("shutdown please")
	fixture.terminal.SendInput("\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not stop the runner")
	}
	if fixture.terminal.Started() {
		t.Fatal("terminal must be stopped after Shutdown")
	}
	if client.callCount() != 1 || !client.wasCanceled(0) {
		t.Fatalf("calls = %d canceled = %v, want the owned turn canceled", client.callCount(), client.wasCanceled(0))
	}
}

func TestTuiHostSessionActionsWorkOnActiveOwnedSession(t *testing.T) {
	extension := &hostHookExtension{
		id: "session-actions",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call != 0 {
				return nil, nil
			}
			if err := ctx.SetSessionName("tui renamed"); err != nil {
				return nil, err
			}
			return nil, ctx.AppendEntry("marker", json.RawMessage(`{"live":true}`))
		},
	}
	fixture := newHostTuiFixture(t, &gatedTurnClient{}, extension)
	fixture.terminal.columns = 200
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)
	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	fixture.terminal.SendInput("x")
	fixture.terminal.SendInput("\r")
	waitUntil(t, 5*time.Second, "session action turn did not run", func() bool { return extension.contextCount() >= 1 })
	if got := extension.contextAt(0); got == nil || got.Mode() != sdk.ModeInteractive || !got.HasUI() {
		t.Fatalf("tui context = %+v, want interactive mode with UI", got)
	}
	waitUntil(t, 5*time.Second, "renamed session name was not rendered", func() bool {
		return strings.Contains(fixture.terminal.Output(), "tui renamed")
	})
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return")
	}
	loader, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if name := sessionDisplayName(loader); name != "tui renamed" {
		t.Fatalf("session name = %q, want tui renamed", name)
	}
	var marker bool
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomEntry); ok && custom.CustomType == "marker" && string(custom.Data) == `{"live":true}` {
			marker = true
		}
	}
	if !marker {
		t.Fatal("marker custom entry missing")
	}
}

func TestTuiHostSessionDisplayStateRebindsAndRestores(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	recA := &sessionRecorder{sessA}
	host.bindSession(sessA, recA, sessA.ID(), sessA.Path(), cwd, "alpha")
	rd := &runDeps{
		host:        host,
		sess:        sessA,
		recorder:    recA,
		sessionPath: sessA.Path(),
		cwd:         cwd,
		model:       "model-a",
		wireModel:   "model-a",
	}
	bridge := &tuiBridge{rd: rd, history: []*agent.Message{{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}}}, entryIDs: []string{"e1"}}
	state := bridge.captureSessionDisplayState()
	if state.name != "alpha" || state.sessionPath != sessA.Path() {
		t.Fatalf("captured state = %+v", state)
	}
	stale := host.snapshot()
	cmCfg := contextmanager.Config{Enabled: true, ContextWindowTokens: 1000, KeepRecentMessages: 1}
	live, err := contextmanager.New(cmCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := newContextPreparerAdapter(live, cmCfg)
	host.attachPreparer(adapter)
	canceled := make(chan error, 1)
	if !adapter.requestCompact(sdk.CompactOptions{OnError: func(err error) { canceled <- err }}) {
		t.Fatal("pending compaction was not accepted")
	}
	next := &activeSession{
		sess:      sessB,
		recorder:  &sessionRecorder{sessB},
		path:      sessB.Path(),
		name:      "beta",
		model:     "model-b",
		wireModel: "model-b",
	}
	bridge.installSessionDisplayState(next)
	select {
	case err := <-canceled:
		if !errors.Is(err, errHostCompactCanceled) {
			t.Fatalf("pending compaction error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("installing a session did not cancel the pending compaction")
	}
	if err := host.appendEntry(stale, "note", nil); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("stale handle = %v, want errHostStaleSession", err)
	}
	handle := host.snapshot()
	if handle == nil || handle.id != sessB.ID() || handle.name != "beta" {
		t.Fatalf("installed handle = %+v, want session B", handle)
	}
	bridge.restoreSessionDisplayState(state)
	handle = host.snapshot()
	if handle == nil || handle.id != sessA.ID() || handle.name != "alpha" {
		t.Fatalf("restored handle = %+v, want session A", handle)
	}
	if bridge.rd.model != "model-a" || bridge.rd.wireModel != "model-a" {
		t.Fatalf("restored model = %q/%q", bridge.rd.model, bridge.rd.wireModel)
	}
}

func TestTuiHostApplyModelUpdatesSnapshotAndRestores(t *testing.T) {
	fixture := newHostTuiFixture(t, newGatedTurnClient(), nil)
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       fixture.deps.Stdin,
		Stdout:      fixture.deps.Stdout,
		Mode:        ui.TUIModeRegular,
		NewTerminal: bridgeTerminalFactory(fixture.terminal),
	})
	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	bridge := newTuiBridge(context.Background(), func() {}, fixture.rd, runner, &tuiCaptureWriter{fallback: fixture.deps.Stdout})
	defer func() {
		bridge.shutdown()
		bridge.wait()
		runner.Stop()
	}()
	var selected string
	for _, key := range fixture.rd.modelRegistry.Keys() {
		info, ok := fixture.rd.modelRegistry.GetByKey(key)
		if ok && info.ID != fixture.rd.model && info.ID != "" {
			if _, ok := resolveWireModel("openrouter", info.ID); ok {
				selected = info.ID
				break
			}
		}
	}
	if selected == "" {
		t.Fatal("no selectable registry model")
	}
	bridge.applyModel(selected)
	model := fixture.host.currentModel()
	if model == nil || model.ID != selected {
		t.Fatalf("host model = %+v, want %q", model, selected)
	}
	var second string
	for _, key := range fixture.rd.modelRegistry.Keys() {
		info, ok := fixture.rd.modelRegistry.GetByKey(key)
		if ok && info.ID != selected && info.ID != fixture.rd.model && info.ID != "" {
			if _, ok := resolveWireModel("openrouter", info.ID); ok {
				second = info.ID
				break
			}
		}
	}
	if second == "" {
		t.Fatal("no second selectable registry model")
	}
	fixture.rd.persistModel = func(string) error { return errors.New("persist boom") }
	bridge.applyModel(second)
	if got := fixture.host.currentModel(); got == nil || got.ID != selected {
		t.Fatalf("host model after failed switch = %+v, want the restored %q", got, selected)
	}
}
