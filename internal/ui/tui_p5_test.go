package ui

import (
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

type probeUITerminal struct {
	*fakeUITerminal
	result bool
	calls  int
}

func (p *probeUITerminal) QueryKittyGraphics(timeout time.Duration) bool {
	p.calls++
	return p.result
}

func writeRunnerWebP(t *testing.T, path string, width, height int) {
	t.Helper()
	payload := make([]byte, 5)
	payload[0] = 0x2f
	bits := uint32(width-1) | uint32(height-1)<<14
	binary.LittleEndian.PutUint32(payload[1:5], bits)
	chunk := make([]byte, 8+len(payload)+len(payload)%2)
	copy(chunk[0:4], "VP8L")
	binary.LittleEndian.PutUint32(chunk[4:8], uint32(len(payload)))
	copy(chunk[8:], payload)
	body := append([]byte("WEBP"), chunk...)
	data := make([]byte, 8+len(body))
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(body)))
	copy(data[8:], body)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write webp: %v", err)
	}
}

func writeRunnerJPEG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create jpeg: %v", err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
}

func writeRunnerGIF(t *testing.T, path string) {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White})
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create gif: %v", err)
	}
	defer file.Close()
	if err := gif.Encode(file, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
}

func TestRunnerResolveImageFullscreenFallbackWithoutIO(t *testing.T) {
	root := t.TempDir()
	writeRunnerPNG(t, filepath.Join(root, "pic.png"))
	writeRunnerJPEG(t, filepath.Join(root, "pic.jpg"))
	writeRunnerGIF(t, filepath.Join(root, "pic.gif"))
	writeRunnerWebP(t, filepath.Join(root, "pic.webp"), 4, 4)
	sources := []string{"pic.png", "pic.jpg", "pic.gif", "pic.webp"}
	iterm2 := func(key string) string {
		if key == "TERM_PROGRAM" {
			return "iTerm.app"
		}
		return ""
	}
	fullscreen, _ := startTestRunner(t, TUIModeFullscreen, func(opts *RunnerOptions) {
		opts.ImagesEnabled = true
		opts.WorkspaceRoot = root
		opts.Env = iterm2
	})
	if capability := fullscreen.GraphicsCapability(); capability.Protocol != tui.GraphicsITerm2 {
		t.Fatalf("fullscreen capability = %+v, want iterm2", capability)
	}
	opens := 0
	fullscreen.imageLoader.SetOpenFile(func(path string) (*os.File, error) {
		opens++
		return nil, os.ErrNotExist
	})
	for _, source := range sources {
		if resolved, ok := fullscreen.resolveImage(source, "pic", 40); ok {
			t.Fatalf("fullscreen iterm2 must fall back for %s, got %+v", source, resolved)
		}
	}
	if opens != 0 {
		t.Fatalf("fullscreen iterm2 fallback performed %d file opens", opens)
	}

	regular, _ := startTestRunner(t, TUIModeRegular, func(opts *RunnerOptions) {
		opts.ImagesEnabled = true
		opts.WorkspaceRoot = root
		opts.Env = iterm2
	})
	for _, source := range sources {
		resolved, ok := regular.resolveImage(source, "pic", 40)
		if !ok || resolved.Protocol != tui.GraphicsITerm2 {
			t.Fatalf("regular iterm2 must render %s, got %+v ok=%v", source, resolved, ok)
		}
	}

	none, _ := startTestRunner(t, TUIModeFullscreen, func(opts *RunnerOptions) {
		opts.ImagesEnabled = true
		opts.WorkspaceRoot = root
		opts.Env = func(key string) string {
			if key == "TERM" {
				return "xterm-256color"
			}
			return ""
		}
	})
	opens = 0
	none.imageLoader.SetOpenFile(func(path string) (*os.File, error) {
		opens++
		return nil, os.ErrNotExist
	})
	if _, ok := none.resolveImage("pic.png", "pic", 40); ok {
		t.Fatal("no protocol must not resolve")
	}
	if opens != 0 {
		t.Fatalf("unavailable protocol performed %d file opens", opens)
	}
}

func TestRunnerKittyGraphicsProbe(t *testing.T) {
	for _, probeOK := range []bool{true, false} {
		terminal := newFakeUITerminal(80, 24)
		prober := &probeUITerminal{fakeUITerminal: terminal, result: probeOK}
		opts := fakeUIRunnerOptions(terminal)
		opts.Mode = TUIModeFullscreen
		opts.Home = t.TempDir()
		opts.ImagesEnabled = true
		opts.Env = func(key string) string {
			if key == "TERM" {
				return "xterm-kitty"
			}
			return ""
		}
		opts.NewTerminal = func(in io.Reader, out io.Writer) tui.Terminal { return prober }
		runner := NewRunner(opts)
		if err := runner.Start(); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		capability := runner.GraphicsCapability()
		if probeOK {
			if capability.Protocol != tui.GraphicsKitty || !capability.Available() {
				t.Fatalf("probe success should enable kitty, got %+v", capability)
			}
		} else if capability.Available() {
			t.Fatalf("probe failure must disable graphics, got %+v", capability)
		}
		if prober.calls != 1 {
			t.Fatalf("expected exactly one probe, got %d", prober.calls)
		}
		runner.Stop()
	}
}

func TestRunnerResolveImageWebPPolicy(t *testing.T) {
	root := t.TempDir()
	writeRunnerWebP(t, filepath.Join(root, "pic.webp"), 4, 4)
	env := func(key string) string {
		if key == "TERM_PROGRAM" {
			return "iTerm.app"
		}
		return ""
	}
	fullscreen, _ := startTestRunner(t, TUIModeFullscreen, func(opts *RunnerOptions) {
		opts.ImagesEnabled = true
		opts.WorkspaceRoot = root
		opts.Env = env
	})
	if _, ok := fullscreen.resolveImage("pic.webp", "pic", 40); ok {
		t.Fatal("fullscreen iterm2 webp must fall back to a placeholder")
	}
	regular, _ := startTestRunner(t, TUIModeRegular, func(opts *RunnerOptions) {
		opts.ImagesEnabled = true
		opts.WorkspaceRoot = root
		opts.Env = env
	})
	resolved, ok := regular.resolveImage("pic.webp", "pic", 40)
	if !ok || resolved.Protocol != tui.GraphicsITerm2 || resolved.Rows < 1 {
		t.Fatalf("regular iterm2 webp should resolve, got %+v ok=%v", resolved, ok)
	}
}

func TestRunnerGraphicsCapabilityInjection(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = TUIModeFullscreen
	opts.Home = t.TempDir()
	opts.ImagesEnabled = true
	opts.Env = func(key string) string {
		switch key {
		case "TERM":
			return "xterm-kitty"
		}
		return ""
	}
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runner.Stop)

	capability := runner.GraphicsCapability()
	if capability.Protocol != tui.GraphicsKitty || !capability.Available() {
		t.Fatalf("unexpected capability %+v", capability)
	}
	if !runner.ImagesEnabled() {
		t.Fatal("images should start enabled")
	}
	runner.SetImagesEnabled(false)
	capability = runner.GraphicsCapability()
	if capability.Available() {
		t.Fatalf("policy disable must win, got %+v", capability)
	}
	if runner.ImagesEnabled() {
		t.Fatal("images should report disabled")
	}
}

func TestRunnerGraphicsCapabilityConservative(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = TUIModeFullscreen
	opts.Home = t.TempDir()
	opts.ImagesEnabled = true
	opts.Env = func(key string) string {
		switch key {
		case "TERM":
			return "xterm-256color"
		case "TMUX":
			return "1"
		}
		return ""
	}
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runner.Stop)
	if capability := runner.GraphicsCapability(); capability.Available() {
		t.Fatalf("multiplexer should be conservative, got %+v", capability)
	}
}

func TestRunnerConsumesGraphicsReplies(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeFullscreen, func(opts *RunnerOptions) {
		opts.ImagesEnabled = true
		opts.Env = func(key string) string {
			if key == "TERM" {
				return "xterm-kitty"
			}
			return ""
		}
	})
	if result := runner.handleInput("\x1b_Gi=1;OK\x1b\\"); !result.Consume {
		t.Fatal("valid graphics reply should be consumed")
	}
	if result := runner.handleInput("\x1b_Gi=1;ENOENT\x1b\\"); !result.Consume {
		t.Fatal("malformed graphics reply should be consumed")
	}
	if result := runner.handleInput("a"); result.Consume {
		t.Fatal("ordinary input must not be consumed by the graphics tracker")
	}
}

func TestRunnerHyperlinksAndOpenerWired(t *testing.T) {
	opened := make(chan string, 4)
	runner, _ := startTestRunner(t, TUIModeFullscreen, func(opts *RunnerOptions) {
		opts.LinkOpener = tui.LinkOpenerFunc(func(ctx context.Context, target string) error {
			opened <- target
			return nil
		})
	})
	if runner.linkOpener == nil {
		t.Fatal("link opener not retained")
	}
	alt, ok := runner.view.(*tui.AltScreen)
	if !ok {
		t.Fatal("fullscreen runner should use the alt screen")
	}
	if err := runner.linkOpener.Open(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("opener failed: %v", err)
	}
	select {
	case target := <-opened:
		if target != "https://example.com" {
			t.Fatalf("unexpected target %q", target)
		}
	default:
		t.Fatal("injected opener was not invoked")
	}
	if alt == nil {
		t.Fatal("alt screen missing")
	}
}

func TestRunnerRenderImageProvider(t *testing.T) {
	root := t.TempDir()
	imagePath := root + "/pic.png"
	writeRunnerPNG(t, imagePath)
	runner, _ := startTestRunner(t, TUIModeFullscreen, func(opts *RunnerOptions) {
		opts.ImagesEnabled = true
		opts.WorkspaceRoot = root
		opts.Env = func(key string) string {
			if key == "TERM" {
				return "xterm-kitty"
			}
			return ""
		}
	})
	if _, ok := runner.view.(*tui.AltScreen); !ok {
		t.Fatal("expected alt screen")
	}
	resolved, rendered := runner.resolveImage("pic.png", "pic", 40)
	if !rendered || resolved.Protocol != tui.GraphicsKitty || resolved.Rows < 1 || resolved.Columns < 1 {
		t.Fatalf("expected a kitty resolution, got %+v rendered=%v", resolved, rendered)
	}
	loaded, found := runner.loadImage("pic.png", resolved.CacheKey)
	if !found {
		t.Fatal("expected cached image bytes")
	}
	sequence, emitted := tui.ImageRenderSequenceFor(tui.GraphicsKitty, loaded, true, 1, 1, resolved.Columns, resolved.Rows)
	if !emitted || !strings.Contains(sequence, "\x1b_G") {
		t.Fatalf("expected a kitty sequence, got %q emitted=%v", sequence, emitted)
	}
	runner.SetImagesEnabled(false)
	if _, rendered := runner.resolveImage("pic.png", "pic", 40); rendered {
		t.Fatal("disabled images must not resolve")
	}
	if _, rendered := runner.resolveImage("../escape.png", "x", 40); rendered {
		t.Fatal("traversal must not resolve")
	}
	if _, rendered := runner.resolveImage("missing.png", "x", 40); rendered {
		t.Fatal("missing file must not resolve")
	}
}

func TestRunnerDefaultImageResolverRegistersDescriptor(t *testing.T) {
	interactive.SetDefaultImageResolver(func(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
		if strings.TrimSpace(source) == "" {
			return tui.ResolvedImage{}, false
		}
		return tui.ResolvedImage{CacheKey: source, Columns: 5, Rows: 2, Label: "[image: " + alt + "]", Protocol: tui.GraphicsITerm2}, true
	})
	t.Cleanup(func() { interactive.SetDefaultImageResolver(nil) })
	resolved, ok := interactive.DefaultImageResolverFor("pic.png", "pic", 40)
	if !ok || resolved.Rows != 2 || resolved.Label != "[image: pic]" || resolved.Protocol != tui.GraphicsITerm2 {
		t.Fatalf("unexpected resolver output %+v ok=%v", resolved, ok)
	}
	if _, ok := interactive.DefaultImageResolverFor("", "pic", 40); ok {
		t.Fatal("empty source should not resolve")
	}
}
