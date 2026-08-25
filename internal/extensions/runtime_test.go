package extensions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

// TestHostAPIReachesHandlers verifies that the API from the HostAPI seam
// is what handlers receive through their handler context.
func TestHostAPIReachesHandlers(t *testing.T) {
	reg := NewRegistry()
	api := &stubAPI{}

	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			if err := ctx.AppendEntry("custom", nil); err != nil {
				return nil, err
			}
			return nil, nil
		}).
		build())

	rt := NewRuntime(reg).SetAPI(func() sdk.API { return api })
	d := rt.Dispatcher()
	if _, err := d.Context(t.Context(), agent.ContextRequest{}); err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(api.calls) != 1 || api.calls[0] != "AppendEntry:custom" {
		t.Fatalf("api calls = %v, want [AppendEntry:custom]", api.calls)
	}
}

// TestHostContextPassthrough verifies that a host-provided handler
// context is what handlers receive, bypassing the default context.
func TestHostContextPassthrough(t *testing.T) {
	reg := NewRegistry()
	fake := &fakeContext{mode: sdk.ModeInteractive}
	var gotMode sdk.Mode
	var gotCwd string

	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			gotMode = ctx.Mode()
			gotCwd = ctx.Cwd()
			return nil, nil
		}).
		build())

	rt := NewRuntime(reg).SetContext(func() sdk.HandlerContext { return fake })
	d := rt.Dispatcher()
	if _, err := d.Context(t.Context(), agent.ContextRequest{}); err != nil {
		t.Fatalf("context: %v", err)
	}
	if gotMode != sdk.ModeInteractive {
		t.Fatalf("mode = %q, want the host context's interactive mode", gotMode)
	}
	if gotCwd != "/work" {
		t.Fatalf("cwd = %q, want the host context's cwd", gotCwd)
	}
}

// TestDefaultContextPrintSemantics verifies the default handler context:
// print-mode behavior with the API exposed and the dialogs reporting
// ErrModeUnsupported.
func TestDefaultContextPrintSemantics(t *testing.T) {
	reg := NewRegistry()
	api := &stubAPI{}
	var gotMode sdk.Mode
	var hasUI bool
	var dialogsErr []error

	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			gotMode = ctx.Mode()
			hasUI = ctx.HasUI()
			if _, err := ctx.UI().Confirm("t", "m"); err != nil {
				dialogsErr = append(dialogsErr, err)
			}
			return nil, nil
		}).
		build())

	rt := NewRuntime(reg).SetAPI(func() sdk.API { return api })
	d := rt.Dispatcher()
	if _, err := d.Context(t.Context(), agent.ContextRequest{}); err != nil {
		t.Fatalf("context: %v", err)
	}
	if gotMode != sdk.ModePrint {
		t.Fatalf("mode = %q, want ModePrint in the default context", gotMode)
	}
	if hasUI {
		t.Fatal("HasUI = true, want false in the default context")
	}
	if len(dialogsErr) != 1 || !errors.Is(dialogsErr[0], sdk.ErrModeUnsupported) {
		t.Fatalf("dialog errors = %v, want one ErrModeUnsupported", dialogsErr)
	}
}

// TestDefaultContextSignal verifies the default context exposes the
// dispatch signal as the abort context.
func TestDefaultContextSignal(t *testing.T) {
	reg := NewRegistry()
	signal, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got context.Context

	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			got = ctx.Signal()
			return nil, nil
		}).
		build())

	d := NewRuntime(reg).Dispatcher()
	if _, err := d.Context(signal, agent.ContextRequest{}); err != nil {
		t.Fatalf("context: %v", err)
	}
	if got != signal {
		t.Fatal("the handler must receive the dispatch signal")
	}
}

// TestStartRunsSetupWithHostAPI verifies the full runtime wiring: Start
// runs the setup phase with the API from the host seam, and a second
// Start is rejected.
func TestStartRunsSetupWithHostAPI(t *testing.T) {
	reg := NewRegistry()
	api := &stubAPI{}
	var order []string

	reg.Register(ext("a").
		setup(func(api sdk.API) error {
			order = append(order, "a")
			return api.SetSessionName("a")
		}).
		build())
	reg.Register(ext("b").
		setup(func(api sdk.API) error {
			order = append(order, "b")
			return nil
		}).
		build())

	rt := NewRuntime(reg).SetAPI(func() sdk.API { return api })
	if err := rt.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("setup order = %v, want [a b]", order)
	}
	if len(api.calls) != 1 || api.calls[0] != "SetSessionName:a" {
		t.Fatalf("api calls = %v, want [SetSessionName:a]", api.calls)
	}
	if err := rt.Start(); !errors.Is(err, ErrSetupAlreadyRun) {
		t.Fatalf("second start error = %v, want ErrSetupAlreadyRun", err)
	}
}

// TestRuntimeWithoutRegistryErrors verifies Start on a runtime without a
// registry returns an error instead of panicking.
func TestRuntimeWithoutRegistryErrors(t *testing.T) {
	var rt Runtime
	if err := rt.Start(); err == nil {
		t.Fatal("Start on a runtime without a registry must return an error")
	}
}

// TestLoggerIsInjectable verifies SetLogger installs the logger used for
// dispatch diagnostics.
func TestLoggerIsInjectable(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			return nil, errors.New("boom")
		}).
		build())

	rt := NewRuntime(reg).SetLogger(log)
	if _, err := rt.Dispatcher().Context(t.Context(), agent.ContextRequest{}); err != nil {
		t.Fatalf("context: %v", err)
	}
	if log.count() != 1 || !strings.Contains(log.all()[0], "boom") {
		t.Fatalf("logs = %v, want the injected logger to record the failure", log.all())
	}
}
