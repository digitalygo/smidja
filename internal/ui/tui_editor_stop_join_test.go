package ui

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func TestRunnerStopDuringBlockedEditorWaitsAndJoinsActionWorker(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = TUIModeFullscreen
	opts.Home = t.TempDir()
	counter := &editorRunCounter{gate: make(chan struct{}), start: make(chan struct{}, 1)}
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		counter.enter()
		terminal.record("run")
		return os.WriteFile(filePath, []byte("edited text"), 0o600)
	})
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	terminal.SendInput("\x07")
	select {
	case <-counter.start:
	case <-time.After(2 * time.Second):
		t.Fatal("the external editor command did not start")
	}
	if terminal.StopCalls() != 0 || terminal.ResumeCalls() != 0 {
		t.Fatalf("terminal restored while the editor command owns stdio: stop=%d resume=%d",
			terminal.StopCalls(), terminal.ResumeCalls())
	}
	probeRan := make(chan struct{})
	if !runner.enqueueAction(func() { close(probeRan) }) {
		t.Fatal("the action queue rejected a probe before shutdown began")
	}
	stopReturned := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
		t.Fatal("Stop returned while the editor command still owned the terminal")
	case <-time.After(150 * time.Millisecond):
	}
	if terminal.StopCalls() != 0 || terminal.ResumeCalls() != 0 {
		t.Fatalf("terminal restored during the blocked editor: stop=%d resume=%d",
			terminal.StopCalls(), terminal.ResumeCalls())
	}
	runner.RequestExit()
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the exit request did not close Done()")
	}
	if terminal.StopCalls() != 0 || terminal.ResumeCalls() != 0 {
		t.Fatal("the exit request restored the terminal during the blocked editor")
	}
	close(counter.gate)
	select {
	case <-stopReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the editor command was released")
	}
	if counter.count() != 1 {
		t.Fatalf("editor runs = %d, want 1", counter.count())
	}
	if terminal.SuspendCalls() != 1 || terminal.ResumeCalls() != 1 {
		t.Fatalf("suspend/resume calls = %d/%d, want 1/1", terminal.SuspendCalls(), terminal.ResumeCalls())
	}
	if got := terminal.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	select {
	case <-probeRan:
		t.Fatal("a queued action ran after shutdown began")
	default:
	}
	if runner.enqueueAction(func() {}) {
		t.Fatal("the action queue accepted work after shutdown")
	}
	runner.actionsMu.Lock()
	workerDone := runner.actionsDone
	runner.actionsMu.Unlock()
	if workerDone == nil {
		t.Fatal("the action worker was never started")
	}
	select {
	case <-workerDone:
	default:
		t.Fatal("Stop returned before the action worker was joined")
	}
	events := terminal.Events()
	finalRestore := -1
	for i, event := range events {
		if event == "altExit" {
			finalRestore = i
		}
	}
	if finalRestore < 0 {
		t.Fatalf("the final restoration never ran: %q", events)
	}
	for _, event := range events[finalRestore+1:] {
		if event == "altEnter" || event == "resume" || event == "suspend" {
			t.Fatalf("protocol write %q after the final restoration: %q", event, events)
		}
	}
	mark := len(terminal.Events())
	time.Sleep(100 * time.Millisecond)
	if got := len(terminal.Events()); got != mark {
		t.Fatalf("terminal wrote after the final restoration: %q", terminal.Events()[mark:])
	}
	writes := terminal.WriteCount()
	runner.Stop()
	runner.Stop()
	if terminal.WriteCount() != writes {
		t.Fatal("repeated Stop must not write to the terminal")
	}
}

type suspendBarrierTerminal struct {
	*fakeUITerminal
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	order   []string
	marker  string
}

func (b *suspendBarrierTerminal) SuspendRaw() error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	err := b.fakeUITerminal.SuspendRaw()
	b.mu.Lock()
	b.order = append(b.order, "suspend")
	b.mu.Unlock()
	return err
}

func (b *suspendBarrierTerminal) ResumeRaw() error {
	err := b.fakeUITerminal.ResumeRaw()
	b.mu.Lock()
	b.order = append(b.order, "resume")
	b.mu.Unlock()
	return err
}

func (b *suspendBarrierTerminal) Write(data string) {
	if b.marker != "" && strings.Contains(data, b.marker) {
		b.mu.Lock()
		b.order = append(b.order, "marker")
		b.mu.Unlock()
	}
	b.fakeUITerminal.Write(data)
}

func (b *suspendBarrierTerminal) ordered() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.order...)
}

func TestRunnerStopDuringSuspendRawResumesBeforeFinalFlush(t *testing.T) {
	underlying := newFakeUITerminal(80, 24)
	barrier := &suspendBarrierTerminal{
		fakeUITerminal: underlying,
		entered:        make(chan struct{}),
		release:        make(chan struct{}),
		marker:         "suspend barrier marker",
	}
	opts := fakeUIRunnerOptions(underlying)
	opts.Mode = TUIModeRegular
	opts.Home = t.TempDir()
	opts.NewTerminal = func(stdin io.Reader, stdout io.Writer) tui.Terminal {
		return barrier
	}
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		underlying.record("run")
		return os.WriteFile(filePath, []byte("edited text"), 0o600)
	})
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	underlying.SendInput("\x07")
	select {
	case <-barrier.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("SuspendRaw did not start")
	}
	runner.Surface().AddNotice(interactive.NoticeWarning, "suspend barrier marker")
	stopReturned := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
		t.Fatal("Stop returned while SuspendRaw still owned the terminal")
	case <-time.After(150 * time.Millisecond):
	}
	if got := underlying.StopCalls(); got != 0 {
		t.Fatalf("terminal stop count = %d, want 0 while suspend blocked", got)
	}
	if got := underlying.ResumeCalls(); got != 0 {
		t.Fatalf("resume calls = %d, want 0 while suspend blocked", got)
	}
	close(barrier.release)
	select {
	case <-stopReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after suspend release")
	}
	if got := underlying.SuspendCalls(); got != 1 {
		t.Fatalf("suspend calls = %d, want 1", got)
	}
	if got := underlying.ResumeCalls(); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
	order := barrier.ordered()
	resumeIdx := -1
	markerIdx := -1
	for i, v := range order {
		if v == "resume" && resumeIdx < 0 {
			resumeIdx = i
		}
		if v == "marker" && markerIdx < 0 {
			markerIdx = i
		}
	}
	if resumeIdx < 0 {
		t.Fatalf("resume never ran before final flush: %q", order)
	}
	if markerIdx < 0 {
		t.Fatalf("final flush marker missing, output:\n%s", underlying.Output())
	}
	if resumeIdx > markerIdx {
		t.Fatalf("resume after final flush: %q", order)
	}
	output := underlying.Output()
	if !strings.Contains(output, "suspend barrier marker") {
		t.Fatalf("stop did not flush pending marker:\n%s", output)
	}
	cursorShow := strings.LastIndex(output, tui.CursorShow)
	if cursorShow < 0 {
		t.Fatalf("output missing terminal restore:\n%s", output)
	}
	if strings.Index(output, "suspend barrier marker") > cursorShow {
		t.Fatalf("marker must render before restore:\n%s", output)
	}
	if got := underlying.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	markEvents := len(underlying.Events())
	markWrites := underlying.WriteCount()
	time.Sleep(100 * time.Millisecond)
	if got := len(underlying.Events()); got != markEvents {
		t.Fatalf("terminal wrote after stop: %q", underlying.Events()[markEvents:])
	}
	if got := underlying.WriteCount(); got != markWrites {
		t.Fatalf("writes after stop = %d, want %d", got, markWrites)
	}
	runner.Stop()
	if got := underlying.WriteCount(); got != markWrites {
		t.Fatal("repeated Stop wrote to the terminal")
	}
}
