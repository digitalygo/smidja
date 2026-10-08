package tui

import (
	"strings"
	"testing"
)

func scrollImageFixture() *ScrollView {
	lines := make([]string, 20)
	for index := range lines {
		lines[index] = "line-" + itoaTest(index)
	}
	lines[5] = "[image: pic]"
	content := &richImageComponent{
		lines: lines,
		images: []ImageDescriptor{{
			Row: 5, RowSpan: 3, OffsetX: 2, Columns: 10,
			Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty,
		}},
	}
	return NewScrollView(content, ScrollViewOptions{Primary: true})
}

func testPlacementProvider(source, cacheKey string) (*LoadedImage, bool) {
	if source != "pic.png" {
		return nil, false
	}
	return placementImage(), true
}

func TestLayoutImageRequestsUseFinalOffsetsWhenFullyVisible(t *testing.T) {
	root := scrollImageFixture()
	RenderLayoutFrame(root, 40, 6, func() {})
	root.ScrollTo(2, ScrollToOptions{})
	frame := RenderLayoutFrame(root, 40, 6, func() {})
	if len(frame.Images) != 1 {
		t.Fatalf("expected one layout image, got %d", len(frame.Images))
	}
	image := frame.Images[0]
	if image.X != 2 || image.Y != 3 || image.Rows != 3 || image.Columns != 10 {
		t.Fatalf("unexpected final geometry %+v", image)
	}
	screen, _ := newTestAltScreen(t, 40, 6)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(testPlacementProvider)
	requests := screen.layoutImageRequests(frame)
	if len(requests) != 1 {
		t.Fatalf("expected one visible request, got %+v", requests)
	}
	request := requests[0]
	if request.X != 2 || request.Y != 3 || request.Rows != 3 || request.Columns != 10 {
		t.Fatalf("request must use final box offsets: %+v", request)
	}
}

func TestAltScreenPlacementSuppressedOnPartialScroll(t *testing.T) {
	root := scrollImageFixture()
	screen, _ := newTestAltScreen(t, 40, 6)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(testPlacementProvider)
	RenderLayoutFrame(root, 40, 6, func() {})

	partialBottom := RenderLayoutFrame(root, 40, 6, func() {})
	if sequence := screen.placementPass(partialBottom, 6); strings.Contains(sequence, "a=T") {
		t.Fatalf("partially clipped image must not emit a full placement: %q", sequence)
	}
	if !strings.Contains(strings.Join(partialBottom.Lines, "\n"), "[image: pic]") {
		t.Fatal("partial placement must keep the text placeholder")
	}

	root.ScrollTo(2, ScrollToOptions{})
	visible := RenderLayoutFrame(root, 40, 6, func() {})
	if sequence := screen.placementPass(visible, 6); !strings.Contains(sequence, "a=T") {
		t.Fatalf("fully visible image should emit a placement: %q", sequence)
	}

	root.ScrollTo(14, ScrollToOptions{})
	partialTop := RenderLayoutFrame(root, 40, 6, func() {})
	if sequence := screen.placementPass(partialTop, 6); !strings.Contains(sequence, "d=i") {
		t.Fatalf("scrolled-away image should be deleted: %q", sequence)
	}
}

func TestAltScreenPlacementSuppressedUnderOverlay(t *testing.T) {
	screen, _ := newTestAltScreen(t, 40, 6)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(testPlacementProvider)
	layout := &LayoutFrame{
		Width:  40,
		Height: 6,
		Lines:  []string{"a", "b", "[image: pic]", "d", "e", "f"},
		Images: []LayoutImage{{Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty, X: 2, Y: 2, Rows: 2, Columns: 10}},
	}
	screen.renderedOverlays = []overlayLayout{{row: 2, col: 0, width: 40, height: 1}}
	if sequence := screen.placementPass(layout, 6); strings.Contains(sequence, "a=T") {
		t.Fatalf("overlay intersection must suppress the placement: %q", sequence)
	}
	screen.renderedOverlays = nil
	if sequence := screen.placementPass(layout, 6); !strings.Contains(sequence, "a=T") {
		t.Fatalf("cleared overlay should allow the placement: %q", sequence)
	}
}

func mainScreenImageRich() RichRender {
	return RichRender{
		Lines: []string{"a", "b", "c", "[image: pic]", "", "", "d"},
		Images: []ImageDescriptor{{
			Row: 3, RowSpan: 3, OffsetX: 2, Columns: 10,
			Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty,
		}},
	}
}

func TestMainScreenPlacementUsesExactVisibleGeometry(t *testing.T) {
	screen, _ := newTestMainScreen(t, 40, 6)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(testPlacementProvider)
	sequence, _ := screen.placementPass(mainScreenImageRich(), 40, 0, 6)
	if !strings.Contains(sequence, CursorTo(3, 2)) {
		t.Fatalf("placement must use exact final offsets: %q", sequence)
	}
	if !strings.Contains(sequence, "c=10,r=3") {
		t.Fatalf("placement must use exact rows and columns: %q", sequence)
	}
}

func TestMainScreenPlacementSuppressedWhenPartiallyClipped(t *testing.T) {
	cases := map[string]struct {
		rich        RichRender
		viewportTop int
		height      int
	}{
		"bottom": {rich: mainScreenImageRich(), viewportTop: 0, height: 5},
		"top":    {rich: mainScreenImageRich(), viewportTop: 5, height: 6},
		"right": {rich: RichRender{
			Lines:  []string{"a", "[image: pic]"},
			Images: []ImageDescriptor{{Row: 1, RowSpan: 2, OffsetX: 35, Columns: 10, Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty}},
		}, viewportTop: 0, height: 6},
		"left": {rich: RichRender{
			Lines:  []string{"a", "[image: pic]"},
			Images: []ImageDescriptor{{Row: 1, RowSpan: 2, OffsetX: -1, Columns: 10, Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty}},
		}, viewportTop: 0, height: 6},
	}
	for name, testCase := range cases {
		screen, _ := newTestMainScreen(t, 40, 6)
		screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
		screen.SetImageBytesProvider(testPlacementProvider)
		sequence, _ := screen.placementPass(testCase.rich, 40, testCase.viewportTop, testCase.height)
		if strings.Contains(sequence, "a=T") {
			t.Fatalf("%s clip must suppress the placement: %q", name, sequence)
		}
	}
}

func TestMainScreenPlacementSuppressedUnderOverlay(t *testing.T) {
	screen, _ := newTestMainScreen(t, 40, 6)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(testPlacementProvider)
	screen.renderedOverlays = []overlayLayout{{row: 3, col: 0, width: 40, height: 1}}
	if sequence, _ := screen.placementPass(mainScreenImageRich(), 40, 0, 6); strings.Contains(sequence, "a=T") {
		t.Fatalf("overlay intersection must suppress the placement: %q", sequence)
	}
	screen.renderedOverlays = nil
	if sequence, _ := screen.placementPass(mainScreenImageRich(), 40, 0, 6); !strings.Contains(sequence, "a=T") {
		t.Fatalf("cleared overlay should allow the placement: %q", sequence)
	}
}

func TestMainScreenPlacementDeletesOnResizeAndScroll(t *testing.T) {
	screen, _ := newTestMainScreen(t, 40, 6)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(testPlacementProvider)
	rich := mainScreenImageRich()
	if sequence, _ := screen.placementPass(rich, 40, 0, 6); !strings.Contains(sequence, "a=T") {
		t.Fatalf("initial frame should transmit: %q", sequence)
	}
	if sequence, _ := screen.placementPass(rich, 40, 0, 4); !strings.Contains(sequence, "d=i") {
		t.Fatalf("resize to a partial clip should delete the placement: %q", sequence)
	}
	if sequence, _ := screen.placementPass(rich, 40, 0, 6); !strings.Contains(sequence, "a=T") {
		t.Fatalf("restored frame should retransmit: %q", sequence)
	}
	if sequence, _ := screen.placementPass(rich, 40, 4, 6); !strings.Contains(sequence, "d=i") {
		t.Fatalf("scrolling the image off-screen should delete it: %q", sequence)
	}
}
