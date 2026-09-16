package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

func startFlushRunner(t *testing.T, mode TUIMode) (*Runner, *fakeUITerminal) {
	t.Helper()
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = mode
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(runner.Stop)
	switch view := runner.view.(type) {
	case *tui.MainScreen:
		view.SetMinRenderInterval(time.Hour)
		view.RenderNow(true)
	case *tui.AltScreen:
		view.SetMinRenderInterval(time.Hour)
		view.RenderNow(true)
	}
	return runner, terminal
}

func assertFinalMarker(t *testing.T, output, marker string) {
	t.Helper()
	if !strings.Contains(output, marker) {
		t.Fatalf("stop did not flush the pending final document with %q:\n%s", marker, output)
	}
	cursorShow := strings.LastIndex(output, tui.CursorShow)
	if cursorShow < 0 {
		t.Fatalf("output missing the terminal restore sequence:\n%s", output)
	}
	if strings.Index(output, marker) > cursorShow {
		t.Fatalf("final marker must render before the terminal restore:\n%s", output)
	}
}

func TestRunnerStopFlushesPendingNotice(t *testing.T) {
	runner, terminal := startFlushRunner(t, TUIModeRegular)
	runner.Surface().AddNotice(interactive.NoticeWarning, "final flush marker")
	runner.Stop()
	assertFinalMarker(t, terminal.Output(), "final flush marker")
}

func TestRunnerStopFlushesPendingReplacement(t *testing.T) {
	runner, terminal := startFlushRunner(t, TUIModeRegular)
	surface := runner.Surface()
	surface.AddUserMessage("replacement prompt")
	surface.StartAssistantTurn()
	surface.AppendAssistantText("stale draft")
	parts := []interactive.AssistantMessagePart{{Text: "authoritative replacement"}}
	surface.ReconcileAssistantContent(parts)
	surface.EndAssistantTurn("stop", "")
	runner.Stop()
	assertFinalMarker(t, terminal.Output(), "authoritative replacement")
}

func TestRunnerStopFlushesPendingErrorNotice(t *testing.T) {
	runner, terminal := startFlushRunner(t, TUIModeRegular)
	runner.Surface().AddNotice(interactive.NoticeError, "final error marker")
	runner.Stop()
	assertFinalMarker(t, terminal.Output(), "final error marker")
}

func TestRunnerStopFlushesPendingCancelMarker(t *testing.T) {
	runner, terminal := startFlushRunner(t, TUIModeRegular)
	runner.Surface().AddNotice(interactive.NoticeWarning, "interrupted")
	runner.Stop()
	assertFinalMarker(t, terminal.Output(), "interrupted")
}

func TestRunnerNoWritesAfterStop(t *testing.T) {
	runner, terminal := startFlushRunner(t, TUIModeRegular)
	runner.Surface().AddNotice(interactive.NoticeWarning, "final flush marker")
	runner.Stop()
	frozen := terminal.Output()
	writes := terminal.WriteCount()
	runner.RequestRender(true)
	runner.RequestRender(false)
	runner.Notify("late notice", sdk.NotifyWarning)
	runner.FlushFinalDocument()
	runner.Surface().AddNotice(interactive.NoticeError, "must never render")
	time.Sleep(60 * time.Millisecond)
	if got := terminal.Output(); got != frozen {
		t.Fatalf("terminal received writes after stop:\n%s", got)
	}
	if got := terminal.WriteCount(); got != writes {
		t.Fatalf("writes after stop = %d, want %d", got, writes)
	}
}

func TestRunnerFlushFinalDocumentRendersSynchronously(t *testing.T) {
	runner, terminal := startFlushRunner(t, TUIModeRegular)
	runner.Surface().AddNotice(interactive.NoticeInfo, "synchronous flush marker")
	runner.FlushFinalDocument()
	if !strings.Contains(terminal.Output(), "synchronous flush marker") {
		t.Fatalf("FlushFinalDocument did not render the pending document:\n%s", terminal.Output())
	}
}

func TestRunnerFlushFinalDocumentSkipsFullscreen(t *testing.T) {
	runner, terminal := startFlushRunner(t, TUIModeFullscreen)
	runner.Surface().AddUserMessage("fullscreen flush marker")
	before := terminal.WriteCount()
	runner.FlushFinalDocument()
	if got := terminal.WriteCount(); got != before {
		t.Fatalf("fullscreen flush added %d writes, want 0", got-before)
	}
	runner.Stop()
	assertFinalMarker(t, terminal.Output(), "fullscreen flush marker")
}

func TestRunnerFlushFinalDocumentIgnoresUnstarted(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	runner.FlushFinalDocument()
	if got := terminal.WriteCount(); got != 0 {
		t.Fatalf("unstarted flush wrote %d times, want 0", got)
	}
}
