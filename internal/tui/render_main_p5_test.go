package tui

import (
	"strings"
	"testing"
)

func newTestMainScreen(t *testing.T, width, height int) (*MainScreen, *fakeTerminal) {
	t.Helper()
	terminal := newFakeTerminal(width, height)
	screen := NewMainScreen(terminal, false)
	screen.SetMinRenderInterval(0)
	return screen, terminal
}

func TestMainScreenRegularWebPiTerm2(t *testing.T) {
	screen, terminal := newTestMainScreen(t, 40, 10)
	webp := &LoadedImage{Format: ImageFormatWebP, Width: 4, Height: 4, WebPPassthrough: true, Data: craftWebP("VP8L", 4, 4)}
	root := &richImageComponent{
		lines:  []string{"top", "[image: pic]", "", "bottom"},
		images: []ImageDescriptor{{Row: 1, RowSpan: 2, Columns: 8, Source: "pic.webp", CacheKey: "pic.webp", Protocol: GraphicsITerm2}},
	}
	screen.AddChild(root)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsITerm2, Enabled: true})
	screen.SetImageBytesProvider(func(source, cacheKey string) (*LoadedImage, bool) {
		if source != "pic.webp" {
			return nil, false
		}
		return webp, true
	})
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	screen.RenderNow(true)
	output := terminal.Output()
	if !strings.Contains(output, "\x1b]1337;File=") {
		t.Fatalf("regular iterm2 webp passthrough missing: %q", output)
	}
	if !strings.Contains(output, "[image: pic]") {
		t.Fatalf("placeholder missing: %q", output)
	}
	screen.Stop(StopOptions{})
}

func TestMainScreenRegularImageDisabledSkipsPlacement(t *testing.T) {
	screen, terminal := newTestMainScreen(t, 40, 10)
	root := &richImageComponent{
		lines:  []string{"top", "[image: pic]", "", "bottom"},
		images: []ImageDescriptor{{Row: 1, RowSpan: 2, Columns: 8, Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty}},
	}
	screen.AddChild(root)
	calls := 0
	screen.SetImageBytesProvider(func(source, cacheKey string) (*LoadedImage, bool) {
		calls++
		return placementImage(), true
	})
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsNone, Enabled: false})
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	screen.RenderNow(true)
	if calls != 0 {
		t.Fatalf("disabled policy should not read image bytes, got %d calls", calls)
	}
	if strings.Contains(terminal.Output(), "\x1b_Ga=T") {
		t.Fatalf("disabled policy emitted graphics: %q", terminal.Output())
	}
	screen.Stop(StopOptions{})
}

func TestImageRenderSequenceBothPathsForRasterFormats(t *testing.T) {
	cases := map[string]*LoadedImage{
		"png":  {Format: ImageFormatPNG, Width: 2, Height: 2, Data: []byte("png")},
		"jpeg": {Format: ImageFormatJPEG, Width: 2, Height: 2, CanonicalPNG: true, Data: []byte("png")},
		"gif":  {Format: ImageFormatGIF, Width: 2, Height: 2, CanonicalPNG: true, Data: []byte("png")},
	}
	for name, image := range cases {
		if _, ok := ImageRenderSequenceFor(GraphicsKitty, image, true, 1, 1, 4, 2); !ok {
			t.Fatalf("%s should render on kitty", name)
		}
		if _, ok := ImageRenderSequenceFor(GraphicsITerm2, image, false, 1, 1, 4, 2); !ok {
			t.Fatalf("%s should render on regular iterm2", name)
		}
	}
	webp := &LoadedImage{Format: ImageFormatWebP, WebPPassthrough: true, Data: craftWebP("VP8L", 4, 4)}
	if _, ok := ImageRenderSequenceFor(GraphicsKitty, webp, true, 1, 1, 4, 4); ok {
		t.Fatal("webp must not render on kitty")
	}
	if _, ok := ImageRenderSequenceFor(GraphicsITerm2, webp, true, 1, 1, 4, 4); ok {
		t.Fatal("webp must not render on fullscreen iterm2")
	}
	if _, ok := ImageRenderSequenceFor(GraphicsITerm2, webp, false, 1, 1, 4, 4); !ok {
		t.Fatal("webp must render on regular iterm2")
	}
}

func TestMainScreenGraphicsAccessorsAndRelease(t *testing.T) {
	screen, terminal := newTestMainScreen(t, 40, 10)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	if screen.Graphics().Protocol != GraphicsKitty {
		t.Fatalf("unexpected graphics %+v", screen.Graphics())
	}
	screen.ImagePlacements().Allocate()
	screen.ReleaseGraphics()
	if !strings.Contains(terminal.Output(), "d=A") {
		t.Fatalf("release should free data: %q", terminal.Output())
	}
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsNone, Enabled: false})
	if screen.Graphics().Available() {
		t.Fatal("disabled graphics should not be available")
	}
}

func TestMainScreenPlacementReconcile(t *testing.T) {
	screen, _ := newTestMainScreen(t, 40, 10)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	screen.SetImageBytesProvider(func(source, cacheKey string) (*LoadedImage, bool) {
		return placementImage(), true
	})
	rich := RichRender{
		Lines:  []string{"top", "[image]", "", "bottom"},
		Images: []ImageDescriptor{{Row: 1, RowSpan: 2, Columns: 8, Source: "a.png", CacheKey: "a.png", Protocol: GraphicsKitty}},
	}
	if sequence, _ := screen.placementPass(rich, 40, 0, 10); !strings.Contains(sequence, "a=T") {
		t.Fatalf("expected transmit: %q", sequence)
	}
	if sequence, _ := screen.placementPass(rich, 40, 0, 10); sequence != "" {
		t.Fatalf("unchanged frame should reuse placements: %q", sequence)
	}
	if sequence, _ := screen.placementPass(RichRender{Lines: rich.Lines}, 40, 0, 10); !strings.Contains(sequence, "d=i") {
		t.Fatalf("removed image should delete placement: %q", sequence)
	}
}
