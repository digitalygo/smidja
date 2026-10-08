package tui

import "testing"

type modalProbe struct {
	inputs []string
	mouse  []MouseEvent
	result *MouseEventResult
}

func (p *modalProbe) Render(int) []string { return []string{"probe"} }

func (p *modalProbe) Invalidate() {}

func (p *modalProbe) HandleInput(data string) { p.inputs = append(p.inputs, data) }

func (p *modalProbe) HandleMouse(event MouseEvent) *MouseEventResult {
	p.mouse = append(p.mouse, event)
	return p.result
}

func TestModalCaptureRoutesProtocolEventsToOverlay(t *testing.T) {
	screen := NewAltScreen(newFakeTerminal(20, 5), false, AltScreenOptions{})
	probe := &modalProbe{}
	screen.ShowOverlay(probe, OverlayOptions{Width: "4", Anchor: AnchorTopLeft})
	screen.compositeOverlays([]string{"aaaa", "bbbb", "cccc", "dddd", "eeee"}, 20, 5)
	screen.SetModalCapture(true)

	screen.handleTerminalInput(FocusIn)
	screen.handleTerminalInput(FocusOut)
	if len(probe.inputs) != 0 {
		t.Fatalf("focus events reached the text component: %q", probe.inputs)
	}

	screen.handleTerminalInput("\x1b[<0;2;1M")
	if len(probe.inputs) != 0 {
		t.Fatalf("mouse press reached the text component: %q", probe.inputs)
	}
	if len(probe.mouse) != 1 || probe.mouse[0].Type != MousePress {
		t.Fatalf("overlay mouse events = %+v", probe.mouse)
	}

	screen.handleTerminalInput("\x1b[<64;2;1M")
	if len(probe.mouse) != 2 || probe.mouse[1].Type != MouseWheel {
		t.Fatalf("overlay wheel events = %+v", probe.mouse)
	}

	screen.handleTerminalInput("\x1b[<0;18;4M")
	if len(probe.mouse) != 2 {
		t.Fatalf("outside click reached the overlay: %+v", probe.mouse)
	}
	if len(probe.inputs) != 0 {
		t.Fatalf("outside click reached the text component: %q", probe.inputs)
	}

	screen.handleTerminalInput("x")
	if len(probe.inputs) != 1 || probe.inputs[0] != "x" {
		t.Fatalf("plain text routing = %q", probe.inputs)
	}
}

func TestWheelEventNonModalOverlayDispatch(t *testing.T) {
	screen := NewAltScreen(newFakeTerminal(20, 5), false, AltScreenOptions{})
	probe := &modalProbe{result: &MouseEventResult{Handled: true}}
	screen.ShowOverlay(probe, OverlayOptions{Width: "4", Anchor: AnchorTopLeft})
	screen.compositeOverlays([]string{"aaaa", "bbbb", "cccc", "dddd", "eeee"}, 20, 5)

	screen.handleWheelEvent(parsedWheelEvent{direction: 1, x: 1, y: 0, button: 64})
	if len(probe.mouse) != 1 || probe.mouse[0].Type != MouseWheel {
		t.Fatalf("overlay wheel events = %+v", probe.mouse)
	}
}

func TestWheelEventModalCaptureWithoutHandlerResult(t *testing.T) {
	screen := NewAltScreen(newFakeTerminal(20, 5), false, AltScreenOptions{})
	probe := &modalProbe{}
	screen.ShowOverlay(probe, OverlayOptions{Width: "4", Anchor: AnchorTopLeft})
	screen.compositeOverlays([]string{"aaaa", "bbbb", "cccc", "dddd", "eeee"}, 20, 5)
	screen.SetModalCapture(true)

	screen.handleWheelEvent(parsedWheelEvent{direction: 1, x: 1, y: 0, button: 64})
	if len(probe.mouse) != 1 || probe.mouse[0].Type != MouseWheel {
		t.Fatalf("overlay wheel events = %+v", probe.mouse)
	}
	if len(probe.inputs) != 0 {
		t.Fatalf("wheel reached the text component: %q", probe.inputs)
	}
}
