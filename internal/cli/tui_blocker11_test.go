package cli

import (
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

type blocker11Event struct {
	kind   string
	reason string
	path   string
	signal context.Context
}

type blocker11Recorder struct {
	mu     sync.Mutex
	events []blocker11Event
}

func (r *blocker11Recorder) record(kind, reason, path string, signal context.Context) {
	r.mu.Lock()
	r.events = append(r.events, blocker11Event{kind: kind, reason: reason, path: path, signal: signal})
	r.mu.Unlock()
}

func (r *blocker11Recorder) snapshot() []blocker11Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]blocker11Event(nil), r.events...)
}

func (r *blocker11Recorder) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (r *blocker11Recorder) MessageEnd(ctx context.Context, m *agent.Message) (*agent.Message, error) {
	return m, nil
}

func (r *blocker11Recorder) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	return nil
}

func (r *blocker11Recorder) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	return nil
}

func (r *blocker11Recorder) ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (agent.ToolCallDecision, error) {
	return agent.ToolCallDecision{FinalArgs: args}, nil
}

func (r *blocker11Recorder) ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res agent.Result) (agent.Result, error) {
	return res, nil
}

func (r *blocker11Recorder) SessionStart(ctx context.Context, reason string) error {
	r.record("start", reason, "", ctx)
	return nil
}

func (r *blocker11Recorder) SessionShutdown(ctx context.Context, reason string) error {
	r.record("shutdown", reason, "", ctx)
	return nil
}

func (r *blocker11Recorder) SessionStartWithFiles(ctx context.Context, reason, previousPath string) error {
	r.record("start", reason, previousPath, ctx)
	return nil
}

func (r *blocker11Recorder) SessionShutdownWithFiles(ctx context.Context, reason, targetPath string) error {
	r.record("shutdown", reason, targetPath, ctx)
	return nil
}

type blocker11CaptureExtension struct {
	mu              sync.Mutex
	starts          []sdk.SessionStartEvent
	shutdowns       []sdk.SessionShutdownEvent
	startModes      []sdk.Mode
	shutdownModes   []sdk.Mode
	startHasUI      []bool
	shutdownHasUI   []bool
	startSignals    []context.Context
	shutdownSignals []context.Context
	pathsAtStart    []string
	pathsAtShutdown []string
	bridge          *tuiBridge
	blockStart      chan struct{}
	enteredStart    chan struct{}
	enterOnce       sync.Once
	commands        *extensions.CommandCatalog
	startupRegister string
}

func (e *blocker11CaptureExtension) ID() string {
	return "blocker11-capture"
}

func (e *blocker11CaptureExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	r.OnSessionStart(func(ctx sdk.HandlerContext, ev sdk.SessionStartEvent) error {
		e.mu.Lock()
		e.starts = append(e.starts, ev)
		e.startModes = append(e.startModes, ctx.Mode())
		e.startHasUI = append(e.startHasUI, ctx.HasUI())
		e.startSignals = append(e.startSignals, ctx.Signal())
		if e.bridge != nil && e.bridge.rd != nil {
			e.pathsAtStart = append(e.pathsAtStart, e.bridge.rd.sessionPath)
		}
		commands := e.commands
		registerName := e.startupRegister
		e.mu.Unlock()
		if registerName != "" && commands != nil {
			_, _ = commands.Register(registerName, sdk.Command{
				Description: "startup registered",
				Handler: func(sdk.CommandContext, string) error {
					return nil
				},
			})
		}
		if e.enteredStart != nil {
			e.enterOnce.Do(func() { close(e.enteredStart) })
		}
		if e.blockStart != nil {
			<-e.blockStart
		}
		return nil
	})
	r.OnSessionShutdown(func(ctx sdk.HandlerContext, ev sdk.SessionShutdownEvent) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.shutdowns = append(e.shutdowns, ev)
		e.shutdownModes = append(e.shutdownModes, ctx.Mode())
		e.shutdownHasUI = append(e.shutdownHasUI, ctx.HasUI())
		e.shutdownSignals = append(e.shutdownSignals, ctx.Signal())
		if e.bridge != nil && e.bridge.rd != nil {
			e.pathsAtShutdown = append(e.pathsAtShutdown, e.bridge.rd.sessionPath)
		}
		return nil
	})
}

func blocker11NamesSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func blocker11HelpSet(t *testing.T, fixture *bridgeFixture) map[string]bool {
	t.Helper()
	set := make(map[string]bool)
	for _, entry := range fixture.bridge.effectiveHelpEntries() {
		set[entry.Name] = true
	}
	return set
}

func blocker11AutocompleteSet(t *testing.T, fixture *bridgeFixture) map[string]bool {
	t.Helper()
	set := make(map[string]bool)
	for _, item := range fixture.bridge.autocompleteInventory() {
		set[item.Value] = true
	}
	return set
}

func TestBlocker11CollisionAliasesRemainAccessible(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	executed := make(chan struct{}, 4)
	if _, err := fixture.commands.Register("help", sdk.Command{
		Description: "extension help",
		Handler: func(sdk.CommandContext, string) error {
			executed <- struct{}{}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	names := blocker11NamesSet(fixture.bridge.commandNames())
	if !names["help"] {
		t.Fatal("host help missing from inventory")
	}
	if !names["help2"] {
		t.Fatalf("collision alias help2 missing, names = %v", fixture.bridge.commandNames())
	}
	help := blocker11HelpSet(t, fixture)
	if !help["help"] || !help["help2"] {
		t.Fatalf("help entries missing collision pair: %v", help)
	}
	autocomplete := blocker11AutocompleteSet(t, fixture)
	if !autocomplete["help"] || !autocomplete["help2"] {
		t.Fatalf("autocomplete missing collision pair: %v", autocomplete)
	}
	if !fixture.bridge.hasCommand("help") || !fixture.bridge.hasCommand("help2") {
		t.Fatal("hasCommand does not match inventory for collision pair")
	}
	fixture.bridge.handle("/help2")
	select {
	case <-executed:
	case <-time.After(3 * time.Second):
		t.Fatal("collision alias help2 did not dispatch")
	}
	count := 0
	for _, name := range fixture.bridge.commandNames() {
		if name == "help" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("host help appears %d times, want 1", count)
	}
}

func TestBlocker11PostCommandRegistration(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	secondRan := make(chan struct{}, 2)
	if _, err := fixture.commands.Register("first", sdk.Command{
		Description: "registers second",
		Handler: func(sdk.CommandContext, string) error {
			_, _ = fixture.commands.Register("second", sdk.Command{
				Description: "late command",
				Handler: func(sdk.CommandContext, string) error {
					secondRan <- struct{}{}
					return nil
				},
			})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.bridge.hasCommand("second") {
		t.Fatal("second must not exist before first runs")
	}
	fixture.bridge.handle("/first")
	if !fixture.bridge.hasCommand("second") {
		t.Fatal("second must be dispatchable after first runs")
	}
	help := blocker11HelpSet(t, fixture)
	autocomplete := blocker11AutocompleteSet(t, fixture)
	if !help["second"] {
		t.Fatal("second missing from help after registration")
	}
	if !autocomplete["second"] {
		t.Fatal("second missing from autocomplete after registration")
	}
	names := blocker11NamesSet(fixture.bridge.commandNames())
	if !names["first"] || !names["second"] {
		t.Fatalf("inventory missing first/second: %v", names)
	}
	fixture.bridge.handle("/second")
	select {
	case <-secondRan:
	case <-time.After(3 * time.Second):
		t.Fatal("late registered second did not dispatch")
	}
	fixture.bridge.handle("/second")
	select {
	case <-secondRan:
	case <-time.After(3 * time.Second):
		t.Fatal("second dispatch is not repeatable")
	}
}

func TestBlocker11HelpAutocompleteDispatchIdentity(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if _, err := fixture.commands.Register("identity", sdk.Command{
		Description: "identity probe",
		Handler: func(sdk.CommandContext, string) error {
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	names := fixture.bridge.commandNames()
	help := blocker11HelpSet(t, fixture)
	autocomplete := blocker11AutocompleteSet(t, fixture)
	if len(names) != len(help) || len(names) != len(autocomplete) {
		t.Fatalf("inventory %d help %d autocomplete %d must agree", len(names), len(help), len(autocomplete))
	}
	for _, name := range names {
		if !help[name] {
			t.Errorf("command %q missing from help", name)
		}
		if !autocomplete[name] {
			t.Errorf("command %q missing from autocomplete", name)
		}
		if !fixture.bridge.hasCommand(name) {
			t.Errorf("command %q missing from hasCommand", name)
		}
	}
	for name := range help {
		if !blocker11NamesSet(names)[name] {
			t.Errorf("help has extra command %q", name)
		}
	}
	for name := range autocomplete {
		if !blocker11NamesSet(names)[name] {
			t.Errorf("autocomplete has extra command %q", name)
		}
	}
	if !fixture.bridge.dispatchCommand("identity", "") {
		t.Fatal("dispatchCommand identity must succeed")
	}
	if fixture.bridge.dispatchCommand("missing-command-xyz", "") {
		t.Fatal("dispatchCommand for unknown must be false")
	}
}

func TestBlocker11DispatcherWithFilesData(t *testing.T) {
	registry := extensions.NewRegistry()
	capture := &blocker11CaptureExtension{}
	if err := registry.Register(capture); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(registry)
	dispatcher := runtime.Dispatcher()
	ctx := context.Background()
	if err := dispatcher.SessionShutdownWithFiles(ctx, "new", "/tmp/target.jsonl"); err != nil {
		t.Fatalf("SessionShutdownWithFiles: %v", err)
	}
	if err := dispatcher.SessionStartWithFiles(ctx, "new", "/tmp/previous.jsonl"); err != nil {
		t.Fatalf("SessionStartWithFiles: %v", err)
	}
	capture.mu.Lock()
	if len(capture.shutdowns) != 1 {
		capture.mu.Unlock()
		t.Fatalf("shutdowns = %d, want 1", len(capture.shutdowns))
	}
	if len(capture.starts) != 1 {
		capture.mu.Unlock()
		t.Fatalf("starts = %d, want 1", len(capture.starts))
	}
	shutdownReason := capture.shutdowns[0].Reason
	shutdownTarget := capture.shutdowns[0].TargetSessionFile
	startReason := capture.starts[0].Reason
	startPrevious := capture.starts[0].PreviousSessionFile
	capture.mu.Unlock()
	if shutdownReason != sdk.SessionShutdownNew {
		t.Fatalf("shutdown reason = %q, want new", shutdownReason)
	}
	if shutdownTarget != "/tmp/target.jsonl" {
		t.Fatalf("shutdown target = %q", shutdownTarget)
	}
	if startReason != sdk.SessionStartNew {
		t.Fatalf("start reason = %q, want new", startReason)
	}
	if startPrevious != "/tmp/previous.jsonl" {
		t.Fatalf("start previous = %q", startPrevious)
	}
	if err := dispatcher.SessionStart(ctx, "resume"); err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if err := dispatcher.SessionShutdown(ctx, "resume"); err != nil {
		t.Fatalf("SessionShutdown: %v", err)
	}
	if len(capture.starts) != 2 || len(capture.shutdowns) != 2 {
		t.Fatalf("legacy calls not recorded: starts %d shutdowns %d", len(capture.starts), len(capture.shutdowns))
	}
}

func TestBlocker11HookDecoratorWithFilesDelegation(t *testing.T) {
	registry := extensions.NewRegistry()
	capture := &blocker11CaptureExtension{}
	if err := registry.Register(capture); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(registry)
	decorator := ui.NewHookDecorator(runtime.Dispatcher(), ui.NewTurnScope(nil, nil))
	ctx := context.Background()
	if err := decorator.SessionShutdownWithFiles(ctx, "fork", "/tmp/fork-target.jsonl"); err != nil {
		t.Fatalf("decorator shutdown: %v", err)
	}
	if err := decorator.SessionStartWithFiles(ctx, "fork", "/tmp/fork-previous.jsonl"); err != nil {
		t.Fatalf("decorator start: %v", err)
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.shutdowns) != 1 || capture.shutdowns[0].TargetSessionFile != "/tmp/fork-target.jsonl" {
		t.Fatalf("decorator shutdown not forwarded: %+v", capture.shutdowns)
	}
	if len(capture.starts) != 1 || capture.starts[0].PreviousSessionFile != "/tmp/fork-previous.jsonl" {
		t.Fatalf("decorator start not forwarded: %+v", capture.starts)
	}
	empty := ui.NewHookDecorator(nil, ui.NewTurnScope(nil, nil))
	if err := empty.SessionStartWithFiles(ctx, "new", "prev"); err != nil {
		t.Fatalf("nil decorator start: %v", err)
	}
	if err := empty.SessionShutdownWithFiles(ctx, "new", "target"); err != nil {
		t.Fatalf("nil decorator shutdown: %v", err)
	}
}

func blocker11FixtureWithRecorder(t *testing.T, recorder *blocker11Recorder) *bridgeFixture {
	t.Helper()
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.rd.hooks = recorder
	return fixture
}

func TestBlocker11TransitionNewOrderingDataReasons(t *testing.T) {
	recorder := &blocker11Recorder{}
	fixture := blocker11FixtureWithRecorder(t, recorder)
	previous := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/new")
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %d, want shutdown plus start", len(events))
	}
	if events[0].kind != "shutdown" || events[1].kind != "start" {
		t.Fatalf("ordering = %v, want shutdown before start", events)
	}
	if events[0].reason != "new" || events[1].reason != "new" {
		t.Fatalf("reasons = %q %q, want new new", events[0].reason, events[1].reason)
	}
	target := fixture.bridge.rd.sessionPath
	if target == previous {
		t.Fatal("new must change the session path")
	}
	if events[0].path != target {
		t.Fatalf("shutdown target = %q, want %q", events[0].path, target)
	}
	if events[1].path != previous {
		t.Fatalf("start previous = %q, want %q", events[1].path, previous)
	}
	for _, ev := range events {
		if ev.signal == nil {
			t.Fatal("transition hook signal is nil")
		}
	}
}

func TestBlocker11TransitionResumeOrdering(t *testing.T) {
	recorder := &blocker11Recorder{}
	fixture := blocker11FixtureWithRecorder(t, recorder)
	target := seedStoredSession(t, fixture, "resume ordering")
	previous := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/resume " + target)
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].kind != "shutdown" || events[1].kind != "start" {
		t.Fatalf("ordering = %v", events)
	}
	if events[0].reason != "resume" || events[1].reason != "resume" {
		t.Fatalf("reasons = %q %q, want resume", events[0].reason, events[1].reason)
	}
	if fixture.bridge.rd.sessionPath != target {
		t.Fatalf("resumed path = %s, want %s", fixture.bridge.rd.sessionPath, target)
	}
	if events[0].path != target {
		t.Fatalf("shutdown target = %q, want %q", events[0].path, target)
	}
	if events[1].path != previous {
		t.Fatalf("start previous = %q, want %q", events[1].path, previous)
	}
}

func TestBlocker11TransitionForkOrdering(t *testing.T) {
	recorder := &blocker11Recorder{}
	fixture := blocker11FixtureWithRecorder(t, recorder)
	seedTurn(t, fixture.sess, "fork ordering origin")
	previous := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/fork")
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].reason != "fork" || events[1].reason != "fork" {
		t.Fatalf("reasons = %q %q, want fork", events[0].reason, events[1].reason)
	}
	target := fixture.bridge.rd.sessionPath
	if target == previous {
		t.Fatal("fork must change the path")
	}
	if events[0].path != target || events[1].path != previous {
		t.Fatalf("paths shutdown %q start %q, want %q %q", events[0].path, events[1].path, target, previous)
	}
}

func TestBlocker11TransitionViaCommandContext(t *testing.T) {
	recorder := &blocker11Recorder{}
	fixture := blocker11FixtureWithRecorder(t, recorder)
	seedTurn(t, fixture.sess, "context origin")
	origin := fixture.bridge.rd.sessionPath
	if _, err := fixture.commands.Register("ctxnew", sdk.Command{
		Description: "context new",
		Handler: func(ctx sdk.CommandContext, _ string) error {
			_, err := ctx.NewSession(sdk.NewSessionOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/ctxnew")
	events := recorder.snapshot()
	if len(events) != 2 || events[0].reason != "new" || events[1].reason != "new" {
		t.Fatalf("ctx new events = %+v", events)
	}
	afterNew := fixture.bridge.rd.sessionPath
	if afterNew == origin {
		t.Fatal("context new must switch files")
	}
	target := seedStoredSession(t, fixture, "context switch target")
	if _, err := fixture.commands.Register("ctxswitch", sdk.Command{
		Description: "context switch",
		Handler: func(ctx sdk.CommandContext, _ string) error {
			_, err := ctx.SwitchSession(target, sdk.SwitchOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/ctxswitch")
	events = recorder.snapshot()
	if len(events) != 4 {
		t.Fatalf("events after switch = %d, want 4", len(events))
	}
	if events[2].reason != "resume" || events[3].reason != "resume" {
		t.Fatalf("switch reasons = %q %q", events[2].reason, events[3].reason)
	}
	if fixture.bridge.rd.sessionPath != target {
		t.Fatalf("switch path = %s, want %s", fixture.bridge.rd.sessionPath, target)
	}
	if _, err := fixture.commands.Register("ctxfork", sdk.Command{
		Description: "context fork",
		Handler: func(ctx sdk.CommandContext, _ string) error {
			_, err := ctx.Fork("", sdk.ForkOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	beforeFork := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/ctxfork")
	events = recorder.snapshot()
	if len(events) != 6 {
		t.Fatalf("events after fork = %d, want 6", len(events))
	}
	if events[4].reason != "fork" || events[5].reason != "fork" {
		t.Fatalf("fork reasons = %q %q", events[4].reason, events[5].reason)
	}
	if fixture.bridge.rd.sessionPath == beforeFork {
		t.Fatal("context fork must switch files")
	}
}

func TestBlocker11HooksReceiveCommittedContext(t *testing.T) {
	registry := extensions.NewRegistry()
	capture := &blocker11CaptureExtension{}
	if err := registry.Register(capture); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(registry)
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.rd.hooks = runtime.Dispatcher()
	fixture.bridge.rd.handlerContext = func(signal context.Context) sdk.HandlerContext {
		return runtime.HandlerContext(signal)
	}
	runtime.SetContextDecorator(func(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext {
		return fixture.runner.InteractiveHandlerContext(signal, base)
	})
	defer runtime.SetContextDecorator(nil)
	capture.bridge = fixture.bridge
	previous := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/new")
	target := fixture.bridge.rd.sessionPath
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.shutdowns) != 1 || len(capture.starts) != 1 {
		t.Fatalf("hooks = %d shutdowns %d starts", len(capture.shutdowns), len(capture.starts))
	}
	if capture.shutdowns[0].Reason != sdk.SessionShutdownNew {
		t.Fatalf("shutdown reason = %q", capture.shutdowns[0].Reason)
	}
	if capture.starts[0].Reason != sdk.SessionStartNew {
		t.Fatalf("start reason = %q", capture.starts[0].Reason)
	}
	if capture.shutdowns[0].TargetSessionFile != target {
		t.Fatalf("shutdown target = %q, want %q", capture.shutdowns[0].TargetSessionFile, target)
	}
	if capture.starts[0].PreviousSessionFile != previous {
		t.Fatalf("start previous = %q, want %q", capture.starts[0].PreviousSessionFile, previous)
	}
	for _, mode := range append(capture.startModes, capture.shutdownModes...) {
		if mode != sdk.ModeInteractive {
			t.Fatalf("hook mode = %v, want interactive", mode)
		}
	}
	for _, hasUI := range append(capture.startHasUI, capture.shutdownHasUI...) {
		if !hasUI {
			t.Fatal("hook must have UI")
		}
	}
	for _, signal := range append(capture.startSignals, capture.shutdownSignals...) {
		if signal == nil {
			t.Fatal("hook signal is nil")
		}
	}
	if len(capture.pathsAtStart) != 1 || capture.pathsAtStart[0] != target {
		t.Fatalf("start hook path = %v, want committed %q", capture.pathsAtStart, target)
	}
	if len(capture.pathsAtShutdown) != 1 || capture.pathsAtShutdown[0] != target {
		t.Fatalf("shutdown hook path = %v, want committed %q", capture.pathsAtShutdown, target)
	}
	if len(fixture.bridge.autocompleteInventory()) == 0 {
		t.Fatal("inventory must be refreshed after new start")
	}
}

func TestBlocker11FailedRollbackNoEvents(t *testing.T) {
	recorder := &blocker11Recorder{}
	fixture := blocker11FixtureWithRecorder(t, recorder)
	origin := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/resume /tmp/missing-blocker11.jsonl")
	if events := recorder.snapshot(); len(events) != 0 {
		t.Fatalf("failed resume emitted %d events", len(events))
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatal("failed resume changed the path")
	}
	if current := fixture.bridge.sessions.Current(); current == nil || current.path != origin {
		t.Fatal("failed resume changed the owner")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	next, err := fixture.bridge.sessions.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.bridge.commitPrepared(canceled, next, "started a new session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commit = %v", err)
	}
	if events := recorder.snapshot(); len(events) != 0 {
		t.Fatalf("canceled transition emitted %d events", len(events))
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatal("canceled transition changed the path")
	}
	candidate, err := fixture.bridge.sessions.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	fixture.bridge.applyStep = func(*activeSession) error {
		return errors.New("display boom blocker11")
	}
	if err := fixture.bridge.commitPrepared(context.Background(), candidate, "started a new session"); err == nil {
		t.Fatal("failing apply must return an error")
	}
	if events := recorder.snapshot(); len(events) != 0 {
		t.Fatalf("failed apply emitted %d events", len(events))
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatal("failed apply changed the path")
	}
	fixture.bridge.applyStep = nil
}

func TestBlocker11NextQueuedPromptAfterStartHook(t *testing.T) {
	registry := extensions.NewRegistry()
	capture := &blocker11CaptureExtension{
		enteredStart: make(chan struct{}),
		blockStart:   make(chan struct{}),
	}
	if err := registry.Register(capture); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(registry)
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.rd.hooks = runtime.Dispatcher()
	fixture.bridge.rd.handlerContext = func(signal context.Context) sdk.HandlerContext {
		return runtime.HandlerContext(signal)
	}
	runtime.SetContextDecorator(func(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext {
		return fixture.runner.InteractiveHandlerContext(signal, base)
	})
	defer runtime.SetContextDecorator(nil)
	capture.bridge = fixture.bridge
	capture.commands = fixture.commands
	capture.startupRegister = "queued-late"
	fixture.bridge.rd.client = &capturingClient{script: []*agent.AssistantMessage{textStop("late answer")}}
	jobs := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() {
		jobs <- struct{}{}
	}
	previous := fixture.bridge.rd.sessionPath
	fixture.bridge.submit("/new")
	select {
	case <-capture.enteredStart:
	case <-time.After(5 * time.Second):
		t.Fatal("transition start hook did not run")
	}
	fixture.bridge.submit("queued after transition")
	assertNever(t, 200*time.Millisecond, "queued prompt ran before start hook finished", func() bool {
		select {
		case <-jobs:
			return true
		default:
			return false
		}
	})
	close(capture.blockStart)
	for i := 0; i < 2; i++ {
		select {
		case <-jobs:
		case <-time.After(5 * time.Second):
			t.Fatal("queued jobs did not finish after start hook")
		}
	}
	if fixture.bridge.rd.sessionPath == previous {
		t.Fatal("transition must switch files before queued work")
	}
	if !fixture.bridge.hasCommand("queued-late") {
		t.Fatal("startup registered command missing after transition")
	}
	help := blocker11HelpSet(t, fixture)
	autocomplete := blocker11AutocompleteSet(t, fixture)
	if !help["queued-late"] || !autocomplete["queued-late"] {
		t.Fatal("late command missing from help/autocomplete after transition")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestBlocker11RetainedContextRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	path := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	var retained sdk.CommandContext
	if _, err := fixture.commands.Register("capture-blocker11", sdk.Command{
		Description: "capture retained",
		Handler: func(ctx sdk.CommandContext, _ string) error {
			retained = ctx
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/capture-blocker11")
	if retained == nil {
		t.Fatal("capture did not run")
	}
	if _, err := retained.NewSession(sdk.NewSessionOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("retained NewSession = %v", err)
	}
	if _, err := retained.Fork("", sdk.ForkOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("retained Fork = %v", err)
	}
	if _, err := retained.SwitchSession(path, sdk.SwitchOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("retained Switch = %v", err)
	}
	if fixture.bridge.rd.sessionPath != path {
		t.Error("retained context changed the path")
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Errorf("retained context created %d files", after-before)
	}
}

func TestBlocker11StartupHookRegistrationBeforeAdmission(t *testing.T) {
	log := &runTUIEventLog{}
	extension := &runTUIHookExtension{
		log:        log,
		startEnter: make(chan struct{}),
		startGate:  make(chan struct{}),
	}
	client := newRunTUITurnClient(log, "startup answer")
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
		t.Fatal("SessionStart did not run")
	}
	fixture.terminal.SendInput("prompt during startup")
	fixture.terminal.SendInput("\r")
	assertNever(t, 200*time.Millisecond, "prompt ran before startup", func() bool {
		return client.callCount() != 0
	})
	close(extension.startGate)
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("queued prompt did not run after startup")
	}
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return")
	}
	events := log.snapshot()
	if len(events) == 0 || events[0] != "start" {
		t.Fatalf("events = %v, want start first", events)
	}
}

type blocker11StartupRegisterExtension struct {
	commands   *extensions.CommandCatalog
	entered    chan struct{}
	registered chan struct{}
	once       sync.Once
}

func (e *blocker11StartupRegisterExtension) ID() string {
	return "blocker11-startup-register"
}

func (e *blocker11StartupRegisterExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	r.OnSessionStart(func(sdk.HandlerContext, sdk.SessionStartEvent) error {
		e.once.Do(func() { close(e.entered) })
		if e.commands != nil {
			_, _ = e.commands.Register("startup-registered", sdk.Command{
				Description: "from startup",
				Handler: func(sdk.CommandContext, string) error {
					return nil
				},
			})
			close(e.registered)
		}
		return nil
	})
}

func TestBlocker11StartupRegistersCommandDispatchable(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	registry := extensions.NewRegistry()
	startup := &blocker11StartupRegisterExtension{
		commands:   fixture.commands,
		entered:    make(chan struct{}),
		registered: make(chan struct{}),
	}
	if err := registry.Register(startup); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(registry)
	fixture.bridge.rd.hooks = runtime.Dispatcher()
	startupCtx := context.Background()
	if err := runtime.Dispatcher().SessionStart(startupCtx, string(sdk.SessionStartStartup)); err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	select {
	case <-startup.registered:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not register the command")
	}
	fixture.bridge.syncCommandInventory()
	if !fixture.bridge.hasCommand("startup-registered") {
		t.Fatal("startup registered command missing from dispatch")
	}
	help := blocker11HelpSet(t, fixture)
	autocomplete := blocker11AutocompleteSet(t, fixture)
	if !help["startup-registered"] || !autocomplete["startup-registered"] {
		t.Fatal("startup command missing from help/autocomplete")
	}
	if !fixture.bridge.dispatchCommand("startup-registered", "") {
		t.Fatal("startup command not dispatchable")
	}
	names := blocker11NamesSet(fixture.bridge.commandNames())
	if !names["startup-registered"] {
		t.Fatal("startup command missing from inventory")
	}
}

func TestBlocker11RefreshAfterSessionTransition(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if _, err := fixture.commands.Register("before-switch", sdk.Command{
		Description: "before",
		Handler: func(sdk.CommandContext, string) error {
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.syncCommandInventory()
	before := blocker11AutocompleteSet(t, fixture)
	if !before["before-switch"] {
		t.Fatal("before-switch missing before transition")
	}
	previous := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/new")
	target := fixture.bridge.rd.sessionPath
	if target == previous {
		t.Fatal("new must switch files")
	}
	after := blocker11AutocompleteSet(t, fixture)
	if !after["before-switch"] {
		t.Fatal("inventory lost commands across session transition")
	}
	help := blocker11HelpSet(t, fixture)
	if !help["before-switch"] {
		t.Fatal("help lost commands across transition")
	}
	if !fixture.bridge.dispatchCommand("before-switch", "") {
		t.Fatal("command not dispatchable after transition")
	}
}

func TestBlocker11MultipleCollisionsDeterministic(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if _, err := fixture.commands.Register("model", sdk.Command{
		Description: "first model override",
		Handler: func(sdk.CommandContext, string) error {
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.commands.Register("model", sdk.Command{
		Description: "second model override",
		Handler: func(sdk.CommandContext, string) error {
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	names := fixture.bridge.commandNames()
	seen := map[string]int{}
	for _, name := range names {
		seen[name]++
	}
	if seen["model"] != 1 {
		t.Fatalf("host model appears %d times", seen["model"])
	}
	aliasCount := 0
	for _, name := range names {
		if strings.HasPrefix(name, "model") && name != "model" {
			aliasCount++
			if !fixture.bridge.hasCommand(name) {
				t.Errorf("alias %q not dispatchable", name)
			}
		}
	}
	if aliasCount != 2 {
		t.Fatalf("aliases for double collision = %d, want 2, names %v", aliasCount, names)
	}
	first := fixture.bridge.commandNames()
	second := fixture.bridge.commandNames()
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatal("inventory is not deterministic")
	}
}

type blocker11LegacyHooks struct {
	mu     sync.Mutex
	events []blocker11Event
}

func (r *blocker11LegacyHooks) record(kind, reason string, signal context.Context) {
	r.mu.Lock()
	r.events = append(r.events, blocker11Event{kind: kind, reason: reason, signal: signal})
	r.mu.Unlock()
}

func (r *blocker11LegacyHooks) snapshot() []blocker11Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]blocker11Event(nil), r.events...)
}

func (r *blocker11LegacyHooks) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (r *blocker11LegacyHooks) MessageEnd(ctx context.Context, m *agent.Message) (*agent.Message, error) {
	return m, nil
}

func (r *blocker11LegacyHooks) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	return nil
}

func (r *blocker11LegacyHooks) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	return nil
}

func (r *blocker11LegacyHooks) ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (agent.ToolCallDecision, error) {
	return agent.ToolCallDecision{FinalArgs: args}, nil
}

func (r *blocker11LegacyHooks) ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res agent.Result) (agent.Result, error) {
	return res, nil
}

func (r *blocker11LegacyHooks) SessionStart(ctx context.Context, reason string) error {
	r.record("start", reason, ctx)
	return nil
}

func (r *blocker11LegacyHooks) SessionShutdown(ctx context.Context, reason string) error {
	r.record("shutdown", reason, ctx)
	return nil
}

func TestBlocker11TransitionReasonFallback(t *testing.T) {
	if got := sessionTransitionReason("resumed session", nil); got != string(sdk.SessionStartResume) {
		t.Fatalf("reason = %q", got)
	}
	if got := sessionTransitionReason("forked session", nil); got != string(sdk.SessionStartFork) {
		t.Fatalf("reason = %q", got)
	}
	if got := sessionTransitionReason("started a new session", nil); got != string(sdk.SessionStartNew) {
		t.Fatalf("reason = %q", got)
	}
	if got := sessionTransitionReason("", &activeSession{mode: sessionModeResume}); got != string(sdk.SessionStartResume) {
		t.Fatalf("fallback resume = %q", got)
	}
	if got := sessionTransitionReason("", &activeSession{mode: sessionModeFork}); got != string(sdk.SessionStartFork) {
		t.Fatalf("fallback fork = %q", got)
	}
	if got := sessionTransitionReason("", &activeSession{mode: sessionModeNew}); got != string(sdk.SessionStartNew) {
		t.Fatalf("fallback new = %q", got)
	}
	if got := sessionTransitionReason("", nil); got != string(sdk.SessionStartNew) {
		t.Fatalf("fallback nil = %q", got)
	}
	if got := sessionTransitionReason("unrelated notice", &activeSession{mode: sessionModeResume}); got != string(sdk.SessionStartResume) {
		t.Fatalf("unrelated fallback = %q", got)
	}
}

func TestBlocker11DispatchFallbackAndNilGuards(t *testing.T) {
	legacy := &blocker11LegacyHooks{}
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.rd.hooks = legacy
	previous := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/new")
	events := legacy.snapshot()
	if len(events) != 2 {
		t.Fatalf("legacy events = %d, want 2", len(events))
	}
	if events[0].reason != "new" || events[1].reason != "new" {
		t.Fatalf("legacy reasons = %+v", events)
	}
	if fixture.bridge.rd.sessionPath == previous {
		t.Fatal("legacy transition must switch files")
	}
	bare := &tuiBridge{}
	bare.dispatchSessionLifecycle(nil, "new", "prev", "target")
	bare.rd = &runDeps{}
	bare.dispatchSessionLifecycle(nil, "new", "prev", "target")
	bare.rd.hooks = legacy
	bare.ctx = nil
	bare.dispatchSessionLifecycle(nil, "new", "prev", "target")
	withSignal, cancel := context.WithCancel(context.Background())
	cancel()
	bare.ctx = withSignal
	bare.dispatchSessionLifecycle(nil, "new", "prev", "target")
	bare.dispatchSessionLifecycle(withSignal, "resume", "prev", "target")
	nilRunner := &tuiBridge{}
	nilRunner.syncCommandInventory()
	fixture.bridge.syncCommandInventory()
}
