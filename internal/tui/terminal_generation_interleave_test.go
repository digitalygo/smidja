package tui

import (
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startInterleaveTerminal(t *testing.T) (*ProcessTerminal, *os.File, *suspendInputRecorder) {
	t.Helper()
	terminal, stdinWrite, _, drain := newTestTerminal(t)
	recorder := &suspendInputRecorder{}
	terminal.OnEOF(recorder.handleEOF)
	if err := terminal.Start(recorder.handle, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		terminal.Stop()
		_ = stdinWrite.Close()
	})
	drain()
	return terminal, stdinWrite, recorder
}

func waitForGateEnter(t *testing.T, entered <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func bufferedSequence(terminal *ProcessTerminal) string {
	terminal.bufferMu.Lock()
	defer terminal.bufferMu.Unlock()
	return terminal.buffer.buffer
}

func readDeliveryGeneration(terminal *ProcessTerminal) int {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	return terminal.deliveryGeneration
}

func TestProcessTerminalQuiesceBufferForSuspendStopsWhenStopped(t *testing.T) {
	terminal := NewProcessTerminal(strings.NewReader(""), io.Discard)
	terminal.bufferMu.Lock()
	terminal.buffer.buffer = ANSIESC
	terminal.bufferMu.Unlock()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.stopped = true
	terminal.mu.Unlock()

	if err := terminal.quiesceBufferForSuspend(); err == nil {
		t.Fatal("quiesceBufferForSuspend() must fail when the terminal is stopped")
	}
	if got := readDeliveryGeneration(terminal); got != generation {
		t.Fatalf("delivery generation = %d, want %d unchanged after a stopped quiesce", got, generation)
	}
	if got := bufferedSequence(terminal); got != ANSIESC {
		t.Fatalf("buffered sequence = %q, want the %q kept when quiesce is refused", got, ANSIESC)
	}
}

func TestProcessTerminalStaleFlushCannotDrainFreshGeneration(t *testing.T) {
	terminal, stdinWrite, recorder := startInterleaveTerminal(t)

	staleGeneration := readDeliveryGeneration(terminal)
	terminal.flushMu.Lock()
	terminal.flushNextEpoch++
	staleEpoch := terminal.flushNextEpoch
	terminal.flushArmed = true
	terminal.flushArmedGeneration = staleGeneration
	terminal.flushArmedEpoch = staleEpoch
	terminal.flushMu.Unlock()
	entered := []chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	releaseOnce := make([]sync.Once, len(release))
	releaseCall := func(index int) {
		releaseOnce[index].Do(func() { close(release[index]) })
	}
	defer func() {
		for index := range release {
			releaseCall(index)
		}
	}()

	var calls int32
	gate := func() {
		index := int(atomic.AddInt32(&calls, 1)) - 1
		if index >= len(entered) {
			index = len(entered) - 1
		}
		entered[index] <- struct{}{}
		<-release[index]
	}
	terminal.consumeGate.Store(&gate)

	staleDone := make(chan struct{})
	go func() {
		terminal.flushPending(staleGeneration, staleEpoch)
		close(staleDone)
	}()
	waitForGateEnter(t, entered[0], "the stale flush to pause before consumption")

	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	freshGeneration := readDeliveryGeneration(terminal)
	if freshGeneration == staleGeneration {
		t.Fatalf("delivery generation did not advance past %d", staleGeneration)
	}

	stdinWrite.WriteString("\x1b[")
	waitForGateEnter(t, entered[1], "the fresh flush to pause before consumption")
	if got := bufferedSequence(terminal); got != "\x1b[" {
		t.Fatalf("buffered fresh sequence = %q, want %q", got, "\x1b[")
	}

	releaseCall(0)
	select {
	case <-staleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the stale flush to return")
	}
	if got := bufferedSequence(terminal); got != "\x1b[" {
		t.Fatalf("fresh sequence after the stale flush = %q, want it preserved", got)
	}
	if got := recorder.count(); got != 0 {
		t.Fatalf("inputs after the stale flush = %q, want none", recorder.joined())
	}

	releaseCall(1)
	waitForSuspendInput(t, recorder, 1)
	time.Sleep(80 * time.Millisecond)
	if got := recorder.joined(); got != "\x1b[" {
		t.Fatalf("inputs = %q, want the fresh sequence delivered exactly once", got)
	}
	if got := recorder.count(); got != 1 {
		t.Fatalf("fresh deliveries = %d, want 1", got)
	}
	if got := readDeliveryGeneration(terminal); got != freshGeneration {
		t.Fatalf("delivery generation changed to %d, want %d", got, freshGeneration)
	}
}

func TestProcessTerminalStaleEOFCannotDrainFreshGeneration(t *testing.T) {
	terminal, _, recorder := startInterleaveTerminal(t)

	staleGeneration := readDeliveryGeneration(terminal)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCall := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseCall()

	var calls int32
	gate := func() {
		if atomic.AddInt32(&calls, 1) != 1 {
			return
		}
		close(entered)
		<-release
	}
	terminal.consumeGate.Store(&gate)

	staleDone := make(chan struct{})
	go func() {
		terminal.deliverEOF(staleGeneration)
		close(staleDone)
	}()
	waitForGateEnter(t, entered, "the stale EOF to pause before consumption")

	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	freshGeneration := readDeliveryGeneration(terminal)
	if freshGeneration == staleGeneration {
		t.Fatalf("delivery generation did not advance past %d", staleGeneration)
	}

	terminal.bufferMu.Lock()
	terminal.buffer.buffer = "\x1b]"
	terminal.bufferMu.Unlock()

	releaseCall()
	select {
	case <-staleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the stale EOF to return")
	}
	if got := bufferedSequence(terminal); got != "\x1b]" {
		t.Fatalf("fresh sequence after the stale EOF = %q, want it preserved", got)
	}
	if got := recorder.count(); got != 0 {
		t.Fatalf("inputs after the stale EOF = %q, want none", recorder.joined())
	}
	if got := recorder.eofCount(); got != 0 {
		t.Fatalf("EOF deliveries after the stale EOF = %d, want 0", got)
	}

	terminal.deliverEOF(freshGeneration)
	if got := recorder.joined(); got != "\x1b]" {
		t.Fatalf("inputs = %q, want the fresh sequence delivered exactly once", got)
	}
	if got := recorder.count(); got != 1 {
		t.Fatalf("fresh deliveries = %d, want 1", got)
	}
	if got := recorder.eofCount(); got != 1 {
		t.Fatalf("EOF deliveries = %d, want 1", got)
	}
}
