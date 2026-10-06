package interactive

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type frameComponent struct {
	text string
}

func (c *frameComponent) Render(width int) []string { return []string{c.text} }

func (c *frameComponent) Invalidate() {}

type disposableFrameComponent struct {
	text    string
	mu      sync.Mutex
	dispose int
}

func (c *disposableFrameComponent) Render(width int) []string { return []string{c.text} }

func (c *disposableFrameComponent) Invalidate() {}

func (c *disposableFrameComponent) Dispose() {
	c.mu.Lock()
	c.dispose++
	c.mu.Unlock()
}

func (c *disposableFrameComponent) DisposeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dispose
}

func TestSanitizeComponentFrame(t *testing.T) {
	input := []string{"\x1b[31mred\x1b[0m \x1b[2Jclear \x1b]8;;https://example.com\x07link\x1b]8;;\x07 \x1b]8;;javascript:alert(1)\x07bad\x1b]8;;\x07 \x1b]52;c;cGF5bG9hZA==\x07 \x1b_Ga=T,f=32,s=4;ZGF0YQ==\x1b\\ \x00\x07tab\t"}
	cleaned := SanitizeComponentFrame(input)
	if len(cleaned) != 1 {
		t.Fatalf("cleaned length = %d", len(cleaned))
	}
	line := cleaned[0]
	for _, forbidden := range []string{"\x1b[2J", "javascript", "\x1b]52", "\x1b_G", "\x00"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("frame %q still contains %q", line, forbidden)
		}
	}
	if !strings.Contains(line, "\x1b[31mred") || !strings.Contains(line, "\x1b[0m") {
		t.Fatalf("permitted SGR was stripped: %q", line)
	}
	if !strings.Contains(line, "https://example.com") {
		t.Fatalf("safe OSC8 was stripped: %q", line)
	}
	if !strings.Contains(line, tabReplacement) {
		t.Fatalf("tab was not expanded: %q", line)
	}
	plain := tui.StripTerminalSequences(line)
	if !strings.Contains(plain, "red clear link bad") {
		t.Fatalf("visible text after sanitize = %q", plain)
	}
	if got := SanitizeComponentFrame(nil); got != nil {
		t.Fatalf("nil frame = %v", got)
	}
}

func TestSanitizeComponentFrameEdgeSequences(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		contains string
		absent   string
	}{
		{name: "c1 csi", input: "\x9b31mred \x9b2Jclear", contains: "red clear", absent: "\x9b"},
		{name: "c1 osc", input: "\x9d52;c;payload\x9cvisible", contains: "visible", absent: "payload"},
		{name: "escape intermediate", input: "\x1b(Bplain", contains: "plain", absent: "("},
		{name: "unterminated osc", input: "\x1b]8;;https://example.com", contains: "", absent: "example"},
		{name: "osc8 with st", input: "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", contains: "https://example.com", absent: ""},
		{name: "osc8 without second semicolon", input: "\x1b]8;broken\x07text", contains: "text", absent: "broken"},
		{name: "incomplete csi", input: "\x1b[12", absent: "12"},
		{name: "truncated escape", input: "\x1b", absent: "x"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cleaned := SanitizeComponentFrame([]string{testCase.input})[0]
			if testCase.contains != "" && !strings.Contains(cleaned, testCase.contains) {
				t.Fatalf("cleaned %q does not contain %q", cleaned, testCase.contains)
			}
			if testCase.absent != "" && strings.Contains(cleaned, testCase.absent) {
				t.Fatalf("cleaned %q still contains %q", cleaned, testCase.absent)
			}
		})
	}
}

func TestSurfaceHeaderBeforeTranscriptAndCustomFooter(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.AddUserMessage("transcript body")
	surface.SetHeaderComponent(&frameComponent{text: "HEADER LINE"})
	surface.SetFooterComponent(&frameComponent{text: "CUSTOM FOOTER"})
	lines := renderPlainFrame(surface, 40, 18)
	headerRow := indexOfFragment(t, lines, "HEADER LINE")
	bodyRow := indexOfFragment(t, lines, "transcript body")
	footerRow := indexOfFragment(t, lines, "CUSTOM FOOTER")
	if headerRow >= bodyRow {
		t.Fatalf("header must precede transcript: header=%d body=%d", headerRow, bodyRow)
	}
	if footerRow <= bodyRow {
		t.Fatalf("custom footer must be in the dock below the transcript: footer=%d body=%d", footerRow, bodyRow)
	}
	if strings.Contains(strings.Join(lines, "\n"), "~/tester") {
		t.Fatalf("built-in footer still visible under a custom footer:\n%s", strings.Join(lines, "\n"))
	}
	previous := surface.SetFooterComponent(nil)
	if previous == nil {
		t.Fatal("restoring the footer must return the custom component")
	}
	if got := surface.SetHeaderComponent(nil); got == nil {
		t.Fatal("restoring the header must return the custom component")
	}
	lines = renderPlainFrame(surface, 40, 18)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "HEADER LINE") || strings.Contains(joined, "CUSTOM FOOTER") {
		t.Fatalf("defaults were not restored:\n%s", joined)
	}
	if !strings.Contains(joined, "ctrl+o tools") {
		t.Fatalf("built-in footer was not restored:\n%s", joined)
	}
	surface.SetHeaderComponent(&frameComponent{text: "DOC HEADER"})
	surface.SetFooterComponent(&frameComponent{text: "DOC FOOTER"})
	document := strings.Join(plainLines(surface.RenderDocument(40)), "\n")
	if !strings.Contains(document, "DOC HEADER") || !strings.Contains(document, "DOC FOOTER") {
		t.Fatalf("live document rendering dropped extension slots:\n%s", document)
	}
	surface.SetHeaderComponent(nil)
	surface.SetFooterComponent(nil)
}

func TestSurfaceWidgetComponentsDisposeAndRestore(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	first := &disposableFrameComponent{text: "WIDGET ONE"}
	surface.SetWidgetComponent("key", first)
	lines := renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "WIDGET ONE") {
		t.Fatalf("widget component not rendered:\n%s", strings.Join(lines, "\n"))
	}
	second := &disposableFrameComponent{text: "WIDGET TWO"}
	surface.SetWidgetComponent("key", second)
	if first.DisposeCount() != 1 {
		t.Fatalf("replaced widget dispose count = %d, want 1", first.DisposeCount())
	}
	surface.ClearWidgetComponent("key")
	if second.DisposeCount() != 1 {
		t.Fatalf("cleared widget dispose count = %d, want 1", second.DisposeCount())
	}
	lines = renderPlainFrame(surface, 40, 18)
	if strings.Contains(strings.Join(lines, "\n"), "WIDGET TWO") {
		t.Fatalf("cleared widget still rendered:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetWidget("strings", []string{"STRING WIDGET"})
	surface.SetWidgetComponent("component", &frameComponent{text: "COMPONENT WIDGET"})
	lines = renderPlainFrame(surface, 40, 18)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "STRING WIDGET") || !strings.Contains(joined, "COMPONENT WIDGET") {
		t.Fatalf("string and component widgets must coexist:\n%s", joined)
	}
}

func TestSurfaceCustomRenderersAndFallback(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.ReplaceTranscript(nil)
	surface.SetRenderMetadata(RenderMetadata{Cwd: "/work", SessionID: "session-1", Model: "model-x", ThinkingLevel: "high"})
	surface.RenderFrame(50, 18)
	var contextWidth int
	surface.SetCustomMessageRenderer("notify", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		contextWidth = ctx.Width
		if ctx.Theme == nil {
			t.Error("renderer context is missing the theme")
		}
		return &frameComponent{text: "CUSTOM MESSAGE " + entry.Text}
	})
	surface.AddCustomMessage(CustomEntryView{CustomType: "notify", Text: "hello"})
	surface.RenderFrame(50, 18)
	if contextWidth != 50 {
		t.Fatalf("renderer context width = %d, want the last frame width 50", contextWidth)
	}
	lines := renderPlainFrame(surface, 50, 20)
	if !strings.Contains(strings.Join(lines, "\n"), "CUSTOM MESSAGE hello") {
		t.Fatalf("custom message renderer not invoked:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetCustomEntryRenderer("audit", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		return &frameComponent{text: "CUSTOM ENTRY"}
	})
	surface.AddCustomEntry(CustomEntryView{CustomType: "audit", Data: `{"a":1}`})
	lines = renderPlainFrame(surface, 50, 20)
	if !strings.Contains(strings.Join(lines, "\n"), "CUSTOM ENTRY") {
		t.Fatalf("custom entry renderer not invoked:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetToolsExpanded(true)
	surface.AddCustomMessage(CustomEntryView{CustomType: "unknown", Label: "fallback label", Text: "fallback text"})
	lines = renderPlainFrame(surface, 50, 24)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "fallback label") || !strings.Contains(joined, "fallback text") {
		t.Fatalf("fallback custom block missing:\n%s", joined)
	}
	surface.RemoveCustomMessageRenderer("notify")
	surface.ClearCustomRenderers()
	if surface.MarkdownTransformer() != nil {
		t.Fatal("clearing renderers must not touch transformers")
	}
}

func TestSurfaceExtensionAccessorsAndCallbacks(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.RenderFrame(64, 18)
	if surface.EditorWidth() != 64 {
		t.Fatalf("EditorWidth = %d, want 64", surface.EditorWidth())
	}
	if surface.MarkdownTransformer() != nil {
		t.Fatal("transformer must start nil")
	}
	if transformed := surface.transformMarkdown("plain", MarkdownTransformContext{}); transformed != "plain" {
		t.Fatalf("identity transform = %q", transformed)
	}
	surface.SetMarkdownTransformer(func(markdown string, ctx MarkdownTransformContext) string {
		return "[" + ctx.Kind + "]" + markdown
	})
	if transformed := surface.transformMarkdown("plain", MarkdownTransformContext{Kind: "user"}); transformed != "[user]plain" {
		t.Fatalf("registered transform = %q", transformed)
	}
	surface.SetRenderMetadata(RenderMetadata{Cwd: "/work", SessionID: "s1", Model: "m1", ThinkingLevel: "high"})
	metadata := surface.ExtensionMetadata()
	if metadata.Cwd != "/work" || metadata.SessionID != "s1" || metadata.Model != "m1" || metadata.ThinkingLevel != "high" {
		t.Fatalf("metadata = %+v", metadata)
	}
	var changed, submitted string
	surface.SetOnSubmit(func(text string) { submitted = text })
	surface.NotifyEditorChange("changed")
	surface.NotifyEditorSubmit("submitted")
	if changed != "" {
		t.Fatalf("NotifyEditorChange should not call the submit callback: %q", changed)
	}
	if submitted != "submitted" {
		t.Fatalf("NotifyEditorSubmit = %q", submitted)
	}
	surface.SetCustomEntryRenderer("audit", func(ctx CustomRenderContext, entry CustomEntryView) tui.Component {
		return &frameComponent{text: "ENTRY RENDERER"}
	})
	surface.RemoveCustomEntryRenderer("audit")
	surface.AddCustomEntry(CustomEntryView{CustomType: "audit", Data: "entry data text"})
	lines := renderPlainFrame(surface, 50, 24)
	if strings.Contains(strings.Join(lines, "\n"), "ENTRY RENDERER") {
		t.Fatal("removed entry renderer still invoked")
	}
	surface.SetToolsExpanded(true)
	lines = renderPlainFrame(surface, 50, 24)
	if !strings.Contains(strings.Join(lines, "\n"), "entry data text") {
		t.Fatalf("entry fallback did not use the string data:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetWidgetComponent("panel", &frameComponent{text: "PANEL"})
	if !surface.ext.widgets.Has("panel") {
		t.Fatal("component widget panel lost the registration")
	}
	surface.ClearWidgetComponent("panel")
	if surface.ext.widgets.Has("panel") {
		t.Fatal("cleared widget is still registered")
	}
	surface.AddCustomMessage(CustomEntryView{CustomType: "", Text: "untyped"})
}

func TestSurfaceMarkdownTransformerStreamsAndReplays(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	var widths []int
	var streaming []bool
	surface.SetMarkdownTransformer(func(markdown string, ctx MarkdownTransformContext) string {
		widths = append(widths, ctx.Width)
		streaming = append(streaming, ctx.Streaming)
		return strings.ReplaceAll(markdown, "dog", "cat") + "\x1b[31m"
	})
	surface.StartAssistantTurn()
	surface.AppendAssistantText("a dog")
	surface.SetThinkingExpanded(true)
	surface.AppendAssistantThinking("think dog")
	lines := renderPlainFrame(surface, 44, 20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "a cat") || !strings.Contains(joined, "think cat") {
		t.Fatalf("streaming transformer did not apply to text and thinking:\n%s", joined)
	}
	if strings.Contains(tui.StripTerminalSequences(joined), "\x1b") {
		t.Fatalf("transformed output kept control bytes:\n%s", joined)
	}
	if len(widths) == 0 {
		t.Fatal("transformer never received a width")
	}
	for _, width := range widths {
		if width <= 0 {
			t.Fatalf("transformer width = %d, want positive", width)
		}
	}
	sawStreaming := false
	for _, value := range streaming {
		sawStreaming = sawStreaming || value
	}
	if !sawStreaming {
		t.Fatal("transformer never observed streaming assistant content")
	}
	surface.SetToolsExpanded(true)
	surface.ReplaceTranscript([]TranscriptEntry{
		{Kind: ReplayUser, Text: "a dog"},
		{Kind: ReplayAssistant, Parts: []AssistantMessagePart{{Text: "a dog"}}},
		{Kind: ReplayCustom, CustomType: "note", CustomKind: CustomKindEntry, CustomLabel: "note", Text: "a dog"},
	})
	lines = renderPlainFrame(surface, 44, 24)
	joined = strings.Join(lines, "\n")
	if strings.Count(joined, "a cat") < 3 || strings.Contains(joined, "a dog") {
		t.Fatalf("replayed content was not transformed as expected:\n%s", joined)
	}
}

func TestSurfaceWorkingVisibilityIndicatorAndThinkingLabel(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester", SpinnerIndicator: &tui.LoaderIndicator{Frames: []string{"A", "B"}, IntervalMs: 40}})
	surface.SetWorking(true)
	lines := renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "Working") {
		t.Fatalf("working row missing:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetWorkingVisible(false)
	lines = renderPlainFrame(surface, 40, 18)
	if strings.Contains(strings.Join(lines, "\n"), "Working") {
		t.Fatalf("working row visible after SetWorkingVisible(false):\n%s", strings.Join(lines, "\n"))
	}
	surface.SetWorkingVisible(true)
	surface.SetWorkingIndicator([]string{}, 0)
	lines = renderPlainFrame(surface, 40, 18)
	if strings.Contains(strings.Join(lines, "\n"), "Working") {
		t.Fatalf("empty frames did not hide the indicator:\n%s", strings.Join(lines, "\n"))
	}
	frames, _, set := surface.Status().Indicator()
	if !set || len(frames) != 0 {
		t.Fatalf("indicator state = %v set=%v, want explicitly empty", frames, set)
	}
	surface.SetWorkingIndicator([]string{"●"}, 25*time.Millisecond)
	lines = renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "●") {
		t.Fatalf("custom frame not rendered:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetWorkingIndicator(nil, 0)
	frames, _, set = surface.Status().Indicator()
	if set || len(frames) == 0 {
		t.Fatalf("reset indicator = %v set=%v, want defaults restored", frames, set)
	}

	surface.StartAssistantTurn()
	surface.AppendAssistantThinking("secret plan")
	surface.SetThinkingExpanded(false)
	lines = renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "Thinking...") {
		t.Fatalf("default hidden thinking label missing:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetHiddenThinkingLabel("Deep thought")
	lines = renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "Deep thought") {
		t.Fatalf("hidden thinking label not applied:\n%s", strings.Join(lines, "\n"))
	}
	surface.SetHiddenThinkingLabel("")
	lines = renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "Thinking...") {
		t.Fatalf("hidden thinking label did not reset:\n%s", strings.Join(lines, "\n"))
	}
}

func TestSurfaceReplayedThinkingUsesHiddenLabel(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.SetHiddenThinkingLabel("Quietly considering")
	surface.ReplaceTranscript([]TranscriptEntry{
		{Kind: ReplayAssistant, Parts: []AssistantMessagePart{{Thinking: true, Text: "secret"}}},
	})
	lines := renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "Quietly considering") {
		t.Fatalf("replayed thinking block ignored the hidden label:\n%s", strings.Join(lines, "\n"))
	}
}

func TestSurfaceActiveEditorSwap(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	custom := &frameComponent{text: "CUSTOM EDITOR"}
	previous := surface.ReplaceActiveEditor(custom)
	if previous != surface.Editor() {
		t.Fatal("first replacement should return the default editor")
	}
	if surface.ActiveEditor() != custom {
		t.Fatal("active editor was not swapped")
	}
	lines := renderPlainFrame(surface, 40, 18)
	if !strings.Contains(strings.Join(lines, "\n"), "CUSTOM EDITOR") {
		t.Fatalf("custom editor not rendered:\n%s", strings.Join(lines, "\n"))
	}
	previous = surface.ReplaceActiveEditor(surface.Editor())
	if previous != custom {
		t.Fatal("restoring should return the custom editor")
	}
	if surface.ActiveEditor() != surface.Editor() {
		t.Fatal("default editor was not restored")
	}
}
