package ui

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

func startTestRunner(t *testing.T, mode TUIMode, mutate func(*RunnerOptions)) (*Runner, *fakeUITerminal) {
	t.Helper()
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = mode
	opts.Home = t.TempDir()
	if mutate != nil {
		mutate(&opts)
	}
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runner.Stop)
	return runner, terminal
}

func renderedOutput(runner *Runner, terminal *fakeUITerminal) string {
	runner.view.RenderNow(true)
	time.Sleep(5 * time.Millisecond)
	mark := terminal.WriteCount()
	runner.view.RenderNow(true)
	return terminal.OutputSince(mark)
}

func TestRunnerRegularStartupShutdown(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if runner.Active() {
		t.Fatal("runner should not be active before Start")
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !runner.Active() {
		t.Fatal("runner should be active after Start")
	}
	if !strings.Contains(terminal.Output(), tui.OSCTitle("smidja")) {
		t.Errorf("output missing default title, got %q", terminal.Output())
	}
	if strings.Contains(terminal.Output(), tui.AltScreenEnter) {
		t.Errorf("regular mode must not enter the alt screen, got %q", terminal.Output())
	}
	runner.Stop()
	if runner.Active() {
		t.Fatal("runner should not be active after Stop")
	}
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() did not close after Stop")
	}
	writes := terminal.WriteCount()
	runner.Stop()
	runner.Stop()
	if got := terminal.WriteCount(); got != writes {
		t.Fatalf("writes after repeated Stop = %d, want %d", got, writes)
	}
	if err := runner.Start(); err == nil {
		t.Fatal("Start() after Stop should fail")
	}
}

func TestRunnerFullscreenEnterBeforeOutputExitAfter(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	runner.Surface().AddUserMessage("fullscreen marker")
	runner.view.RenderNow(true)
	output := terminal.Output()
	enter := strings.Index(output, tui.AltScreenEnter)
	marker := strings.Index(output, "fullscreen marker")
	if enter < 0 {
		t.Fatalf("output missing alt-screen enter:\n%s", output)
	}
	if marker < 0 {
		t.Fatalf("output missing the user message:\n%s", output)
	}
	if enter > marker {
		t.Fatalf("alt-screen enter must precede output:\n%s", output)
	}
	runner.Stop()
	final := terminal.Output()
	exit := strings.Index(final, tui.AltScreenExit)
	firstMarker := strings.Index(final, "fullscreen marker")
	if exit < 0 {
		t.Fatalf("output missing alt-screen exit:\n%s", final)
	}
	if exit < firstMarker {
		t.Fatalf("alt-screen exit must follow output:\n%s", final)
	}
}

func TestRunnerStartupFailurePropagates(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	terminal.startErr = errors.New("no terminal here")
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = TUIModeFullscreen
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err == nil {
		t.Fatal("Start() should propagate the terminal error")
	} else if err.Error() != "no terminal here" {
		t.Fatalf("Start() error = %v", err)
	}
	if runner.Active() {
		t.Fatal("runner should not be active after failed Start")
	}
	runner.Stop()
	output := terminal.Output()
	if !strings.Contains(output, tui.AltScreenEnter) || !strings.Contains(output, tui.AltScreenExit) {
		t.Fatalf("failed start must balance the alt-screen enter with an exit, got %q", output)
	}
}

func TestRunnerSubmitRoutesToCallback(t *testing.T) {
	var submitted []string
	_, terminal := startTestRunner(t, TUIModeRegular, func(opts *RunnerOptions) {
		opts.OnSubmit = func(text string) { submitted = append(submitted, text) }
	})
	for _, key := range []string{"h", "i", "\r"} {
		terminal.SendInput(key)
	}
	if len(submitted) != 1 || submitted[0] != "hi" {
		t.Fatalf("submitted = %q, want [hi]", submitted)
	}
}

func TestRunnerSubmitDisabledWhileWorking(t *testing.T) {
	var submitted []string
	runner, terminal := startTestRunner(t, TUIModeRegular, func(opts *RunnerOptions) {
		opts.OnSubmit = func(text string) { submitted = append(submitted, text) }
	})
	runner.SetWorking(true)
	if !runner.Working() {
		t.Fatal("runner should report working")
	}
	for _, key := range []string{"x", "\r"} {
		terminal.SendInput(key)
	}
	if len(submitted) != 0 {
		t.Fatalf("submitted while working = %q, want none", submitted)
	}
	runner.SetWorking(false)
	terminal.SendInput("\r")
	if len(submitted) != 1 || submitted[0] != "x" {
		t.Fatalf("submitted after work = %q, want [x]", submitted)
	}
}

func TestRunnerInterruptOnlyWhileWorking(t *testing.T) {
	interrupts := 0
	runner, terminal := startTestRunner(t, TUIModeRegular, func(opts *RunnerOptions) {
		opts.OnInterrupt = func() { interrupts++ }
	})
	terminal.SendInput("\x1b")
	if interrupts != 0 {
		t.Fatal("interrupt must not fire while idle")
	}
	runner.SetWorking(true)
	terminal.SendInput("\x1b")
	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
	runner.Interrupt()
	if interrupts != 2 {
		t.Fatalf("interrupts = %d, want 2 after direct Interrupt", interrupts)
	}
	runner.SetWorking(false)
	runner.Interrupt()
	if interrupts != 2 {
		t.Fatalf("interrupts = %d, want still 2 when idle", interrupts)
	}
}

func TestRunnerExitOnEmptyEditor(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	terminal.SendInput("\x04")
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("ctrl+d on an empty editor should exit")
	}
}

func TestRunnerExitKeyKeepsNonEmptyEditor(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	terminal.SendInput("z")
	terminal.SendInput("\x04")
	select {
	case <-runner.Done():
		t.Fatal("ctrl+d with editor text must not exit")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRunnerEOFExits(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	terminal.FireEOF()
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("EOF should exit the runner")
	}
}

func TestRunnerSignalLoopExits(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	signals := make(chan os.Signal, 1)
	go runner.signalLoop(signals)
	signals <- os.Interrupt
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("signal should exit the runner")
	}
}

func TestRunnerRequestExitDoesNotStopRunner(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	runner.RequestExit()
	runner.RequestExit()
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("RequestExit should close Done()")
	}
	if !runner.Active() {
		t.Fatal("RequestExit must not stop the runner")
	}
	for _, event := range terminal.Events() {
		if event == "altExit" {
			t.Fatal("RequestExit must not restore the terminal")
		}
	}
	runner.Stop()
	if runner.Active() {
		t.Fatal("Stop must stop the runner after an exit request")
	}
	restored := false
	for _, event := range terminal.Events() {
		if event == "altExit" {
			restored = true
		}
	}
	if !restored {
		t.Fatal("Stop must restore the terminal after an exit request")
	}
}

func TestRunnerExternalEditorSuspendsRawMode(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	var edited []string
	var submitted []string
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	opts.OnSubmit = func(text string) { submitted = append(submitted, text) }
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		edited = append(edited, string(data))
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
	terminal.SendInput("\x07")
	waitForCondition(t, "the external editor suspend and resume", func() bool {
		return terminal.SuspendCalls() == 1 && terminal.ResumeCalls() == 1
	})
	if len(edited) != 1 || !strings.Contains(edited[0], "draft") {
		t.Fatalf("editor input = %q, want the buffer text", edited)
	}
	waitForCondition(t, "the external editor result", func() bool {
		return strings.TrimSpace(runner.Surface().Editor().Text()) == "edited text"
	})
	for _, key := range []string{"\r"} {
		terminal.SendInput(key)
	}
	waitForCondition(t, "submit after external edit", func() bool {
		return len(submitted) == 1 && submitted[0] == "edited text"
	})
}

func TestRunnerExternalEditorResumesOnError(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
		return errors.New("editor exploded")
	})
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	terminal.SendInput("\x07")
	waitForCondition(t, "the external editor suspend and resume", func() bool {
		return terminal.SuspendCalls() == 1 && terminal.ResumeCalls() == 1
	})
}

func TestRunnerUISetMethods(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	runner.Notify("hello notice", sdk.NotifyInfo)
	runner.Notify("careful", sdk.NotifyWarning)
	runner.Notify("broken", sdk.NotifyError)
	runner.SetStatus("ext", "extension status")
	runner.SetWidget("ext", []string{"widget line"})
	runner.SetWorking(true)
	runner.SetWorkingMessage("crunching")
	output := renderedOutput(runner, terminal)
	runner.SetWorking(false)
	for _, want := range []string{"hello notice", "careful", "broken", "extension status", "widget line", "crunching"} {
		if !strings.Contains(output, want) {
			t.Errorf("frame missing %q:\n%s", want, output)
		}
	}
	runner.SetStatus("ext", "")
	runner.SetWidget("ext", nil)
	cleared := renderedOutput(runner, terminal)
	if strings.Contains(cleared, "extension status") || strings.Contains(cleared, "widget line") {
		t.Errorf("cleared status and widget should disappear:\n%s", cleared)
	}
}

func TestRunnerUIDialogsUnsupported(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	if _, err := runner.Confirm("t", "m"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Confirm error = %v, want ErrModeUnsupported", err)
	}
	if _, err := runner.Select("t", []string{"a"}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Select error = %v, want ErrModeUnsupported", err)
	}
	if _, err := runner.Input("t", "p"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Input error = %v, want ErrModeUnsupported", err)
	}
	if _, err := runner.Editor("t", "p"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Editor error = %v, want ErrModeUnsupported", err)
	}
}

func TestRunnerUIInactiveDrops(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	runner.Notify("early", sdk.NotifyInfo)
	runner.SetStatus("k", "v")
	runner.SetWidget("k", []string{"v"})
	runner.SetWorkingMessage("early work")
	writes := terminal.WriteCount()
	runner.RequestRender(true)
	if got := terminal.WriteCount(); got != writes {
		t.Fatalf("writes before Start = %d, want %d", got, writes)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runner.SetTitle("updated title")
	runner.Stop()
	if !strings.Contains(terminal.Output(), tui.OSCTitle("updated title")) {
		t.Errorf("output missing updated title:\n%s", terminal.Output())
	}
	before := terminal.WriteCount()
	runner.Notify("late", sdk.NotifyInfo)
	runner.SetStatus("k", "late")
	runner.RequestRender(true)
	if got := terminal.WriteCount(); got != before {
		t.Fatalf("writes after Stop = %d, want %d", got, before)
	}
}

func TestRunnerAccessors(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if runner.Terminal() != tui.Terminal(terminal) {
		t.Fatal("Terminal() should return the injected terminal")
	}
	var submitted []string
	runner.SetOnSubmit(func(text string) { submitted = append(submitted, text) })
	interrupts := 0
	runner.SetOnInterrupt(func() { interrupts++ })
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	terminal.SendInput("\r")
	runner.view.Invalidate()
	if interrupts != 0 {
		t.Fatal("no interrupt expected")
	}
	_ = submitted
}

func TestRunnerSetWorkingInterruptCallback(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	interrupts := 0
	runner.SetOnInterrupt(func() { interrupts++ })
	runner.SetWorking(true)
	runner.Interrupt()
	runner.SetWorking(false)
	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
}

func TestRunnerDefaultsToProcessStdio(t *testing.T) {
	var gotStdin io.Reader
	var gotStdout io.Writer
	terminal := newFakeUITerminal(80, 24)
	runner := NewRunner(RunnerOptions{
		Home: t.TempDir(),
		NewTerminal: func(stdin io.Reader, stdout io.Writer) tui.Terminal {
			gotStdin, gotStdout = stdin, stdout
			return terminal
		},
	})
	if gotStdin != os.Stdin {
		t.Fatal("nil stdin should default to os.Stdin")
	}
	if gotStdout != os.Stdout {
		t.Fatal("nil stdout should default to os.Stdout")
	}
	runner.Stop()
	_ = runner
}

func TestRunnerDefaultTerminalFactory(t *testing.T) {
	runner := NewRunner(RunnerOptions{Home: t.TempDir()})
	if _, ok := runner.Terminal().(*tui.ProcessTerminal); !ok {
		t.Fatalf("default terminal = %T, want *tui.ProcessTerminal", runner.Terminal())
	}
	runner.Stop()
}

func TestRunnerRegularRootClampsHeight(t *testing.T) {
	terminal := newFakeUITerminal(80, 0)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	runner.Surface().AddUserMessage("zero height marker")
	runner.view.RenderNow(true)
}

func TestRunnerToolsExpandKey(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	block := runner.Surface().AddToolExecution("probe-tool", json.RawMessage(`{}`))
	block.SetResult("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12", interactive.ToolSuccess)
	before := renderedOutput(runner, terminal)
	if !strings.Contains(before, "to expand") {
		t.Fatalf("collapsed tool should hint at expansion:\n%s", before)
	}
	terminal.SendInput("\x0f")
	after := renderedOutput(runner, terminal)
	if !strings.Contains(after, "to collapse") {
		t.Fatalf("expand key should expand the tool block:\n%s", after)
	}
}
