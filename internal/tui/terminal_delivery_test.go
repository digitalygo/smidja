package tui

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessTerminalSuspendDropsTrailingInputFromSameRead(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	recorder := &suspendInputRecorder{}
	suspended := make(chan struct{})
	var once sync.Once
	terminal.OnEOF(recorder.handleEOF)
	if err := terminal.Start(func(data string) {
		recorder.handle(data)
		once.Do(func() {
			if err := terminal.SuspendRaw(); err != nil {
				t.Errorf("SuspendRaw() error = %v", err)
			}
			close(suspended)
		})
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer terminal.Stop()

	stdinWrite.WriteString("\x07tail")
	select {
	case <-suspended:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not suspend in time")
	}
	assertNoFurtherInput(t, recorder, 1, 150*time.Millisecond)
	if got := recorder.joined(); got != "\x07" {
		t.Fatalf("inputs = %q, want only the suspending control byte", got)
	}
}

func TestProcessTerminalSuspendDropsQueuedNegotiation(t *testing.T) {
	terminal, stdinWrite, _, drain := newTestTerminal(t)
	defer stdinWrite.Close()

	recorder := &suspendInputRecorder{}
	suspended := make(chan struct{})
	var once sync.Once
	if err := terminal.Start(func(data string) {
		recorder.handle(data)
		once.Do(func() {
			if err := terminal.SuspendRaw(); err != nil {
				t.Errorf("SuspendRaw() error = %v", err)
			}
			close(suspended)
		})
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer terminal.Stop()
	drain()

	stdinWrite.WriteString("\x07\x1b[?1;2c")
	select {
	case <-suspended:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not suspend in time")
	}
	time.Sleep(100 * time.Millisecond)
	output := drain()
	if countOccurrences(output, ModifyOtherKeysEnable) != 0 {
		t.Fatalf("queued negotiation re-enabled modifyOtherKeys while suspended: %q", output)
	}
	if terminal.ModifyOtherKeysActive() {
		t.Fatal("modifyOtherKeys must stay inactive while suspended")
	}
	if terminal.KittyProtocolActive() {
		t.Fatal("kitty protocol must stay inactive while suspended")
	}
}

func TestProcessTerminalSuspendDropsQueuedEOF(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	recorder := &suspendInputRecorder{}
	var exits int32
	terminal.OnEOF(func() {
		recorder.handleEOF()
		atomic.AddInt32(&exits, 1)
	})
	if err := terminal.Start(func(data string) {
		recorder.handle(data)
		if data == "a" {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-gate
		}
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer terminal.Stop()

	stdinWrite.WriteString("a")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	terminal.mu.Lock()
	pump := terminal.pump
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()
	pump.enqueue(deliveryBatch{eof: true, generation: generation})
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	close(gate)
	assertNoFurtherInput(t, recorder, 1, 150*time.Millisecond)
	if got := atomic.LoadInt32(&exits); got != 0 {
		t.Fatalf("queued EOF requested exit %d times, want 0", got)
	}
	if recorder.eofCount() != 0 {
		t.Fatalf("queued EOF deliveries = %d, want 0", recorder.eofCount())
	}
}

func TestProcessTerminalResumeDeliversFreshBatchExactlyOnce(t *testing.T) {
	terminal, stdinWrite, _, recorder, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()

	drain()
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	drain()
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	drain()

	stdinWrite.WriteString("fresh")
	waitForSuspendInput(t, recorder, 5)
	time.Sleep(80 * time.Millisecond)
	if got := recorder.joined(); got != "fresh" {
		t.Fatalf("inputs = %q, want exactly one delivery of the fresh batch", got)
	}
}

func TestProcessTerminalDeliveryGenerationKeepsInputOrder(t *testing.T) {
	terminal, stdinWrite, _, recorder, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()
	drain()

	stdinWrite.WriteString("one")
	waitForSuspendInput(t, recorder, 3)

	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	drain()

	stdinWrite.WriteString("two")
	waitForSuspendInput(t, recorder, 6)
	if got := recorder.joined(); got != "onetwo" {
		t.Fatalf("inputs = %q, want ordered onetwo across the generation change", got)
	}
}

func TestProcessTerminalDeliveryDropsStaleGenerationBatches(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.mu.Lock()
	stale := terminal.deliveryGeneration - 1
	current := terminal.deliveryGeneration
	terminal.mu.Unlock()

	terminal.pump.enqueue(deliveryBatch{sequences: []string{"stale"}, generation: stale})
	terminal.pump.enqueue(deliveryBatch{sequences: []string{"current"}, generation: current})
	waitForFlushCount(t, recorder, 1)
	time.Sleep(50 * time.Millisecond)

	got := recorder.snapshot()
	if len(got) != 1 || got[0] != "current" {
		t.Fatalf("delivered %q, want only the current generation", got)
	}
}

func TestProcessTerminalDeliveryPreservesSameGenerationOrder(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()

	for _, sequence := range []string{"a", "b", "c"} {
		terminal.pump.enqueue(deliveryBatch{sequences: []string{sequence}, generation: generation})
	}
	waitForFlushCount(t, recorder, 3)
	if got := strings.Join(recorder.snapshot(), ""); got != "abc" {
		t.Fatalf("delivered %q, want abc in order", got)
	}
}

func TestProcessTerminalDeliverEOFDropsStaleGeneration(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.mu.Lock()
	stale := terminal.deliveryGeneration - 1
	current := terminal.deliveryGeneration
	terminal.mu.Unlock()

	var eofs int32
	terminal.OnEOF(func() { atomic.AddInt32(&eofs, 1) })

	terminal.bufferMu.Lock()
	terminal.buffer.buffer = ANSIESC
	terminal.bufferMu.Unlock()
	terminal.deliverEOF(stale)
	terminal.deliverEOFHandler(stale)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("stale deliverEOF dispatched %q, want nothing", got)
	}
	if got := atomic.LoadInt32(&eofs); got != 0 {
		t.Fatalf("stale EOF handler calls = %d, want 0", got)
	}

	terminal.bufferMu.Lock()
	terminal.buffer.buffer = ANSIESC
	terminal.bufferMu.Unlock()
	terminal.deliverEOF(current)
	if got := recorder.snapshot(); len(got) != 1 || got[0] != ANSIESC {
		t.Fatalf("current deliverEOF dispatched %q, want the lone escape", got)
	}
	if got := atomic.LoadInt32(&eofs); got != 1 {
		t.Fatalf("current EOF handler calls = %d, want 1", got)
	}
}
