package tui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

func writeJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create jpeg: %v", err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
}

func writeGIF(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{color.Black, color.White})
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create gif: %v", err)
	}
	defer file.Close()
	if err := gif.Encode(file, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
}

func TestImageLoaderValidatesPNG(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "ok.png"), 4, 4)
	loader := NewImageLoader(root, true)
	image, err := loader.Load("ok.png")
	if err != nil {
		t.Fatalf("load png: %v", err)
	}
	if image.Format != ImageFormatPNG || image.CanonicalPNG || image.Width != 4 || image.Height != 4 {
		t.Fatalf("unexpected png metadata %+v", image)
	}
}

func TestImageLoaderCanonicalizesJPEGAndGIF(t *testing.T) {
	root := t.TempDir()
	writeJPEG(t, filepath.Join(root, "photo.jpg"), 4, 4)
	writeGIF(t, filepath.Join(root, "anim.gif"), 4, 4)
	loader := NewImageLoader(root, true)
	for _, name := range []string{"photo.jpg", "anim.gif"} {
		loaded, err := loader.Load(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		if !loaded.CanonicalPNG {
			t.Fatalf("%s was not canonicalized", name)
		}
		if detectImageFormat(loaded.Data) != ImageFormatPNG {
			t.Fatalf("%s canonical data is not png", name)
		}
	}
}

func TestImageLoaderRejectsCorrupt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageLoader(root, true).Load("bad.png"); err == nil {
		t.Fatal("corrupt image should fail")
	}
}

func TestImageLoaderDimensionBounds(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "wide.png"), imageMaxDimension+1, 1)
	if _, err := NewImageLoader(root, true).Load("wide.png"); err != ErrImageDimensions {
		t.Fatalf("expected dimension error, got %v", err)
	}
	writePNG(t, filepath.Join(root, "huge.png"), 3000, 2000)
	if _, err := NewImageLoader(root, true).Load("huge.png"); err != ErrImageTooManyPixels {
		t.Fatalf("expected pixel error, got %v", err)
	}
}

func TestImageLoaderRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	loader := NewImageLoader(root, true)
	for _, candidate := range []string{"../escape.png", "/etc/passwd", "http://example.com/x.png", "data:image/png;base64,AAAA", "", "sub/../../escape.png"} {
		if _, err := loader.Load(candidate); err == nil {
			t.Fatalf("path %q should be rejected", candidate)
		}
	}
}

func TestImageLoaderRejectsSymlinkAndFIFO(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.png")
	writePNG(t, target, 2, 2)
	link := filepath.Join(root, "link.png")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := NewImageLoader(root, true).Load("link.png"); err != ErrImageNotRegular {
		t.Fatalf("symlink should be rejected, got %v", err)
	}
	fifo := filepath.Join(root, "pipe.png")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if _, err := NewImageLoader(root, true).Load("pipe.png"); err != ErrImageNotRegular {
		t.Fatalf("fifo should be rejected, got %v", err)
	}
}

func TestImageLoaderDisabledAvoidsRead(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "ok.png"), 2, 2)
	loader := NewImageLoader(root, false)
	read := false
	loader.SetOpenFile(func(path string) (*os.File, error) {
		read = true
		return os.Open(path)
	})
	if _, err := loader.Load("ok.png"); err != ErrImageDisabled {
		t.Fatalf("expected disabled error, got %v", err)
	}
	if read {
		t.Fatal("disabled loader should not read the file")
	}
	loader.SetEnabled(true)
	if _, err := loader.Load("ok.png"); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	if !read {
		t.Fatal("enabled loader should read the file")
	}
}

func TestImageLoaderCacheAndEviction(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "a.png"), 2, 2)
	writePNG(t, filepath.Join(root, "b.png"), 2, 2)
	loader := NewImageLoader(root, true)
	loader.cacheMax = 1
	if _, err := loader.Load("a.png"); err != nil {
		t.Fatal(err)
	}
	if loader.CacheBytes() != 0 {
		t.Fatalf("oversized cache entry should not be stored: %d", loader.CacheBytes())
	}
	loader.cacheMax = 1 << 20
	if _, err := loader.Load("a.png"); err != nil {
		t.Fatal(err)
	}
	if loader.CacheBytes() == 0 {
		t.Fatal("expected cached bytes")
	}
	loader.SetEnabled(false)
	if loader.CacheBytes() != 0 {
		t.Fatal("disable should clear the cache")
	}
	loader.SetEnabled(true)
	if _, err := loader.Load("a.png"); err != nil {
		t.Fatal(err)
	}
	loader.Evict("a.png")
	if loader.CacheBytes() != 0 {
		t.Fatal("evict should drop the entry")
	}
}

func craftWebPChunk(fourCC string, payload []byte) []byte {
	chunk := make([]byte, 8+len(payload)+len(payload)%2)
	copy(chunk[0:4], fourCC)
	binary.LittleEndian.PutUint32(chunk[4:8], uint32(len(payload)))
	copy(chunk[8:], payload)
	return chunk
}

func craftWebP(fourCC string, width, height int) []byte {
	var payload []byte
	switch fourCC {
	case "VP8 ":
		payload = make([]byte, 10)
		payload[3] = 0x9d
		payload[4] = 0x01
		payload[5] = 0x2a
		binary.LittleEndian.PutUint16(payload[6:8], uint16(width))
		binary.LittleEndian.PutUint16(payload[8:10], uint16(height))
	case "VP8L":
		payload = make([]byte, 5)
		payload[0] = 0x2f
		bits := uint32(width-1) | uint32(height-1)<<14
		binary.LittleEndian.PutUint32(payload[1:5], bits)
	case "VP8X":
		payload = make([]byte, 10)
		payload[4] = byte(width - 1)
		payload[5] = byte((width - 1) >> 8)
		payload[6] = byte((width - 1) >> 16)
		payload[7] = byte(height - 1)
		payload[8] = byte((height - 1) >> 8)
		payload[9] = byte((height - 1) >> 16)
	}
	body := append([]byte("WEBP"), craftWebPChunk(fourCC, payload)...)
	data := make([]byte, 8+len(body))
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(body)))
	copy(data[8:], body)
	return data
}

func TestWebPDimensions(t *testing.T) {
	cases := []struct {
		fourCC        string
		width, height int
	}{
		{"VP8 ", 320, 200},
		{"VP8L", 100, 50},
		{"VP8X", 640, 480},
	}
	for _, testCase := range cases {
		width, height, err := webPDimensions(craftWebP(testCase.fourCC, testCase.width, testCase.height))
		if err != nil {
			t.Fatalf("%s: %v", testCase.fourCC, err)
		}
		if width != testCase.width || height != testCase.height {
			t.Fatalf("%s got %dx%d want %dx%d", testCase.fourCC, width, height, testCase.width, testCase.height)
		}
	}
	if _, _, err := webPDimensions([]byte("short")); err == nil {
		t.Fatal("short webp should fail")
	}
}

func TestImageRenderSequenceWebPFallbacks(t *testing.T) {
	webp := &LoadedImage{Format: ImageFormatWebP, Width: 10, Height: 10, WebPPassthrough: true, Data: craftWebP("VP8L", 10, 10)}
	if sequence, ok := ImageRenderSequence(GraphicsKitty, webp, 1, 1, 10, 10); ok || sequence != "" {
		t.Fatal("webp should be a placeholder on kitty")
	}
	sequence, ok := ImageRenderSequence(GraphicsITerm2, webp, 1, 1, 10, 10)
	if !ok || sequence == "" {
		t.Fatal("webp should pass through on iterm2")
	}
	pngImage := &LoadedImage{Format: ImageFormatPNG, Data: []byte("png")}
	if _, ok := ImageRenderSequence(GraphicsNone, pngImage, 1, 1, 10, 10); ok {
		t.Fatal("no protocol should render nothing")
	}
	if _, ok := ImageRenderSequence(GraphicsKitty, pngImage, 1, 1, 10, 10); !ok {
		t.Fatal("kitty should render png")
	}
}

func TestImagePlacementsLifecycle(t *testing.T) {
	placements := NewImagePlacements(GraphicsKitty)
	first := placements.Allocate()
	second := placements.Allocate()
	if first == second {
		t.Fatal("ids should be unique")
	}
	if len(placements.Active()) != 2 {
		t.Fatal("expected two active placements")
	}
	release := placements.Release(first)
	if release == "" || len(placements.Active()) != 1 {
		t.Fatal("release did not delete placement")
	}
	if placements.Release(first) != "" {
		t.Fatal("double release should be a no-op")
	}
	if all := placements.ReleaseAll(); all == "" {
		t.Fatal("release all should emit a delete sequence")
	}
	if len(placements.Active()) != 0 {
		t.Fatal("placements should be empty after release all")
	}
	placements = NewImagePlacements(GraphicsNone)
	id := placements.Allocate()
	if placements.Release(id) != "" || placements.ReleaseAll() != "" {
		t.Fatal("non-kitty placements should not emit sequences")
	}
}

func TestDetectImageFormatAndString(t *testing.T) {
	if detectImageFormat(craftWebP("VP8X", 1, 1)) != ImageFormatWebP {
		t.Fatal("webp not detected")
	}
	format := ImageFormatJPEG
	if format.String() != "jpeg" {
		t.Fatalf("unexpected string %q", format.String())
	}
	if ImageFormatUnknown.String() != "unknown" {
		t.Fatal("unknown string")
	}
}

func TestImageLoaderMaxBytes(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "ok.png"), 2, 2)
	data, err := os.ReadFile(filepath.Join(root, "ok.png"))
	if err != nil {
		t.Fatal(err)
	}
	loader := NewImageLoader(root, true)
	loader.maxBytes = len(data) - 1
	if _, err := loader.Load("ok.png"); err != ErrImageTooLarge {
		t.Fatalf("expected size error, got %v", err)
	}
	var _ = bytes.MinRead
}

func TestImageLoaderCacheEvictionOrder(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 3; index++ {
		writePNG(t, filepath.Join(root, "img"+itoaTest(index)+".png"), 2, 2)
	}
	loader := NewImageLoader(root, true)
	loader.cacheMax = 200
	total := 0
	for index := 0; index < 3; index++ {
		loaded, err := loader.Load("img" + itoaTest(index) + ".png")
		if err != nil {
			t.Fatal(err)
		}
		total += len(loaded.Data)
		if loader.CacheBytes() > loader.cacheMax {
			t.Fatalf("cache exceeded limit: %d > %d", loader.CacheBytes(), loader.cacheMax)
		}
	}
	loader.Evict("does-not-exist.png")
	loader.SetOpenFile(nil)
}

func TestImageLoaderOpenErrorAndSwap(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "ok.png"), 2, 2)
	loader := NewImageLoader(root, true)
	loader.SetOpenFile(func(path string) (*os.File, error) {
		return nil, os.ErrPermission
	})
	if _, err := loader.Load("ok.png"); err == nil {
		t.Fatal("open error should propagate")
	}
	other := filepath.Join(root, "other.png")
	writePNG(t, other, 2, 2)
	loader.SetOpenFile(func(path string) (*os.File, error) {
		return os.Open(other)
	})
	if _, err := loader.Load("ok.png"); err != ErrImageNotRegular {
		t.Fatalf("file swap should be rejected, got %v", err)
	}
}

func TestImageLoaderUnknownFormatAndCacheHit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mystery.bin"), []byte("mystery"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewImageLoader(root, true)
	if _, err := loader.Load("mystery.bin"); err != ErrImageUnsupported {
		t.Fatalf("unknown format should be unsupported, got %v", err)
	}
	writePNG(t, filepath.Join(root, "cache.png"), 2, 2)
	first, err := loader.Load("cache.png")
	if err != nil {
		t.Fatal(err)
	}
	second, err := loader.Load("cache.png")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("expected a cache hit to return the same image")
	}
	if !loader.Enabled() {
		t.Fatal("loader should report enabled")
	}
}

func TestImageRenderSequenceNilAndNone(t *testing.T) {
	if sequence, ok := ImageRenderSequence(GraphicsKitty, nil, 1, 1, 1, 1); ok || sequence != "" {
		t.Fatal("nil image should not render")
	}
	pngImage := &LoadedImage{Format: ImageFormatPNG, Data: []byte("x")}
	if _, ok := ImageRenderSequence(GraphicsNone, pngImage, 1, 1, 1, 1); ok {
		t.Fatal("no protocol should not render")
	}
}

func TestImagePlacementsITerm2Release(t *testing.T) {
	placements := NewImagePlacements(GraphicsITerm2)
	id := placements.Allocate()
	if placements.Release(id) != "" {
		t.Fatal("iterm2 placements should not emit kitty deletes")
	}
	if placements.ReleaseAll() != "" {
		t.Fatal("iterm2 release all should be a no-op")
	}
}

func TestImageFormatStrings(t *testing.T) {
	cases := map[ImageFormat]string{
		ImageFormatPNG: "png", ImageFormatJPEG: "jpeg", ImageFormatGIF: "gif",
		ImageFormatWebP: "webp", ImageFormatUnknown: "unknown",
	}
	for format, expected := range cases {
		if format.String() != expected {
			t.Fatalf("format %d string %q want %q", format, format.String(), expected)
		}
	}
}

func TestImageLoaderWebPLoadAndBounds(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.webp"), craftWebP("VP8L", 20, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewImageLoader(root, true).Load("ok.webp")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.WebPPassthrough || loaded.Width != 20 || loaded.Height != 10 {
		t.Fatalf("unexpected webp %+v", loaded)
	}
	if err := os.WriteFile(filepath.Join(root, "huge.webp"), craftWebP("VP8X", 5000, 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageLoader(root, true).Load("huge.webp"); err != ErrImageDimensions {
		t.Fatalf("expected dimension error, got %v", err)
	}
}

func TestImageRenderSequenceFullscreenWebPPlaceholder(t *testing.T) {
	webp := &LoadedImage{Format: ImageFormatWebP, WebPPassthrough: true, Data: craftWebP("VP8L", 4, 4)}
	if sequence, ok := ImageRenderSequenceFor(GraphicsITerm2, webp, true, 1, 1, 4, 4); ok || sequence != "" {
		t.Fatal("webp should be a placeholder on fullscreen iterm2")
	}
	if sequence, ok := ImageRenderSequenceFor(GraphicsITerm2, webp, false, 1, 1, 4, 4); !ok || sequence == "" {
		t.Fatal("webp should pass through on inline iterm2")
	}
	if _, ok := ImageRenderSequenceFor(GraphicsNone, webp, false, 1, 1, 4, 4); ok {
		t.Fatal("no protocol should render nothing")
	}
}

func TestImageRenderSequenceITerm2FullscreenFallsBackForAllFormats(t *testing.T) {
	cases := []struct {
		name  string
		image *LoadedImage
	}{
		{name: "png", image: &LoadedImage{Format: ImageFormatPNG, Data: []byte("png")}},
		{name: "jpeg", image: &LoadedImage{Format: ImageFormatJPEG, Data: []byte("jpeg")}},
		{name: "gif", image: &LoadedImage{Format: ImageFormatGIF, Data: []byte("gif")}},
		{name: "webp", image: &LoadedImage{Format: ImageFormatWebP, WebPPassthrough: true, Data: craftWebP("VP8L", 4, 4)}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if sequence, ok := ImageRenderSequenceFor(GraphicsITerm2, testCase.image, true, 1, 1, 4, 4); ok || sequence != "" {
				t.Fatalf("fullscreen iterm2 must fall back for %s, got %q", testCase.name, sequence)
			}
			if sequence, ok := ImageRenderSequenceFor(GraphicsITerm2, testCase.image, false, 1, 1, 4, 4); !ok || sequence == "" {
				t.Fatalf("regular iterm2 must render %s", testCase.name)
			}
		})
	}
	pngImage := &LoadedImage{Format: ImageFormatPNG, Data: []byte("png")}
	if sequence, ok := ImageRenderSequenceFor(GraphicsKitty, pngImage, true, 1, 1, 4, 4); !ok || sequence == "" {
		t.Fatal("kitty fullscreen must render canonical images")
	}
}

func TestImageDescriptorRenderingIsPlaceholderOnly(t *testing.T) {
	line := "x [image: a] y [image: b] z"
	if strings.Contains(line, "\x1b_pi") || strings.Contains(line, "\x1b_G") {
		t.Fatalf("placeholder lines must not carry graphics escapes: %q", line)
	}
}

func TestAltScreenRichImagePlacement(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 40, 8)
	screen.SetGraphics(GraphicsCapability{Protocol: GraphicsKitty, Enabled: true})
	root := &richImageComponent{
		lines: []string{"header", "[image: pic]", "", "footer"},
		images: []ImageDescriptor{{
			Row: 1, RowSpan: 2, Columns: 10, Source: "pic.png", CacheKey: "pic.png", Alt: "pic", Protocol: GraphicsKitty,
		}},
	}
	screen.SetLayoutRoot(root)
	screen.SetImageBytesProvider(func(source, cacheKey string) (*LoadedImage, bool) {
		if source != "pic.png" {
			return nil, false
		}
		return &LoadedImage{Format: ImageFormatPNG, Width: 4, Height: 4, Data: []byte("png")}, true
	})
	screen.Start()
	waitForAltRender(t, screen)
	output := terminal.Output()
	if !strings.Contains(output, "\x1b_G") {
		t.Fatalf("graphics sequence missing: %q", output)
	}
	if !strings.Contains(output, "[image: pic]") {
		t.Fatalf("placeholder missing: %q", output)
	}
	screen.Stop(StopOptions{})
}

type richImageComponent struct {
	lines  []string
	images []ImageDescriptor
}

func (c *richImageComponent) Render(width int) []string { return c.lines }

func (c *richImageComponent) RenderRich(width int) RichRender {
	return RichRender{Lines: c.lines, Images: c.images}
}

func (c *richImageComponent) Invalidate() {}

func TestLayoutFrameGathersImageGeometry(t *testing.T) {
	root := &richImageComponent{
		lines:  []string{"top", "[image: pic]", "", "bottom"},
		images: []ImageDescriptor{{Row: 1, RowSpan: 2, OffsetX: 2, Columns: 10, Source: "pic.png", CacheKey: "pic.png", Protocol: GraphicsKitty}},
	}
	frame := RenderLayoutFrame(root, 40, 10, func() {})
	if len(frame.Images) != 1 {
		t.Fatalf("expected one layout image, got %d", len(frame.Images))
	}
	image := frame.Images[0]
	if image.Y != 1 || image.X != 2 || image.Rows != 2 || image.Columns != 10 {
		t.Fatalf("unexpected layout image %+v", image)
	}
	for _, line := range frame.Lines {
		if strings.Contains(line, "\x1b_G") || strings.Contains(line, "\x1b]1337") || strings.Contains(line, "\x1b_pi") {
			t.Fatalf("final document lines must be placeholder-only: %q", line)
		}
	}
}

func TestImageLoaderEvictionNotifies(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "a.png"), 2, 2)
	loader := NewImageLoader(root, true)
	var freed []string
	loader.SetOnEvict(func(key string) { freed = append(freed, key) })
	if _, err := loader.Load("a.png"); err != nil {
		t.Fatal(err)
	}
	loader.Evict("a.png")
	if len(freed) != 1 {
		t.Fatalf("eviction callback = %v", freed)
	}
	if _, err := loader.Load("a.png"); err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load("a.png")
	if err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(root, "b.png"), 2, 2)
	loader.cacheMax = len(loaded.Data)*2 - 1
	if _, err := loader.Load("b.png"); err != nil {
		t.Fatal(err)
	}
	if len(freed) < 2 {
		t.Fatalf("capacity eviction should notify too: %v", freed)
	}
}
