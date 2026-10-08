package tui

import (
	"sync"
	"testing"
	"time"
)

type steppedRenderGate struct {
	mu      sync.Mutex
	entered chan struct{}
	block   chan struct{}
	done    bool
}

func newSteppedRenderGate() *steppedRenderGate {
	return &steppedRenderGate{entered: make(chan struct{}, 8), block: make(chan struct{})}
}

func (g *steppedRenderGate) pause() {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	g.mu.Lock()
	block := g.block
	g.mu.Unlock()
	<-block
}

func (g *steppedRenderGate) release() {
	g.mu.Lock()
	close(g.block)
	g.block = make(chan struct{})
	g.mu.Unlock()
}

func (g *steppedRenderGate) install(screen *AltScreen) {
	callback := g.pause
	screen.layoutPublishGate.Store(&callback)
}

func (g *steppedRenderGate) uninstall(screen *AltScreen) {
	screen.layoutPublishGate.Store(nil)
	g.mu.Lock()
	if !g.done {
		g.done = true
		close(g.block)
	}
	g.mu.Unlock()
}

func (g *steppedRenderGate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("render did not reach layout publish gate")
	}
}

func TestAltScreenPromptNavigationSurvivesGatedPublication(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 10)
	content := &plainComponent{lines: gatedPromptLines(100, 30)}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	view := screen.implicitScrollView
	if top := view.ScrollTop(); top != 90 {
		t.Fatalf("initial scroll top = %d, want 90", top)
	}

	gate := newSteppedRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)

	rendered := make(chan struct{})
	go func() {
		screen.RenderNow(false)
		close(rendered)
	}()
	gate.waitEntered(t)

	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 60 {
		t.Fatalf("previous prompt during render = %d, want 60", top)
	}
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("repeated previous prompt during render = %d, want 30", top)
	}

	gate.release()
	<-rendered

	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("previous prompt after publication = %d, want 0", top)
	}
}

func TestAltScreenPageNavigationDuringGatedGrowthKeepsFrameCursor(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 10)
	content := &plainComponent{lines: gatedPromptLines(50, 30)}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	view := screen.implicitScrollView
	if top := view.ScrollTop(); top != 40 {
		t.Fatalf("initial scroll top = %d, want 40", top)
	}

	gate := newSteppedRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)

	content.lines = gatedPromptLines(200, 30)
	rendered := make(chan struct{})
	go func() {
		screen.RenderNow(false)
		close(rendered)
	}()
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 190 {
		t.Fatalf("gated scroll top = %d, want 190", top)
	}

	screen.handleViewportInput("\x1b[6~")
	gate.release()
	<-rendered

	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 60 {
		t.Fatalf("next prompt after gated page = %d, want 60", top)
	}
}

func TestAltScreenWheelNavigationDuringGatedViewportGrowthKeepsFrameCursor(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 20)
	content := &plainComponent{lines: gatedPromptLines(50, 30)}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	view := screen.implicitScrollView
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("initial scroll top = %d, want 30", top)
	}

	gate := newSteppedRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)

	terminal.SetSize(20, 10)
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 40 {
		t.Fatalf("gated scroll top = %d, want 40", top)
	}

	screen.handleViewportInput("\x1b[<64;5;2M")
	gate.release()
	waitForLayoutHeight(t, screen, 10)

	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("next prompt after gated wheel = %d, want 30", top)
	}
}
