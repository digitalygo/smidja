package ui

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

const testSearchKey = "\x1b[102;6u"

func openTestSearch(t *testing.T, runner *Runner, terminal *fakeUITerminal) *tui.AltScreen {
	t.Helper()
	alt, ok := runner.view.(*tui.AltScreen)
	if !ok {
		t.Fatal("fullscreen runner should use the alt screen")
	}
	terminal.SendInput(testSearchKey)
	if !alt.SearchActive() {
		t.Fatal("search did not open")
	}
	if !alt.ModalCapture() {
		t.Fatal("search capture missing")
	}
	return alt
}

func TestRunnerRequestExitClosesSearch(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	alt := openTestSearch(t, runner, terminal)
	runner.RequestExit()
	if alt.SearchActive() || alt.ModalCapture() {
		t.Fatal("RequestExit must close search and release capture")
	}
}

func TestRunnerStopClosesSearch(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	alt := openTestSearch(t, runner, terminal)
	runner.Stop()
	if alt.SearchActive() || alt.ModalCapture() {
		t.Fatal("Stop must close search and release capture")
	}
}

func TestRunnerSessionReplacementClosesSearch(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	alt := openTestSearch(t, runner, terminal)
	runner.Surface().ReplaceTranscript(nil)
	if alt.SearchActive() {
		t.Fatal("session replacement must close search")
	}
	if alt.ModalCapture() {
		t.Fatal("session replacement must release capture")
	}
}

func TestRunnerSearchCaptureWithDialogSource(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	alt := openTestSearch(t, runner, terminal)
	runner.view.SetModalCapture(true)
	runner.view.SetModalCapture(false)
	if !alt.SearchActive() {
		t.Fatal("dialog cycle must not close search")
	}
	if !alt.ModalCapture() {
		t.Fatal("dialog cycle must not release search capture")
	}
	runner.RequestExit()
	if alt.ModalCapture() {
		t.Fatal("RequestExit must release the remaining search source")
	}
}

func TestRunnerSearchKeysStayOutOfEditor(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	alt := openTestSearch(t, runner, terminal)
	terminal.SendInput("zzz")
	if alt.Search().Query() != "zzz" {
		t.Fatalf("search did not receive keys: %q", alt.Search().Query())
	}
	if strings.Contains(runner.Surface().Editor().Text(), "zzz") {
		t.Fatal("search keys reached the chat editor")
	}
	terminal.SendInput("\x07")
	if runner.editorActive.Load() {
		t.Fatal("external editor opened from a search key")
	}
	if alt.Search().Query() != "zzz" {
		t.Fatalf("ctrl+g changed the query: %q", alt.Search().Query())
	}
}
