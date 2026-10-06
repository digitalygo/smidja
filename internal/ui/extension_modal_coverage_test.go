package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/sdk"
)

type immediateModalComponent struct {
	extensionTestComponent
	value string
}

func (c *immediateModalComponent) SetModalDone(done func(sdk.ModalResult)) {
	done(sdk.ModalResult{Value: c.value})
}

func TestRunnerModalDirectEntryPoints(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	direct := &extensionTestComponent{text: "DIRECT MODAL"}
	result, err := runner.ShowModal(func(done func(sdk.ModalResult)) sdk.Component {
		done(sdk.ModalResult{Value: "direct"})
		return direct
	})
	if err != nil || result.Value != "direct" {
		t.Fatalf("Runner.ShowModal = %+v err=%v", result, err)
	}
	if direct.DisposeCount() != 1 {
		t.Fatalf("direct modal dispose count = %d, want 1", direct.DisposeCount())
	}
	if err := registry.RegisterComponent("registered", func() sdk.Component {
		return &immediateModalComponent{value: "component"}
	}); err != nil {
		t.Fatal(err)
	}
	result, err = runner.ShowComponent("registered")
	if err != nil || result.Value != "component" {
		t.Fatalf("Runner.ShowComponent = %+v err=%v", result, err)
	}
	if _, err := runner.ShowComponent("missing"); err == nil {
		t.Fatal("missing component must fail")
	}
}

func TestRunnerModalFailurePaths(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	if _, err := runner.showModalWithContext(context.Background(), func(done func(sdk.ModalResult)) sdk.Component {
		panic("factory boom")
	}); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("panicking modal factory = %v", err)
	}
	if _, err := runner.showModalWithContext(context.Background(), func(done func(sdk.ModalResult)) sdk.Component {
		return nil
	}); !errors.Is(err, ErrExtensionUINilFactory) {
		t.Fatalf("nil modal component = %v", err)
	}
	component := &extensionTestComponent{text: "CONTEXT"}
	result, err := runner.showModalWithContext(nil, func(done func(sdk.ModalResult)) sdk.Component {
		done(sdk.ModalResult{Value: "nil-context"})
		return component
	})
	if err != nil || result.Value != "nil-context" {
		t.Fatalf("nil context modal = %+v err=%v", result, err)
	}
	if _, err := runner.showExtensionModal(context.Background(), func(done func(sdk.ModalResult)) (tui.Component, error) {
		return nil, nil
	}); !errors.Is(err, errNilDialog) {
		t.Fatalf("nil extension modal = %v", err)
	}
	closed := &extensionTestComponent{text: "CLOSED"}
	runner.dialogs.shutdown()
	if _, err := runner.showModalWithContext(context.Background(), func(done func(sdk.ModalResult)) sdk.Component {
		return closed
	}); !errors.Is(err, errDialogsClosed) {
		t.Fatalf("closed dialogs modal = %v", err)
	}
	if closed.DisposeCount() != 1 {
		t.Fatalf("closed dialog dispose count = %d, want 1", closed.DisposeCount())
	}
}

func TestRunnerModalUnavailablePaths(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	if _, err := runner.showComponentWithContext(context.Background(), "key"); !errors.Is(err, ErrExtensionUIUnavailable) {
		t.Fatalf("component without a registry = %v", err)
	}
	stopped, _ := startTestRunner(t, TUIModeRegular, nil)
	stopped.Stop()
	if _, err := stopped.showModalWithContext(context.Background(), func(done func(sdk.ModalResult)) sdk.Component {
		return &extensionTestComponent{text: "STOPPED"}
	}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("modal after stop = %v", err)
	}
}
