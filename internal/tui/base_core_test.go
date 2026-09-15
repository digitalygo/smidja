package tui

import (
	"testing"
	"time"
)

type countingInvalidatingComponent struct {
	staticComponent
	invalidations int
}

func (c *countingInvalidatingComponent) Invalidate() { c.invalidations++ }

func TestBaseCoreAccessors(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	if base.Mode() != "regular" {
		t.Fatalf("mode = %q", base.Mode())
	}
	if base.ClearOnShrink() {
		t.Fatal("clear on shrink should default to false")
	}
	base.SetClearOnShrink(true)
	if !base.ClearOnShrink() {
		t.Fatal("clear on shrink not applied")
	}

	base.SetShowHardwareCursor(false)
	if base.ShowHardwareCursor() {
		t.Fatal("hardware cursor should start hidden")
	}
	base.SetShowHardwareCursor(true)
	if !base.ShowHardwareCursor() {
		t.Fatal("hardware cursor not enabled")
	}
	base.SetShowHardwareCursor(true)
	base.SetShowHardwareCursor(false)
	if base.ShowHardwareCursor() {
		t.Fatal("hardware cursor not disabled")
	}
}

func TestBaseMountedRootsAndInvalidate(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	child := newRecordingComponent("child")
	base.AddChild(child)
	if len(base.MountedRoots()) != 1 || base.MountedRoots()[0] != Component(child) {
		t.Fatalf("mounted roots = %v", base.MountedRoots())
	}

	root := &countingInvalidatingComponent{staticComponent: staticComponent{lines: []string{"root"}}}
	overlay := &countingInvalidatingComponent{staticComponent: staticComponent{lines: []string{"overlay"}}}
	base.SetHooks(tuiHooks{mountedRoots: func() []Component { return []Component{root} }})
	base.ShowOverlay(overlay, OverlayOptions{})
	base.Invalidate()
	if root.invalidations != 1 {
		t.Fatalf("hook root invalidations = %d, want 1", root.invalidations)
	}
	if overlay.invalidations != 1 {
		t.Fatalf("overlay invalidations = %d, want 1", overlay.invalidations)
	}
}

func TestBaseRemoveChildAndClear(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	child := newRecordingComponent("child")
	base.AddChild(child)
	base.RemoveChild(child)
	if len(base.Children()) != 0 {
		t.Fatalf("children after remove = %d", len(base.Children()))
	}
	base.AddChild(child)
	base.Clear()
	if len(base.Children()) != 0 {
		t.Fatalf("children after clear = %d", len(base.Children()))
	}
}

func TestBaseRequestRenderForce(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	rendered := make(chan struct{}, 4)
	resets := 0
	base.SetHooks(tuiHooks{
		resetRenderState: func() { resets++ },
		doRender:         func() { rendered <- struct{}{} },
	})
	base.RequestRender(true)
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("forced render did not run")
	}
	if resets != 1 {
		t.Fatalf("reset calls = %d, want 1", resets)
	}
}

func TestBaseRequestImmediateCoalesces(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	rendered := make(chan struct{}, 4)
	base.SetHooks(tuiHooks{doRender: func() { rendered <- struct{}{} }})
	base.RequestRender(true)
	base.RequestRender(true)
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("immediate render did not run")
	}
}
