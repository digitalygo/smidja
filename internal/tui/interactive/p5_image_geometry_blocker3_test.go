package interactive

import (
	"reflect"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func imageBlockResolver(columns, rows int, label string) tui.ImageResolver {
	return func(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
		return tui.ResolvedImage{CacheKey: source, Columns: columns, Rows: rows, Label: label, Protocol: tui.GraphicsITerm2}, true
	}
}

func plainTestLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimSpace(tui.StripTerminalSequences(line))
	}
	return out
}

func assertPlaceholderOnlyLines(t *testing.T, lines []string) {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, "\x1b_G") || strings.Contains(line, "\x1b]1337") || strings.Contains(line, "\x1b_pi") {
			t.Fatalf("line %d leaked graphics: %q", i, line)
		}
	}
}

func TestMarkdownInlineImagePromotesToBlockWide(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("before ![alt text](pic.png) after", 0, 0, theme, MarkdownStyle{}, false)
	markdown.SetImageResolver(imageBlockResolver(20, 2, "[image: alt text]"))
	rich := markdown.RenderRich(80)
	assertPlaceholderOnlyLines(t, rich.Lines)
	lines := plainTestLines(rich.Lines)
	want := []string{"before", "[image: alt text]", "", "after"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("plain lines = %q, want %q", lines, want)
	}
	if len(rich.Images) != 1 {
		t.Fatalf("expected one image descriptor, got %d", len(rich.Images))
	}
	descriptor := rich.Images[0]
	if descriptor.Row != 1 || descriptor.RowSpan != 2 || descriptor.Columns != 20 || descriptor.OffsetX != 0 {
		t.Fatalf("unexpected descriptor %+v", descriptor)
	}
	if descriptor.Row+descriptor.RowSpan != 3 || lines[descriptor.Row+descriptor.RowSpan] != "after" {
		t.Fatalf("text after image must start after the reserved rows: %q", lines)
	}
}

func TestMarkdownInlineImagePromotesToBlockNarrow(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("alpha beta gamma ![x](pic.png) delta", 0, 0, theme, MarkdownStyle{}, false)
	markdown.SetImageResolver(imageBlockResolver(10, 2, "[image: x]"))
	rich := markdown.RenderRich(10)
	assertPlaceholderOnlyLines(t, rich.Lines)
	lines := plainTestLines(rich.Lines)
	want := []string{"alpha beta", "gamma", "[image: x]", "", "delta"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("plain lines = %q, want %q", lines, want)
	}
	if len(rich.Images) != 1 {
		t.Fatalf("expected one image descriptor, got %d", len(rich.Images))
	}
	descriptor := rich.Images[0]
	if descriptor.Row != 2 || descriptor.RowSpan != 2 || descriptor.Columns != 10 {
		t.Fatalf("unexpected descriptor %+v", descriptor)
	}
	if descriptor.Row+descriptor.RowSpan != 4 || lines[4] != "delta" {
		t.Fatalf("following content must start after reserved rows: %q", lines)
	}
}

func TestMarkdownInlineImageKeepsMultipleImagesOrdered(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("one ![a](a.png) two ![b](b.png) three", 0, 0, theme, MarkdownStyle{}, false)
	markdown.SetImageResolver(func(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
		return tui.ResolvedImage{CacheKey: source, Columns: 12, Rows: 2, Label: "[image: " + alt + "]", Protocol: tui.GraphicsITerm2}, true
	})
	rich := markdown.RenderRich(80)
	assertPlaceholderOnlyLines(t, rich.Lines)
	lines := plainTestLines(rich.Lines)
	want := []string{"one", "[image: a]", "", "two", "[image: b]", "", "three"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("plain lines = %q, want %q", lines, want)
	}
	if len(rich.Images) != 2 {
		t.Fatalf("expected two descriptors, got %d", len(rich.Images))
	}
	for index, descriptor := range rich.Images {
		if lines[descriptor.Row] != want[1+index*3] {
			t.Fatalf("descriptor %d row %d does not match its label line", index, descriptor.Row)
		}
		for row := descriptor.Row + 1; row < descriptor.Row+descriptor.RowSpan; row++ {
			if lines[row] != "" {
				t.Fatalf("reserved row %d must be empty, got %q", row, lines[row])
			}
		}
	}
}

func TestMarkdownImageResolverLabelFallback(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("![caption](pic.png)", 0, 0, theme, MarkdownStyle{}, false)
	markdown.SetImageResolver(imageBlockResolver(12, 1, ""))
	rich := markdown.RenderRich(40)
	if len(rich.Images) != 1 {
		t.Fatalf("expected one descriptor, got %d", len(rich.Images))
	}
	joined := strings.Join(plainTestLines(rich.Lines), "\n")
	if !strings.Contains(joined, "[image: caption] (pic.png)") {
		t.Fatalf("empty resolver label must fall back to the alt and source: %q", joined)
	}
}

func TestMarkdownImageColumnsClampToAvailableWidth(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("![wide](pic.png)", 0, 0, theme, MarkdownStyle{}, false)
	markdown.SetImageResolver(imageBlockResolver(200, 1, "[image: wide]"))
	rich := markdown.RenderRich(24)
	if len(rich.Images) != 1 {
		t.Fatalf("expected one descriptor, got %d", len(rich.Images))
	}
	if rich.Images[0].Columns != 24 {
		t.Fatalf("descriptor columns = %d, want 24", rich.Images[0].Columns)
	}
}

func TestImageTokenParsingGuards(t *testing.T) {
	if _, _, _, ok := nextImageToken(imageTokenBoundary + "0"); ok {
		t.Fatal("missing closing boundary should not parse")
	}
	if _, _, _, ok := nextImageToken(imageTokenBoundary + "x" + imageTokenBoundary); ok {
		t.Fatal("non-numeric index should not parse")
	}
	if _, _, _, ok := nextImageToken(imageTokenBoundary + "-1" + imageTokenBoundary); ok {
		t.Fatal("negative index should not parse")
	}
	index, before, after, ok := nextImageToken("pre" + imageToken(3) + "post")
	if !ok || index != 3 || before != "pre" || after != "post" {
		t.Fatalf("token parse = %d %q %q %v", index, before, after, ok)
	}
	if _, _, _, ok := nextImageToken("plain"); ok {
		t.Fatal("plain line should not parse as a token")
	}
}

func TestAnsiSequenceLengthBranches(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
		ok   bool
	}{
		{name: "bare escape", text: "\x1b", want: 1, ok: true},
		{name: "incomplete csi", text: "\x1b[", want: 0, ok: false},
		{name: "osc bel", text: "\x1b]0;title\x07rest", want: 10, ok: true},
		{name: "osc st", text: "\x1b]0;title\x1b\\rest", want: 11, ok: true},
		{name: "unterminated osc", text: "\x1b]unterminated", want: 0, ok: false},
		{name: "two byte escape", text: "\x1bMrest", want: 2, ok: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			length, ok := ansiSequenceLength(testCase.text, 0)
			if length != testCase.want || ok != testCase.ok {
				t.Fatalf("ansiSequenceLength = %d,%v want %d,%v", length, ok, testCase.want, testCase.ok)
			}
		})
	}
	if length, ok := ansiSequenceLength("plain", 0); length != 0 || ok {
		t.Fatalf("plain text = %d,%v want 0,false", length, ok)
	}
}

func TestMarkdownInlineImagePlainResolversKeepContent(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("before ![missing](missing.png) after", 0, 0, theme, MarkdownStyle{}, false)
	markdown.SetImageResolver(nil)
	rich := markdown.RenderRich(80)
	assertPlaceholderOnlyLines(t, rich.Lines)
	if len(rich.Images) != 0 {
		t.Fatalf("plain rendering must not emit descriptors: %+v", rich.Images)
	}
	joined := strings.Join(plainTestLines(rich.Lines), "\n")
	for _, want := range []string{"before", "after", "missing.png"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plain rendering lost %q: %q", want, joined)
		}
	}
}

func TestUserMessageImageBlockAccountsForPadding(t *testing.T) {
	theme := mustTheme(t)
	SetDefaultImageResolver(imageBlockResolver(10, 2, "[image: x]"))
	t.Cleanup(func() { SetDefaultImageResolver(nil) })
	message := NewUserMessage("before ![x](pic.png) after", theme, false)
	rich := message.RenderRich(30)
	assertPlaceholderOnlyLines(t, rich.Lines)
	lines := plainTestLines(rich.Lines)
	want := []string{"", "before", "[image: x]", "", "after", ""}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("plain lines = %q, want %q", lines, want)
	}
	if len(rich.Images) != 1 {
		t.Fatalf("expected one descriptor, got %d", len(rich.Images))
	}
	descriptor := rich.Images[0]
	if descriptor.Row != 2 || descriptor.RowSpan != 2 || descriptor.OffsetX != 1 {
		t.Fatalf("user block image geometry must include padding: %+v", descriptor)
	}
	if !strings.HasPrefix(rich.Lines[0], tui.OSC133PromptStart) {
		t.Fatalf("prompt marker missing: %q", rich.Lines[0])
	}
}

func TestAssistantMessageImageBlocksAccountForOffsets(t *testing.T) {
	theme := mustTheme(t)
	SetDefaultImageResolver(func(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
		return tui.ResolvedImage{CacheKey: source, Columns: 12, Rows: 2, Label: "[image: " + alt + "]", Protocol: tui.GraphicsITerm2}, true
	})
	t.Cleanup(func() { SetDefaultImageResolver(nil) })
	message := NewAssistantMessage(theme, false)
	message.AppendText("one ![a](a.png) two ![b](b.png) three")
	rich := message.RenderRich(60)
	assertPlaceholderOnlyLines(t, rich.Lines)
	lines := plainTestLines(rich.Lines)
	want := []string{"", "one", "[image: a]", "", "two", "[image: b]", "", "three"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("plain lines = %q, want %q", lines, want)
	}
	if len(rich.Images) != 2 {
		t.Fatalf("expected two descriptors, got %d", len(rich.Images))
	}
	first, second := rich.Images[0], rich.Images[1]
	if first.Row != 2 || second.Row != 5 {
		t.Fatalf("assistant offsets wrong: %+v", rich.Images)
	}
	if first.OffsetX != 1 || second.OffsetX != 1 {
		t.Fatalf("assistant markdown padding not applied: %+v", rich.Images)
	}
	if second.Row != first.Row+first.RowSpan+1 {
		t.Fatalf("second image must follow the first block and its text: %+v", rich.Images)
	}
}
