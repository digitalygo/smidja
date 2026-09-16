package tui

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type suspendInputRecorder struct {
	mu     sync.Mutex
	inputs []string
	eofs   int
}

func (r *suspendInputRecorder) handle(data string) {
	r.mu.Lock()
	r.inputs = append(r.inputs, data)
	r.mu.Unlock()
}

func (r *suspendInputRecorder) handleEOF() {
	r.mu.Lock()
	r.eofs++
	r.mu.Unlock()
}

func (r *suspendInputRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inputs)
}

func (r *suspendInputRecorder) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.inputs, "")
}

func (r *suspendInputRecorder) eofCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.eofs
}

func waitForSuspendInput(t *testing.T, recorder *suspendInputRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if recorder.count() >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d inputs, got %q", want, recorder.joined())
		}
		time.Sleep(time.Millisecond)
	}
}

func startSuspendTestTerminal(t *testing.T) (*ProcessTerminal, *os.File, *fakeOps, *suspendInputRecorder, func() string) {
	t.Helper()
	terminal, stdinWrite, ops, drain := newTestTerminal(t)
	recorder := &suspendInputRecorder{}
	terminal.OnEOF(recorder.handleEOF)
	if err := terminal.Start(recorder.handle, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return terminal, stdinWrite, ops, recorder, drain
}

func assertNoFurtherInput(t *testing.T, recorder *suspendInputRecorder, want int, wait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if got := recorder.count(); got > want {
			t.Fatalf("inputs = %q, want no delivery beyond %d", recorder.joined(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func countOccurrences(haystack, needle string) int {
	count := 0
	for offset := 0; offset+len(needle) <= len(haystack); {
		index := strings.Index(haystack[offset:], needle)
		if index < 0 {
			break
		}
		count++
		offset += index + len(needle)
	}
	return count
}

func TestProcessTerminalSuspendsAndResumesReader(t *testing.T) {
	terminal, stdinWrite, ops, recorder, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()

	stdinWrite.WriteString("a")
	waitForSuspendInput(t, recorder, 1)
	drain()

	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	suspendOutput := drain()
	if !strings.Contains(suspendOutput, KittyProtocolPop) {
		t.Fatalf("suspend output missing kitty pop: %q", suspendOutput)
	}
	if !strings.Contains(suspendOutput, BracketedPasteOff) {
		t.Fatalf("suspend output missing bracketed paste disable: %q", suspendOutput)
	}
	if strings.Index(suspendOutput, KittyProtocolPop) > strings.Index(suspendOutput, BracketedPasteOff) {
		t.Fatalf("kitty pop must precede paste disable: %q", suspendOutput)
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls = %d, want 1", ops.restoreCalls)
	}

	stdinWrite.WriteString("MARK")
	assertNoFurtherInput(t, recorder, 1, 150*time.Millisecond)
	if got := recorder.count(); got != 1 {
		t.Fatalf("inputs delivered while parked = %d (%q), want 1", got, recorder.joined())
	}

	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	resumeOutput := drain()
	if !strings.Contains(resumeOutput, BracketedPasteOn) {
		t.Fatalf("resume output missing bracketed paste enable: %q", resumeOutput)
	}
	if !strings.Contains(resumeOutput, kittyQuery) {
		t.Fatalf("resume output missing kitty re-query: %q", resumeOutput)
	}
	if strings.Index(resumeOutput, BracketedPasteOn) > strings.Index(resumeOutput, kittyQuery) {
		t.Fatalf("paste enable must precede kitty re-query: %q", resumeOutput)
	}
	if ops.rawCalls != 2 {
		t.Fatalf("rawCalls = %d, want 2 after start and resume", ops.rawCalls)
	}

	stdinWrite.WriteString("b")
	waitForSuspendInput(t, recorder, 6)
	if got := recorder.joined(); got != "aMARKb" {
		t.Fatalf("inputs = %q, want exactly one delivery of each byte", got)
	}
	if recorder.eofCount() != 0 {
		t.Fatalf("eof deliveries = %d, want 0 while parked", recorder.eofCount())
	}
	stdinWrite.Close()
	deadline := time.Now().Add(2 * time.Second)
	for recorder.eofCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the reader EOF")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProcessTerminalSuspendRawIsIdempotent(t *testing.T) {
	terminal, stdinWrite, ops, _, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()

	drain()
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("first SuspendRaw() error = %v", err)
	}
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("second SuspendRaw() error = %v", err)
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls = %d, want 1", ops.restoreCalls)
	}
	if got := countOccurrences(drain(), BracketedPasteOff); got != 1 {
		t.Fatalf("bracketed paste disable writes = %d, want 1", got)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("first ResumeRaw() error = %v", err)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("second ResumeRaw() error = %v", err)
	}
	if ops.rawCalls != 2 {
		t.Fatalf("rawCalls = %d, want 2", ops.rawCalls)
	}
	if got := countOccurrences(drain(), BracketedPasteOn); got != 1 {
		t.Fatalf("bracketed paste enable writes = %d, want 1", got)
	}
}

func TestProcessTerminalSuspendRestoreErrorUnparksReader(t *testing.T) {
	terminal, stdinWrite, ops, recorder, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()

	drain()
	ops.restoreErr = errors.New("restore refused")
	if err := terminal.SuspendRaw(); err == nil {
		t.Fatal("SuspendRaw() with restore error should fail")
	}
	terminal.mu.Lock()
	suspended := terminal.suspended
	terminal.mu.Unlock()
	if suspended {
		t.Fatal("terminal must not be marked suspended after a failed SuspendRaw")
	}
	if !strings.Contains(drain(), BracketedPasteOn) {
		t.Fatalf("failed suspend must re-enable disabled protocols, got %q", drain())
	}
	stdinWrite.WriteString("x")
	waitForSuspendInput(t, recorder, 1)
	if got := recorder.joined(); got != "x" {
		t.Fatalf("inputs = %q, want x delivered after unpark", got)
	}
}

func TestProcessTerminalResumeRawErrorKeepsReaderParked(t *testing.T) {
	terminal, stdinWrite, ops, recorder, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()

	stdinWrite.WriteString("a")
	waitForSuspendInput(t, recorder, 1)
	drain()
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	ops.makeRawErr = errors.New("raw refused")
	if err := terminal.ResumeRaw(); err == nil {
		t.Fatal("ResumeRaw() with makeRaw error should fail")
	}
	terminal.mu.Lock()
	suspended := terminal.suspended
	terminal.mu.Unlock()
	if !suspended {
		t.Fatal("terminal must stay suspended after a failed ResumeRaw")
	}
	stdinWrite.WriteString("y")
	assertNoFurtherInput(t, recorder, 1, 150*time.Millisecond)
	if got := recorder.count(); got != 1 {
		t.Fatalf("inputs while resume failed = %d, want still 1", got)
	}
	ops.makeRawErr = nil
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("Retry ResumeRaw() error = %v", err)
	}
	stdinWrite.WriteString("z")
	waitForSuspendInput(t, recorder, 3)
	if got := recorder.joined(); got != "ayz" {
		t.Fatalf("inputs = %q, want ayz delivered once after recovery", got)
	}
}

func TestProcessTerminalStopDuringSuspension(t *testing.T) {
	terminal, stdinWrite, _, recorder, _ := startSuspendTestTerminal(t)
	defer stdinWrite.Close()

	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	terminal.Stop()
	terminal.mu.Lock()
	suspended := terminal.suspended
	stopped := terminal.stopped
	wasRaw := terminal.wasRaw
	terminal.mu.Unlock()
	if suspended || !stopped || wasRaw != nil {
		t.Fatalf("stop state suspended=%v stopped=%v wasRaw=%v", suspended, stopped, wasRaw)
	}
	select {
	case <-terminal.readerDone:
	default:
		t.Fatal("readerDone must be closed after Stop while parked")
	}
	if recorder.eofCount() != 0 {
		t.Fatalf("eof deliveries = %d, want 0 for a parked reader", recorder.eofCount())
	}
}

func TestProcessTerminalKeepsSingleResizeWatcherAcrossSuspension(t *testing.T) {
	terminal, stdinWrite, _, drain := newTestTerminal(t)
	defer stdinWrite.Close()

	recorder := &suspendInputRecorder{}
	resizes := 0
	var resizeMu sync.Mutex
	countResize := func() {
		resizeMu.Lock()
		resizes++
		resizeMu.Unlock()
	}
	terminal.OnEOF(recorder.handleEOF)
	if err := terminal.Start(recorder.handle, countResize); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	drain()
	waitForResizeCount(t, &resizeMu, &resizes, 1)

	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	refreshTerminalDimensions()
	waitForResizeCount(t, &resizeMu, &resizes, 2)
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	refreshTerminalDimensions()
	waitForResizeCount(t, &resizeMu, &resizes, 3)
	time.Sleep(150 * time.Millisecond)
	resizeMu.Lock()
	defer resizeMu.Unlock()
	if resizes != 3 {
		t.Fatalf("resize deliveries = %d, want exactly 3 for one watcher", resizes)
	}
}

func waitForResizeCount(t *testing.T, mu *sync.Mutex, count *int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := *count
		mu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d resize deliveries, got %d", want, got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProcessTerminalSuspendDisablesModifyOtherKeys(t *testing.T) {
	terminal, stdinWrite, ops, _, drain := startSuspendTestTerminal(t)
	defer stdinWrite.Close()

	terminal.enableModifyOtherKeys()
	drain()

	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	suspendOutput := drain()
	if !strings.Contains(suspendOutput, ModifyOtherKeysDisable) {
		t.Fatalf("suspend output missing modifyOtherKeys disable: %q", suspendOutput)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	resumeOutput := drain()
	if !strings.Contains(resumeOutput, ModifyOtherKeysEnable) {
		t.Fatalf("resume output missing modifyOtherKeys enable: %q", resumeOutput)
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls = %d, want 1", ops.restoreCalls)
	}
}

func TestProcessTerminalDiscardsInputReadDuringQuiesce(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	terminal.stdinReadableFn = func(uintptr, time.Duration) (bool, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return true, nil
	}
	recorder := &suspendInputRecorder{}
	terminal.OnEOF(recorder.handleEOF)
	if err := terminal.Start(recorder.handle, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-entered
	suspendDone := make(chan error, 1)
	go func() { suspendDone <- terminal.SuspendRaw() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		terminal.mu.Lock()
		paused := terminal.readerPaused
		terminal.mu.Unlock()
		if paused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the reader park to begin")
		}
		time.Sleep(time.Millisecond)
	}
	stdinWrite.WriteString("q")
	close(release)
	if err := <-suspendDone; err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	assertNoFurtherInput(t, recorder, 0, 150*time.Millisecond)
	if got := recorder.count(); got != 0 {
		t.Fatalf("inputs = %q, want the byte read during quiesce to be discarded", recorder.joined())
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	stdinWrite.WriteString("b")
	waitForSuspendInput(t, recorder, 1)
	if got := recorder.joined(); got != "b" {
		t.Fatalf("inputs = %q, want only b delivered after resume", got)
	}
}

func TestProcessTerminalParkTimeoutWithholdsReplacementUntilExit(t *testing.T) {
	var gates []chan struct{}
	var entered []chan struct{}
	for i := 0; i < 2; i++ {
		gates = append(gates, make(chan struct{}))
		entered = append(entered, make(chan struct{}, 1))
	}
	terminal, stdinWrite, ops, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	var gateCalls int32
	terminal.stdinReadableFn = func(uintptr, time.Duration) (bool, error) {
		call := int(atomic.AddInt32(&gateCalls, 1)) - 1
		if call >= len(gates) {
			call = len(gates) - 1
		}
		select {
		case entered[call] <- struct{}{}:
		default:
		}
		<-gates[call]
		return true, nil
	}
	recorder := &suspendInputRecorder{}
	terminal.OnEOF(recorder.handleEOF)
	if err := terminal.Start(recorder.handle, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	terminal.readerParkWait = 50 * time.Millisecond

	<-entered[0]
	suspendDone := make(chan error, 1)
	go func() { suspendDone <- terminal.SuspendRaw() }()
	if err := <-suspendDone; err == nil {
		t.Fatal("SuspendRaw() should fail when the reader cannot park in time")
	} else if !errors.Is(err, errReaderParkTimeout) {
		t.Fatalf("SuspendRaw() error = %v, want errReaderParkTimeout", err)
	}

	terminal.mu.Lock()
	generation := terminal.readerGeneration
	paused := terminal.readerPaused
	suspended := terminal.suspended
	terminal.mu.Unlock()
	if generation != 1 {
		t.Fatalf("reader generation = %d, want 1 while the retired reader is still blocked", generation)
	}
	if !paused {
		t.Fatal("terminal must stay quiesced after a park timeout")
	}
	if suspended {
		t.Fatal("terminal must not report suspended after a park timeout")
	}
	if ops.restoreCalls != 0 {
		t.Fatalf("restoreCalls = %d, want 0 when parking fails", ops.restoreCalls)
	}
	select {
	case <-entered[1]:
		t.Fatal("a replacement reader started before the retired reader exited")
	case <-time.After(100 * time.Millisecond):
	}

	stdinWrite.WriteString("old")
	close(gates[0])
	waitForReaderGeneration(t, terminal, 2)
	<-entered[1]
	stdinWrite.WriteString("z")
	close(gates[1])
	waitForSuspendInput(t, recorder, 1)
	if got := recorder.joined(); got != "z" {
		t.Fatalf("inputs = %q, want exactly one delivery from the replacement reader", got)
	}
	if recorder.eofCount() != 0 {
		t.Fatalf("eof deliveries = %d, want 0 for the retired reader", recorder.eofCount())
	}
	terminal.Stop()
}

func waitForReaderGeneration(t *testing.T, terminal *ProcessTerminal, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		terminal.mu.Lock()
		generation := terminal.readerGeneration
		terminal.mu.Unlock()
		if generation >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for reader generation %d, got %d", want, generation)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProcessTerminalFlushPendingDropsWhileParked(t *testing.T) {
	terminal, recorder := newFlushTerminal(t)
	terminal.mu.Lock()
	terminal.readerPaused = true
	terminal.mu.Unlock()
	terminal.feedForTest(ANSIESC)
	time.Sleep(40 * time.Millisecond)
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("flush callback ran while parked: %q", got)
	}
}
