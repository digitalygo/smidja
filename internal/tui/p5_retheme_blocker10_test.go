package tui

import (
	"strings"
	"sync"
	"testing"
)

func p5LoadTheme(t *testing.T, name string, mode ColorMode) *Theme {
	t.Helper()
	registry := NewThemeRegistry("", "", mode)
	theme, err := registry.SetTheme(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return theme
}

func TestP5AltScreenRethemePreservesSearchSelection(t *testing.T) {
	dark := p5LoadTheme(t, "dark", ColorModeTrueColor)
	light := p5LoadTheme(t, "light", ColorModeTrueColor)
	screen, terminal := newTestAltScreen(t, 80, 24)
	lines := make([]string, 0, 40)
	for i := 0; i < 30; i++ {
		lines = append(lines, "retheme marker line")
	}
	lines = append(lines, "plain filler")
	root := NewScrollView(&staticComponent{lines: lines}, ScrollViewOptions{Follow: "end", Primary: true, Scrollbar: ScrollbarAlways})
	screen.SetLayoutRoot(root)
	screen.SetTheme(dark)
	root.SetScrollbarStyles(func(text string) string {
		current := screen.currentTheme()
		if current == nil {
			return text
		}
		return current.Fg("scrollbarTrack", text)
	}, func(text string) string {
		current := screen.currentTheme()
		if current == nil {
			return text
		}
		return current.Fg("scrollbarThumb", text)
	})
	if got := screen.Theme(); got != dark {
		t.Fatal("alt theme not stored")
	}
	screen.Start()
	waitForAltRender(t, screen)
	screen.frameMu.RLock()
	layout := screen.currentLayout
	screen.frameMu.RUnlock()
	if layout == nil {
		t.Fatal("no layout")
	}
	box := GetScrollViewBox(layout, layout.PrimaryScrollView)
	if box == nil {
		t.Fatal("no box")
	}
	screen.Selection().Begin(SelectionPoint{Line: 0, Column: 0}, screen.contentGeneration(box))
	screen.Selection().Update(SelectionPoint{Line: 1000, Column: 1000})
	screen.searchActive.Store(true)
	screen.Search().SetQuery("retheme")
	screen.RenderNow(true)
	before := terminal.Output()
	darkSearchBg, _ := dark.GetBgAnsi("searchMatchBg")
	darkSelectedBg, _ := dark.GetBgAnsi("selectedBg")
	darkTrack, _ := dark.GetFgAnsi("scrollbarTrack")
	for _, want := range []string{darkSearchBg, darkSelectedBg, darkTrack} {
		if !strings.Contains(before, want) {
			t.Fatalf("before missing %q:\n%s", want, before)
		}
	}
	queryBefore := screen.Search().Query()
	currentBefore := screen.Search().Current()
	startBefore, endBefore, okBefore := screen.Selection().Range()
	if !okBefore {
		t.Fatal("selection missing before")
	}
	screen.SetTheme(light)
	if got := screen.Search().Query(); got != queryBefore {
		t.Fatalf("query after = %q want %q", got, queryBefore)
	}
	if got := screen.Search().Current(); got != currentBefore {
		t.Fatalf("current after = %d want %d", got, currentBefore)
	}
	nextStart, nextEnd, okAfter := screen.Selection().Range()
	if !okAfter || nextStart != startBefore || nextEnd != endBefore {
		t.Fatalf("selection after = %+v..%+v ok=%v want %+v..%+v", nextStart, nextEnd, okAfter, startBefore, endBefore)
	}
	terminal.ResetWrites()
	screen.RenderNow(true)
	after := terminal.Output()
	lightSearchBg, _ := light.GetBgAnsi("searchMatchBg")
	lightSelectedBg, _ := light.GetBgAnsi("selectedBg")
	lightTrack, _ := light.GetFgAnsi("scrollbarTrack")
	for _, want := range []string{lightSearchBg, lightSelectedBg, lightTrack} {
		if !strings.Contains(after, want) {
			t.Fatalf("after missing %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, darkSearchBg) && darkSearchBg != lightSearchBg {
		t.Fatalf("after kept dark search:\n%s", after)
	}
	screen.SetTheme(nil)
	if screen.Theme() != light {
		t.Fatal("nil theme must not clear")
	}
	screen.SetSearchStyle(nil)
	screen.SetSelectionStyle(nil)
	screen.Stop(StopOptions{})
}

func TestP5AltScreenRethemePromptAndScrollbarStyles(t *testing.T) {
	dark := p5LoadTheme(t, "dark", ColorModeTrueColor)
	light := p5LoadTheme(t, "light", ColorModeTrueColor)
	screen, _ := newTestAltScreen(t, 40, 10)
	root := NewScrollView(&staticComponent{lines: []string{"hello"}}, ScrollViewOptions{Primary: true, Scrollbar: ScrollbarAlways})
	screen.SetLayoutRoot(root)
	screen.SetTheme(dark)
	screen.openSearch()
	if screen.searchOverlay == nil {
		t.Fatal("overlay missing")
	}
	overlay := screen.searchOverlay
	overlay.SetPromptStyle(nil)
	screen.SetTheme(light)
	screen.RenderNow(true)
	darkAccent, _ := dark.GetFgAnsi("accent")
	lightAccent, _ := light.GetFgAnsi("accent")
	_ = darkAccent
	if lightAccent == "" {
		t.Fatal("light accent missing")
	}
	track, thumb := root.scrollbarStyles()
	if track == nil || thumb == nil {
		t.Fatal("scrollbar styles missing")
	}
	root.SetScrollbarTrackStyle(nil)
	root.SetScrollbarThumbStyle(nil)
	root.SetScrollbarStyles(nil, nil)
	root.SetScrollbarTrackStyle(func(s string) string { return s })
	root.SetScrollbarThumbStyle(func(s string) string { return s })
	root.SetScrollbarStyles(func(s string) string { return "t" + s }, func(s string) string { return "h" + s })
	overlay.SetPromptStyle(func(s string) string { return s })
	screen.Stop(StopOptions{})
}

func TestP5ScrollViewScrollbarStylesConcurrent(t *testing.T) {
	view := NewScrollView(&staticComponent{lines: []string{"a", "b"}}, ScrollViewOptions{Scrollbar: ScrollbarAlways})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		for i := 0; i < 50; i++ {
			track, thumb := view.scrollbarStyles()
			_ = track("x")
			_ = thumb("x")
		}
	}()
	go func() {
		defer wait.Done()
		for i := 0; i < 50; i++ {
			view.SetScrollbarStyles(func(s string) string { return s }, func(s string) string { return s })
		}
	}()
	wait.Wait()
}

func TestP5AltScreenRethemeNilFallbacks(t *testing.T) {
	dark := p5LoadTheme(t, "dark", ColorModeTrueColor)
	screen, terminal := newTestAltScreen(t, 40, 10)
	root := NewScrollView(&staticComponent{lines: []string{"retheme here"}}, ScrollViewOptions{Primary: true, Scrollbar: ScrollbarAlways})
	screen.SetLayoutRoot(root)
	screen.SetTheme(dark)
	screen.frameMu.Lock()
	screen.theme = nil
	searchStyle := screen.searchStyle
	selectionStyle := screen.selectionStyle
	screen.frameMu.Unlock()
	if searchStyle == nil || selectionStyle == nil {
		t.Fatal("styles missing")
	}
	if got := searchStyle(false, "x"); got != SGRInverse+"x"+SGRInverseOff {
		t.Fatalf("search nil fallback = %q", got)
	}
	if got := selectionStyle("x"); got != SGRInverse+"x"+SGRInverseOff {
		t.Fatalf("selection nil fallback = %q", got)
	}
	screen.openSearch()
	overlay := screen.searchOverlay
	if overlay == nil {
		t.Fatal("overlay missing")
	}
	screen.frameMu.Lock()
	screen.theme = nil
	screen.frameMu.Unlock()
	overlay.mu.Lock()
	prompt := overlay.promptStyle
	overlay.mu.Unlock()
	if prompt != nil && prompt("q") != "q" {
		t.Fatalf("prompt nil fallback = %q", prompt("q"))
	}
	track, thumb := screen.implicitScrollView.scrollbarStyles()
	screen.frameMu.Lock()
	screen.theme = nil
	screen.frameMu.Unlock()
	if track("t") != "t" || thumb("h") != "h" {
		t.Fatalf("scrollbar nil fallback")
	}
	screen.frameMu.Lock()
	screen.implicitScrollView = nil
	screen.frameMu.Unlock()
	screen.SetTheme(dark)
	if screen.Theme() != dark {
		t.Fatal("theme not set with nil implicit")
	}
	_ = terminal
	screen.Stop(StopOptions{})
}
