package ui

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

type kittyKeyCase struct {
	name    string
	press   string
	repeat  string
	release string

	mutate  func(*RunnerOptions)
	prepare func(t *testing.T, runner *Runner)

	afterPress   func(t *testing.T, runner *Runner, terminal *fakeUITerminal)
	afterRepeat  func(t *testing.T, runner *Runner, terminal *fakeUITerminal)
	afterRelease func(t *testing.T, runner *Runner, terminal *fakeUITerminal)
}

type kittyRunnerMode struct {
	name string
	mode TUIMode
}

func kittyRunnerModes() []kittyRunnerMode {
	return []kittyRunnerMode{
		{name: "regular", mode: TUIModeRegular},
		{name: "fullscreen", mode: TUIModeFullscreen},
	}
}

func TestRunnerKittyActionKeysRunOnPressAndRepeatButNotRelease(t *testing.T) {
	builders := []func() kittyKeyCase{
		toolsExpansionCase,
		thinkingToggleCase,
		interruptCase,
		externalEditorCase,
		editorTextInputCase,
	}
	for _, mode := range kittyRunnerModes() {
		t.Run(mode.name, func(t *testing.T) {
			for _, build := range builders {
				kc := build()
				t.Run(kc.name, func(t *testing.T) {
					runner, terminal := startTestRunner(t, mode.mode, kc.mutate)
					if kc.prepare != nil {
						kc.prepare(t, runner)
					}
					terminal.SendInput(kc.press)
					kc.afterPress(t, runner, terminal)
					terminal.SendInput(kc.repeat)
					kc.afterRepeat(t, runner, terminal)
					terminal.SendInput(kc.release)
					kc.afterRelease(t, runner, terminal)
				})
			}
		})
	}
}

func TestRunnerKittyQuitKeyExitsOnPressAndRepeatButNotRelease(t *testing.T) {
	events := []struct {
		name     string
		data     string
		wantExit bool
	}{
		{name: "press", data: "\x1b[100;5u", wantExit: true},
		{name: "repeat", data: "\x1b[100;5:2u", wantExit: true},
		{name: "release", data: "\x1b[100;5:3u", wantExit: false},
	}
	for _, mode := range kittyRunnerModes() {
		t.Run(mode.name, func(t *testing.T) {
			for _, event := range events {
				t.Run(event.name, func(t *testing.T) {
					runner, terminal := startTestRunner(t, mode.mode, nil)
					terminal.SendInput(event.data)
					if !event.wantExit {
						select {
						case <-runner.Done():
							t.Fatal("ctrl+d release must not request exit")
						case <-time.After(50 * time.Millisecond):
						}
						return
					}
					select {
					case <-runner.Done():
					case <-time.After(2 * time.Second):
						t.Fatalf("ctrl+d %s must request exit", event.name)
					}
				})
			}
		})
	}
}

func toolsExpansionCase() kittyKeyCase {
	var block *interactive.ToolExecution
	return kittyKeyCase{
		name:    "app.tools.expand",
		press:   "\x1b[111;5u",
		repeat:  "\x1b[111;5:2u",
		release: "\x1b[111;5:3u",
		prepare: func(t *testing.T, runner *Runner) {
			block = runner.Surface().AddToolExecution("read", nil)
		},
		afterPress: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if !block.IsExpanded() {
				t.Fatal("ctrl+o press must expand tool output exactly once")
			}
		},
		afterRepeat: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if block.IsExpanded() {
				t.Fatal("ctrl+o repeat must act like a press and collapse tool output again")
			}
		},
		afterRelease: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if block.IsExpanded() {
				t.Fatal("ctrl+o release must not toggle tool output")
			}
		},
	}
}

func thinkingToggleCase() kittyKeyCase {
	return kittyKeyCase{
		name:    "app.thinking.toggle",
		press:   "\x1b[116;5u",
		repeat:  "\x1b[116;5:2u",
		release: "\x1b[116;5:3u",
		prepare: func(t *testing.T, runner *Runner) {
			assistant := runner.Surface().StartAssistantTurn()
			assistant.AppendThinking("secret reasoning")
		},
		afterPress: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if !strings.Contains(renderedRunnerFrame(runner), "secret") {
				t.Fatal("ctrl+t press must expand thinking exactly once")
			}
		},
		afterRepeat: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if strings.Contains(renderedRunnerFrame(runner), "secret") {
				t.Fatal("ctrl+t repeat must act like a press and collapse thinking again")
			}
		},
		afterRelease: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if strings.Contains(renderedRunnerFrame(runner), "secret") {
				t.Fatal("ctrl+t release must not toggle thinking")
			}
		},
	}
}

func interruptCase() kittyKeyCase {
	interrupts := 0
	return kittyKeyCase{
		name:    "app.interrupt",
		press:   "\x1b[27;1u",
		repeat:  "\x1b[27;1:2u",
		release: "\x1b[27;1:3u",
		mutate: func(opts *RunnerOptions) {
			opts.OnInterrupt = func() { interrupts++ }
		},
		prepare: func(t *testing.T, runner *Runner) {
			runner.SetWorking(true)
		},
		afterPress: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if interrupts != 1 {
				t.Fatalf("interrupts = %d, want 1 after escape press", interrupts)
			}
		},
		afterRepeat: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if interrupts != 2 {
				t.Fatalf("interrupts = %d, want 2 after escape repeat", interrupts)
			}
		},
		afterRelease: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			if interrupts != 2 {
				t.Fatalf("interrupts = %d, want still 2 after escape release", interrupts)
			}
		},
	}
}

func externalEditorCase() kittyKeyCase {
	var runs atomic.Int32
	return kittyKeyCase{
		name:    "app.editor.external",
		press:   "\x1b[103;5u",
		repeat:  "\x1b[103;5:2u",
		release: "\x1b[103;5:3u",
		mutate: func(opts *RunnerOptions) {
			opts.ExternalRunner = tui.RunnerFunc(func(command, filePath string) error {
				runs.Add(1)
				return os.WriteFile(filePath, []byte("external edit"), 0o600)
			})
		},
		afterPress: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			waitForCondition(t, "the external editor to run once on press", func() bool {
				return runs.Load() == 1 && terminal.SuspendCalls() == 1 && terminal.ResumeCalls() == 1
			})
		},
		afterRepeat: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			waitForCondition(t, "the external editor to run again on repeat", func() bool {
				return runs.Load() == 2 && terminal.SuspendCalls() == 2 && terminal.ResumeCalls() == 2
			})
		},
		afterRelease: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			time.Sleep(50 * time.Millisecond)
			if runs.Load() != 2 || terminal.SuspendCalls() != 2 {
				t.Fatalf("editor runs = %d, suspend calls = %d, want the ctrl+g release to open nothing", runs.Load(), terminal.SuspendCalls())
			}
		},
	}
}

func editorTextInputCase() kittyKeyCase {
	return kittyKeyCase{
		name:    "editor.text",
		press:   "\x1b[97u",
		repeat:  "\x1b[97;1:2u",
		release: "\x1b[97;1:3u",
		afterPress: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			assertEditorText(t, runner, "a", "the 'a' press must type one character")
		},
		afterRepeat: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			assertEditorText(t, runner, "aa", "the 'a' repeat must act like a press and type again")
		},
		afterRelease: func(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
			assertEditorText(t, runner, "aa", "the 'a' release must not type a character")
		},
	}
}

func assertEditorText(t *testing.T, runner *Runner, want, description string) {
	t.Helper()
	if got := runner.Surface().Editor().Text(); got != want {
		t.Fatalf("editor text = %q, want %q: %s", got, want, description)
	}
}

func renderedRunnerFrame(runner *Runner) string {
	lines := runner.Surface().RenderFrame(80, 24).Lines
	stripped := make([]string, 0, len(lines))
	for _, line := range lines {
		stripped = append(stripped, tui.StripTerminalSequences(line))
	}
	return strings.Join(stripped, "\n")
}
