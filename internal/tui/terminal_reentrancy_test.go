package tui

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessTerminalReaderExitedLockedStates(t *testing.T) {
	terminal := NewProcessTerminal(strings.NewReader(""), io.Discard)
	terminal.mu.Lock()
	if !terminal.readerExitedLocked() {
		terminal.mu.Unlock()
		t.Fatal("a terminal without a reader must report the reader as exited")
	}
	terminal.readerDone = make(chan struct{})
	if terminal.readerExitedLocked() {
		terminal.mu.Unlock()
		t.Fatal("an open reader must not report exited")
	}
	close(terminal.readerDone)
	if !terminal.readerExitedLocked() {
		terminal.mu.Unlock()
		t.Fatal("a closed reader must report exited")
	}
	terminal.mu.Unlock()
}

func TestProcessTerminalInputHandlerRunsWithoutHoldingLocks(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	type lockSnapshot struct {
		bufferFree   bool
		terminalFree bool
	}
	results := make(chan lockSnapshot, 1)
	if err := terminal.Start(func(string) {
		bufferFree := terminal.bufferMu.TryLock()
		if bufferFree {
			terminal.bufferMu.Unlock()
		}
		terminalFree := terminal.mu.TryLock()
		if terminalFree {
			terminal.mu.Unlock()
		}
		results <- lockSnapshot{bufferFree: bufferFree, terminalFree: terminalFree}
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer terminal.Stop()

	stdinWrite.WriteString("a")
	select {
	case got := <-results:
		if !got.bufferFree {
			t.Fatal("input handler ran while the decoder lock was held")
		}
		if !got.terminalFree {
			t.Fatal("input handler ran while the terminal lock was held")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input handler was never invoked")
	}
}

func TestProcessTerminalInputHandlerCanStopWithoutDeadlock(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	handlerDone := make(chan struct{})
	if err := terminal.Start(func(string) {
		terminal.Stop()
		close(handlerDone)
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stdinWrite.WriteString("x")
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("input handler calling Stop deadlocked")
	}
	terminal.mu.Lock()
	stopped := terminal.stopped
	terminal.mu.Unlock()
	if !stopped {
		t.Fatal("terminal must be stopped after the handler Stop")
	}
}

func TestProcessTerminalInputHandlerCanSuspendWithoutDeadlock(t *testing.T) {
	terminal, stdinWrite, ops, drain := newTestTerminal(t)
	defer stdinWrite.Close()

	var (
		mu     sync.Mutex
		inputs []string
		once   sync.Once
	)
	suspendResult := make(chan error, 1)
	if err := terminal.Start(func(data string) {
		mu.Lock()
		inputs = append(inputs, data)
		mu.Unlock()
		once.Do(func() { suspendResult <- terminal.SuspendRaw() })
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer terminal.Stop()
	drain()

	stdinWrite.WriteString("s")
	select {
	case err := <-suspendResult:
		if err != nil {
			t.Fatalf("SuspendRaw() from the handler error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input handler calling SuspendRaw deadlocked")
	}
	terminal.mu.Lock()
	suspended := terminal.suspended
	terminal.mu.Unlock()
	if !suspended {
		t.Fatal("terminal must be suspended after the handler SuspendRaw")
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls = %d, want 1", ops.restoreCalls)
	}

	stdinWrite.WriteString("dropped")
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	before := len(inputs)
	mu.Unlock()
	if before != 1 {
		t.Fatalf("inputs while suspended = %d, want 1", before)
	}

	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	stdinWrite.WriteString("r")
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		joined := len(inputs) > 1 && inputs[len(inputs)-1] == "r"
		mu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resumed reader did not deliver input")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	got := inputs
	mu.Unlock()
	if got[0] != "s" || got[len(got)-1] != "r" {
		t.Fatalf("inputs = %q, want s then r", got)
	}
}

func TestProcessTerminalCompletesProtocolInitBeforeInput(t *testing.T) {
	terminal, stdinWrite, ops, drain := newTestTerminal(t)
	defer stdinWrite.Close()

	observed := make(chan string, 1)
	if err := terminal.Start(func(string) { observed <- drain() }, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer terminal.Stop()
	if ops.rawCalls != 1 {
		t.Fatalf("rawCalls = %d, want raw mode applied before input is enabled", ops.rawCalls)
	}

	stdinWrite.WriteString("a")
	select {
	case output := <-observed:
		if !containsString(output, BracketedPasteOn) {
			t.Fatalf("bracketed paste enable must precede the first input, got %q", output)
		}
		if !containsString(output, kittyQuery) {
			t.Fatalf("kitty query must precede the first input, got %q", output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input was never delivered")
	}
}

func TestProcessTerminalEarlyEOFDeliversOnceAndRestoresBeforeStop(t *testing.T) {
	terminal, stdinWrite, ops, drain := newTestTerminal(t)
	if err := stdinWrite.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	var eofs int32
	terminal.OnEOF(func() { atomic.AddInt32(&eofs, 1) })
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForCondition(t, func() bool { return atomic.LoadInt32(&eofs) == 1 }, "single early EOF delivery")
	drain()
	terminal.Stop()

	after := drain()
	if got := atomic.LoadInt32(&eofs); got != 1 {
		t.Fatalf("EOF deliveries = %d, want exactly 1", got)
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls = %d, want 1", ops.restoreCalls)
	}
	if countOccurrences(after, BracketedPasteOn) != 0 {
		t.Fatalf("bracketed paste enabled after restore: %q", after)
	}
	if countOccurrences(after, kittyQuery) != 0 {
		t.Fatalf("kitty protocol enabled after restore: %q", after)
	}
	if !containsString(after, BracketedPasteOff) || !containsString(after, KittyProtocolPop) {
		t.Fatalf("restore output missing protocol disable: %q", after)
	}
}

func TestProcessTerminalDropsNegotiationAfterStop(t *testing.T) {
	terminal, stdinWrite, _, drain := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	drain()
	terminal.Stop()

	terminal.dispatchSequence("\x1b[?1;2c")
	terminal.dispatchSequence("\x1b[?0u")
	after := drain()
	if countOccurrences(after, ModifyOtherKeysEnable) != 0 {
		t.Fatalf("protocol negotiation ran after stop: %q", after)
	}
}

func TestProcessTerminalResumeStartsExactlyOneReader(t *testing.T) {
	terminal, stdinWrite, _, recorder, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()
	drain()

	terminal.mu.Lock()
	firstGeneration := terminal.readerGeneration
	terminal.mu.Unlock()
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	terminal.mu.Lock()
	parkedGeneration := terminal.readerGeneration
	parked := terminal.readerPaused
	terminal.mu.Unlock()
	if parkedGeneration != firstGeneration || !parked {
		t.Fatalf("generation while parked = %d (paused=%v), want generation %d parked", parkedGeneration, parked, firstGeneration)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	terminal.mu.Lock()
	resumedGeneration := terminal.readerGeneration
	terminal.mu.Unlock()
	if resumedGeneration != firstGeneration+1 {
		t.Fatalf("reader generation after resume = %d, want exactly %d", resumedGeneration, firstGeneration+1)
	}

	stdinWrite.WriteString("x")
	waitForSuspendInput(t, recorder, 1)
	if got := recorder.joined(); got != "x" {
		t.Fatalf("inputs = %q, want a single delivery from one reader", got)
	}
	terminal.Stop()
}
