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
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type lifecycleGateClient struct {
	mu         sync.Mutex
	calls      int
	entered    chan struct{}
	enterOnce  sync.Once
	release    chan struct{}
	terminal   *fakeBridgeTerminal
	terminalUp bool
}

func (c *lifecycleGateClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	c.enterOnce.Do(func() { close(c.entered) })
	<-ctx.Done()
	c.mu.Lock()
	if c.terminal != nil {
		c.terminalUp = c.terminal.Started()
	}
	c.mu.Unlock()
	if c.release != nil {
		<-c.release
	}
	return nil, ctx.Err()
}

func (c *lifecycleGateClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *lifecycleGateClient) terminalUpAtCancel() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminalUp
}

type recordingSpy struct {
	agent.Recorder
	mu         sync.Mutex
	windowOpen bool
	violations int
	appends    int
}

func (s *recordingSpy) AppendUser(m *agent.UserMessage) error {
	s.arrived()
	return s.Recorder.AppendUser(m)
}

func (s *recordingSpy) AppendAssistant(m *agent.AssistantMessage) error {
	s.arrived()
	return s.Recorder.AppendAssistant(m)
}

func (s *recordingSpy) AppendToolResult(m *agent.ToolResultMessage) error {
	s.arrived()
	return s.Recorder.AppendToolResult(m)
}

func (s *recordingSpy) arrived() {
	s.mu.Lock()
	s.appends++
	if s.windowOpen {
		s.violations++
	}
	s.mu.Unlock()
}

func (s *recordingSpy) openWindow() {
	s.mu.Lock()
	s.windowOpen = true
	s.mu.Unlock()
}

func (s *recordingSpy) violationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.violations
}

func assertNever(t *testing.T, window time.Duration, message string, probe func() bool) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if probe() {
			t.Fatal(message)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type releaseGateClient struct {
	entered   chan struct{}
	enterOnce sync.Once
	release   chan struct{}
	answer    *agent.AssistantMessage

	terminal            *fakeBridgeTerminal
	mu                  sync.Mutex
	terminalUpAtRelease bool
}

func (c *releaseGateClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.enterOnce.Do(func() { close(c.entered) })
	<-c.release
	if c.terminal != nil {
		c.mu.Lock()
		c.terminalUpAtRelease = c.terminal.Started()
		c.mu.Unlock()
	}
	if onText != nil && c.answer != nil {
		for _, block := range c.answer.Content {
			if block.Type == agent.BlockTypeText {
				onText(block.Text)
			}
		}
	}
	return c.answer, nil
}

func (c *releaseGateClient) terminalWasUpUntilJoin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminalUpAtRelease
}

type orderingClient struct {
	events *[]string
}

func (c *orderingClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	*c.events = append(*c.events, "turn:"+turnPrompt(req))
	return textStop("ordering answer"), nil
}

func turnPrompt(req *agent.TurnRequest) string {
	if len(req.Messages) == 0 {
		return ""
	}
	last := req.Messages[len(req.Messages)-1]
	if last == nil || last.User == nil {
		return ""
	}
	var text string
	if err := json.Unmarshal(last.User.Content, &text); err != nil {
		return string(last.User.Content)
	}
	return text
}

func TestTUIBridgeSubmitExecutesInFIFOOrder(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	var events []string
	fixture.bridge.rd.client = &orderingClient{events: &events}
	fixture.commands.Register("mark", sdk.Command{
		Description: "record a marker",
		Handler: func(ctx sdk.CommandContext, args string) error {
			events = append(events, "slash:"+args)
			return nil
		},
	})
	turns := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("one")
	fixture.bridge.submit("/mark between")
	fixture.bridge.submit("two")
	for i := 0; i < 3; i++ {
		select {
		case <-turns:
		case <-time.After(5 * time.Second):
			t.Fatal("queued job did not finish")
		}
	}
	want := []string{"turn:one", "slash:between", "turn:two"}
	if len(events) != len(want) {
		t.Fatalf("events = %q, want %q", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %q, want %q", events, want)
		}
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestTUIBridgeQuitWaitsForPriorPrompt(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	client := &releaseGateClient{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		answer:  textStop("queued answer"),
	}
	fixture.bridge.rd.client = client
	fixture.bridge.submit("slow prompt")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	fixture.bridge.submit("/quit")
	assertNever(t, 200*time.Millisecond, "/quit overtook the prior prompt", func() bool {
		select {
		case <-fixture.runner.Done():
			return true
		default:
			return false
		}
	})
	if !fixture.terminal.Started() {
		t.Fatal("queued /quit stopped the terminal")
	}
	close(client.release)
	select {
	case <-fixture.runner.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("exit was not requested after the prior prompt finished")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestTUIBridgeShutdownCancelsAndJoinsActiveTurn(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	client := &lifecycleGateClient{entered: make(chan struct{}), release: make(chan struct{})}
	fixture.bridge.rd.client = client
	turns := make(chan struct{}, 4)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("slow turn")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	if !fixture.runner.Working() {
		t.Fatal("runner should be working during the active turn")
	}
	shutdownDone := make(chan struct{})
	go func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned while the active turn was still running")
	case <-time.After(150 * time.Millisecond):
	}
	close(client.release)
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join the active turn")
	}
	select {
	case <-turns:
	default:
		t.Fatal("afterTurn did not run for the canceled turn")
	}
	if fixture.runner.Working() {
		t.Error("runner should be idle after shutdown")
	}
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "interrupted") {
		t.Errorf("frame missing the interrupt notice:\n%s", text)
	}
	if len(fixture.bridge.history) != 1 {
		t.Errorf("history length = %d, want only the canceled user turn", len(fixture.bridge.history))
	}
}

func TestTUIBridgeShutdownCancelsQueuedSubmissions(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	spy := &recordingSpy{Recorder: fixture.bridge.rd.recorder}
	fixture.bridge.rd.recorder = spy
	client := &lifecycleGateClient{entered: make(chan struct{})}
	fixture.bridge.rd.client = client
	fixture.bridge.submit("active turn")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	fixture.bridge.submit("queued one")
	fixture.bridge.submit("queued two")
	fixture.bridge.shutdown()
	fixture.bridge.wait()
	fixture.bridge.submit("late submission")
	assertNever(t, 200*time.Millisecond, "submission started after shutdown", func() bool {
		return client.callCount() != 1
	})
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "active turn") || !strings.Contains(text, "interrupted") {
		t.Errorf("frame missing the active canceled turn:\n%s", text)
	}
	for _, rejected := range []string{"queued one", "queued two", "late submission"} {
		if strings.Contains(text, rejected) {
			t.Errorf("rejected submission %q reached the surface:\n%s", rejected, text)
		}
	}
	spy.openWindow()
	assertNever(t, 200*time.Millisecond, "recording happened after shutdown returned", func() bool {
		return spy.violationCount() > 0
	})
}

func TestTUIBridgeRepeatedShutdownAndWaitIsSafe(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("settled answer")}, nil)
	turns := make(chan struct{}, 4)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("settle down")
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
	fixture.bridge.shutdown()
	fixture.bridge.wait()
	fixture.bridge.interrupt()
	fixture.bridge.submit("after final shutdown")
	assertNever(t, 200*time.Millisecond, "submission started after final shutdown", func() bool {
		return fixture.runner.Working()
	})
	select {
	case <-turns:
		t.Fatal("submission after shutdown ran a turn")
	default:
	}
	if text := bridgeFrameText(t, fixture); strings.Contains(text, "after final shutdown") {
		t.Errorf("rejected submission reached the surface:\n%s", text)
	}
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "settled answer") {
		t.Errorf("frame missing the completed turn answer:\n%s", text)
	}
}

func TestTUIBridgeSubmitSequencesTwoTurns(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		textStop("first answer"),
		textStop("second answer"),
	}, nil)
	turns := make(chan struct{}, 4)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("first question")
	fixture.bridge.submit("second question")
	for i := 0; i < 2; i++ {
		select {
		case <-turns:
		case <-time.After(5 * time.Second):
			t.Fatal("queued turn did not finish")
		}
	}
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "first answer") || !strings.Contains(text, "second answer") {
		t.Errorf("frame missing a queued response:\n%s", text)
	}
	if len(fixture.bridge.history) != 4 {
		t.Fatalf("history length = %d, want two full turns", len(fixture.bridge.history))
	}
	if fixture.runner.Working() {
		t.Error("runner should be idle after both turns")
	}
}

func TestRunTUIExitRequestKeepsTerminalUntilWorkerJoined(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	terminal := newFakeBridgeTerminal()
	client := &releaseGateClient{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		answer:   textStop("released answer"),
		terminal: terminal,
	}
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	runtime := extensions.NewRuntime(extensions.NewRegistry())
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal), nil)
	}()
	select {
	case <-terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	terminal.SendInput("slow prompt")
	terminal.SendInput("\r")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("submitted turn did not start")
	}
	terminal.FireEOF()
	assertNever(t, 200*time.Millisecond, "exit request restored the terminal while the worker was alive", func() bool {
		return terminal.StopCount() > 0 || !terminal.Started()
	})
	close(client.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the exit request")
	}
	if !client.terminalWasUpUntilJoin() {
		t.Error("terminal was restored before the worker was joined")
	}
	if got := terminal.StopCount(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	if terminal.Started() {
		t.Error("terminal should be stopped after runTUI returns")
	}
}

func TestRunTUIJoinsActiveTurnBeforeRunnerStop(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	terminal := newFakeBridgeTerminal()
	client := &lifecycleGateClient{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		terminal: terminal,
	}
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	runtime := extensions.NewRuntime(extensions.NewRegistry())
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal), nil)
	}()
	select {
	case <-terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	terminal.SendInput("slow prompt")
	terminal.SendInput("\r")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("submitted turn did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("runTUI returned while the active turn was still running")
	case <-time.After(150 * time.Millisecond):
	}
	close(client.release)
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("runTUI = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the turn joined")
	}
	if !client.terminalUpAtCancel() {
		t.Error("runner was torn down before the active turn unwound")
	}
	if terminal.Started() {
		t.Error("terminal should be stopped after runTUI returns")
	}
}

func TestRunTUIEditorResumeFailureJoinsBridgeBeforeStop(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	terminal := newFakeBridgeTerminal()
	terminal.resumeErr = errors.New("resume refused")
	client := &lifecycleGateClient{
		entered:  make(chan struct{}),
		terminal: terminal,
	}
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	runtime := extensions.NewRuntime(extensions.NewRegistry())
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
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal), nil)
	}()
	select {
	case <-terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	terminal.SendInput("slow prompt")
	terminal.SendInput("\r")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("submitted turn did not start")
	}
	terminal.SendInput("\x07")
	waitForSuspendCount(t, terminal, 1)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the editor resume failure")
	}
	if !client.terminalUpAtCancel() {
		t.Error("terminal was torn down before the bridge joined the blocked turn")
	}
	if got := terminal.StopCount(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	if terminal.Started() {
		t.Error("terminal should be stopped after runTUI returns")
	}
	if got := terminal.SuspendCalls(); got != 1 {
		t.Fatalf("suspend calls = %d, want 1", got)
	}
	if got := terminal.ResumeCalls(); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
	if output := terminal.Output(); !strings.Contains(output, "resume refused") {
		t.Errorf("output missing the resume failure notice:\n%s", output)
	}
}

func waitForSuspendCount(t *testing.T, terminal *fakeBridgeTerminal, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if terminal.SuspendCalls() >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d suspend calls, got %d", want, terminal.SuspendCalls())
		}
		time.Sleep(2 * time.Millisecond)
	}
}
