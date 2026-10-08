package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type editorRunCounter struct {
	mu    sync.Mutex
	runs  int
	gate  chan struct{}
	start chan struct{}
}

func (c *editorRunCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs
}

func (c *editorRunCounter) enter() {
	c.mu.Lock()
	c.runs++
	c.mu.Unlock()
	if c.start != nil {
		c.start <- struct{}{}
	}
	if c.gate != nil {
		<-c.gate
	}
}

func waitForOutputText(t *testing.T, terminal *fakeUITerminal, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if strings.Contains(terminal.Output(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in output:\n%s", want, terminal.Output())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func eventsSince(terminal *fakeUITerminal, mark int) []string {
	events := terminal.Events()
	if mark > len(events) {
		mark = len(events)
	}
	return events[mark:]
}

func waitForCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitForEvents(t *testing.T, terminal *fakeUITerminal, mark int, want []string) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got []string
	for {
		got = eventsSince(terminal, mark)
		if len(got) >= len(want) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for events %q, got %q", want, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func editorRunner(t *testing.T, mode TUIMode, mutate func(*RunnerOptions)) (*Runner, *fakeUITerminal, *editorRunCounter) {
	t.Helper()
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = mode
	opts.Home = t.TempDir()
	counter := &editorRunCounter{}
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		counter.enter()
		terminal.record("run")
		return os.WriteFile(filePath, []byte("edited text"), 0o600)
	})
	if mutate != nil {
		mutate(&opts)
	}
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runner.Stop)
	return runner, terminal, counter
}

func TestRunnerExternalEditorFullscreenSuspensionOrdering(t *testing.T) {
	runner, terminal, counter := editorRunner(t, TUIModeFullscreen, nil)
	mark := len(terminal.Events())
	runner.Surface().AddUserMessage("before edit")
	terminal.SendInput("\x07")
	want := []string{"altExit", "suspend", "run", "resume", "altEnter"}
	got := waitForEvents(t, terminal, mark, want)
	if len(got) != len(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %q, want %q", got, want)
		}
	}
	if terminal.SuspendCalls() != 1 || terminal.ResumeCalls() != 1 {
		t.Fatalf("suspend/resume calls = %d/%d, want 1/1", terminal.SuspendCalls(), terminal.ResumeCalls())
	}
	if counter.count() != 1 {
		t.Fatalf("editor runs = %d, want 1", counter.count())
	}
	if got := strings.TrimSpace(runner.Surface().Editor().Text()); got != "edited text" {
		t.Fatalf("editor text = %q, want the external result", got)
	}
}

func TestRunnerExternalEditorRegularSuspensionOrdering(t *testing.T) {
	runner, terminal, counter := editorRunner(t, TUIModeRegular, nil)
	mark := len(terminal.Events())
	for _, key := range []string{"d", "r", "a", "f", "t"} {
		terminal.SendInput(key)
	}
	terminal.SendInput("\x07")
	want := []string{"suspend", "run", "resume"}
	got := waitForEvents(t, terminal, mark, want)
	if len(got) != len(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %q, want %q", got, want)
		}
	}
	if counter.count() != 1 {
		t.Fatalf("editor runs = %d, want 1", counter.count())
	}
	if got := strings.TrimSpace(runner.Surface().Editor().Text()); got != "edited text" {
		t.Fatalf("editor text = %q, want the external result", got)
	}
}

func TestRunnerExternalEditorRepeatedInvocation(t *testing.T) {
	runner, terminal, counter := editorRunner(t, TUIModeRegular, nil)
	terminal.SendInput("\x07")
	waitForEvents(t, terminal, 0, []string{"suspend", "run", "resume"})
	terminal.SendInput("\x07")
	want := []string{"suspend", "run", "resume", "suspend", "run", "resume"}
	got := waitForEvents(t, terminal, 0, want)
	if len(got) != len(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %q, want %q", got, want)
		}
	}
	if terminal.SuspendCalls() != 2 || terminal.ResumeCalls() != 2 {
		t.Fatalf("suspend/resume calls = %d/%d, want 2/2", terminal.SuspendCalls(), terminal.ResumeCalls())
	}
	if counter.count() != 2 {
		t.Fatalf("editor runs = %d, want 2", counter.count())
	}
	if !runner.Active() {
		t.Fatal("runner must stay active after repeated editor invocations")
	}
}

func TestRunnerExternalEditorReentrantCallIsNoop(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	counter := &editorRunCounter{}
	var runner *Runner
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		counter.enter()
		terminal.record("run")
		runner.runExternalEditor()
		return nil
	})
	runner = NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	mark := len(terminal.Events())
	terminal.SendInput("\x07")
	want := []string{"suspend", "run", "resume"}
	got := waitForEvents(t, terminal, mark, want)
	if len(got) != len(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %q, want %q", got, want)
		}
	}
	if counter.count() != 1 {
		t.Fatalf("editor runs = %d, want 1", counter.count())
	}
}

func TestRunnerExternalEditorInputDroppedWhileActive(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
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
	defer runner.Stop()
	for _, key := range []string{"d", "r", "a", "f", "t"} {
		terminal.SendInput(key)
	}
	finished := make(chan struct{})
	go func() {
		terminal.SendInput("\x07")
		close(finished)
	}()
	<-counter.start
	terminal.SendInput("zzz")
	if text := runner.Surface().Editor().Text(); strings.Contains(text, "z") {
		t.Fatalf("editor text = %q, want input dropped while the editor is active", text)
	}
	close(counter.gate)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("editor invocation did not finish")
	}
	waitForCondition(t, "the external editor result", func() bool {
		return strings.TrimSpace(runner.Surface().Editor().Text()) == "edited text"
	})
	terminal.SendInput("q")
	waitForCondition(t, "input accepted after resume", func() bool {
		return strings.Contains(runner.Surface().Editor().Text(), "q")
	})
}

func TestRunnerExternalEditorCommandFailureStillResumes(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	counter := &editorRunCounter{}
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		counter.enter()
		terminal.record("run")
		return errors.New("editor exploded")
	})
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	mark := len(terminal.Events())
	terminal.SendInput("\x07")
	want := []string{"suspend", "run", "resume"}
	got := waitForEvents(t, terminal, mark, want)
	if len(got) != len(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %q, want %q", got, want)
		}
	}
	if terminal.ResumeCalls() != 1 {
		t.Fatalf("resumeCalls = %d, want 1 after command failure", terminal.ResumeCalls())
	}
	if !runner.Active() {
		t.Fatal("runner must stay active after a command failure")
	}
	waitForOutputText(t, terminal, "editor exploded")
}

func TestRunnerExternalEditorSuspendFailurePreventsCommand(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	counter := &editorRunCounter{}
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		counter.enter()
		return nil
	})
	terminal.suspendErr = errors.New("suspend refused")
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	mark := len(terminal.Events())
	terminal.SendInput("\x07")
	got := waitForEvents(t, terminal, mark, []string{"suspend"})
	if len(got) != 1 || got[0] != "suspend" {
		t.Fatalf("events = %q, want only [suspend]", got)
	}
	if counter.count() != 0 {
		t.Fatalf("editor runs = %d, want 0 when suspend fails", counter.count())
	}
	if terminal.ResumeCalls() != 0 {
		t.Fatalf("resumeCalls = %d, want 0 when suspend failed", terminal.ResumeCalls())
	}
	if !runner.Active() {
		t.Fatal("runner must stay active after a suspend failure")
	}
	waitForOutputText(t, terminal, "suspend refused")
}

func TestRunnerExternalEditorResumeFailureRequestsExitOnly(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	counter := &editorRunCounter{}
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		counter.enter()
		terminal.record("run")
		return os.WriteFile(filePath, []byte("edited text"), 0o600)
	})
	terminal.resumeErr = errors.New("resume refused")
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	mark := len(terminal.Events())
	terminal.SendInput("\x07")
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a resume failure must request exit")
	}
	if !runner.Active() {
		t.Fatal("the action callback must not stop the runner directly")
	}
	if counter.count() != 1 {
		t.Fatalf("editor runs = %d, want 1", counter.count())
	}
	if terminal.SuspendCalls() != 1 || terminal.ResumeCalls() != 1 {
		t.Fatalf("suspend/resume calls = %d/%d, want 1/1", terminal.SuspendCalls(), terminal.ResumeCalls())
	}
	want := []string{"suspend", "run", "resume"}
	got := waitForEvents(t, terminal, mark, want)
	if len(got) != len(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %q, want %q", got, want)
		}
	}
	if !strings.Contains(terminal.Output(), "resume refused") {
		t.Fatalf("output must report the resume error:\n%s", terminal.Output())
	}
	if runner.enqueueAction(func() {}) {
		t.Fatal("the action queue must reject work after a resume failure requested exit")
	}
	stopDone := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after a resume failure")
	}
	if runner.Active() {
		t.Fatal("runner must report inactive after Stop")
	}
	if got := terminal.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	writes := terminal.WriteCount()
	events := len(terminal.Events())
	runner.Stop()
	if terminal.WriteCount() != writes || len(terminal.Events()) != events {
		t.Fatal("repeated Stop must not write to the terminal")
	}
}

func TestRunnerExternalEditorRequestExitDuringEditorIsSafe(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	counter := &editorRunCounter{}
	var exitingRunner *Runner
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		counter.enter()
		terminal.record("run")
		exitingRunner.RequestExit()
		return os.WriteFile(filePath, []byte("edited text"), 0o600)
	})
	runner := NewRunner(opts)
	exitingRunner = runner
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	terminal.SendInput("\x07")
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("runner must finish after RequestExit during the editor")
	}
	if counter.count() != 1 {
		t.Fatalf("editor runs = %d, want 1", counter.count())
	}
	waitForCondition(t, "resume after the external editor", func() bool {
		return terminal.ResumeCalls() == 1
	})
	if terminal.SuspendCalls() != 1 || terminal.ResumeCalls() != 1 {
		t.Fatalf("suspend/resume calls = %d/%d, want 1/1", terminal.SuspendCalls(), terminal.ResumeCalls())
	}
	runner.Stop()
	runner.Stop()
	if got := terminal.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want exactly one orderly stop", got)
	}
}

func TestRunnerExternalEditorDefaultRunnerAttachesRunnerStdio(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	stdout := &lockedBuffer{}
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	opts.Stdin = strings.NewReader("STDIN-MARK\n")
	opts.Stdout = stdout
	opts.ExternalCommand = "cat -"
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	for _, key := range []string{"d", "r", "a", "f", "t"} {
		terminal.SendInput(key)
	}
	terminal.SendInput("\x07")
	waitForCondition(t, "the external editor command to finish", func() bool {
		return strings.Contains(stdout.String(), "draft") &&
			strings.TrimSpace(runner.Surface().Editor().Text()) == "draft"
	})
	if got := strings.TrimSpace(runner.Surface().Editor().Text()); got != "draft" {
		t.Fatalf("editor text = %q, want the unchanged file content from cat", got)
	}
	if !strings.Contains(stdout.String(), "STDIN-MARK") {
		t.Fatalf("runner stdin was not attached to the editor command, output:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "draft") {
		t.Fatalf("runner stdout was not attached to the editor command, output:\n%s", stdout.String())
	}
}

func TestRunEditorCommandAttachesStdio(t *testing.T) {
	directory := t.TempDir()
	filePath := filepath.Join(directory, "prompt.md")
	if err := os.WriteFile(filePath, []byte("file body"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var out bytes.Buffer
	if err := runEditorCommand("cat -", filePath, strings.NewReader("stdin body\n"), &out); err != nil {
		t.Fatalf("runEditorCommand() error = %v", err)
	}
	if !strings.Contains(out.String(), "stdin body") {
		t.Fatalf("stdin not attached to the command, output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "file body") {
		t.Fatalf("stdout not attached to the command, output:\n%s", out.String())
	}

	out.Reset()
	if err := runEditorCommand("cat /definitely-missing-smidja-file", filePath, strings.NewReader(""), &out); err == nil {
		t.Fatal("runEditorCommand() should propagate the command exit error")
	}
	if out.Len() == 0 {
		t.Fatalf("stderr not captured into the runner writer, output:\n%s", out.String())
	}

	out.Reset()
	if err := runEditorCommand("   ", filePath, strings.NewReader(""), &out); err == nil {
		t.Fatal("runEditorCommand() should reject an empty command")
	}
}
