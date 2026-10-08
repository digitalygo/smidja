package tui

import (
	"strings"
	"testing"
	"time"
)

func waitResumeChan(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not complete", what)
	}
}

func TestResumeBarsQueuedRenderUntilAltProtocolsRestored(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 80, 24)
	screen.AddChild(&plainComponent{lines: []string{"hello barrier"}})
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	waitForAltRender(t, screen)
	time.Sleep(50 * time.Millisecond)
	screen.SuspendScreen()
	if !screen.Base.IsSuspended() {
		t.Fatal("screen should be suspended")
	}
	time.Sleep(50 * time.Millisecond)
	terminal.ResetWrites()
	origResume := screen.Base.hooks.resumeProtocols
	entered := make(chan struct{})
	release := make(chan struct{})
	screen.Base.hooks.resumeProtocols = func() {
		close(entered)
		<-release
		if origResume != nil {
			origResume()
		}
	}
	beforeRedraws := screen.FullRedraws()
	resumeDone := make(chan struct{})
	go func() {
		screen.ResumeScreen()
		close(resumeDone)
	}()
	waitResumeChan(t, entered, "resume hook")
	renderDone := make(chan struct{})
	go func() {
		screen.RenderNow(true)
		close(renderDone)
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-resumeDone:
		t.Fatal("resume completed before release")
	default:
	}
	select {
	case <-renderDone:
		t.Fatal("queued render completed before release")
	default:
	}
	if got := terminal.WriteCount(); got != 0 {
		t.Fatalf("writes before protocol restore = %d, want 0:\n%s", got, terminal.Output())
	}
	if output := terminal.Output(); strings.Contains(output, AltScreenEnter) || strings.Contains(output, SyncOutputBegin) {
		t.Fatalf("frame before protocol marker: %q", output)
	}
	close(release)
	waitResumeChan(t, resumeDone, "resume")
	waitResumeChan(t, renderDone, "queued render")
	deadline := time.Now().Add(2 * time.Second)
	for {
		output := terminal.Output()
		if strings.Contains(output, AltScreenEnter) && strings.Contains(output, SyncOutputBegin) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for protocol and frame: %q", output)
		}
		time.Sleep(time.Millisecond)
	}
	output := terminal.Output()
	enterIdx := strings.Index(output, AltScreenEnter)
	syncIdx := strings.Index(output, SyncOutputBegin)
	if enterIdx < 0 {
		t.Fatalf("missing alt-screen enter: %q", output)
	}
	if syncIdx < 0 {
		t.Fatalf("missing synchronized frame: %q", output)
	}
	if enterIdx > syncIdx {
		t.Fatalf("frame before alt-screen enter:\n%s", output)
	}
	if !strings.Contains(output, "hello barrier") {
		t.Fatalf("first frame missing content: %q", output)
	}
	if !strings.Contains(output, CursorEraseScreen) {
		t.Fatalf("first frame should be full redraw: %q", output)
	}
	if screen.Base.IsSuspended() {
		t.Fatal("screen should be resumed")
	}
	if got := screen.FullRedraws(); got <= beforeRedraws {
		t.Fatalf("full redraws = %d, want > %d", got, beforeRedraws)
	}
}

func TestResumeStopDuringResumePreventsRender(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 80, 24)
	screen.AddChild(&plainComponent{lines: []string{"stop race content"}})
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	waitForAltRender(t, screen)
	time.Sleep(50 * time.Millisecond)
	screen.SuspendScreen()
	time.Sleep(50 * time.Millisecond)
	terminal.ResetWrites()
	for {
		select {
		case <-renderSignalsAlt[screen]:
		default:
			goto drained
		}
	}
drained:
	origResume := screen.Base.hooks.resumeProtocols
	entered := make(chan struct{})
	release := make(chan struct{})
	screen.Base.hooks.resumeProtocols = func() {
		close(entered)
		<-release
		if origResume != nil {
			origResume()
		}
	}
	resumeDone := make(chan struct{})
	go func() {
		screen.ResumeScreen()
		close(resumeDone)
	}()
	waitResumeChan(t, entered, "resume hook")
	renderDone := make(chan struct{})
	go func() {
		screen.RenderNow(true)
		close(renderDone)
	}()
	time.Sleep(30 * time.Millisecond)
	stopDone := make(chan struct{})
	go func() {
		screen.Stop(StopOptions{})
		close(stopDone)
	}()
	time.Sleep(50 * time.Millisecond)
	if !screen.Base.IsStopped() {
		t.Fatal("stop should mark stopped while resume holds barrier")
	}
	select {
	case <-stopDone:
		t.Fatal("stop completed before resume release")
	default:
	}
	select {
	case <-resumeDone:
		t.Fatal("resume completed before release")
	default:
	}
	close(release)
	waitResumeChan(t, resumeDone, "resume")
	waitResumeChan(t, stopDone, "stop")
	waitResumeChan(t, renderDone, "queued render")
	time.Sleep(100 * time.Millisecond)
	if !screen.Base.IsStopped() {
		t.Fatal("screen should stay stopped")
	}
	output := terminal.Output()
	if strings.Contains(output, CursorTo(0, 0)) {
		t.Fatalf("render frame must not follow stop:\n%s", output)
	}
	select {
	case <-renderSignalsAlt[screen]:
		t.Fatal("render must not run after stop")
	default:
	}
	afterStop := terminal.WriteCount()
	screen.RenderNow(true)
	screen.ResumeScreen()
	time.Sleep(50 * time.Millisecond)
	if got := terminal.WriteCount(); got != afterStop {
		t.Fatalf("writes after stop = %d, want %d:\n%s", got, afterStop, terminal.Output())
	}
}

func TestResumeMainScreenBarrierRestoresFullRender(t *testing.T) {
	screen, terminal := newTestScreen(t, 80, 24)
	screen.AddChild(&plainComponent{lines: []string{"main barrier content"}})
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	waitForRender(t, screen)
	time.Sleep(50 * time.Millisecond)
	screen.SuspendScreen()
	if !screen.Base.IsSuspended() {
		t.Fatal("main screen should suspend")
	}
	time.Sleep(50 * time.Millisecond)
	terminal.ResetWrites()
	entered := make(chan struct{})
	release := make(chan struct{})
	screen.Base.hooks.resumeProtocols = func() {
		close(entered)
		<-release
		terminal.Write("PROTOCOL-RESTORED")
	}
	beforeRedraws := screen.FullRedraws()
	resumeDone := make(chan struct{})
	go func() {
		screen.ResumeScreen()
		close(resumeDone)
	}()
	waitResumeChan(t, entered, "main resume hook")
	renderDone := make(chan struct{})
	go func() {
		screen.RenderNow(true)
		close(renderDone)
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-resumeDone:
		t.Fatal("main resume completed before release")
	default:
	}
	select {
	case <-renderDone:
		t.Fatal("main queued render completed before release")
	default:
	}
	if got := terminal.WriteCount(); got != 0 {
		t.Fatalf("main writes before restore = %d, want 0:\n%s", got, terminal.Output())
	}
	close(release)
	waitResumeChan(t, resumeDone, "main resume")
	waitResumeChan(t, renderDone, "main queued render")
	deadline := time.Now().Add(2 * time.Second)
	for {
		output := terminal.Output()
		if strings.Contains(output, "PROTOCOL-RESTORED") && strings.Contains(output, SyncOutputBegin) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for main marker and frame: %q", output)
		}
		time.Sleep(time.Millisecond)
	}
	output := terminal.Output()
	markerIdx := strings.Index(output, "PROTOCOL-RESTORED")
	syncIdx := strings.Index(output, SyncOutputBegin)
	if markerIdx < 0 || syncIdx < 0 || markerIdx > syncIdx {
		t.Fatalf("main marker must precede frame:\n%s", output)
	}
	if !strings.Contains(output, "main barrier content") {
		t.Fatalf("main first frame missing content: %q", output)
	}
	if strings.Contains(output, AltScreenEnter) {
		t.Fatalf("main resume must not enter alt screen: %q", output)
	}
	if screen.Base.IsSuspended() {
		t.Fatal("main screen should be resumed")
	}
	if got := screen.FullRedraws(); got <= beforeRedraws {
		t.Fatalf("main full redraws = %d, want > %d", got, beforeRedraws)
	}
}

func TestResumePreBarrierStopAbortsHook(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 80, 24)
	screen.AddChild(&plainComponent{lines: []string{"pre barrier"}})
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer screen.Stop(StopOptions{})
	waitForAltRender(t, screen)
	time.Sleep(50 * time.Millisecond)
	screen.SuspendScreen()
	time.Sleep(50 * time.Millisecond)
	terminal.ResetWrites()
	screen.renderMu.Lock()
	resumeDone := make(chan struct{})
	go func() {
		screen.ResumeScreen()
		close(resumeDone)
	}()
	time.Sleep(50 * time.Millisecond)
	stopDone := make(chan struct{})
	go func() {
		screen.Stop(StopOptions{})
		close(stopDone)
	}()
	time.Sleep(50 * time.Millisecond)
	if !screen.Base.IsStopped() {
		screen.renderMu.Unlock()
		t.Fatal("stop should mark stopped while barrier held")
	}
	screen.renderMu.Unlock()
	waitResumeChan(t, resumeDone, "aborted resume")
	waitResumeChan(t, stopDone, "stop")
	if output := terminal.Output(); strings.Contains(output, AltScreenEnter) {
		t.Fatalf("aborted resume must not re-enter alt screen: %q", output)
	}
	if output := terminal.Output(); strings.Contains(output, CursorTo(0, 0)) {
		t.Fatalf("aborted resume must not render:\n%s", output)
	}
	afterStop := terminal.WriteCount()
	screen.ResumeScreen()
	screen.RenderNow(true)
	time.Sleep(50 * time.Millisecond)
	if got := terminal.WriteCount(); got != afterStop {
		t.Fatalf("writes after aborted resume = %d, want %d", got, afterStop)
	}
}
