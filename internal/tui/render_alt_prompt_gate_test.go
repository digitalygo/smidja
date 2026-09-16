package tui

import (
	"sync"
	"testing"
	"time"
)

type renderGate struct {
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	openOnce  sync.Once
}

func newRenderGate() *renderGate {
	return &renderGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *renderGate) pause() {
	g.enterOnce.Do(func() { close(g.entered) })
	<-g.release
}

func (g *renderGate) install(screen *AltScreen) {
	callback := g.pause
	screen.layoutPublishGate.Store(&callback)
}

func (g *renderGate) uninstall(screen *AltScreen) {
	screen.layoutPublishGate.Store(nil)
	g.open()
}

func (g *renderGate) open() {
	g.openOnce.Do(func() { close(g.release) })
}

func (g *renderGate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("render did not reach layout publish gate")
	}
}

func gatedPromptLines(count, step int) []string {
	result := make([]string, count)
	for i := range result {
		if i%step == 0 {
			result[i] = OSC133PromptStart + "prompt " + itoa(i)
			continue
		}
		result[i] = "line " + itoa(i)
	}
	return result
}

func waitForLayoutHeight(t *testing.T, screen *AltScreen, height int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		screen.frameMu.RLock()
		layout := screen.currentLayout
		screen.frameMu.RUnlock()
		if layout != nil && layout.Height == height {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("layout height did not become %d", height)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAltScreenPromptNavigationDuringGatedGrowth(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 8)
	content := &plainComponent{lines: gatedPromptLines(30, 28)}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	view := screen.implicitScrollView
	if top := view.ScrollTop(); top != 22 {
		t.Fatalf("initial scroll top = %d, want 22", top)
	}

	gate := newRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)

	content.lines = gatedPromptLines(200, 28)
	rendered := make(chan struct{})
	go func() {
		screen.RenderNow(false)
		close(rendered)
	}()
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 192 {
		t.Fatalf("gated scroll top = %d, want 192", top)
	}

	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("previous prompt during growth = %d, want 0", top)
	}

	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("previous prompt without an earlier prompt = %d, want 0", top)
	}

	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 28 {
		t.Fatalf("next prompt during growth = %d, want 28", top)
	}

	gate.open()
	<-rendered
}

func TestAltScreenPromptNavigationDuringGatedResize(t *testing.T) {
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

	gate := newRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)

	terminal.SetSize(20, 10)
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 40 {
		t.Fatalf("gated scroll top = %d, want 40", top)
	}

	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("previous prompt during resize = %d, want 0", top)
	}

	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("next prompt during resize = %d, want 30", top)
	}

	gate.open()
	waitForLayoutHeight(t, screen, 10)
}

func TestAltScreenPromptNavigationWithoutPrimaryScrollView(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	screen.SetLayoutRoot(&plainComponent{lines: []string{"a", "b"}})
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	screen.handleViewportInput("\x1b[1;5A")
	screen.handleViewportInput("\x1b[1;5B")
}

func TestCapturedMaxScrollTop(t *testing.T) {
	child := &LayoutBox{Rect: LayoutRect{Height: 4}}
	box := &LayoutBox{Rect: LayoutRect{Height: 10}, scrollContentLines: []string{"a", "b", "c", "d"}, Children: []*LayoutBox{child}}
	if got := capturedMaxScrollTop(box); got != 0 {
		t.Fatalf("fitted content max = %d, want 0", got)
	}
	child.Rect.Height = 30
	if got := capturedMaxScrollTop(box); got != 20 {
		t.Fatalf("overflowing content max = %d, want 20", got)
	}
	childless := &LayoutBox{scrollContentLines: make([]string, 5)}
	if got := capturedMaxScrollTop(childless); got != 4 {
		t.Fatalf("childless max = %d, want 4", got)
	}
}

func TestAltScreenPromptNavigationDuringGatedViewportGrowth(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 20, 10)
	content := &plainComponent{lines: gatedPromptLines(50, 30)}
	screen.AddChild(content)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})
	view := screen.implicitScrollView
	if top := view.ScrollTop(); top != 40 {
		t.Fatalf("initial scroll top = %d, want 40", top)
	}
	gate := newRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)
	terminal.SetSize(20, 20)
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("gated scroll top = %d, want 30", top)
	}
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("previous prompt during viewport growth = %d, want 30", top)
	}
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("repeated previous prompt during viewport growth = %d, want 0", top)
	}
	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("repeated next prompt during viewport growth = %d, want 30", top)
	}
	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("next prompt without more prompts = %d, want 30", top)
	}
	gate.open()
	waitForLayoutHeight(t, screen, 20)
}

func TestAltScreenPromptNavigationDuringGatedViewportShrink(t *testing.T) {
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
	gate := newRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)
	terminal.SetSize(20, 10)
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 40 {
		t.Fatalf("gated scroll top = %d, want 40", top)
	}
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("previous prompt during viewport shrink = %d, want 0", top)
	}
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("repeated previous prompt during viewport shrink = %d, want 0", top)
	}
	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("next prompt during viewport shrink = %d, want 30", top)
	}
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("previous prompt after next during viewport shrink = %d, want 0", top)
	}
	gate.open()
	waitForLayoutHeight(t, screen, 10)
}

func TestAltScreenPromptNavigationRepeatedGatedContentGrowth(t *testing.T) {
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
	gate := newRenderGate()
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
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("previous prompt during content growth = %d, want 30", top)
	}
	screen.handleViewportInput("\x1b[1;5A")
	if top := view.ScrollTop(); top != 0 {
		t.Fatalf("repeated previous prompt during content growth = %d, want 0", top)
	}
	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("repeated next prompt during content growth = %d, want 30", top)
	}
	screen.handleViewportInput("\x1b[1;5B")
	if top := view.ScrollTop(); top != 30 {
		t.Fatalf("next prompt beyond published frame = %d, want 30", top)
	}
	gate.open()
	<-rendered
}
