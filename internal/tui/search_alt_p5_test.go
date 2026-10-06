package tui

import (
	"strings"
	"testing"
)

func TestAltScreenSearchCaptureSurvivesDialogSource(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.AddChild(&plainComponent{lines: []string{"content"}})
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	if !screen.searchActive.Load() || !screen.ModalCapture() {
		t.Fatal("search should activate with modal capture")
	}
	screen.SetModalCapture(true)
	if !screen.ModalCapture() {
		t.Fatal("dialog source should keep capture enabled")
	}
	screen.SetModalCapture(false)
	if !screen.ModalCapture() {
		t.Fatal("dialog release must not disable search capture")
	}
	if !screen.searchActive.Load() {
		t.Fatal("dialog release must not close search")
	}
	screen.CloseSearch()
	if screen.searchActive.Load() || screen.ModalCapture() {
		t.Fatal("closing search should release only its own source")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchDialogSupersedesThenResumes(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.AddChild(&plainComponent{lines: []string{"content"}})
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	dialog := newRecordingComponent("dialog")
	handle := screen.ShowOverlay(dialog, OverlayOptions{Anchor: AnchorCenter})
	if screen.FocusedComponent() != dialog {
		t.Fatal("dialog should take focus")
	}
	screen.handleTerminalInput("x")
	if len(dialog.inputs) != 1 || dialog.inputs[0] != "x" {
		t.Fatalf("dialog input missing: %q", dialog.inputs)
	}
	if screen.Search().Query() != "" {
		t.Fatalf("search must not receive dialog input: %q", screen.Search().Query())
	}
	handle.Hide()
	if !screen.searchActive.Load() || !screen.ModalCapture() {
		t.Fatal("search should resume after the dialog closes")
	}
	if screen.FocusedComponent() != screen.searchOverlay {
		t.Fatal("search overlay should regain focus")
	}
	screen.handleTerminalInput("y")
	if screen.Search().Query() != "y" {
		t.Fatalf("search did not resume input: %q", screen.Search().Query())
	}
	screen.CloseSearch()
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchKeysDoNotReachFocused(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	focused := newRecordingComponent("focused")
	screen.AddChild(focused)
	screen.SetFocus(focused)
	screen.Start()
	screen.RenderNow(true)
	screen.handleTerminalInput("p")
	if len(focused.inputs) != 1 || focused.inputs[0] != "p" {
		t.Fatalf("baseline routing broken: %q", focused.inputs)
	}
	focused.inputs = nil
	screen.openSearch()
	screen.handleTerminalInput("x")
	screen.handleTerminalInput("\x07")
	if len(focused.inputs) != 0 {
		t.Fatalf("search keys leaked to the focused component: %q", focused.inputs)
	}
	if screen.Search().Query() != "x" {
		t.Fatalf("search did not receive the key: %q", screen.Search().Query())
	}
	screen.handleTerminalInput("\x1b")
	if screen.searchActive.Load() {
		t.Fatal("escape should close search")
	}
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchUsesPublishedFrameScrollTop(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	root := newScrollRoot(30)
	screen.SetLayoutRoot(root)
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	if layout == nil || layout.PrimaryScrollView == nil {
		t.Fatal("published layout missing")
	}
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil {
		t.Fatal("scroll box missing")
	}
	if layout.PrimaryScrollTop == 0 {
		t.Fatal("follow-end frame should have a captured scroll top")
	}
	box.scrollView.ScrollTo(0, ScrollToOptions{DisableFollow: true})
	if box.scrollView.ScrollTop() != 0 {
		t.Fatal("live scroll did not move")
	}
	screen.updateSearch(layout)
	screen.frameMu.RLock()
	got := screen.searchFrameScrollTop
	screen.frameMu.RUnlock()
	if got != layout.PrimaryScrollTop {
		t.Fatalf("search snapshot used live scroll: %d != %d", got, layout.PrimaryScrollTop)
	}
	screen.CloseSearch()
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchHighlightOverridesMarkdownANSI(t *testing.T) {
	screen, terminal := newTestAltScreen(t, 40, 4)
	content := &staticComponent{lines: []string{"\x1b[31mhello\x1b[0m world"}}
	screen.SetLayoutRoot(NewScrollView(content, ScrollViewOptions{Follow: "end", Primary: true}))
	screen.SetSearchStyle(func(selected bool, text string) string {
		return "\x1b[45m" + text + "\x1b[49m"
	})
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	screen.Search().SetQuery("hello")
	terminal.ResetWrites()
	screen.RenderNow(true)
	output := terminal.Output()
	if !strings.Contains(output, "\x1b[45mhello\x1b[49m") {
		t.Fatalf("search style missing: %q", output)
	}
	if strings.Contains(output, "\x1b[31mhello") {
		t.Fatalf("markdown color overrode the search style: %q", output)
	}
	screen.CloseSearch()
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchInterruptedPasteResetsOnReopen(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.AddChild(&plainComponent{lines: []string{"content"}})
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	screen.handleTerminalInput(BracketedPasteStart + "partial")
	if screen.Search().Query() != "" {
		t.Fatalf("interrupted paste committed: %q", screen.Search().Query())
	}
	screen.CloseSearch()
	screen.openSearch()
	screen.handleTerminalInput("x")
	if screen.Search().Query() != "x" {
		t.Fatalf("reopened search retained the interrupted paste: %q", screen.Search().Query())
	}
	screen.handleTerminalInput(BracketedPasteEnd)
	if screen.Search().Query() != "x" {
		t.Fatalf("stale end marker changed the query: %q", screen.Search().Query())
	}
	screen.handleTerminalInput(BracketedPasteStart + "full" + BracketedPasteEnd)
	if screen.Search().Query() != "xfull" {
		t.Fatalf("paste after reopen failed: %q", screen.Search().Query())
	}
	screen.CloseSearch()
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchPasteResetsAcrossSessionReplacement(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.SetLayoutRoot(newScrollRoot(30))
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	screen.handleTerminalInput(BracketedPasteStart + "partial")
	screen.SetLayoutRoot(newScrollRoot(10))
	if screen.searchActive.Load() {
		t.Fatal("session replacement must close search")
	}
	screen.openSearch()
	screen.handleTerminalInput("y")
	if screen.Search().Query() != "y" {
		t.Fatalf("session replacement retained the interrupted paste: %q", screen.Search().Query())
	}
	screen.CloseSearch()
	screen.Stop(StopOptions{})
}

func TestAltScreenSearchStopClosesSearch(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.AddChild(&plainComponent{lines: []string{"content"}})
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	if !screen.searchActive.Load() || !screen.ModalCapture() {
		t.Fatal("search should be active before stop")
	}
	screen.Stop(StopOptions{})
	if screen.searchActive.Load() || screen.ModalCapture() {
		t.Fatal("stop must close search and release capture")
	}
}

func TestAltScreenSearchSetLayoutRootClosesSearch(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	first := newScrollRoot(30)
	screen.SetLayoutRoot(first)
	screen.Start()
	screen.RenderNow(true)
	screen.openSearch()
	if !screen.searchActive.Load() {
		t.Fatal("search should be active")
	}
	second := newScrollRoot(10)
	screen.SetLayoutRoot(second)
	if screen.searchActive.Load() {
		t.Fatal("session replacement must close search")
	}
	if screen.ModalCapture() {
		t.Fatal("session replacement must release capture")
	}
	screen.Stop(StopOptions{})
}
