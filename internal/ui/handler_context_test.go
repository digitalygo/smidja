package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/sdk"
)

type perCallStubAPI struct {
	sdk.API
	configValue string
}

func (a *perCallStubAPI) ConfigValue(key string) string {
	if key == "marker" {
		return a.configValue
	}
	return ""
}

type perCallStubContext struct {
	sdk.API
	signal context.Context
	cwd    string
	model  *sdk.Model
	system string
}

var _ sdk.HandlerContext = (*perCallStubContext)(nil)

func (c *perCallStubContext) UI() sdk.UI                       { return nil }
func (c *perCallStubContext) Mode() sdk.Mode                   { return sdk.ModePrint }
func (c *perCallStubContext) HasUI() bool                      { return false }
func (c *perCallStubContext) Cwd() string                      { return c.cwd }
func (c *perCallStubContext) SessionManager() sdk.SessionView  { return nil }
func (c *perCallStubContext) ModelRegistry() sdk.ModelRegistry { return nil }
func (c *perCallStubContext) Model() *sdk.Model                { return c.model }
func (c *perCallStubContext) ThinkingLevel() sdk.ThinkingLevel { return sdk.ThinkingMinimal }
func (c *perCallStubContext) Signal() context.Context          { return c.signal }
func (c *perCallStubContext) Abort()                           {}
func (c *perCallStubContext) Shutdown()                        {}
func (c *perCallStubContext) ContextUsage() *sdk.ContextUsage  { return nil }
func (c *perCallStubContext) Compact(sdk.CompactOptions)       {}
func (c *perCallStubContext) SystemPrompt() string             { return c.system }

func perCallRuntime(t *testing.T, runner *Runner, base sdk.HandlerContext) *extensions.Runtime {
	t.Helper()
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	runtime.SetContext(func() sdk.HandlerContext { return base })
	runtime.SetContextDecorator(func(signal context.Context, b sdk.HandlerContext) sdk.HandlerContext {
		return runner.InteractiveHandlerContext(signal, b)
	})
	t.Cleanup(func() { runtime.SetContextDecorator(nil) })
	return runtime
}

func TestPerCallUsesInvocationSignalNotBase(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	baseSignal := context.Background()
	base := &perCallStubContext{signal: baseSignal, cwd: "/work", system: "base-system"}
	runtime := perCallRuntime(t, runner, base)
	invocation, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := runtime.HandlerContext(invocation)
	if got.Signal() != invocation {
		t.Fatal("wrapper must expose the invocation signal, not the host base signal")
	}
	if got.Mode() != sdk.ModeInteractive || !got.HasUI() {
		t.Fatalf("mode/hasUI = %v/%v, want interactive/true", got.Mode(), got.HasUI())
	}
	if base.Signal() != baseSignal {
		t.Fatal("wrapper must not mutate the host base context")
	}
	done := make(chan error, 1)
	go func() {
		_, err := got.UI().Confirm("per-call", "bound to invocation")
		done <- err
	}()
	waitForDialog(t, runner)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Confirm error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dialog bound to the invocation signal did not cancel")
	}
	if runner.dialogs.Active() {
		t.Fatal("dialog still active after invocation cancel")
	}
}

func TestPerCallPreservesBaseCapabilities(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	wantModel := &sdk.Model{ID: "per-call-model"}
	base := &perCallStubContext{
		API:    &perCallStubAPI{configValue: "per-call-config"},
		signal: context.Background(),
		cwd:    "/work/per-call",
		model:  wantModel,
		system: "per-call-system",
	}
	runtime := perCallRuntime(t, runner, base)
	invocation := context.Background()
	got := runtime.HandlerContext(invocation)
	if got.Signal() != invocation {
		t.Fatal("wrapper must expose the invocation signal")
	}
	if got.Cwd() != "/work/per-call" {
		t.Fatalf("Cwd = %q, want base value", got.Cwd())
	}
	if got.Model() != wantModel {
		t.Fatal("Model was not delegated to the base")
	}
	if got.SystemPrompt() != "per-call-system" {
		t.Fatalf("SystemPrompt = %q, want base value", got.SystemPrompt())
	}
	if got.ThinkingLevel() != sdk.ThinkingMinimal {
		t.Fatalf("ThinkingLevel = %q, want base value", got.ThinkingLevel())
	}
	if got.ConfigValue("marker") != "per-call-config" {
		t.Fatalf("ConfigValue = %q, want base API value", got.ConfigValue("marker"))
	}
	if got.Mode() != sdk.ModeInteractive || !got.HasUI() {
		t.Fatalf("mode/hasUI = %v/%v, want interactive/true", got.Mode(), got.HasUI())
	}
}

func TestPerCallConcurrentDialogsIsolateCancellation(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	base := &perCallStubContext{signal: context.Background(), cwd: "/work"}
	runtime := perCallRuntime(t, runner, base)
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	hctxA := runtime.HandlerContext(ctxA)
	hctxB := runtime.HandlerContext(ctxB)
	if hctxA.Signal() != ctxA || hctxB.Signal() != ctxB {
		t.Fatal("concurrent calls must receive distinct signals")
	}
	errA := make(chan error, 1)
	go func() {
		_, err := hctxA.UI().Confirm("first", "first per-call dialog")
		errA <- err
	}()
	waitForDialog(t, runner)
	errB := make(chan error, 1)
	go func() {
		_, err := hctxB.UI().Confirm("second", "second per-call dialog")
		errB <- err
	}()
	waitForWaiters(t, runner, 1)
	cancelB()
	select {
	case err := <-errB:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second dialog error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled second dialog did not resolve")
	}
	select {
	case <-errA:
		t.Fatal("first dialog must remain pending after the second is cancelled")
	case <-time.After(50 * time.Millisecond):
	}
	if !runner.dialogs.Active() {
		t.Fatal("first dialog should still be active")
	}
	runner.view.RenderNow(true)
	terminal.SendInput("\r")
	select {
	case err := <-errA:
		if err != nil {
			t.Fatalf("first dialog error = %v, want nil after accept", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remaining first dialog did not resolve")
	}
}

func TestPerCallSessionStartUsesCallSignal(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	base := &perCallStubContext{signal: context.Background(), cwd: "/work"}
	registry := extensions.NewRegistry()
	var captured []context.Context
	var capturedModes []sdk.Mode
	extension := &perCallSessionExtension{captured: &captured, modes: &capturedModes}
	if err := registry.Register(extension); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(registry)
	runtime.SetContext(func() sdk.HandlerContext { return base })
	runtime.SetContextDecorator(func(signal context.Context, b sdk.HandlerContext) sdk.HandlerContext {
		return runner.InteractiveHandlerContext(signal, b)
	})
	t.Cleanup(func() { runtime.SetContextDecorator(nil) })
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	if err := runtime.Dispatcher().SessionStart(first, string(sdk.SessionStartStartup)); err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	if err := runtime.Dispatcher().SessionStart(second, string(sdk.SessionStartStartup)); err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("captured signals = %d, want 2", len(captured))
	}
	if captured[0] != first || captured[1] != second {
		t.Fatal("SessionStart must use its own call signal, not the host base signal")
	}
	for _, mode := range capturedModes {
		if mode != sdk.ModeInteractive {
			t.Fatalf("hook mode = %v, want interactive", mode)
		}
	}
}

type perCallSessionExtension struct {
	captured *[]context.Context
	modes    *[]sdk.Mode
}

func (e *perCallSessionExtension) ID() string { return "per-call-session" }

func (e *perCallSessionExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	r.OnSessionStart(func(ctx sdk.HandlerContext, _ sdk.SessionStartEvent) error {
		*e.captured = append(*e.captured, ctx.Signal())
		*e.modes = append(*e.modes, ctx.Mode())
		return nil
	})
}

func TestPerCallPrintContextsUnchanged(t *testing.T) {
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	signal, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := runtime.HandlerContext(signal)
	if got.Signal() != signal {
		t.Fatal("print context must keep the invocation signal")
	}
	if got.Mode() != sdk.ModePrint || got.HasUI() {
		t.Fatalf("mode/hasUI = %v/%v, want print/false", got.Mode(), got.HasUI())
	}
	if _, err := got.UI().Confirm("t", "m"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("print Confirm error = %v, want ErrModeUnsupported", err)
	}
	base := &perCallStubContext{signal: context.Background(), cwd: "/work"}
	runtime.SetContext(func() sdk.HandlerContext { return base })
	plain := runtime.HandlerContext(signal)
	if plain.Cwd() != "/work" {
		t.Fatalf("non-TUI host context Cwd = %q, want base value", plain.Cwd())
	}
	if plain.Mode() != sdk.ModePrint || plain.HasUI() {
		t.Fatalf("non-TUI host mode/hasUI = %v/%v, want print/false", plain.Mode(), plain.HasUI())
	}
}

func TestPerCallNilSignalFallbacks(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := &perCallStubContext{signal: baseCtx, cwd: "/work"}
	fallback := runner.InteractiveHandlerContext(nil, base)
	if fallback.Signal() != baseCtx {
		t.Fatal("nil invocation signal must fall back to the base signal")
	}
	empty := runner.InteractiveHandlerContext(nil, nil)
	if empty.Signal() == nil {
		t.Fatal("nil signal and nil base must yield a background signal")
	}
	if empty.Mode() != sdk.ModeInteractive || !empty.HasUI() {
		t.Fatalf("nil-base mode/hasUI = %v/%v, want interactive/true", empty.Mode(), empty.HasUI())
	}
	direct := NewHandlerContext(base, nil, nil, sdk.ModeInteractive)
	if direct.Signal() == nil {
		t.Fatal("NewHandlerContext with nil signal must yield a background signal")
	}
	orphan := &execHandlerContext{}
	if orphan.Signal() == nil {
		t.Fatal("orphan wrapper must yield a background signal")
	}
	nilSignalBase := &perCallStubContext{signal: nil}
	viaBase := (&execHandlerContext{HandlerContext: nilSignalBase}).Signal()
	if viaBase == nil {
		t.Fatal("nil base signal must fall back to background")
	}
}
