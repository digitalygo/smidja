package ui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func seedFullscreenRethemeContent(surface *interactive.Surface) {
	surface.AddNotice(interactive.NoticeInfo, "notice retheme marker")
	assistant := surface.StartAssistantTurn()
	assistant.AppendText(strings.Repeat("assistant retheme marker\n", 80))
	surface.AddToolExecution("read", nil)
	surface.EndAssistantTurn("stop", "")
}

func fullscreenRenderOutput(runner *Runner, terminal *fakeUITerminal) string {
	runner.view.RenderNow(true)
	mark := terminal.WriteCount()
	runner.view.RenderNow(true)
	return terminal.OutputSince(mark)
}

func TestFullscreenConcurrentRenderAndSurfaceSetTheme(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	surface := runner.Surface()
	seedFullscreenRethemeContent(surface)

	transcript := surface.Transcript()
	transcript.SetScrollbar(tui.ScrollbarAlways)
	runner.view.RenderNow(true)

	terminal.SendInput("\x1b[5~")
	runner.view.RenderNow(true)
	if !transcript.IsScrollbarVisible() {
		t.Fatal("transcript scrollbar should be visible with overflowing content")
	}
	scrolled := transcript.ScrollTop()
	if scrolled <= 0 {
		t.Fatalf("page up did not scroll transcript: %d", scrolled)
	}
	terminal.SendInput("\x1b[F")
	runner.view.RenderNow(true)
	if bottom := transcript.ScrollTop(); bottom <= scrolled {
		t.Fatalf("scroll to bottom did not advance transcript: %d <= %d", bottom, scrolled)
	}

	confirmed := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("Retheme", "active dialog body")
		confirmed <- ok
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)

	darkTheme := surface.Theme()
	lightTheme, err := runner.ThemeRegistry().SetTheme("light")
	if err != nil {
		t.Fatalf("load light theme: %v", err)
	}
	darkTrack := requireThemeAnsi(t, darkTheme, "scrollbarTrack")
	lightTrack := requireThemeAnsi(t, lightTheme, "scrollbarTrack")
	if darkTrack == lightTrack {
		t.Fatal("test requires distinct dark and light scrollbar track ANSI")
	}

	before := fullscreenRenderOutput(runner, terminal)
	if !strings.Contains(before, darkTrack) {
		t.Fatalf("fullscreen frame missing dark scrollbar track before switch:\n%s", before)
	}
	if !strings.Contains(tui.StripTerminalSequences(before), "active dialog body") {
		t.Fatalf("fullscreen frame missing active dialog before switch:\n%s", before)
	}

	const iterations = 150
	var wait sync.WaitGroup
	start := make(chan struct{})
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < iterations; i++ {
			runner.view.RenderNow(true)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				surface.SetTheme(darkTheme)
			} else {
				surface.SetTheme(lightTheme)
			}
		}
	}()
	close(start)
	wait.Wait()

	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme light: %v", err)
	}
	frame := surface.RenderFrame(80, 24)
	if frame.PrimaryScrollView != transcript {
		t.Fatal("layout frame lost the primary scroll view after retheme")
	}
	document := tui.StripTerminalSequences(strings.Join(surface.RenderDocument(80), "\n"))
	for _, marker := range []string{"notice retheme marker", "assistant retheme marker", "read"} {
		if !strings.Contains(document, marker) {
			t.Fatalf("document missing %q after retheme:\n%s", marker, document)
		}
	}
	after := fullscreenRenderOutput(runner, terminal)
	if !strings.Contains(after, lightTrack) {
		t.Fatalf("fullscreen frame missing light scrollbar track after switch:\n%s", after)
	}
	if strings.Contains(after, darkTrack) {
		t.Fatalf("fullscreen frame kept dark scrollbar track after switch:\n%s", after)
	}
	if !strings.Contains(tui.StripTerminalSequences(after), "active dialog body") {
		t.Fatalf("fullscreen frame missing active dialog after switch:\n%s", after)
	}

	terminal.SendInput("\r")
	select {
	case <-confirmed:
	case <-time.After(2 * time.Second):
		t.Fatal("active dialog did not resolve after concurrent retheme")
	}
}
