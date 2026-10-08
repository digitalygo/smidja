package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func strippedOutput(output string) string {
	return tui.StripTerminalSequences(output)
}

func TestRunnerRegularGrowingDocument(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	const count = 40
	for i := 0; i < count; i++ {
		runner.Surface().AddUserMessage(fmt.Sprintf("regular-growing-%02d", i))
	}
	output := strippedOutput(renderedOutput(runner, terminal))
	for i := 0; i < count; i++ {
		marker := fmt.Sprintf("regular-growing-%02d", i)
		if !strings.Contains(output, marker) {
			t.Fatalf("regular document missing %q:\n%s", marker, output)
		}
	}
	document := runner.Surface().RenderDocument(80)
	joined := strippedOutput(strings.Join(document, "\n"))
	if !strings.Contains(joined, "regular-growing-00") || !strings.Contains(joined, "regular-growing-39") {
		t.Fatalf("RenderDocument missing long transcript:\n%s", joined)
	}
	viewDocument := runner.view.Render(80)
	viewText := strippedOutput(strings.Join(viewDocument, "\n"))
	if !strings.Contains(viewText, "regular-growing-00") || !strings.Contains(viewText, "regular-growing-39") {
		t.Fatalf("MainScreen view missing growing document:\n%s", viewText)
	}
	if len(viewDocument) < count {
		t.Fatalf("view document lines = %d, want at least %d", len(viewDocument), count)
	}
}

func TestRunnerRegularDocumentDockOrder(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	surface := runner.Surface()
	surface.SetWorkspace("/home/tester/proj")
	surface.SetSessionName("main")
	surface.SetModel("claude-x")
	surface.AddUserMessage("transcript-anchor-regular")
	surface.Editor().SetText("pending-anchor-regular")
	surface.Editor().HandleInput("\x1b\r")
	surface.Editor().SetText("editor-anchor-regular")
	surface.SetWorking(true)
	surface.SetWorkingMessage("status-region-anchor-regular")
	surface.SetWidget("dock-widget", []string{"widget-anchor-regular"})
	surface.SetStatus("dock-status", "footer-status-anchor-regular")

	output := strippedOutput(renderedOutput(runner, terminal))
	markers := []string{
		"transcript-anchor-regular",
		"editor-anchor-regular",
		"pending-anchor-regular",
		"status-region-anchor-regular",
		"widget-anchor-regular",
		"footer-status-anchor-regular",
	}
	positions := make([]int, len(markers))
	for i, marker := range markers {
		index := strings.Index(output, marker)
		if index < 0 {
			t.Fatalf("regular frame missing %q:\n%s", marker, output)
		}
		positions[i] = index
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] <= positions[i-1] {
			t.Fatalf("regular dock order wrong: %q at %d follows %q at %d\n%s", markers[i], positions[i], markers[i-1], positions[i-1], output)
		}
	}
}

func TestRunnerRegularResizeUpdatesEditor(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	runner.Surface().AddUserMessage("resize-anchor-regular")
	lines := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("editor-line-%02d", i))
	}
	runner.Surface().Editor().SetText(strings.Join(lines, "\n"))
	before := len(runner.Surface().Editor().Render(80))
	terminal.SetSize(80, 40)
	after := len(runner.Surface().Editor().Render(80))
	if after <= before {
		t.Fatalf("editor lines before = %d, after = %d, want growth after resize to 40 rows", before, after)
	}
	if got := runner.Terminal().Rows(); got != 40 {
		t.Fatalf("terminal rows = %d, want 40", got)
	}
	output := strippedOutput(renderedOutput(runner, terminal))
	if !strings.Contains(output, "resize-anchor-regular") {
		t.Fatalf("rendering not preserved after resize:\n%s", output)
	}
	if !strings.Contains(output, "editor-line-19") {
		t.Fatalf("editor content missing after resize:\n%s", output)
	}
}

func TestRunnerFullscreenFinalDocument(t *testing.T) {
	terminal := newFakeUITerminal(80, 5)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = TUIModeFullscreen
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	const count = 20
	for i := 0; i < count; i++ {
		runner.Surface().AddUserMessage(fmt.Sprintf("fullscreen-final-%02d", i))
	}
	runner.Surface().SetWidget("final-widget", []string{"fullscreen-widget-anchor"})
	runner.Surface().SetStatus("final-status", "fullscreen-status-anchor")
	runner.view.RenderNow(true)
	runner.Stop()
	final := terminal.Output()
	enter := strings.Index(final, tui.AltScreenEnter)
	exit := strings.Index(final, tui.AltScreenExit)
	if enter < 0 {
		t.Fatalf("output missing alt-screen enter:\n%s", strippedOutput(final))
	}
	if exit < 0 {
		t.Fatalf("output missing alt-screen exit:\n%s", strippedOutput(final))
	}
	if enter > strings.Index(final, "fullscreen-final-00") {
		t.Fatalf("alt-screen enter must precede output:\n%s", strippedOutput(final))
	}
	afterExit := final[exit:]
	strippedAfter := strippedOutput(afterExit)
	for i := 0; i < count; i++ {
		marker := fmt.Sprintf("fullscreen-final-%02d", i)
		if !strings.Contains(strippedAfter, marker) {
			t.Fatalf("final document after exit missing %q:\n%s", marker, strippedAfter)
		}
	}
	for _, marker := range []string{"fullscreen-widget-anchor", "fullscreen-status-anchor"} {
		if !strings.Contains(strippedAfter, marker) {
			t.Fatalf("final document missing dock %q:\n%s", marker, strippedAfter)
		}
	}
	lastMarker := strings.LastIndex(final, "fullscreen-final-19")
	if lastMarker < exit {
		t.Fatalf("alt-screen exit must precede final document output:\n%s", strippedOutput(final))
	}
}
