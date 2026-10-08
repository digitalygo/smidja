package interactive

import (
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestP5MarkdownRethemeViaCurrentTokens(t *testing.T) {
	registry := tui.NewThemeRegistry("", "", tui.ColorModeTrueColor)
	dark, err := registry.SetTheme("dark")
	if err != nil {
		t.Fatalf("dark: %v", err)
	}
	light, err := registry.SetTheme("light")
	if err != nil {
		t.Fatalf("light: %v", err)
	}
	text := "```go\npackage main\nfunc hello() string { return \"hi\" }\n```\n" +
		"```mermaid\ngraph TD\n  A[Start] --> B[End]\n```\n" +
		"```mermaid\ngraph TD\n  A --> B\n  B --> A\n```\n" +
		"math $\\alpha$ inline\n$$\n\\alpha\n$$\n" +
		"![diagram](missing-image.png)\n"
	markdown := NewMarkdown(text, 0, 0, dark, MarkdownStyle{}, false)
	before := strings.Join(markdown.Render(80), "\n")
	darkKeyword, _ := dark.GetFgAnsi("syntaxKeyword")
	darkBorder, _ := dark.GetFgAnsi("border")
	darkCode, _ := dark.GetFgAnsi("mdCode")
	darkWarning, _ := dark.GetFgAnsi("warning")
	darkLink, _ := dark.GetFgAnsi("mdLink")
	for _, want := range []string{darkKeyword, darkBorder, darkCode, darkWarning, darkLink} {
		if !strings.Contains(before, want) {
			t.Fatalf("before missing %q:\n%s", want, before)
		}
	}
	markdown.SetTheme(light)
	after := strings.Join(markdown.Render(80), "\n")
	lightKeyword, _ := light.GetFgAnsi("syntaxKeyword")
	lightBorder, _ := light.GetFgAnsi("border")
	lightCode, _ := light.GetFgAnsi("mdCode")
	lightWarning, _ := light.GetFgAnsi("warning")
	lightLink, _ := light.GetFgAnsi("mdLink")
	for _, want := range []string{lightKeyword, lightBorder, lightCode, lightWarning, lightLink} {
		if !strings.Contains(after, want) {
			t.Fatalf("after missing %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, darkKeyword) && darkKeyword != lightKeyword {
		t.Fatalf("after kept dark syntax:\n%s", after)
	}
	plain := tui.StripTerminalSequences(after)
	if !strings.Contains(plain, "[image:") {
		t.Fatalf("after missing image placeholder:\n%s", after)
	}
	if !strings.Contains(plain, "mermaid:") {
		t.Fatalf("after missing warning:\n%s", after)
	}
}

func TestP5SurfaceScrollbarRetheme(t *testing.T) {
	registry := tui.NewThemeRegistry("", "", tui.ColorModeTrueColor)
	dark, err := registry.SetTheme("dark")
	if err != nil {
		t.Fatalf("dark: %v", err)
	}
	light, err := registry.SetTheme("light")
	if err != nil {
		t.Fatalf("light: %v", err)
	}
	surface := NewSurface(SurfaceOptions{Theme: dark, Keybindings: mustKeys(t)})
	assistant := surface.StartAssistantTurn()
	assistant.AppendText(strings.Repeat("scroll line\n", 60))
	surface.EndAssistantTurn("stop", "")
	transcript := surface.Transcript()
	transcript.SetScrollbar(tui.ScrollbarAlways)
	darkTrack, _ := dark.GetFgAnsi("scrollbarTrack")
	lightTrack, _ := light.GetFgAnsi("scrollbarTrack")
	frameBefore := surface.RenderDocument(80)
	joinedBefore := strings.Join(frameBefore, "\n")
	if !strings.Contains(joinedBefore, darkTrack) {
		t.Fatalf("before missing dark track:\n%s", joinedBefore)
	}
	surface.SetTheme(light)
	frameAfter := surface.RenderDocument(80)
	joinedAfter := strings.Join(frameAfter, "\n")
	if !strings.Contains(joinedAfter, lightTrack) {
		t.Fatalf("after missing light track:\n%s", joinedAfter)
	}
	if strings.Contains(joinedAfter, darkTrack) && darkTrack != lightTrack {
		t.Fatalf("after kept dark track:\n%s", joinedAfter)
	}
	surface.SetTheme(nil)
	if surface.Theme() != light {
		t.Fatal("nil theme must not clear surface")
	}
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		for i := 0; i < 50; i++ {
			_ = surface.RenderDocument(80)
		}
	}()
	go func() {
		defer wait.Done()
		for i := 0; i < 50; i++ {
			if i%2 == 0 {
				surface.SetTheme(light)
			} else {
				surface.SetTheme(dark)
			}
		}
	}()
	wait.Wait()
	surface.SetTheme(light)
}
