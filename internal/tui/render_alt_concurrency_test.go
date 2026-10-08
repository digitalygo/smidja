package tui

import (
	"sync"
	"testing"
)

func promptScrollLines(count int) []string {
	result := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if i%20 == 0 {
			result = append(result, OSC133PromptStart+"prompt "+itoa(i))
			continue
		}
		result = append(result, "line "+itoa(i))
	}
	return result
}

type capturingMouseComponent struct {
	staticComponent
}

func (m *capturingMouseComponent) Render(width int) []string { return []string{"[capture]"} }

func (m *capturingMouseComponent) HandleMouse(event MouseEvent) *MouseEventResult {
	if event.Type == MousePress {
		return &MouseEventResult{Handled: true, Capture: true}
	}
	return &MouseEventResult{Handled: true}
}

func runConcurrentPairs(iterations int, first func(int), second func(int)) {
	var wait sync.WaitGroup
	start := make(chan struct{})
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < iterations; i++ {
			first(i)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < iterations; i++ {
			second(i)
		}
	}()
	close(start)
	wait.Wait()
}

func TestAltScreenConcurrentRenderAndViewportInput(t *testing.T) {
	screen, _ := newTestAltScreen(t, 40, 8)
	screen.AddChild(&plainComponent{lines: promptScrollLines(240)})
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	inputs := []string{
		"\x1b[5~", "\x1b[6~", "\x1b[1;5A", "\x1b[1;5B",
		"\x1b[H", "\x1b[F", "\x1b[<65;5;2M", "\x1b[<64;5;2M",
	}
	runConcurrentPairs(300,
		func(int) { screen.RenderNow(false) },
		func(i int) { screen.handleViewportInput(inputs[i%len(inputs)]) },
	)

	if top := screen.implicitScrollView.ScrollTop(); top < 0 {
		t.Fatalf("scroll top = %d", top)
	}
}

func TestAltScreenConcurrentRenderAndMouseInput(t *testing.T) {
	screen, _ := newTestAltScreen(t, 40, 6)
	target := &capturingMouseComponent{}
	wrapper := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{{Component: target}}}}
	screen.SetLayoutRoot(wrapper)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	inputs := []string{
		"\x1b[<0;2;1M", "\x1b[<32;3;1M", "\x1b[<0;3;1m",
		FocusOut, FocusIn,
	}
	runConcurrentPairs(300,
		func(int) { screen.RenderNow(false) },
		func(i int) { screen.handleViewportInput(inputs[i%len(inputs)]) },
	)
}

func TestAltScreenReleaseWithoutPress(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	screen.AddChild(&plainComponent{lines: []string{"x"}})
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	if result := screen.handleViewportInput("\x1b[<0;3;1m"); !result.Consume {
		t.Fatal("stray release should be consumed")
	}
	if screen.hasMousePressTarget() {
		t.Fatal("stray release must not leave a press target")
	}
}

func TestAltScreenMountedRoots(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 4)
	implicit := &plainComponent{lines: []string{"implicit"}}
	screen.AddChild(implicit)
	if roots := screen.MountedRoots(); len(roots) != 1 || roots[0] != Component(implicit) {
		t.Fatalf("implicit mounted roots = %v", roots)
	}
	explicit := &plainComponent{lines: []string{"explicit"}}
	screen.SetLayoutRoot(explicit)
	screen.SetLayoutRoot(explicit)
	if roots := screen.MountedRoots(); len(roots) != 1 || roots[0] != Component(explicit) {
		t.Fatalf("explicit mounted roots = %v", roots)
	}
	screen.Invalidate()
}

func TestAltScreenViewportKeybindingBranches(t *testing.T) {
	screen, _ := newTestAltScreen(t, 20, 5)
	screen.AddChild(&plainComponent{lines: lines(40, 'c')})
	manager := NewDefaultKeybindingsManager(nil)
	manager.SetUserBindings(KeybindingsConfig{
		"tui.altScreen.halfPageUp":   {"shift+pageUp"},
		"tui.altScreen.halfPageDown": {"shift+pageDown"},
		"tui.altScreen.lineUp":       {"shift+home"},
		"tui.altScreen.lineDown":     {"shift+end"},
	})
	screen.SetKeybindings(manager)
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	for _, input := range []string{"\x1b[5$", "\x1b[6$", "\x1b[7$", "\x1b[8$"} {
		if result := screen.handleViewportInput(input); !result.Consume {
			t.Fatalf("input %q should be consumed", input)
		}
	}
	if result := screen.handleViewportInput("z"); result.Consume {
		t.Fatal("unbound key should not be consumed")
	}
}

func TestAltScreenConcurrentResetRenderAndViewportInput(t *testing.T) {
	screen, _ := newTestAltScreen(t, 40, 8)
	screen.AddChild(&plainComponent{lines: promptScrollLines(240)})
	screen.Start()
	waitForAltRender(t, screen)
	defer screen.Stop(StopOptions{})

	inputs := []string{
		"\x1b[5~", "\x1b[6~", "\x1b[1;5A", "\x1b[1;5B", "\x1b[H", "\x1b[F",
	}
	var wait sync.WaitGroup
	start := make(chan struct{})
	wait.Add(3)
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < 200; i++ {
			screen.RenderNow(false)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < 200; i++ {
			screen.RequestRender(true)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < 200; i++ {
			screen.beforeTerminalStart()
			screen.handleViewportInput(inputs[i%len(inputs)])
		}
	}()
	close(start)
	wait.Wait()
}
