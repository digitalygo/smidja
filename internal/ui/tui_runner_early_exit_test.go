package ui

import (
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

type earlyExitSignalSeam struct {
	mu       sync.Mutex
	notifyCh chan<- os.Signal
	stops    int
}

func (s *earlyExitSignalSeam) install(t *testing.T) {
	t.Helper()
	previousNotify, previousStop := notifySignals, stopSignals
	notifySignals = func(channel chan<- os.Signal, _ ...os.Signal) {
		s.mu.Lock()
		s.notifyCh = channel
		s.mu.Unlock()
	}
	stopSignals = func(chan<- os.Signal) {
		s.mu.Lock()
		s.stops++
		s.mu.Unlock()
	}
	t.Cleanup(func() {
		notifySignals = previousNotify
		stopSignals = previousStop
	})
}

func (s *earlyExitSignalSeam) signalChannel() chan<- os.Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifyCh
}

func (s *earlyExitSignalSeam) stopCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops
}

type runnerActionQueueState struct {
	channel chan func()
	stop    chan struct{}
	stopped bool
	done    chan struct{}
}

func actionQueueState(runner *Runner) runnerActionQueueState {
	runner.actionsMu.Lock()
	defer runner.actionsMu.Unlock()
	return runnerActionQueueState{
		channel: runner.actionsCh,
		stop:    runner.actionsStop,
		stopped: runner.actionsStopped,
		done:    runner.actionsDone,
	}
}

func waitForGoroutineCount(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines leaked: before %d, after %d", before, runtime.NumGoroutine())
		}
		time.Sleep(time.Millisecond)
	}
}

func assertClosedActionQueue(t *testing.T, runner *Runner) {
	t.Helper()
	state := actionQueueState(runner)
	if state.channel != nil || state.stop != nil || state.done != nil || !state.stopped {
		t.Fatalf("action queue must stay closed after an exit request, state = %+v", state)
	}
}

func TestRunnerExitSignalBetweenSignalRegistrationAndActionInitializationKeepsQueueClosed(t *testing.T) {
	signals := &earlyExitSignalSeam{}
	signals.install(t)
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	goroutinesBeforeStart := runtime.NumGoroutine()
	runner.onSignalsRegistered = func() {
		channel := signals.signalChannel()
		if channel == nil {
			t.Error("signals must be registered before the seam runs")
			return
		}
		channel <- os.Interrupt
		<-runner.Done()
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !runner.Active() {
		t.Fatal("Start must leave the runner owner-safe when exit is requested during startup")
	}
	select {
	case <-runner.Done():
	default:
		t.Fatal("Done() must be closed after an exit request")
	}
	assertClosedActionQueue(t, runner)
	if runner.enqueueAction(func() {}) {
		t.Fatal("queued actions must reject after an exit request")
	}
	runner.requestExternalEditor()
	if runner.editorActive.Load() {
		t.Fatal("editor actions must reject after an exit request")
	}
	if suspended := terminal.SuspendCalls(); suspended != 0 {
		t.Fatalf("suspend calls = %d, want 0", suspended)
	}
	if err := runner.Start(); err == nil {
		t.Fatal("repeated Start while active must fail")
	}
	runner.Stop()
	runner.Stop()
	if stops := terminal.StopCalls(); stops != 1 {
		t.Fatalf("terminal stop calls = %d, want exactly one orderly stop", stops)
	}
	if unregisters := signals.stopCount(); unregisters != 1 {
		t.Fatalf("signal unregister calls = %d, want 1", unregisters)
	}
	if runner.Active() {
		t.Fatal("Stop must stop the runner")
	}
	waitForGoroutineCount(t, goroutinesBeforeStart)
}

func TestRunnerRequestExitBetweenSignalRegistrationAndActionInitializationStaysMonotonic(t *testing.T) {
	signals := &earlyExitSignalSeam{}
	signals.install(t)
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	goroutinesBeforeStart := runtime.NumGoroutine()
	runner.onSignalsRegistered = func() {
		runner.RequestExit()
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-runner.Done()
	assertClosedActionQueue(t, runner)
	runner.beginActions()
	if reopened := actionQueueState(runner); reopened.channel != nil || reopened.done != nil {
		t.Fatalf("beginActions must not reopen the action queue, state = %+v", reopened)
	}
	if runner.enqueueAction(func() {}) {
		t.Fatal("queued actions must reject after beginActions refusal")
	}
	runner.Stop()
	runner.Stop()
	if stops := terminal.StopCalls(); stops != 1 {
		t.Fatalf("terminal stop calls = %d, want exactly one orderly stop", stops)
	}
	waitForGoroutineCount(t, goroutinesBeforeStart)
}

func TestRunnerBeginActionsNeverResurrectsAStoppedQueue(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)

	runner.beginActions()
	open := actionQueueState(runner)
	if open.channel == nil || open.stop == nil || open.done == nil || open.stopped {
		t.Fatalf("beginActions must open the action queue, state = %+v", open)
	}
	executed := make(chan struct{})
	if !runner.enqueueAction(func() { close(executed) }) {
		t.Fatal("queued actions must run while the queue is open")
	}
	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		t.Fatal("queued action did not run")
	}
	runner.beginActions()
	reused := actionQueueState(runner)
	if reused.channel != open.channel || reused.done != open.done {
		t.Fatalf("beginActions must reuse the open action queue, state = %+v", reused)
	}
	select {
	case <-reused.done:
		t.Fatal("a second action worker must not be spawned")
	default:
	}
	runner.endActions()
	runner.joinActions()
	select {
	case <-actionQueueState(runner).done:
	default:
		t.Fatal("endActions must retire the action worker")
	}
	runner.beginActions()
	stopped := actionQueueState(runner)
	if !stopped.stopped || stopped.channel != open.channel || stopped.done != open.done {
		t.Fatalf("beginActions must not reopen a stopped action queue, state = %+v", stopped)
	}
	if runner.enqueueAction(func() {}) {
		t.Fatal("queued actions must reject once the action queue stopped")
	}
	runner.RequestExit()
	runner.beginActions()
	afterExit := actionQueueState(runner)
	if !afterExit.stopped || afterExit.channel != open.channel || afterExit.done != open.done {
		t.Fatalf("beginActions must not reopen after an exit request, state = %+v", afterExit)
	}
	if runner.enqueueAction(func() {}) {
		t.Fatal("queued actions must reject after an exit request")
	}
}
