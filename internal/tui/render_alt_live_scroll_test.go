package tui

import (
	"testing"
)

func TestAltScreenGatedGrowthPageUpUsesCapturedTarget(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 10)
	content := &plainComponent{lines: lines(50, 'c')}
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
	content.lines = lines(200, 'c')
	rendered := make(chan struct{})
	go func() {
		screen.RenderNow(false)
		close(rendered)
	}()
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 190 {
		t.Fatalf("gated scroll top = %d, want 190", top)
	}
	screen.handleViewportInput("\x1b[5~")
	if top := view.ScrollTop(); top != 34 {
		t.Fatalf("page up during growth = %d, want 34", top)
	}
	gate.open()
	<-rendered
}

func TestAltScreenGatedGrowthWheelUsesCapturedTarget(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 10)
	content := &plainComponent{lines: lines(50, 'c')}
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
	content.lines = lines(200, 'c')
	rendered := make(chan struct{})
	go func() {
		screen.RenderNow(false)
		close(rendered)
	}()
	gate.waitEntered(t)
	if top := view.ScrollTop(); top != 190 {
		t.Fatalf("gated scroll top = %d, want 190", top)
	}
	screen.handleViewportInput("\x1b[<64;5;2M")
	if top := view.ScrollTop(); top != 39 {
		t.Fatalf("wheel up during growth = %d, want 39", top)
	}
	gate.open()
	<-rendered
}

func TestAltScreenGatedGrowthWheelChainingUsesCapturedResidual(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 10)
	innerContent := &plainComponent{lines: lines(30, 'i')}
	inner := NewScrollView(innerContent, ScrollViewOptions{})
	innerHost := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: inner, Options: StackEntryOptions{Basis: intPtr(3), Shrink: ShrinkNone}},
	}}}
	trailer := &plainComponent{lines: lines(47, 't')}
	outerContent := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: innerHost},
		{Component: trailer},
	}}}
	outer := NewScrollView(outerContent, ScrollViewOptions{Follow: "end", Primary: true})
	screen.SetLayoutRoot(outer)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})
	if top := outer.ScrollTop(); top != 40 {
		t.Fatalf("initial outer top = %d, want 40", top)
	}
	if top := inner.ScrollTop(); top != 0 {
		t.Fatalf("initial inner top = %d, want 0", top)
	}
	gate := newRenderGate()
	gate.install(screen)
	defer gate.uninstall(screen)
	trailer.lines = lines(197, 't')
	rendered := make(chan struct{})
	go func() {
		screen.RenderNow(false)
		close(rendered)
	}()
	gate.waitEntered(t)
	if top := outer.ScrollTop(); top != 190 {
		t.Fatalf("gated outer top = %d, want 190", top)
	}
	screen.handleViewportInput("\x1b[<64;5;2M")
	if top := inner.ScrollTop(); top != 0 {
		t.Fatalf("inner top after chained wheel = %d, want 0", top)
	}
	if top := outer.ScrollTop(); top != 39 {
		t.Fatalf("outer top after chained wheel = %d, want 39", top)
	}
	gate.open()
	<-rendered
}

func TestAltScreenWheelFallbackToUnseenPrimary(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 10)
	innerContent := &plainComponent{lines: lines(30, 'i')}
	inner := NewScrollView(innerContent, ScrollViewOptions{Primary: true})
	innerHost := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: inner, Options: StackEntryOptions{Basis: intPtr(3), Shrink: ShrinkNone}},
	}}}
	trailer := &plainComponent{lines: lines(20, 't')}
	outerContent := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: innerHost},
		{Component: trailer},
	}}}
	outer := NewScrollView(outerContent, ScrollViewOptions{})
	screen.SetLayoutRoot(outer)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})
	if top := inner.ScrollTop(); top != 0 {
		t.Fatalf("initial inner top = %d, want 0", top)
	}
	if top := outer.ScrollTop(); top != 0 {
		t.Fatalf("initial outer top = %d, want 0", top)
	}
	screen.handleViewportInput("\x1b[<64;5;9M")
	if top := outer.ScrollTop(); top != 0 {
		t.Fatalf("outer top after fallback wheel = %d, want 0", top)
	}
	if top := inner.ScrollTop(); top != 0 {
		t.Fatalf("inner top after fallback wheel = %d, want 0", top)
	}
}

func TestAltScreenWheelWithoutBoundsUsesLiveTarget(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	screen.SetLayoutRoot(&plainComponent{lines: []string{"a", "b"}})
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})
	screen.handleViewportInput("\x1b[<64;5;2M")
	screen.handleViewportInput("\x1b[5~")
}
