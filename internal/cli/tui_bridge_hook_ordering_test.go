package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type runTUIEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *runTUIEventLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *runTUIEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type runTUIHookExtension struct {
	log *runTUIEventLog

	startEnter    chan struct{}
	startGate     chan struct{}
	startOnce     sync.Once
	startDialog   bool
	startOnSignal bool

	mu             sync.Mutex
	startSignalErr error

	shutdownNotify string
	terminalUp     func() bool

	startDialogHandled bool
	shutdownDialogErr  error
	shutdownTerminalUp bool
}

func (e *runTUIHookExtension) ID() string { return "run-tui-hooks" }

func (e *runTUIHookExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	r.OnSessionStart(func(ctx sdk.HandlerContext, _ sdk.SessionStartEvent) error {
		e.log.add("start")
		if e.startEnter != nil {
			e.startOnce.Do(func() { close(e.startEnter) })
		}
		if e.startGate != nil {
			<-e.startGate
		}
		if e.startOnSignal {
			<-ctx.Signal().Done()
			e.mu.Lock()
			e.startSignalErr = ctx.Signal().Err()
			e.mu.Unlock()
			e.log.add("start-returned")
		}
		if e.startDialog {
			ok, err := ctx.UI().Confirm("startup", "startup hook dialog")
			if err != nil {
				return err
			}
			e.mu.Lock()
			e.startDialogHandled = ok
			e.mu.Unlock()
		}
		return nil
	})
	r.OnSessionShutdown(func(ctx sdk.HandlerContext, _ sdk.SessionShutdownEvent) error {
		e.log.add("shutdown")
		if e.terminalUp != nil {
			e.mu.Lock()
			e.shutdownTerminalUp = e.terminalUp()
			e.mu.Unlock()
		}
		if e.shutdownNotify != "" {
			ctx.UI().Notify(e.shutdownNotify, sdk.NotifyInfo)
		}
		_, err := ctx.UI().Confirm("shutdown", "shutdown hook dialog")
		e.mu.Lock()
		e.shutdownDialogErr = err
		e.mu.Unlock()
		return nil
	})
}

func (e *runTUIHookExtension) startSignalResult() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startSignalErr
}

func (e *runTUIHookExtension) shutdownResults() (error, bool, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.shutdownDialogErr, e.shutdownTerminalUp, e.startDialogHandled
}

type runTUITurnClient struct {
	log     *runTUIEventLog
	answer  *agent.AssistantMessage
	entered chan struct{}
	once    sync.Once

	mu      sync.Mutex
	calls   int
	prompts []string
	gate    chan struct{}
}

func newRunTUITurnClient(log *runTUIEventLog, answer string) *runTUITurnClient {
	return &runTUITurnClient{
		log:     log,
		answer:  textStop(answer),
		entered: make(chan struct{}),
	}
}

func (c *runTUITurnClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	prompt := turnPrompt(req)
	c.mu.Lock()
	c.calls++
	c.prompts = append(c.prompts, prompt)
	gate := c.gate
	c.mu.Unlock()
	c.once.Do(func() { close(c.entered) })
	c.log.add("turn:" + prompt)
	if gate != nil {
		<-gate
	}
	if c.answer != nil && onText != nil {
		for _, block := range c.answer.Content {
			if block.Type == agent.BlockTypeText {
				onText(block.Text)
			}
		}
	}
	return c.answer, nil
}

func (c *runTUITurnClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *runTUITurnClient) promptList() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.prompts...)
}

func (c *runTUITurnClient) setGate(gate chan struct{}) {
	c.mu.Lock()
	c.gate = gate
	c.mu.Unlock()
}

type runTUIBlockingTurnClient struct {
	log       *runTUIEventLog
	entered   chan struct{}
	enterOnce sync.Once
	terminal  *fakeBridgeTerminal

	mu               sync.Mutex
	terminalUpOnExit bool
}

func (c *runTUIBlockingTurnClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.enterOnce.Do(func() { close(c.entered) })
	<-ctx.Done()
	c.log.add("turn-canceled")
	if c.terminal != nil {
		c.mu.Lock()
		c.terminalUpOnExit = c.terminal.Started()
		c.mu.Unlock()
	}
	return nil, ctx.Err()
}

type runTUIFixture struct {
	cwd      string
	deps     *Deps
	rd       *runDeps
	lineUI   *ui.LineUI
	terminal *fakeBridgeTerminal
	runtime  *extensions.Runtime
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
}

func newRunTUIFixture(t *testing.T, client agent.Client, extension sdk.Extension) *runTUIFixture {
	t.Helper()
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	registry := extensions.NewRegistry()
	if extension != nil {
		if err := registry.Register(extension); err != nil {
			t.Fatal(err)
		}
	}
	runtime := extensions.NewRuntime(registry)
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
		sessionPath: sess.Path(),
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		hooks:       runtime.Dispatcher(),
		commands:    extensions.NewCommandCatalog(),
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return runtime.HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	return &runTUIFixture{
		cwd:      cwd,
		deps:     deps,
		rd:       rd,
		lineUI:   lineUI,
		terminal: terminal,
		runtime:  runtime,
		stdout:   &stdout,
		stderr:   &stderr,
	}
}

func (f *runTUIFixture) start(ctx context.Context) chan error {
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, f.deps, f.rd, f.lineUI, ui.TUIModeRegular, f.cwd, f.cwd, nil, bridgeTerminalFactory(f.terminal), f.runtime)
	}()
	return done
}

func eventIndex(events []string, target string) int {
	for index, event := range events {
		if event == target {
			return index
		}
	}
	return -1
}

func TestTuiLifecycleDropsJobHeldAtAdmission(t *testing.T) {
	lifecycle := newTuiLifecycle()
	lifecycle.holdAdmission()
	ran := make(chan struct{}, 1)
	if !lifecycle.enqueue(func() { ran <- struct{}{} }) {
		t.Fatal("enqueue refused before shutdown")
	}
	time.Sleep(20 * time.Millisecond)
	lifecycle.beginShutdown()
	lifecycle.wait()
	select {
	case <-ran:
		t.Fatal("job ran after shutdown while admission was still held")
	default:
	}
	if lifecycle.awaitAdmission() {
		t.Fatal("admission reported open after shutdown")
	}
}

func TestRunTUIPromptsWaitForSessionStartHook(t *testing.T) {
	log := &runTUIEventLog{}
	extension := &runTUIHookExtension{
		log:        log,
		startEnter: make(chan struct{}),
		startGate:  make(chan struct{}),
	}
	client := newRunTUITurnClient(log, "hook gated answer")
	fixture := newRunTUIFixture(t, client, extension)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)

	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	select {
	case <-extension.startEnter:
	case <-time.After(5 * time.Second):
		t.Fatal("SessionStart hook did not run")
	}

	fixture.terminal.SendInput("prompt during startup")
	fixture.terminal.SendInput("\r")
	assertNever(t, 200*time.Millisecond, "prompt ran before SessionStart finished", func() bool {
		return client.callCount() != 0
	})

	close(extension.startGate)
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("queued prompt did not run after SessionStart finished")
	}
	if prompts := client.promptList(); len(prompts) != 1 || prompts[0] != "prompt during startup" {
		t.Fatalf("prompts = %v, want the queued startup prompt", prompts)
	}
	events := log.snapshot()
	if start, turn := eventIndex(events, "start"), eventIndex(events, "turn:prompt during startup"); start < 0 || turn < 0 || start > turn {
		t.Fatalf("ordering events = %v, want start before turn", events)
	}

	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after exit")
	}
	if fixture.terminal.Started() {
		t.Error("terminal should be stopped after runTUI returns")
	}
}

func TestRunTUIStartupHookDialogCancellation(t *testing.T) {
	log := &runTUIEventLog{}
	extension := &runTUIHookExtension{
		log:         log,
		startEnter:  make(chan struct{}),
		startDialog: true,
	}
	client := newRunTUITurnClient(log, "unused answer")
	fixture := newRunTUIFixture(t, client, extension)
	ctx, cancel := context.WithCancel(context.Background())
	done := fixture.start(ctx)

	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	waitForOutputSettled(t, fixture.terminal, "startup hook dialog", 5*time.Second)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runTUI = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the startup dialog canceled")
	}
	if fixture.terminal.Started() {
		t.Error("terminal should be stopped after the canceled startup dialog")
	}
	if _, _, handled := extension.shutdownResults(); handled {
		t.Error("canceled startup dialog reported acceptance")
	}
}

func TestRunTUICancelsAndJoinsTurnBeforeShutdownHook(t *testing.T) {
	log := &runTUIEventLog{}
	client := &runTUIBlockingTurnClient{log: log, entered: make(chan struct{})}
	extension := &runTUIHookExtension{
		log:            log,
		shutdownNotify: "final shutdown notice",
	}
	fixture := newRunTUIFixture(t, client, extension)
	client.terminal = fixture.terminal
	extension.terminalUp = fixture.terminal.Started
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)

	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	fixture.terminal.SendInput("active prompt")
	fixture.terminal.SendInput("\r")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("active turn did not start")
	}

	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after exit")
	}

	events := log.snapshot()
	canceled, shutdown := eventIndex(events, "turn-canceled"), eventIndex(events, "shutdown")
	if canceled < 0 || shutdown < 0 || canceled > shutdown {
		t.Fatalf("ordering events = %v, want the turn canceled and joined before the shutdown hook", events)
	}
	shutdownErr, terminalUp, _ := extension.shutdownResults()
	if shutdownErr == nil {
		t.Fatal("shutdown hook opened a dialog after dialog admission closed")
	}
	if !terminalUp {
		t.Error("shutdown hook ran after the renderer stopped")
	}
	if output := fixture.terminal.Output(); !strings.Contains(output, "final shutdown notice") {
		t.Fatalf("final shutdown notice was not rendered before terminal restore:\n%s", output)
	}
	if got := fixture.terminal.StopCount(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	if fixture.terminal.Started() {
		t.Error("terminal should be stopped after runTUI returns")
	}
}

func TestRunTUIShutdownHookCannotOpenDialog(t *testing.T) {
	log := &runTUIEventLog{}
	client := newRunTUITurnClient(log, "noop answer")
	extension := &runTUIHookExtension{log: log}
	fixture := newRunTUIFixture(t, client, extension)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)

	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after exit")
	}
	shutdownErr, _, _ := extension.shutdownResults()
	if shutdownErr == nil {
		t.Fatal("shutdown hook opened a dialog after dialog admission closed")
	}
	if client.callCount() != 0 {
		t.Fatalf("client calls = %d, want none for an empty session", client.callCount())
	}
}

func TestRunTUIExitsWhileWorkerBlockedInDialog(t *testing.T) {
	log := &runTUIEventLog{}
	client := newRunTUITurnClient(log, "unused answer")
	extension := &runTUIHookExtension{log: log}
	fixture := newRunTUIFixture(t, client, extension)
	dialogErr := make(chan error, 1)
	fixture.rd.commands.Register("ask", sdk.Command{
		Description: "open a worker dialog",
		Handler: func(ctx sdk.CommandContext, _ string) error {
			_, err := ctx.UI().Confirm("worker", "worker dialog")
			dialogErr <- err
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)

	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	fixture.terminal.SendInput("/ask")
	fixture.terminal.SendInput("\r")
	waitForOutputSettled(t, fixture.terminal, "worker dialog", 5*time.Second)

	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI deadlocked while the worker was blocked in a dialog")
	}
	select {
	case err := <-dialogErr:
		if err == nil {
			t.Fatal("worker dialog resolved without an error after exit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker dialog did not resolve")
	}
	if fixture.terminal.Started() {
		t.Error("terminal should be stopped after runTUI returns")
	}
}

func TestRunTUIStartupHookDialogAcceptsInput(t *testing.T) {
	log := &runTUIEventLog{}
	extension := &runTUIHookExtension{
		log:         log,
		startEnter:  make(chan struct{}),
		startDialog: true,
	}
	client := newRunTUITurnClient(log, "unused answer")
	fixture := newRunTUIFixture(t, client, extension)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)

	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	waitForOutputSettled(t, fixture.terminal, "startup hook dialog", 5*time.Second)
	fixture.terminal.SendInput("y")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, handled := extension.shutdownResults(); handled {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, _, handled := extension.shutdownResults(); !handled {
		t.Fatal("startup dialog did not accept input while the UI was active")
	}
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after exit")
	}
}
