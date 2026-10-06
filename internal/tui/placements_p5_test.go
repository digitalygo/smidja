package tui

import (
	"strings"
	"testing"
)

func placementImage() *LoadedImage {
	return &LoadedImage{Format: ImageFormatPNG, Width: 4, Height: 4, Data: []byte("png")}
}

func TestPlacementsReuseIdenticalFrames(t *testing.T) {
	placements := NewImagePlacements(GraphicsKitty)
	request := PlacementRequest{CacheKey: "a.png", X: 0, Y: 0, Rows: 2, Columns: 10, Generation: 1, Image: placementImage()}
	first := placements.Reconcile([]PlacementRequest{request})
	if len(first.Renders) != 1 || !strings.Contains(first.Renders[0].Sequence, "\x1b_G") {
		t.Fatalf("first reconcile should transmit: %+v", first)
	}
	second := placements.Reconcile([]PlacementRequest{request})
	if len(second.Renders) != 0 || len(second.Deletions) != 0 {
		t.Fatalf("unchanged frame should reuse placements: %+v", second)
	}
	moved := request
	moved.Y = 3
	third := placements.Reconcile([]PlacementRequest{moved})
	if len(third.Deletions) != 1 || len(third.Renders) != 1 {
		t.Fatalf("moved placement should delete and re-emit: %+v", third)
	}
	if !strings.Contains(third.Deletions[0], "d=i") {
		t.Fatalf("unexpected deletion %q", third.Deletions[0])
	}
	removed := placements.Reconcile(nil)
	if len(removed.Deletions) != 1 {
		t.Fatalf("scrolled-away placement should be deleted: %+v", removed)
	}
}

func TestPlacementsFreeOnReleaseAllAndCacheEviction(t *testing.T) {
	placements := NewImagePlacements(GraphicsKitty)
	request := PlacementRequest{CacheKey: "a.png", X: 1, Y: 1, Rows: 2, Columns: 10, Generation: 1, Image: placementImage()}
	placements.Reconcile([]PlacementRequest{request})
	if sequence := placements.FreeCacheKey("a.png"); !strings.Contains(sequence, "d=I") {
		t.Fatalf("eviction should free uploaded data: %q", sequence)
	}
	placements.Reconcile([]PlacementRequest{request})
	if sequence := placements.ReleaseAll(); !strings.Contains(sequence, "d=A") {
		t.Fatalf("release all should free uploaded data: %q", sequence)
	}
	if sequence := placements.ReleaseAll(); sequence != "" {
		t.Fatalf("idempotent release all should be empty: %q", sequence)
	}
}

func TestPlacementsNonKittyEmitsNoDeletes(t *testing.T) {
	placements := NewImagePlacements(GraphicsITerm2)
	request := PlacementRequest{CacheKey: "a.png", X: 0, Y: 0, Rows: 2, Columns: 10, Generation: 1, Image: placementImage()}
	first := placements.Reconcile([]PlacementRequest{request})
	if len(first.Renders) != 1 || !strings.Contains(first.Renders[0].Sequence, "\x1b]1337") {
		t.Fatalf("iterm2 should emit inline sequences: %+v", first)
	}
	removed := placements.Reconcile(nil)
	if len(removed.Deletions) != 0 {
		t.Fatalf("iterm2 has no delete command: %+v", removed)
	}
}

func TestAltScreenOverlaySuppressesPlacement(t *testing.T) {
	screen, _ := newTestAltScreen(t, 40, 10)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(func(source, cacheKey string) (*LoadedImage, bool) {
		return placementImage(), true
	})
	layout := &LayoutFrame{
		Width:  40,
		Height: 10,
		Images: []LayoutImage{{Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty, X: 0, Y: 1, Rows: 2, Columns: 10}},
	}
	if sequence := screen.placementPass(layout, 10); !strings.Contains(sequence, "\x1b_Ga=T") {
		t.Fatalf("expected a transmit sequence: %q", sequence)
	}
	screen.mu.Lock()
	screen.renderedOverlays = []overlayLayout{{row: 1, col: 0, width: 40, height: 2}}
	screen.mu.Unlock()
	if sequence := screen.placementPass(layout, 10); !strings.Contains(sequence, "d=i") {
		t.Fatalf("overlay must suppress and delete the placement: %q", sequence)
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenGraphicsReconcileAcrossLifecycle(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 40, 10)
	root := &richImageComponent{
		lines:  []string{"top", "[image: pic]", "", "bottom"},
		images: []ImageDescriptor{{Row: 1, RowSpan: 2, Columns: 10, Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty}},
	}
	screen.SetLayoutRoot(root)
	screen.SetImageBytesProvider(func(source, cacheKey string) (*LoadedImage, bool) {
		if source != "pic.png" {
			return nil, false
		}
		return placementImage(), true
	})
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.Start()
	waitForAltRender(t, screen)
	if !strings.Contains(terminal.Output(), "\x1b_Ga=T") {
		t.Fatalf("image was not transmitted: %q", terminal.Output())
	}

	terminal.ResetWrites()
	screen.RequestRender(true)
	waitForAltRender(t, screen)
	if strings.Contains(terminal.Output(), "\x1b_Ga=T") {
		t.Fatalf("unchanged frame re-transmitted the image: %q", terminal.Output())
	}

	terminal.ResetWrites()
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsNone, Enabled: false})
	if !strings.Contains(terminal.Output(), "d=A") {
		t.Fatalf("disable must free placements: %q", terminal.Output())
	}

	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.RequestRender(true)
	waitForAltRender(t, screen)
	terminal.ResetWrites()
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsITerm2, Enabled: true})
	if !strings.Contains(terminal.Output(), "d=A") {
		t.Fatalf("protocol switch must free old resources: %q", terminal.Output())
	}

	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.RequestRender(true)
	waitForAltRender(t, screen)
	terminal.ResetWrites()
	screen.SuspendScreen()
	if !strings.Contains(terminal.Output(), "d=A") {
		t.Fatalf("suspend must free placements: %q", terminal.Output())
	}
	screen.ResumeScreen()
	waitForAltRender(t, screen)
	terminal.ResetWrites()
	screen.Stop(StopOptions{})
	if !strings.Contains(terminal.Output(), "d=A") {
		t.Fatalf("stop must free placements: %q", terminal.Output())
	}
}
