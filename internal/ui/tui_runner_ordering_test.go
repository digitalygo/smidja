package ui

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

type signalSeams struct {
	mu     sync.Mutex
	order  []string
	notify chan<- os.Signal
}

func (s *signalSeams) record(event string) {
	s.mu.Lock()
	s.order = append(s.order, event)
	s.mu.Unlock()
}

func (s *signalSeams) events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func (s *signalSeams) install(t *testing.T) {
	t.Helper()
	previousNotify := notifySignals
	previousStop := stopSignals
	notifySignals = func(channel chan<- os.Signal, _ ...os.Signal) {
		s.mu.Lock()
		s.notify = channel
		s.mu.Unlock()
		s.record("notify")
	}
	stopSignals = func(chan<- os.Signal) {
		s.record("unregister")
	}
	t.Cleanup(func() {
		notifySignals = previousNotify
		stopSignals = previousStop
	})
}

func (s *signalSeams) signal() chan<- os.Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notify
}

func assertEventOrder(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("signal ordering = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signal ordering = %q, want %q", got, want)
		}
	}
}

func TestRunnerRegistersSignalsBeforeTerminalStartAndUnregistersAfterRestore(t *testing.T) {
	seams := &signalSeams{}
	seams.install(t)
	terminal := newFakeUITerminal(80, 24)
	terminal.onStart = func() { seams.record("terminal-start") }
	terminal.onStop = func() { seams.record("terminal-stop") }
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runner.Stop()
	runner.Stop()
	assertEventOrder(t, seams.events(), []string{"notify", "terminal-start", "terminal-stop", "unregister"})
}

func TestRunnerStartFailureUnwindsSignalsAndTerminal(t *testing.T) {
	seams := &signalSeams{}
	seams.install(t)
	terminal := newFakeUITerminal(80, 24)
	terminal.startErr = errors.New("terminal refused")
	terminal.onStart = func() { seams.record("terminal-start") }
	terminal.onStop = func() { seams.record("terminal-stop") }
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err == nil {
		t.Fatal("Start() should fail when the terminal refuses")
	}
	assertEventOrder(t, seams.events(), []string{"notify", "terminal-start", "terminal-stop", "unregister"})
	if runner.Active() {
		t.Fatal("runner must not be active after a failed Start")
	}
	runner.Stop()
	if got := terminal.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
}

func TestRunnerEarlySignalDuringStartupYieldsOneOrderlyStop(t *testing.T) {
	seams := &signalSeams{}
	seams.install(t)
	terminal := newFakeUITerminal(80, 24)
	var once sync.Once
	terminal.onStart = func() {
		once.Do(func() {
			channel := seams.signal()
			if channel != nil {
				channel <- os.Interrupt
			}
		})
	}
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("early signal must request exit")
	}
	runner.Stop()
	runner.Stop()
	if got := terminal.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want exactly one orderly stop", got)
	}
	events := seams.events()
	if events[0] != "notify" {
		t.Fatalf("signals must be registered before the terminal starts, got %q", events)
	}
	if events[len(events)-1] != "unregister" {
		t.Fatalf("signals must be unregistered last, got %q", events)
	}
}

func TestRunnerEarlyEOFDuringStartupYieldsOneOrderlyStop(t *testing.T) {
	seams := &signalSeams{}
	seams.install(t)
	terminal := newFakeUITerminal(80, 24)
	var once sync.Once
	terminal.onStart = func() {
		once.Do(terminal.FireEOF)
	}
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("early EOF must request exit")
	}
	runner.Stop()
	runner.Stop()
	if got := terminal.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want exactly one orderly stop", got)
	}
}

func TestRunnerRequestExternalEditorInactiveIsNoop(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	runner.requestExternalEditor()
	if runner.editorActive.Load() {
		t.Fatal("inactive runner must not activate the external editor")
	}
	runner.Stop()
}

func TestRunnerRequestExternalEditorResetsFlagWhenActionCannotQueue(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	runner.endActions()
	runner.requestExternalEditor()
	if runner.editorActive.Load() {
		t.Fatal("editor flag must reset when the action cannot be queued")
	}
	if terminal.SuspendCalls() != 0 {
		t.Fatalf("suspend calls = %d, want 0 when the action is not queued", terminal.SuspendCalls())
	}
	if !runner.Active() {
		t.Fatal("the runner must stay active when only its actions are stopped")
	}
}
