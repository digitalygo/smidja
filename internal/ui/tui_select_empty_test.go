package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/digitalygo/smidja/sdk"
)

func assertNoModalInstalled(t *testing.T, runner *Runner) {
	t.Helper()
	if runner.dialogs.Active() {
		t.Fatal("empty Select activated a modal dialog")
	}
	if runner.view.HasOverlay() {
		t.Fatal("empty Select installed an overlay")
	}
	if runner.view.ModalCapture() {
		t.Fatal("empty Select enabled modal capture")
	}
	if count := runner.dialogs.waitingCount(); count != 0 {
		t.Fatalf("empty Select left %d waiting dialogs", count)
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("empty Select changed focus")
	}
}

func TestSelectEmptyOptionsReturnsImmediatelyWithoutModal(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	for _, options := range [][]string{nil, {}} {
		value, err := runner.Select("Pick", options)
		if err != nil {
			t.Fatalf("Select with %d options error = %v", len(options), err)
		}
		if value != "" {
			t.Fatalf("Select with %d options = %q, want empty", len(options), value)
		}
	}
	assertNoModalInstalled(t, runner)

	done := make(chan string, 1)
	go func() {
		value, _ := runner.Select("Pick", []string{"only"})
		done <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != "only" {
			t.Fatalf("Select after empty-options calls = %q, want only", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Select did not resolve after empty-options calls")
	}
	if runner.view.ModalCapture() || runner.view.HasOverlay() {
		t.Fatal("follow-up Select left modal state behind")
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored after the follow-up Select")
	}
}

func TestBoundUISelectEmptyOptionsReturnsImmediatelyWithoutModal(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	bound := runner.BoundUI(context.Background())
	for _, options := range [][]string{nil, {}} {
		value, err := bound.Select("Pick", options)
		if err != nil {
			t.Fatalf("bound Select with %d options error = %v", len(options), err)
		}
		if value != "" {
			t.Fatalf("bound Select with %d options = %q, want empty", len(options), value)
		}
	}
	assertNoModalInstalled(t, runner)
}

func TestSelectEmptyOptionsPreservesInactiveAndClosedErrors(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	inactive := NewRunner(opts)
	if _, err := inactive.Select("t", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("inactive Select = %v, want ErrModeUnsupported", err)
	}

	stopped, _ := startTestRunner(t, TUIModeRegular, nil)
	stopped.Stop()
	if _, err := stopped.Select("t", []string{}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("stopped Select = %v, want ErrModeUnsupported", err)
	}

	closed, _ := startTestRunner(t, TUIModeRegular, nil)
	closed.CancelDialogs()
	for _, options := range [][]string{nil, {}} {
		if _, err := closed.Select("t", options); !errors.Is(err, errDialogsClosed) {
			t.Fatalf("closed Select with %d options = %v, want errDialogsClosed", len(options), err)
		}
	}
}
