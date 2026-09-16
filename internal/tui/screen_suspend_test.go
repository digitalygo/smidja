package tui

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type renderCounter struct {
	mu    sync.Mutex
	calls int
}

func (c *renderCounter) bump() {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
}

func (c *renderCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func waitForRenderCount(t *testing.T, counter *renderCounter, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if counter.count() >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d renders, got %d", want, counter.count())
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForTerminalWrites(t *testing.T, terminal *fakeTerminal, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if terminal.WriteCount() >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d terminal writes, got %d", want, terminal.WriteCount())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBaseSuspendScreenQuiescesRendersAndAltProtocols(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	screen := NewAltScreen(terminal, false, AltScreenOptions{})
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	screen.RequestRender(false)
	waitForTerminalWrites(t, terminal, 2)
	time.Sleep(100 * time.Millisecond)
	terminal.ResetWrites()

	screen.SuspendScreen()
	screen.SuspendScreen()
	suspendOutput := terminal.Output()
	if !strings.Contains(suspendOutput, AltScreenExit) {
		t.Fatalf("suspend output missing alt-screen exit:\n%s", suspendOutput)
	}
	if !strings.Contains(suspendOutput, MouseDisable()) {
		t.Fatalf("suspend output missing mouse disable:\n%s", suspendOutput)
	}
	if !strings.Contains(suspendOutput, CursorShow) {
		t.Fatalf("suspend output missing cursor show:\n%s", suspendOutput)
	}
	if got := strings.Count(suspendOutput, AltScreenExit); got != 1 {
		t.Fatalf("alt-screen exit writes = %d, want 1 for repeated suspend", got)
	}
	terminal.ResetWrites()
	screen.RenderNow(true)
	screen.RequestRender(false)
	screen.RequestRender(true)
	if got := terminal.WriteCount(); got != 0 {
		t.Fatalf("writes while suspended = %d, want 0", got)
	}

	screen.ResumeScreen()
	waitForTerminalWrites(t, terminal, 1)
	resumeOutput := terminal.Output()
	if !strings.Contains(resumeOutput, AltScreenEnter) {
		t.Fatalf("resume output missing alt-screen enter:\n%s", resumeOutput)
	}
	screen.ResumeScreen()
	if got := strings.Count(terminal.Output(), AltScreenEnter); got != 1 {
		t.Fatalf("alt-screen enter writes after resume = %d, want 1", got)
	}
}

func TestBaseSuspendScreenRegularModeHasNoProtocolWrites(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	screen := NewMainScreen(terminal, false)
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	screen.RequestRender(false)
	waitForTerminalWrites(t, terminal, 1)
	time.Sleep(100 * time.Millisecond)
	terminal.ResetWrites()

	screen.SuspendScreen()
	if got := terminal.Output(); got != "" {
		t.Fatalf("regular mode suspend wrote protocols: %q", got)
	}
	screen.RenderNow(true)
	if got := terminal.WriteCount(); got != 0 {
		t.Fatalf("writes while suspended = %d, want 0", got)
	}
	screen.ResumeScreen()
	waitForTerminalWrites(t, terminal, 1)
}

func TestSuspendScreenIgnoresStoppedScreen(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	screen := NewMainScreen(terminal, false)
	screen.Stop(StopOptions{})
	screen.SuspendScreen()
	screen.ResumeScreen()
	if got := terminal.Output(); strings.Contains(got, AltScreenEnter) {
		t.Fatalf("stopped screen wrote protocol sequences: %q", got)
	}
}

func TestSuspendScreenInactiveAltScreenWritesNothing(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	screen := NewAltScreen(terminal, false, AltScreenOptions{})
	screen.SuspendScreen()
	screen.ResumeScreen()
	if got := terminal.Output(); got != "" {
		t.Fatalf("inactive alt screen wrote protocol sequences: %q", got)
	}
}

func waitForSuspended(t *testing.T, screen *Base) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if screen.IsSuspended() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the screen to suspend")
		}
		time.Sleep(time.Millisecond)
	}
}

func assertNoWrites(t *testing.T, terminal *fakeTerminal, context string) {
	t.Helper()
	if got := terminal.WriteCount(); got != 0 {
		t.Fatalf("%s wrote %d times, want 0:\n%s", context, got, terminal.Output())
	}
}

func TestBaseRenderNowRechecksSuspensionAfterBarrier(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	screen := NewMainScreen(terminal, false)
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	waitForTerminalWrites(t, terminal, 1)
	time.Sleep(60 * time.Millisecond)
	terminal.ResetWrites()

	screen.renderMu.Lock()
	rendered := make(chan struct{})
	go func() {
		screen.RenderNow(true)
		close(rendered)
	}()
	time.Sleep(30 * time.Millisecond)
	suspendDone := make(chan struct{})
	go func() {
		screen.SuspendScreen()
		close(suspendDone)
	}()
	waitForSuspended(t, screen.Base)
	screen.renderMu.Unlock()
	select {
	case <-suspendDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SuspendScreen did not complete")
	}
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatched render did not complete")
	}
	assertNoWrites(t, terminal, "render crossing the suspend barrier")

	screen.RenderNow(true)
	screen.RequestRender(false)
	screen.RequestRender(true)
	time.Sleep(50 * time.Millisecond)
	assertNoWrites(t, terminal, "suspended screen")

	screen.ResumeScreen()
	waitForTerminalWrites(t, terminal, 1)
}

func TestBaseScheduledRenderRechecksSuspensionAfterBarrier(t *testing.T) {
	terminal := newFakeTerminal(80, 24)
	screen := NewMainScreen(terminal, false)
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	waitForTerminalWrites(t, terminal, 1)
	time.Sleep(60 * time.Millisecond)
	terminal.ResetWrites()

	screen.renderMu.Lock()
	screen.RequestRender(false)
	time.Sleep(30 * time.Millisecond)
	suspendDone := make(chan struct{})
	go func() {
		screen.SuspendScreen()
		close(suspendDone)
	}()
	waitForSuspended(t, screen.Base)
	screen.renderMu.Unlock()
	select {
	case <-suspendDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SuspendScreen did not complete")
	}
	time.Sleep(50 * time.Millisecond)
	assertNoWrites(t, terminal, "scheduled render crossing the suspend barrier")

	screen.ResumeScreen()
	waitForTerminalWrites(t, terminal, 1)
}

func TestStdinFileDescriptorRejectsUnusableFiles(t *testing.T) {
	if fd, gated := stdinFileDescriptor(nil); gated {
		t.Fatalf("stdinFileDescriptor(nil) = %d gated, want ungated", fd)
	}
	file, err := os.CreateTemp(t.TempDir(), "descriptor")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, gated := stdinFileDescriptor(file); gated {
		t.Fatalf("stdinFileDescriptor(closed file) should be ungated")
	}
}
