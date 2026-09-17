package tui

import "testing"

func TestModalCaptureDiscardsAndRoutesInput(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	editor := newRecordingComponent("editor")
	base.AddChild(editor)
	base.SetFocus(editor)

	base.SetModalCapture(true)
	base.handleTerminalInput("secret")
	if len(editor.inputs) != 0 {
		t.Fatalf("modal capture without an overlay delivered input to the editor: %q", editor.inputs)
	}

	overlay := newRecordingComponent("overlay")
	handle := base.ShowOverlay(overlay, OverlayOptions{})
	base.handleTerminalInput("y")
	if len(overlay.inputs) != 1 || overlay.inputs[0] != "y" {
		t.Fatalf("modal capture did not route input to the top overlay: %q", overlay.inputs)
	}
	if len(editor.inputs) != 0 {
		t.Fatalf("modal capture leaked input to the editor: %q", editor.inputs)
	}

	handle.Hide()
	base.handleTerminalInput("again")
	if len(editor.inputs) != 0 {
		t.Fatalf("input after overlay hide reached the editor during capture: %q", editor.inputs)
	}

	base.SetModalCapture(false)
	base.handleTerminalInput("z")
	if len(editor.inputs) != 1 || editor.inputs[0] != "z" {
		t.Fatalf("released capture did not resume editor input: %q", editor.inputs)
	}

	base.SetModalCapture(true)
	optIn := newRecordingComponent("optin")
	optIn.releaseOptIn = true
	optInHandle := base.ShowOverlay(optIn, OverlayOptions{})
	release := "\x1b[97;1:3u"
	if !IsKeyRelease(release) {
		t.Fatalf("test sequence %q is not a key release", release)
	}
	base.handleTerminalInput(release)
	if len(optIn.inputs) != 1 || optIn.inputs[0] != release {
		t.Fatalf("opt-in overlay did not receive the key release: %q", optIn.inputs)
	}
	optInHandle.Hide()

	plain := newRecordingComponent("plain")
	plainHandle := base.ShowOverlay(plain, OverlayOptions{})
	base.handleTerminalInput(release)
	if len(plain.inputs) != 0 {
		t.Fatalf("non-opt-in overlay received a key release: %q", plain.inputs)
	}
	plainHandle.Hide()
}
