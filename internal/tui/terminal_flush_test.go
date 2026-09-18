package tui

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type flushRecorder struct {
	mu        sync.Mutex
	sequences []string
}

func (r *flushRecorder) record(sequence string) {
	r.mu.Lock()
	r.sequences = append(r.sequences, sequence)
	r.mu.Unlock()
}

func (r *flushRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sequences...)
}

func newFlushTerminal(t *testing.T) (*ProcessTerminal, *flushRecorder) {
	t.Helper()
	terminal := NewProcessTerminal(strings.NewReader(""), io.Discard)
	terminal.buffer = newStdinBuffer(10)
	terminal.buffer.onSequence = terminal.dispatchSequence
	recorder := &flushRecorder{}
	terminal.inputHandler = recorder.record
	terminal.pump = newDeliveryPump(terminal.deliverBatch)
	go terminal.pump.run()
	t.Cleanup(terminal.stopDelivery)
	terminal.mu.Lock()
	terminal.running = true
	terminal.stopped = false
	terminal.mu.Unlock()
	return terminal, recorder
}

func (t *ProcessTerminal) feedForTest(data string) {
	t.mu.Lock()
	generation := t.deliveryGeneration
	pump := t.pump
	t.mu.Unlock()
	t.ingestInputChunk([]byte(data), generation, pump)
}

func waitForFlushCount(t *testing.T, recorder *flushRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(recorder.snapshot()) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d flushed sequences, got %q", want, recorder.snapshot())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProcessTerminalFlushesLoneEscapeOnce(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.feedForTest(ANSIESC)
	waitForFlushCount(t, recorder, 1)
	time.Sleep(40 * time.Millisecond)
	got := recorder.snapshot()
	if len(got) != 1 || got[0] != ANSIESC {
		t.Fatalf("lone escape delivered %q, want exactly one escape", got)
	}
}

func TestProcessTerminalKeepsCompleteSequencesIntact(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.feedForTest(ANSIESC + "[")
	terminal.feedForTest("A")
	time.Sleep(40 * time.Millisecond)
	got := recorder.snapshot()
	if len(got) != 1 || got[0] != CursorUp {
		t.Fatalf("arrow sequence delivered %q, want a single intact CursorUp", got)
	}
}

func TestProcessTerminalKeepsPrintableInputIntact(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.feedForTest("ab")
	time.Sleep(40 * time.Millisecond)
	got := recorder.snapshot()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("printable input delivered %q, want a and b", got)
	}
}

func TestProcessTerminalStopsFlushTimerOnShutdown(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.feedForTest(ANSIESC)
	terminal.mu.Lock()
	terminal.running = false
	terminal.stopped = true
	terminal.mu.Unlock()
	terminal.cancelFlushTimer()
	time.Sleep(40 * time.Millisecond)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("flush callback ran after shutdown: %q", got)
	}
}

func TestProcessTerminalFlushDelayReportsPendingState(t *testing.T) {
	terminal, _ := newFlushTerminal(t)
	terminal.bufferMu.Lock()
	delay, pending := terminal.buffer.flushDelay()
	terminal.bufferMu.Unlock()
	if pending {
		t.Fatalf("empty buffer reported pending with delay %d", delay)
	}
	terminal.feedForTest(ANSIESC)
	terminal.bufferMu.Lock()
	delay, pending = terminal.buffer.flushDelay()
	terminal.bufferMu.Unlock()
	if !pending || delay <= 0 {
		t.Fatalf("lone escape flush delay = %d pending=%v, want a positive delay", delay, pending)
	}
}
