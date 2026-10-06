package ui

import (
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func p5RethemeFg(t *testing.T, theme *tui.Theme, token tui.ThemeColor) string {
	t.Helper()
	ansi, ok := theme.GetFgAnsi(token)
	if !ok {
		t.Fatalf("theme %q missing fg %q", theme.Name, token)
	}
	return ansi
}

func p5RethemeBg(t *testing.T, theme *tui.Theme, token tui.ThemeColor) string {
	t.Helper()
	ansi, ok := theme.GetBgAnsi(token)
	if !ok {
		t.Fatalf("theme %q missing bg %q", theme.Name, token)
	}
	return ansi
}

func seedP5RethemeContent(t *testing.T, runner *Runner) {
	t.Helper()
	surface := runner.Surface()
	surface.AddNotice(0, "notice retheme marker")
	assistant := surface.StartAssistantTurn()
	assistant.AppendText("retheme marker one\nretheme marker two\n")
	assistant.AppendText("```go\npackage main\nfunc hello() string { return \"hi\" }\n```\n")
	assistant.AppendText("```mermaid\ngraph TD\n  A[Start] --> B[End]\n```\n")
	assistant.AppendText("```mermaid\ngraph TD\n  A --> B\n  B --> A\n```\n")
	assistant.AppendText("retheme math $\\alpha$ inline\n$$\n\\alpha\n$$\n")
	assistant.AppendText("![retheme diagram](missing-image.png)\nretheme marker bottom\n")
	surface.EndAssistantTurn("stop", "")
	surface.Transcript().SetScrollbar(tui.ScrollbarAlways)
	runner.view.RenderNow(true)
}

func TestP5RethemeAnsiFramesPreserveState(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	terminal.SetSize(80, 50)
	seedP5RethemeContent(t, runner)
	alt, ok := runner.view.(*tui.AltScreen)
	if !ok {
		t.Fatal("fullscreen runner should use alt screen")
	}
	runner.view.RenderNow(true)
	terminal.SendInput(testSearchKey)
	if !alt.SearchActive() {
		t.Fatal("search did not open")
	}
	terminal.SendInput("retheme")
	runner.view.RenderNow(true)
	if got := alt.Search().Query(); got != "retheme" {
		t.Fatalf("search query = %q want retheme", got)
	}
	if alt.Search().MatchCount() == 0 {
		t.Fatal("expected search matches")
	}
	alt.Selection().Begin(tui.SelectionPoint{Line: 0, Column: 0}, alt.Selection().Generated())
	alt.Selection().Update(tui.SelectionPoint{Line: 1000, Column: 1000})
	runner.view.RenderNow(true)
	searchCurrent := alt.Search().Current()
	selectionStart, selectionEnd, selectionOk := alt.Selection().Range()
	if !selectionOk {
		t.Fatal("expected active selection")
	}
	dark := runner.Surface().Theme()
	before := fullscreenRenderOutput(runner, terminal)
	darkSearchBg := p5RethemeBg(t, dark, "searchMatchBg")
	darkSearchFg := p5RethemeFg(t, dark, "searchMatchText")
	darkSelectedBg := p5RethemeBg(t, dark, "selectedBg")
	darkTrack := p5RethemeFg(t, dark, "scrollbarTrack")
	darkThumb := p5RethemeFg(t, dark, "scrollbarThumb")
	darkBorder := p5RethemeFg(t, dark, "border")
	darkCode := p5RethemeFg(t, dark, "mdCode")
	darkKeyword := p5RethemeFg(t, dark, "syntaxKeyword")
	darkWarning := p5RethemeFg(t, dark, "warning")
	darkLink := p5RethemeFg(t, dark, "mdLink")
	for _, want := range []string{darkSearchBg, darkSearchFg, darkSelectedBg, darkTrack, darkBorder, darkCode, darkKeyword, darkWarning, darkLink} {
		if !strings.Contains(before, want) {
			t.Fatalf("before frame missing dark ANSI %q:\n%s", want, before)
		}
	}
	if !strings.Contains(before, darkThumb) && !strings.Contains(before, darkTrack) {
		t.Fatalf("before frame missing scrollbar ANSI:\n%s", before)
	}
	if !strings.Contains(tui.StripTerminalSequences(before), "retheme") {
		t.Fatalf("before frame missing query text:\n%s", before)
	}
	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	light := runner.Surface().Theme()
	if got := alt.Search().Query(); got != "retheme" {
		t.Fatalf("query after retheme = %q want retheme", got)
	}
	if got := alt.Search().Current(); got != searchCurrent {
		t.Fatalf("search current after retheme = %d want %d", got, searchCurrent)
	}
	nextStart, nextEnd, nextOk := alt.Selection().Range()
	if !nextOk {
		t.Fatal("selection lost after retheme")
	}
	if nextStart != selectionStart || nextEnd != selectionEnd {
		t.Fatalf("selection after retheme = %+v..%+v want %+v..%+v", nextStart, nextEnd, selectionStart, selectionEnd)
	}
	after := fullscreenRenderOutput(runner, terminal)
	lightSearchBg := p5RethemeBg(t, light, "searchMatchBg")
	lightSearchFg := p5RethemeFg(t, light, "searchMatchText")
	lightSelectedBg := p5RethemeBg(t, light, "selectedBg")
	lightTrack := p5RethemeFg(t, light, "scrollbarTrack")
	lightBorder := p5RethemeFg(t, light, "border")
	lightCode := p5RethemeFg(t, light, "mdCode")
	lightKeyword := p5RethemeFg(t, light, "syntaxKeyword")
	lightWarning := p5RethemeFg(t, light, "warning")
	lightLink := p5RethemeFg(t, light, "mdLink")
	for _, want := range []string{lightSearchBg, lightSearchFg, lightSelectedBg, lightTrack, lightBorder, lightCode, lightKeyword, lightWarning, lightLink} {
		if !strings.Contains(after, want) {
			t.Fatalf("after frame missing light ANSI %q:\n%s", want, after)
		}
	}
	for _, stale := range []string{darkSearchBg, darkSelectedBg, darkTrack, darkBorder, darkCode, darkKeyword, darkWarning, darkLink} {
		if strings.Contains(after, stale) && stale != lightSearchBg && stale != lightSelectedBg && stale != lightTrack && stale != lightBorder && stale != lightCode && stale != lightKeyword && stale != lightWarning && stale != lightLink {
			t.Fatalf("after frame kept dark ANSI %q:\n%s", stale, after)
		}
	}
	if !strings.Contains(tui.StripTerminalSequences(after), "retheme") {
		t.Fatalf("after frame missing query text:\n%s", after)
	}
	plainAfter := tui.StripTerminalSequences(after)
	if !strings.Contains(plainAfter, "[image:") {
		t.Fatalf("after frame missing image placeholder:\n%s", after)
	}
	if !strings.Contains(plainAfter, "mermaid:") {
		t.Fatalf("after frame missing mermaid warning:\n%s", after)
	}
	altTheme := alt.Theme()
	if altTheme == nil || altTheme.Name != "light" {
		t.Fatalf("alt theme = %+v want light", altTheme)
	}
	alt.SetTheme(nil)
	if alt.Theme() == nil || alt.Theme().Name != "light" {
		t.Fatal("nil theme must not clear alt theme")
	}
	runner.Surface().Transcript().SetScrollbarTrackStyle(nil)
	runner.Surface().Transcript().SetScrollbarThumbStyle(nil)
	runner.Surface().Transcript().SetScrollbarStyles(nil, nil)
	alt.Search().Rebuild()
}

func TestP5RethemeConcurrentRenderRace(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	seedP5RethemeContent(t, runner)
	alt, ok := runner.view.(*tui.AltScreen)
	if !ok {
		t.Fatal("fullscreen runner should use alt screen")
	}
	alt.Selection().Begin(tui.SelectionPoint{Line: 0, Column: 0}, alt.Selection().Generated())
	alt.Selection().Update(tui.SelectionPoint{Line: 1000, Column: 1000})
	const iterations = 80
	var wait sync.WaitGroup
	start := make(chan struct{})
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < iterations; i++ {
			runner.view.RenderNow(true)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				_ = runner.ApplyTheme("light")
			} else {
				_ = runner.ApplyTheme("dark")
			}
		}
	}()
	close(start)
	wait.Wait()
	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("final ApplyTheme: %v", err)
	}
	frame := fullscreenRenderOutput(runner, terminal)
	light := runner.Surface().Theme()
	if !strings.Contains(frame, p5RethemeBg(t, light, "selectedBg")) {
		t.Fatalf("final frame missing light selection:\n%s", frame)
	}
	if !strings.Contains(tui.StripTerminalSequences(frame), "retheme marker") && !strings.Contains(tui.StripTerminalSequences(frame), "retheme") {
		t.Fatalf("final frame lost content:\n%s", frame)
	}
}
