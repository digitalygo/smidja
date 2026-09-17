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
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/skills"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type bridgeFixture struct {
	runner   *ui.Runner
	terminal *fakeBridgeTerminal
	bridge   *tuiBridge
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	client   *capturingClient
	probe    *int
	commands *extensions.CommandCatalog
	store    *session.Store
	sess     *session.Session
	cwd      string
}

func newBridgeFixture(t *testing.T, script []*agent.AssistantMessage, tools []agent.Tool) *bridgeFixture {
	t.Helper()
	cwd := t.TempDir()
	browser := t.TempDir()
	store, err := session.NewStore(browser)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	terminal := newFakeBridgeTerminal()
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      &bytes.Buffer{},
		Home:        t.TempDir(),
		NewTerminal: bridgeTerminalFactory(terminal),
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("runner.Start: %v", err)
	}
	t.Cleanup(runner.Stop)
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	client := &capturingClient{script: script}
	var stdout, stderr bytes.Buffer
	commands := extensions.NewCommandCatalog()
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      client,
		tools:       tools,
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		hooks:       runtime.Dispatcher(),
		retry:       retryAdapter,
		retryPolicy: agent.RetryPolicy{Enabled: false},
		catalog:     commandsCatalogFor(tools),
		commands:    commands,
		store:       store,
		sess:        sess,
		cwd:         cwd,
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return runtime.HandlerContext(signal)
		},
	}
	bridge := newTuiBridge(context.Background(), nil, rd, runner, &tuiCaptureWriter{fallback: &stdout})
	return &bridgeFixture{
		runner:   runner,
		terminal: terminal,
		bridge:   bridge,
		stdout:   &stdout,
		stderr:   &stderr,
		client:   client,
		commands: commands,
		store:    store,
		sess:     sess,
		cwd:      cwd,
	}
}

func commandsCatalogFor(tools []agent.Tool) *extensions.ToolCatalog {
	catalog := extensions.NewToolCatalog()
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		_ = catalog.Register(tool)
	}
	return catalog
}

func bridgeFrameText(t *testing.T, fixture *bridgeFixture) string {
	t.Helper()
	frame := fixture.runner.Surface().RenderFrame(80, 24)
	var builder strings.Builder
	for _, line := range frame.Lines {
		builder.WriteString(tui.StripTerminalSequences(line))
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestBridgeSubmitStreamsResponse(t *testing.T) {
	answer := textStop("streamed answer")
	answer.Usage = agent.Usage{Input: 120, Output: 34, Cost: agent.Cost{Total: 0.001}}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{answer}, nil)
	fixture.bridge.handle("hello there")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "hello there") {
		t.Errorf("frame missing the user prompt:\n%s", text)
	}
	if !strings.Contains(text, "streamed answer") {
		t.Errorf("frame missing the streamed answer:\n%s", text)
	}
	if fixture.runner.Working() {
		t.Error("runner should be idle after the turn")
	}
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("history length = %d, want user plus assistant", len(fixture.bridge.history))
	}
	if got := fixture.stdout.String(); strings.Contains(got, "streamed answer") {
		t.Errorf("agent text leaked to terminal stdout: %q", got)
	}
}

func TestBridgeAcceptsAnotherPrompt(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		textStop("first answer"),
		textStop("second answer"),
	}, nil)
	fixture.bridge.handle("first question")
	fixture.bridge.handle("second question")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "first answer") || !strings.Contains(text, "second answer") {
		t.Errorf("frame missing a response:\n%s", text)
	}
	if len(fixture.bridge.history) != 4 {
		t.Fatalf("history length = %d, want two full turns", len(fixture.bridge.history))
	}
}

func TestBridgeStreamsThinkingSeparately(t *testing.T) {
	thinking := &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "quiet plan"},
			{Type: agent.BlockTypeText, Text: "loud answer"},
		},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "test/model",
		StopReason: "stop",
		Timestamp:  1,
	}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{thinking}, nil)
	fixture.bridge.handle("think hard")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "loud answer") {
		t.Errorf("frame missing the answer:\n%s", text)
	}
	if !strings.Contains(text, "Thinking") {
		t.Errorf("frame missing the thinking block:\n%s", text)
	}
}

func TestBridgeShowsToolLifecycle(t *testing.T) {
	probeCalls := 0
	probe := &probeTool{calls: &probeCalls}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		toolUse("call_1", "probe", `{"x":1}`),
		textStop("tool done"),
	}, []agent.Tool{probe})
	fixture.bridge.handle("run the tool")
	if probeCalls != 1 {
		t.Fatalf("probe executed %d times, want 1", probeCalls)
	}
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "probe") {
		t.Errorf("frame missing the tool block:\n%s", text)
	}
	if !strings.Contains(text, "probe result") {
		t.Errorf("frame missing the tool result:\n%s", text)
	}
	if !strings.Contains(text, "tool done") {
		t.Errorf("frame missing the follow-up answer:\n%s", text)
	}
	if got := strings.Count(text, "probe result"); got != 1 {
		t.Errorf("tool result appears %d times, want exactly one finalized block", got)
	}
}

type retrySpy struct {
	agent.HookDispatcher
	mu       sync.Mutex
	starts   int
	ends     int
	lastFail string
}

func (s *retrySpy) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	s.mu.Lock()
	s.starts++
	s.mu.Unlock()
	return s.HookDispatcher.AutoRetryStart(ctx, attempt, maxAttempts, delayMs, errorMessage)
}

func (s *retrySpy) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	s.mu.Lock()
	s.ends++
	s.lastFail = finalError
	s.mu.Unlock()
	return s.HookDispatcher.AutoRetryEnd(ctx, success, attempt, finalError)
}

type flakyClient struct {
	mu      sync.Mutex
	calls   int
	answer  *agent.AssistantMessage
	started chan struct{}
}

func (c *flakyClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	if c.started != nil {
		close(c.started)
		c.started = nil
	}
	c.mu.Unlock()
	if c.calls == 1 {
		return nil, errors.New("HTTP 503 Service Unavailable")
	}
	for _, block := range c.answer.Content {
		if block.Type == agent.BlockTypeText && onText != nil {
			onText(block.Text)
		}
	}
	return c.answer, nil
}

func TestBridgeMapsRetries(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	spy := &retrySpy{HookDispatcher: fixture.bridge.rd.hooks}
	fixture.bridge.rd.hooks = spy
	fixture.bridge.rd.retryPolicy = agent.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 1}
	fixture.bridge.rd.retryPolicySet = true
	fixture.bridge.rd.client = &flakyClient{answer: textStop("recovered")}
	fixture.bridge.handle("flaky prompt")
	spy.mu.Lock()
	starts, ends := spy.starts, spy.ends
	spy.mu.Unlock()
	if starts == 0 || ends == 0 {
		t.Fatalf("retry hooks = %d/%d, want at least one scheduled and finished", starts, ends)
	}
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "recovered") {
		t.Errorf("frame missing the retried answer:\n%s", text)
	}
	if strings.Contains(text, "Retrying (") {
		t.Errorf("retry state should clear after the turn:\n%s", text)
	}
}

func TestBridgeInterruptCancelsTurn(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	client := &lifecycleGateClient{entered: make(chan struct{})}
	fixture.bridge.rd.client = client
	turns := make(chan struct{}, 4)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("long task")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	if !fixture.runner.Working() {
		t.Fatal("runner should report working during the turn")
	}
	fixture.bridge.interrupt()
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted turn did not finish")
	}
	if fixture.runner.Working() {
		t.Error("runner should be idle after interrupt")
	}
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "interrupted") {
		t.Errorf("frame missing the interrupt notice:\n%s", text)
	}
	fixture.bridge.rd.client = &capturingClient{script: []*agent.AssistantMessage{textStop("back again")}}
	fixture.bridge.submit("after interrupt")
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("turn after interrupt did not finish")
	}
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "back again") {
		t.Errorf("editor should accept prompts after interrupt:\n%s", text)
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestBridgeSlashCommands(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	helpDone := make(chan struct{})
	go func() {
		fixture.bridge.handle("/help")
		close(helpDone)
	}()
	output := waitForOutputSettled(t, fixture.terminal, "show command", 3*time.Second)
	if !strings.Contains(tui.StripTerminalSequences(output), "quit") {
		t.Errorf("help missing the quit command:\n%s", output)
	}
	fixture.terminal.SendInput("\x1b")
	select {
	case <-helpDone:
	case <-time.After(2 * time.Second):
		t.Fatal("help overlay did not close")
	}
	fixture.bridge.handle("/nope")
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "unknown command") {
		t.Errorf("frame missing the unknown command notice:\n%s", text)
	}
	fixture.bridge.handle("/quit")
	select {
	case <-fixture.runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("/quit should exit the runner")
	}
}

func TestBridgeSkillListRoutesToNotice(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	catalog := skills.New()
	if err := catalog.Add("bundle", "quick", "content"); err != nil {
		t.Fatal(err)
	}
	registerSkillCommand(fixture.commands, catalog, fixture.bridge.capture)
	fixture.bridge.handle("/skill")
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "bundle/quick") {
		t.Errorf("frame missing the skill list:\n%s", text)
	}
	if got := fixture.stdout.String(); strings.Contains(got, "bundle/quick") {
		t.Errorf("skill list leaked to terminal stdout: %q", got)
	}
}

func TestBridgeTurnErrorStaysAlive(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("alive")}, nil)
	fixture.bridge.rd.client = &capturingClient{}
	fixture.bridge.handle("broken turn")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "script exhausted") {
		t.Errorf("frame missing the turn error:\n%s", text)
	}
	if fixture.runner.Working() {
		t.Error("runner should be idle after a failed turn")
	}
	fixture.bridge.rd.client = &capturingClient{script: []*agent.AssistantMessage{textStop("alive")}}
	fixture.bridge.handle("next turn")
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "alive") {
		t.Errorf("session should survive a failed turn:\n%s", text)
	}
}

func TestBridgeExitCommandVariants(t *testing.T) {
	for _, input := range []string{"/quit", "/exit"} {
		fixture := newBridgeFixture(t, nil, nil)
		fixture.bridge.handle(input)
		select {
		case <-fixture.runner.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("%s should exit the runner", input)
		}
	}
}

func TestRunTUIFallsBackToLineUI(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	terminal := newFakeBridgeTerminal()
	terminal.startErr = errors.New("no screen for you")
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader("/quit\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      &fakeClient{script: []*agent.AssistantMessage{textStop("unused")}},
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
	if err := runTUI(context.Background(), deps, rd, lineUI, ui.TUIModeFullscreen, cwd, cwd, nil, bridgeTerminalFactory(terminal), nil); err != nil {
		t.Fatalf("runTUI fallback: %v", err)
	}
	if !strings.Contains(stderr.String(), "tui unavailable") {
		t.Errorf("stderr = %q, want the fallback note", stderr.String())
	}
}

func TestBridgeCommandContextUnsupported(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	hctx := &tuiCommandContext{bridge: fixture.bridge}
	if err := hctx.WaitForIdle(); err != sdk.ErrModeUnsupported {
		t.Errorf("WaitForIdle = %v", err)
	}
	if _, err := hctx.NavigateTree("x", sdk.TreeOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("NavigateTree = %v", err)
	}
	if err := hctx.Reload(); err != sdk.ErrModeUnsupported {
		t.Errorf("Reload = %v", err)
	}
}

func TestBridgeCommandContextRejectsUnreadySessionControl(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	hctx := &tuiCommandContext{bridge: fixture.bridge}
	if _, err := hctx.NewSession(sdk.NewSessionOptions{}); err == nil || err == sdk.ErrModeUnsupported {
		t.Errorf("NewSession readiness error = %v, want a session-control error", err)
	}
	if _, err := hctx.Fork("x", sdk.ForkOptions{}); err == nil || err == sdk.ErrModeUnsupported {
		t.Errorf("Fork readiness error = %v, want a session-control error", err)
	}
	if _, err := hctx.SwitchSession("x", sdk.SwitchOptions{}); err == nil || err == sdk.ErrModeUnsupported {
		t.Errorf("SwitchSession readiness error = %v, want a session-control error", err)
	}
}

func TestBridgeCommandContextRejectsUnsupportedOptions(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	hctx := &tuiCommandContext{bridge: fixture.bridge}
	if _, err := hctx.NewSession(sdk.NewSessionOptions{Setup: func(sdk.SessionView) error { return nil }}); err == nil {
		t.Error("NewSession must reject an unsupported setup callback")
	}
	if _, err := hctx.Fork("x", sdk.ForkOptions{Position: "middle"}); err == nil {
		t.Error("Fork must reject an unsupported position")
	}
	if _, err := hctx.SwitchSession("x", sdk.SwitchOptions{WithSession: func(sdk.CommandContext) error { return nil }}); err == nil {
		t.Error("SwitchSession must reject an unsupported callback")
	}
}

func TestBridgeSkillInjectionRunsTurn(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("injected answer")}, nil)
	catalog := skills.New()
	if err := catalog.Add("bundle", "quick", "skill body"); err != nil {
		t.Fatal(err)
	}
	registerSkillCommand(fixture.commands, catalog, fixture.bridge.capture)
	fixture.bridge.handle("/skill quick")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "injected answer") {
		t.Errorf("frame missing the injected turn answer:\n%s", text)
	}
	if !strings.Contains(fixture.client.lastUserText(), "skill body") {
		t.Errorf("injected skill body never reached the loop, got %q", fixture.client.lastUserText())
	}
}

func TestBridgeCommandErrorNotice(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.commands.Register("boom", sdk.Command{
		Description: "always fails",
		Handler: func(ctx sdk.CommandContext, args string) error {
			return errors.New("controlled failure")
		},
	})
	fixture.bridge.handle("/boom")
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "controlled failure") {
		t.Errorf("frame missing the command error:\n%s", text)
	}
}

type partialBlockingClient struct {
	entered chan struct{}
}

func (c *partialBlockingClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	close(c.entered)
	if onText != nil {
		onText("partial answer")
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBridgeAbortKeepsStreamedContent(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.client = &partialBlockingClient{entered: make(chan struct{})}
	turns := make(chan struct{}, 4)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("abort me")
	select {
	case <-fixture.bridge.rd.client.(*partialBlockingClient).entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	fixture.bridge.interrupt()
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted turn did not finish")
	}
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "partial answer") {
		t.Errorf("frame lost the streamed content:\n%s", text)
	}
	if !strings.Contains(text, "Operation aborted") {
		t.Errorf("frame missing the abort marker:\n%s", text)
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestSwitchWriterDelegates(t *testing.T) {
	var first, second bytes.Buffer
	writer := &switchWriter{target: &first}
	if _, err := writer.Write([]byte("one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	writer.Set(&second)
	if _, err := writer.Write([]byte("two")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if first.String() != "one" || second.String() != "two" {
		t.Errorf("delegated writes = %q/%q", first.String(), second.String())
	}
}

func TestCaptureWriterBuffersOnlyWhenCapturing(t *testing.T) {
	var fallback bytes.Buffer
	capture := &tuiCaptureWriter{fallback: &fallback}
	if _, err := capture.Write([]byte("direct")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	capture.Begin()
	if _, err := capture.Write([]byte("held")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := capture.End(); got != "held" {
		t.Errorf("captured = %q, want held", got)
	}
	if fallback.String() != "direct" {
		t.Errorf("fallback = %q, want direct", fallback.String())
	}
	if got := capture.End(); got != "" {
		t.Errorf("second End() = %q, want empty", got)
	}
}

func TestNoticeWriterPostsWarnings(t *testing.T) {
	terminal := newFakeBridgeTerminal()
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      &bytes.Buffer{},
		Home:        t.TempDir(),
		NewTerminal: bridgeTerminalFactory(terminal),
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer runner.Stop()
	writer := &tuiNoticeWriter{runner: runner}
	if n, err := writer.Write([]byte("overflow happened")); err != nil || n != 17 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if n, err := writer.Write([]byte("   ")); err != nil || n != 3 {
		t.Fatalf("blank Write() = %d, %v", n, err)
	}
	frame := runner.Surface().RenderFrame(80, 24)
	var builder strings.Builder
	for _, line := range frame.Lines {
		builder.WriteString(tui.StripTerminalSequences(line))
		builder.WriteString("\n")
	}
	if !strings.Contains(builder.String(), "overflow happened") {
		t.Errorf("frame missing the notice:\n%s", builder.String())
	}
}

func TestBridgeHandlesEmptyInput(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.handle("   ")
	if len(fixture.bridge.history) != 0 {
		t.Fatalf("history = %d, want empty", len(fixture.bridge.history))
	}
}

func TestBridgeSubmitRunsAsync(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	turned := make(chan struct{})
	fixture.bridge.afterTurn = func() { close(turned) }
	fixture.bridge.submit("/help")
	waitForOutputSettled(t, fixture.terminal, "show command", 3*time.Second)
	fixture.terminal.SendInput("\x1b")
	select {
	case <-turned:
	case <-time.After(5 * time.Second):
		t.Fatal("async submit did not complete")
	}
}

func TestLastAssistantUsageSkips(t *testing.T) {
	if _, _, ok := lastAssistantUsage(nil); ok {
		t.Fatal("empty history should report no usage")
	}
	history := []*agent.Message{nil, {User: &agent.UserMessage{Role: "user"}}}
	if _, _, ok := lastAssistantUsage(history); ok {
		t.Fatal("history without assistant should report no usage")
	}
	assistant := textStop("x")
	assistant.Usage = agent.Usage{Input: 7}
	withAssistant := append(history, &agent.Message{Assistant: assistant})
	usage, reason, ok := lastAssistantUsage(withAssistant)
	if !ok || usage.Input != 7 || reason != "stop" {
		t.Fatalf("usage = %+v %q %v", usage, reason, ok)
	}
}

func TestRunTUIReturnsOnContextCancel(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
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
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	rd := &runDeps{
		model:       "test/model",
		sessionPath: sess.Path(),
		client:      &capturingClient{},
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
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal), nil)
	}()
	select {
	case <-terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("runTUI = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after cancel")
	}
}

func toolUseWithText(id, name, args, text string) *agent.AssistantMessage {
	message := toolUse(id, name, args)
	message.Content = append([]agent.ContentBlock{{Type: agent.BlockTypeText, Text: text}}, message.Content...)
	return message
}

func TestBridgeSeparatesAssistantBlocksAcrossToolLoop(t *testing.T) {
	probeCalls := 0
	probe := &probeTool{calls: &probeCalls}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		toolUseWithText("call_1", "probe", `{"x":1}`, "first answer"),
		textStop("second answer"),
	}, []agent.Tool{probe})
	fixture.bridge.handle("loop through tools")
	text := bridgeFrameText(t, fixture)
	first := strings.Index(text, "first answer")
	tool := strings.Index(text, "probe result")
	second := strings.Index(text, "second answer")
	if first < 0 || tool < 0 || second < 0 {
		t.Fatalf("frame missing a block marker (%d, %d, %d):\n%s", first, tool, second, text)
	}
	if first > tool || tool > second {
		t.Fatalf("blocks out of order: first=%d tool=%d second=%d\n%s", first, tool, second, text)
	}
	if strings.Contains(text, "first answersecond answer") {
		t.Fatalf("post-tool text merged into the pre-tool block:\n%s", text)
	}
	for _, marker := range []string{"first answer", "probe result", "second answer"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}

func TestBridgeLengthStopShowsTruncation(t *testing.T) {
	truncated := textStop("cut off here")
	truncated.StopReason = "length"
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{truncated}, nil)
	fixture.bridge.handle("write too much")
	text := bridgeFrameText(t, fixture)
	if got := strings.Count(text, "cut off here"); got != 1 {
		t.Fatalf("truncated text rendered %d times, want 1:\n%s", got, text)
	}
	if got := strings.Count(text, "Response was truncated before completion."); got != 1 {
		t.Fatalf("truncation notice rendered %d times, want 1:\n%s", got, text)
	}
}

func TestBridgeProviderErrorAfterToolRoundIsVisible(t *testing.T) {
	probeCalls := 0
	probe := &probeTool{calls: &probeCalls}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		toolUseWithText("call_1", "probe", `{"x":1}`, "step one"),
	}, []agent.Tool{probe})
	fixture.bridge.handle("fail after the tool")
	text := bridgeFrameText(t, fixture)
	for _, marker := range []string{"step one", "probe result", "Error: ", "script exhausted"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("frame missing %q:\n%s", marker, text)
		}
	}
	for _, marker := range []string{"step one", "probe result"} {
		if got := strings.Count(text, marker); got != 1 {
			t.Fatalf("%q rendered %d times, want 1:\n%s", marker, got, text)
		}
	}
}
