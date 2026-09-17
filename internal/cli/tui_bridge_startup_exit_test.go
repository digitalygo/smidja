package cli

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
)

func (f *runTUIFixture) startMode(ctx context.Context, mode ui.TUIMode) chan error {
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, f.deps, f.rd, f.lineUI, mode, f.cwd, f.cwd, nil, bridgeTerminalFactory(f.terminal), f.runtime)
	}()
	return done
}

func waitForGoroutinesToSettle(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines leaked: before %d, after %d", before, runtime.NumGoroutine())
		}
		time.Sleep(time.Millisecond)
	}
}

func runStartupHookExitScenario(t *testing.T, mode ui.TUIMode, exit func(*runTUIFixture)) {
	t.Helper()
	log := &runTUIEventLog{}
	extension := &runTUIHookExtension{
		log:           log,
		startEnter:    make(chan struct{}),
		startOnSignal: true,
	}
	client := newRunTUITurnClient(log, "unused answer")
	fixture := newRunTUIFixture(t, client, extension)
	extension.terminalUp = fixture.terminal.Started
	goroutinesBefore := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.startMode(ctx, mode)

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
	assertNever(t, 200*time.Millisecond, "prompt ran while the startup hook was still blocked", func() bool {
		return client.callCount() != 0
	})

	exit(fixture)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the blocked startup hook was canceled")
	}

	if err := extension.startSignalResult(); !errors.Is(err, context.Canceled) {
		t.Fatalf("startup hook signal err = %v, want context.Canceled", err)
	}
	if client.callCount() != 0 {
		t.Fatalf("client calls = %d, want none after the canceled startup hook", client.callCount())
	}
	events := log.snapshot()
	start, returned, shutdown := eventIndex(events, "start"), eventIndex(events, "start-returned"), eventIndex(events, "shutdown")
	if start < 0 || returned < 0 || shutdown < 0 || start > returned || returned > shutdown {
		t.Fatalf("ordering events = %v, want start, start-returned, shutdown in order", events)
	}
	shutdownErr, terminalUp, _ := extension.shutdownResults()
	if shutdownErr == nil {
		t.Fatal("shutdown hook opened a dialog after dialog admission closed")
	}
	if !terminalUp {
		t.Error("shutdown hook ran after the renderer stopped")
	}
	if fixture.terminal.Started() {
		t.Error("terminal should be stopped after runTUI returns")
	}
	if got := fixture.terminal.StopCount(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	if mode.Fullscreen() {
		if output := fixture.terminal.Output(); !strings.Contains(output, tui.AltScreenExit) {
			t.Fatalf("output missing alt-screen exit:\n%s", output)
		}
	}
	waitForGoroutinesToSettle(t, goroutinesBefore)
}

func TestRunTUIStartupHookCanceledByEOF(t *testing.T) {
	runStartupHookExitScenario(t, ui.TUIModeFullscreen, func(fixture *runTUIFixture) {
		fixture.terminal.FireEOF()
	})
}

func TestRunTUIStartupHookCanceledByCtrlD(t *testing.T) {
	runStartupHookExitScenario(t, ui.TUIModeRegular, func(fixture *runTUIFixture) {
		fixture.terminal.SendInput("\x04")
	})
}

func TestRunTUIStartupHookCanceledBySignal(t *testing.T) {
	runStartupHookExitScenario(t, ui.TUIModeFullscreen, func(fixture *runTUIFixture) {
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatalf("kill: %v", err)
		}
	})
}

func TestRunTUIStartupCompletionOpensAdmissionUnderRace(t *testing.T) {
	for iteration := 0; iteration < 5; iteration++ {
		log := &runTUIEventLog{}
		extension := &runTUIHookExtension{
			log:        log,
			startEnter: make(chan struct{}),
			startGate:  make(chan struct{}),
		}
		client := newRunTUITurnClient(log, "admission answer")
		fixture := newRunTUIFixture(t, client, extension)
		ctx := context.Background()
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

		fixture.terminal.SendInput("queued prompt")
		fixture.terminal.SendInput("\r")
		close(extension.startGate)

		select {
		case <-client.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("queued prompt did not run after SessionStart completed")
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
		if client.callCount() != 1 {
			t.Fatalf("client calls = %d, want 1", client.callCount())
		}
		events := log.snapshot()
		if start, turn := eventIndex(events, "start"), eventIndex(events, "turn:queued prompt"); start < 0 || turn < 0 || start > turn {
			t.Fatalf("ordering events = %v, want start before turn", events)
		}
		if fixture.terminal.Started() {
			t.Error("terminal should be stopped after runTUI returns")
		}
	}
}
