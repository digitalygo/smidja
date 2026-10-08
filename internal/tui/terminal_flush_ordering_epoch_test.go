package tui

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newOrderingTerminal(t *testing.T, escapeTimeout int) (*ProcessTerminal, *flushRecorder) {
	t.Helper()
	terminal := NewProcessTerminal(strings.NewReader(""), io.Discard)
	terminal.buffer = newStdinBuffer(escapeTimeout)
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

func readArmedFlush(t *testing.T, terminal *ProcessTerminal) (int, uint64) {
	t.Helper()
	terminal.flushMu.Lock()
	defer terminal.flushMu.Unlock()
	return terminal.flushArmedGeneration, terminal.flushArmedEpoch
}

func armFlushForTest(terminal *ProcessTerminal, generation int) uint64 {
	terminal.flushMu.Lock()
	defer terminal.flushMu.Unlock()
	terminal.flushNextEpoch++
	epoch := terminal.flushNextEpoch
	terminal.flushArmed = true
	terminal.flushArmedGeneration = generation
	terminal.flushArmedEpoch = epoch
	return epoch
}

func waitForChannel(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestProcessTerminalFlushPublicationBlocksReaderInput(t *testing.T) {
	terminal, recorder := newOrderingTerminal(t, 5000)
	terminal.bufferMu.Lock()
	terminal.buffer.buffer = ANSIESC
	terminal.bufferMu.Unlock()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()
	epoch := armFlushForTest(terminal, generation)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	publish := func() {
		entered <- struct{}{}
		<-release
	}
	terminal.publishGate.Store(&publish)
	done := make(chan struct{})
	go func() {
		terminal.flushPending(generation, epoch)
		close(done)
	}()
	waitForChannel(t, entered, "flush publication to pause")
	readerDone := make(chan struct{})
	go func() {
		terminal.bufferMu.Lock()
		terminal.mu.Lock()
		pump := terminal.pump
		gen := terminal.deliveryGeneration
		terminal.buffer.process([]byte("x"))
		sequences := terminal.buffer.takePending()
		if len(sequences) > 0 && pump != nil {
			pump.enqueue(deliveryBatch{sequences: sequences, generation: gen})
		}
		terminal.mu.Unlock()
		terminal.bufferMu.Unlock()
		terminal.scheduleFlush()
		close(readerDone)
	}()
	select {
	case <-readerDone:
		t.Fatalf("reader input overtook paused flush publication")
	case <-time.After(60 * time.Millisecond):
	}
	if got := len(recorder.snapshot()); got != 0 {
		t.Fatalf("deliveries before release = %d, want 0", got)
	}
	close(release)
	waitForChannel(t, done, "paused flush to complete")
	waitForChannel(t, readerDone, "blocked reader to complete")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(recorder.snapshot()) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for ordered deliveries, got %q", recorder.snapshot())
		}
		time.Sleep(time.Millisecond)
	}
	got := recorder.snapshot()
	if len(got) != 2 || got[0] != ANSIESC || got[1] != "x" {
		t.Fatalf("delivered %q, want escape then x in order", got)
	}
}

func TestProcessTerminalFlushPublicationBlocksEOF(t *testing.T) {
	terminal, _ := newOrderingTerminal(t, 5000)
	terminal.bufferMu.Lock()
	terminal.buffer.buffer = ANSIESC
	terminal.bufferMu.Unlock()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	readerGeneration := terminal.readerGeneration
	terminal.mu.Unlock()
	epoch := armFlushForTest(terminal, generation)
	type orderRecorder struct {
		mu     sync.Mutex
		events []string
	}
	order := &orderRecorder{}
	terminal.mu.Lock()
	terminal.inputHandler = func(sequence string) {
		order.mu.Lock()
		order.events = append(order.events, sequence)
		order.mu.Unlock()
	}
	terminal.OnEOF(func() {
		order.mu.Lock()
		order.events = append(order.events, "eof")
		order.mu.Unlock()
	})
	terminal.mu.Unlock()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	publish := func() {
		entered <- struct{}{}
		<-release
	}
	terminal.publishGate.Store(&publish)
	done := make(chan struct{})
	go func() {
		terminal.flushPending(generation, epoch)
		close(done)
	}()
	waitForChannel(t, entered, "flush publication to pause")
	session := readerSession{generation: readerGeneration}
	eofDone := make(chan struct{})
	go func() {
		terminal.deliverEOFForSession(session)
		close(eofDone)
	}()
	select {
	case <-eofDone:
		t.Fatalf("eof overtook paused flush publication")
	case <-time.After(60 * time.Millisecond):
	}
	close(release)
	waitForChannel(t, done, "paused flush to complete")
	waitForChannel(t, eofDone, "blocked eof to complete")
	deadline := time.Now().Add(2 * time.Second)
	for {
		order.mu.Lock()
		count := len(order.events)
		order.mu.Unlock()
		if count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			order.mu.Lock()
			events := append([]string(nil), order.events...)
			order.mu.Unlock()
			t.Fatalf("timed out waiting for input before eof, got %q", events)
		}
		time.Sleep(time.Millisecond)
	}
	order.mu.Lock()
	events := append([]string(nil), order.events...)
	order.mu.Unlock()
	if len(events) != 2 || events[0] != ANSIESC || events[1] != "eof" {
		t.Fatalf("delivery order = %q, want escape then eof", events)
	}
}

func TestProcessTerminalStaleFlushCannotConsumeReplacementInput(t *testing.T) {
	terminal, recorder := newOrderingTerminal(t, 5000)
	terminal.bufferMu.Lock()
	terminal.buffer.sequenceTimeoutMs = 200
	terminal.bufferMu.Unlock()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	pump := terminal.pump
	terminal.mu.Unlock()
	terminal.ingestInputChunk([]byte(ANSIESC), generation, pump)
	terminal.bufferMu.Lock()
	buffered := terminal.buffer.buffer
	terminal.bufferMu.Unlock()
	if buffered != ANSIESC {
		t.Fatalf("buffered escape = %q, want it retained for the stale schedule", buffered)
	}
	_, oldEpoch := readArmedFlush(t, terminal)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	consume := func() {
		entered <- struct{}{}
		<-release
	}
	terminal.consumeGate.Store(&consume)
	oldDone := make(chan struct{})
	go func() {
		terminal.flushPending(generation, oldEpoch)
		close(oldDone)
	}()
	waitForChannel(t, entered, "stale flush to pause before consume")

	replacement := ANSIESC + "["
	terminal.ingestInputChunk([]byte("["), generation, pump)

	close(release)
	waitForChannel(t, oldDone, "stale flush to return")

	terminal.bufferMu.Lock()
	terminal.flushMu.Lock()
	remaining := terminal.buffer.buffer
	newEpoch := terminal.flushArmedEpoch
	terminal.flushMu.Unlock()
	terminal.bufferMu.Unlock()
	if newEpoch == oldEpoch {
		t.Fatalf("replacement ingest did not advance flush epoch %d", oldEpoch)
	}
	if remaining != replacement {
		t.Fatalf("buffer after stale flush = %q, want %q preserved", remaining, replacement)
	}
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("deliveries after stale flush = %q, want none", got)
	}

	terminal.consumeGate.Store(nil)
	waitForFlushCount(t, recorder, 1)
	time.Sleep(80 * time.Millisecond)
	got := recorder.snapshot()
	if len(got) != 1 || got[0] != replacement {
		t.Fatalf("replacement delivered %q, want exactly one %q", got, replacement)
	}
}

func TestProcessTerminalCancelInvalidatesFlushToken(t *testing.T) {
	terminal, recorder := newOrderingTerminal(t, 5000)
	terminal.bufferMu.Lock()
	terminal.buffer.process([]byte(ANSIESC))
	terminal.bufferMu.Unlock()
	terminal.scheduleFlush()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()
	_, oldEpoch := readArmedFlush(t, terminal)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	consume := func() {
		entered <- struct{}{}
		<-release
	}
	terminal.consumeGate.Store(&consume)
	oldDone := make(chan struct{})
	go func() {
		terminal.flushPending(generation, oldEpoch)
		close(oldDone)
	}()
	waitForChannel(t, entered, "old timer to pause before consume")
	terminal.cancelFlushTimer()
	close(release)
	waitForChannel(t, oldDone, "cancelled timer to return")
	terminal.bufferMu.Lock()
	remaining := terminal.buffer.buffer
	terminal.bufferMu.Unlock()
	if remaining != ANSIESC {
		t.Fatalf("buffer after cancel = %q, want escape preserved", remaining)
	}
	time.Sleep(60 * time.Millisecond)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("cancelled flush delivered %q, want nothing", got)
	}
	terminal.consumeGate.Store(nil)
	terminal.bufferMu.Lock()
	terminal.buffer.escapeTimeoutMs = 20
	terminal.bufferMu.Unlock()
	terminal.scheduleFlush()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(recorder.snapshot()) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for rescheduled delivery after cancel")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(40 * time.Millisecond)
	got := recorder.snapshot()
	if len(got) != 1 || got[0] != ANSIESC {
		t.Fatalf("rescheduled after cancel delivered %q, want exactly one escape", got)
	}
}

func TestProcessTerminalStopInvalidatesFlushToken(t *testing.T) {
	terminal, recorder := newOrderingTerminal(t, 5000)
	terminal.bufferMu.Lock()
	terminal.buffer.process([]byte(ANSIESC))
	terminal.bufferMu.Unlock()
	terminal.scheduleFlush()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()
	_, oldEpoch := readArmedFlush(t, terminal)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	consume := func() {
		entered <- struct{}{}
		<-release
	}
	terminal.consumeGate.Store(&consume)
	oldDone := make(chan struct{})
	go func() {
		terminal.flushPending(generation, oldEpoch)
		close(oldDone)
	}()
	waitForChannel(t, entered, "old timer to pause before consume")
	terminal.Stop()
	close(release)
	waitForChannel(t, oldDone, "stopped timer to return")
	time.Sleep(60 * time.Millisecond)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("stopped flush delivered %q, want nothing", got)
	}
}

func TestProcessTerminalSuspendInvalidatesFlushToken(t *testing.T) {
	terminal, recorder := newOrderingTerminal(t, 5000)
	terminal.bufferMu.Lock()
	terminal.buffer.process([]byte(ANSIESC))
	terminal.bufferMu.Unlock()
	terminal.scheduleFlush()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()
	_, oldEpoch := readArmedFlush(t, terminal)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	consume := func() {
		entered <- struct{}{}
		<-release
	}
	terminal.consumeGate.Store(&consume)
	oldDone := make(chan struct{})
	go func() {
		terminal.flushPending(generation, oldEpoch)
		close(oldDone)
	}()
	waitForChannel(t, entered, "old timer to pause before consume")
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	close(release)
	waitForChannel(t, oldDone, "suspended timer to return")
	time.Sleep(60 * time.Millisecond)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("suspended flush delivered %q, want nothing", got)
	}
	terminal.bufferMu.Lock()
	remaining := terminal.buffer.buffer
	terminal.bufferMu.Unlock()
	if remaining != "" {
		t.Fatalf("buffer after suspend = %q, want empty", remaining)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	terminal.consumeGate.Store(nil)
	terminal.bufferMu.Lock()
	terminal.buffer.process([]byte("z"))
	terminal.bufferMu.Unlock()
	terminal.mu.Lock()
	pump := terminal.pump
	gen := terminal.deliveryGeneration
	terminal.mu.Unlock()
	terminal.bufferMu.Lock()
	sequences := terminal.buffer.takePending()
	if pump != nil && len(sequences) > 0 {
		pump.enqueue(deliveryBatch{sequences: sequences, generation: gen})
	}
	terminal.bufferMu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(recorder.snapshot()) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for post-resume delivery")
		}
		time.Sleep(time.Millisecond)
	}
	got := recorder.snapshot()
	if len(got) != 1 || got[0] != "z" {
		t.Fatalf("post-resume delivered %q, want exactly z", got)
	}
}

func TestProcessTerminalFlushPendingInactiveDrops(t *testing.T) {
	terminal, recorder := newOrderingTerminal(t, 5000)
	terminal.bufferMu.Lock()
	terminal.buffer.buffer = ANSIESC
	terminal.bufferMu.Unlock()
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()
	epoch := armFlushForTest(terminal, generation)
	terminal.mu.Lock()
	terminal.running = false
	terminal.stopped = true
	terminal.mu.Unlock()
	terminal.flushPending(generation, epoch)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("inactive flush delivered %q, want nothing", got)
	}
	terminal.flushMu.Lock()
	armed := terminal.flushArmed
	terminal.flushMu.Unlock()
	if armed {
		t.Fatalf("inactive flush left timer armed")
	}
}

func TestProcessTerminalFlushPendingEmptyDrops(t *testing.T) {
	terminal, recorder := newOrderingTerminal(t, 5000)
	terminal.mu.Lock()
	generation := terminal.deliveryGeneration
	terminal.mu.Unlock()
	epoch := armFlushForTest(terminal, generation)
	terminal.flushPending(generation, epoch)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("empty flush delivered %q, want nothing", got)
	}
	terminal.flushMu.Lock()
	armed := terminal.flushArmed
	terminal.flushMu.Unlock()
	if armed {
		t.Fatalf("empty flush left timer armed")
	}
}

func TestProcessTerminalFlushEpochMonotonic(t *testing.T) {
	terminal, _ := newOrderingTerminal(t, 5000)
	var last uint64
	for i := 0; i < 3; i++ {
		terminal.bufferMu.Lock()
		terminal.buffer.buffer = ANSIESC
		terminal.bufferMu.Unlock()
		terminal.scheduleFlush()
		_, epoch := readArmedFlush(t, terminal)
		if epoch <= last {
			t.Fatalf("flush epoch %d not monotonic after %d", epoch, last)
		}
		last = epoch
	}
	terminal.cancelFlushTimer()
	terminal.flushMu.Lock()
	afterCancel := terminal.flushNextEpoch
	terminal.flushMu.Unlock()
	if afterCancel <= last {
		t.Fatalf("cancel did not advance flush epoch %d beyond %d", afterCancel, last)
	}
	var calls int32
	_ = atomic.AddInt32(&calls, 1)
}
