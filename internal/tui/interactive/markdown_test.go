package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func renderMarkdownPlain(t *testing.T, text string, width int) []string {
	t.Helper()
	theme := mustTheme(t)
	markdown := NewMarkdown(text, 0, 0, theme, MarkdownStyle{}, false)
	lines := markdown.Render(width)
	result := make([]string, len(lines))
	for index, line := range lines {
		result[index] = strings.TrimRight(tui.StripTerminalSequences(line), " ")
	}
	return result
}

func TestParseMarkdownBlocksTerminatesOnHeadings(t *testing.T) {
	blocks := parseMarkdownBlocks("# Title\n\nbody text")
	if len(blocks) != 3 {
		t.Fatalf("expected heading, space and paragraph blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].kind != mdHeading || blocks[0].text != "Title" || blocks[0].level != 1 {
		t.Fatalf("heading block = %+v", blocks[0])
	}
	if blocks[2].kind != mdParagraph || blocks[2].text != "body text" {
		t.Fatalf("paragraph block = %+v", blocks[2])
	}
}

func TestMarkdownHeadings(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trailing hashes stripped", input: "# Title #", want: "Title"},
		{name: "hash inside word kept", input: "# C#", want: "C#"},
		{name: "empty heading", input: "#", want: ""},
		{name: "hashes only stripped", input: "## ###", want: ""},
		{name: "hashtag is paragraph", input: "#hashtag", want: "#hashtag"},
		{name: "seven hashes is paragraph", input: "####### x", want: "####### x"},
		{name: "level three prefix", input: "### Deep", want: "### Deep"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := renderMarkdownPlain(t, testCase.input, 40)
			if len(lines) == 0 {
				t.Fatalf("no output for %q", testCase.input)
			}
			if lines[0] != testCase.want {
				t.Fatalf("heading %q rendered %q, want %q", testCase.input, lines[0], testCase.want)
			}
		})
	}
}

func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func TestMarkdownFenceRules(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantCodeLines []string
	}{
		{
			name:          "short close does not end fence",
			input:         "```\nstill code\n``",
			wantCodeLines: []string{"```", "  still code", "  ``", "```"},
		},
		{
			name:          "longer close ends fence",
			input:         "```\ncode\n`````\n",
			wantCodeLines: []string{"```", "  code", "```"},
		},
		{
			name:          "indented close does not end fence",
			input:         "```\n    ```\ncode\n```\n",
			wantCodeLines: []string{"```", "      ```", "  code", "```"},
		},
		{
			name:          "tilde fence with backticks in info",
			input:         "~~~go\nx := 1\n~~~\n",
			wantCodeLines: []string{"```go", "  x := 1", "```"},
		},
		{
			name:          "language label kept",
			input:         "```go\nx := 1\n```\n",
			wantCodeLines: []string{"```go", "  x := 1", "```"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := trimTrailingBlanks(renderMarkdownPlain(t, testCase.input, 40))
			if len(lines) != len(testCase.wantCodeLines) {
				t.Fatalf("rendered %d lines, want %d: %#v", len(lines), len(testCase.wantCodeLines), lines)
			}
			for index, want := range testCase.wantCodeLines {
				if lines[index] != want {
					t.Fatalf("line %d = %q, want %q (all %#v)", index, lines[index], want, lines)
				}
			}
		})
	}
}

func TestMarkdownRuleDetection(t *testing.T) {
	if got := renderMarkdownPlain(t, "a---b", 20); got[0] != "a---b" {
		t.Fatalf("embedded dashes should stay a paragraph, got %q", got[0])
	}
	if got := renderMarkdownPlain(t, "***", 20); !strings.Contains(got[0], "─") {
		t.Fatalf("asterisk rule not rendered: %q", got[0])
	}
	if got := renderMarkdownPlain(t, "- - -", 20); !strings.Contains(got[0], "─") {
		t.Fatalf("spaced rule not rendered: %q", got[0])
	}
	if got := renderMarkdownPlain(t, "-*-", 20); strings.Contains(got[0], "───") {
		t.Fatalf("mixed markers should not be a rule: %q", got[0])
	}
}

func TestMarkdownTightList(t *testing.T) {
	lines := renderMarkdownPlain(t, "- one\n- two\n- three", 30)
	if len(lines) != 3 {
		t.Fatalf("tight list rendered %d lines, want 3: %#v", len(lines), lines)
	}
	if lines[0] != "- one" || lines[1] != "- two" || lines[2] != "- three" {
		t.Fatalf("tight list = %#v", lines)
	}
}

func TestMarkdownLooseList(t *testing.T) {
	lines := renderMarkdownPlain(t, "- one\n\n- two", 30)
	if len(lines) != 3 {
		t.Fatalf("loose list rendered %d lines, want 3: %#v", len(lines), lines)
	}
	if lines[0] != "- one" || lines[1] != "" || lines[2] != "- two" {
		t.Fatalf("loose list = %#v", lines)
	}
}

func TestMarkdownNestedList(t *testing.T) {
	lines := renderMarkdownPlain(t, "- parent\n    - child\n        - grandchild", 40)
	expected := []string{"- parent", "    - child", "        - grandchild"}
	if len(lines) != len(expected) {
		t.Fatalf("nested list rendered %d lines, want %d: %#v", len(lines), len(expected), lines)
	}
	for index, want := range expected {
		if lines[index] != want {
			t.Fatalf("nested line %d = %q, want %q (all %#v)", index, lines[index], want, lines)
		}
	}
}

func TestMarkdownTaskList(t *testing.T) {
	lines := renderMarkdownPlain(t, "- [ ] todo\n- [x] done", 30)
	if len(lines) != 2 {
		t.Fatalf("task list rendered %d lines: %#v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "- [ ] todo") {
		t.Fatalf("unchecked task = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "- [x] done") {
		t.Fatalf("checked task = %q", lines[1])
	}
}

func TestMarkdownOrderedList(t *testing.T) {
	lines := renderMarkdownPlain(t, "1. one\n2. two", 30)
	if len(lines) != 2 || lines[0] != "1. one" || lines[1] != "2. two" {
		t.Fatalf("ordered list = %#v", lines)
	}
}

func TestMarkdownBlockquoteAndRule(t *testing.T) {
	lines := renderMarkdownPlain(t, "> quoted text\n\n---", 30)
	assertPlain := strings.Join(lines, "\n")
	if !strings.Contains(assertPlain, "quoted text") {
		t.Fatalf("quote not rendered: %#v", lines)
	}
	if !strings.Contains(assertPlain, "│") {
		t.Fatalf("quote border not rendered: %#v", lines)
	}
	if !strings.Contains(assertPlain, "─") {
		t.Fatalf("rule not rendered: %#v", lines)
	}
}

func TestMarkdownTableAlignsAndBorders(t *testing.T) {
	table := "| Name | Count |\n| :--- | ---: |\n| a | 1 |\n| longer | 22 |"
	lines := renderMarkdownPlain(t, table, 40)
	if len(lines) < 6 {
		t.Fatalf("table rendered %d lines: %#v", len(lines), lines)
	}
	structural := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		structural = append(structural, strings.TrimRight(line, " "))
	}
	width := tui.VisibleWidth(structural[0])
	for index, line := range structural {
		if got := tui.VisibleWidth(line); got != width {
			t.Fatalf("table line %d width %d, border width %d (line %q)", index, got, width, line)
		}
	}
	if !strings.HasSuffix(structural[3], "1 │") {
		t.Fatalf("right aligned numeric cell unexpected: %q", structural[3])
	}
}

func TestMarkdownTableFallsBackWhenNarrow(t *testing.T) {
	table := "| Name | Description |\n| --- | --- |\n| a | " + strings.Repeat("word ", 20) + "|"
	lines := renderMarkdownPlain(t, table, 24)
	if len(lines) == 0 {
		t.Fatal("narrow table produced no output")
	}
	for _, line := range lines {
		if tui.VisibleWidth(line) > 24 {
			t.Fatalf("narrow table line exceeds width: %q", line)
		}
	}
}

func TestMarkdownInlineStyles(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("Some **bold** and *italic* and `code` and ~~strike~~ text", 0, 0, theme, MarkdownStyle{}, false)
	lines := markdown.Render(60)
	plain := tui.StripTerminalSequences(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "Some bold and italic and code and strike text") {
		t.Fatalf("inline rendering = %q", plain)
	}
}

func TestMarkdownInlineMalformedTerminates(t *testing.T) {
	cases := []string{
		"**unclosed bold",
		"*unclosed italic",
		"`unclosed code",
		"[label](",
		"~~unclosed",
		strings.Repeat("**", 200) + "deep" + strings.Repeat("**", 200),
		strings.Repeat("[", 500),
	}
	for _, input := range cases {
		lines := renderMarkdownPlain(t, input, 40)
		if len(lines) == 0 {
			t.Fatalf("malformed input %q produced no output", input)
		}
	}
}

func TestMarkdownUnclosedFenceConsumesRest(t *testing.T) {
	lines := renderMarkdownPlain(t, "```go\nx := 1\ny := 2", 40)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "x := 1") || !strings.Contains(joined, "y := 2") {
		t.Fatalf("unclosed fence content lost: %#v", lines)
	}
}

func TestMarkdownNestedQuoteDepthBounded(t *testing.T) {
	input := strings.Repeat("> ", 40) + "deep"
	lines := renderMarkdownPlain(t, input, 60)
	if len(lines) == 0 {
		t.Fatal("deep nesting produced no output")
	}
}

func TestMarkdownHyperlinkSchemes(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("[safe](https://example.com) and [unsafe](javascript:alert(1))", 0, 0, theme, MarkdownStyle{}, true)
	rendered := strings.Join(markdown.Render(80), "\n")
	if !strings.Contains(rendered, tui.OSC8Hyperlink("", "https://example.com")) {
		t.Fatalf("safe link missing OSC 8 sequence: %q", rendered)
	}
	if strings.Contains(rendered, tui.OSC8Hyperlink("", "javascript:alert(1)")) {
		t.Fatalf("unsafe scheme leaked into OSC 8: %q", rendered)
	}
	if !strings.Contains(rendered, tui.OSC8Close) {
		t.Fatalf("safe link missing OSC 8 close: %q", rendered)
	}
	plain := tui.StripTerminalSequences(rendered)
	if !strings.Contains(plain, "safe") || !strings.Contains(plain, "unsafe") {
		t.Fatalf("link labels lost: %q", plain)
	}
	if !strings.Contains(plain, "javascript:alert(1)") {
		t.Fatalf("unsafe link should still show its target: %q", plain)
	}
}

func TestMarkdownHyperlinkCloseOrdering(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("[docs](https://example.com/docs) tail", 0, 0, theme, MarkdownStyle{}, true)
	rendered := strings.Join(markdown.Render(80), "\n")
	if !strings.Contains(rendered, tui.OSC8Hyperlink("", "https://example.com/docs")) {
		t.Fatalf("labelled link missing OSC 8 opener: %q", rendered)
	}
	closeIndex := strings.Index(rendered, tui.OSC8Close)
	if closeIndex < 0 {
		t.Fatalf("labelled link missing OSC 8 close: %q", rendered)
	}
	if suffixIndex := strings.Index(rendered, "(https://example.com/docs)"); suffixIndex < 0 || closeIndex > suffixIndex {
		t.Fatalf("close not before URL suffix: %q", rendered)
	}
	if proseIndex := strings.Index(rendered, "tail"); proseIndex < 0 || closeIndex > proseIndex {
		t.Fatalf("close not before trailing prose: %q", rendered)
	}
}

func TestMarkdownLabelledHyperlinkShowsURL(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("[docs](https://example.com/docs)", 0, 0, theme, MarkdownStyle{}, true)
	rendered := strings.Join(markdown.Render(80), "\n")
	if !strings.Contains(rendered, theme.Fg("mdLinkUrl", " (https://example.com/docs)")) {
		t.Fatalf("labelled link missing styled URL suffix: %q", rendered)
	}
}

func TestMarkdownRejectedHyperlinkSchemes(t *testing.T) {
	theme := mustTheme(t)
	for _, target := range []string{"javascript:alert", "data:text/plain,hi", "file:///etc/hosts"} {
		t.Run(target, func(t *testing.T) {
			markdown := NewMarkdown("[label]("+target+")", 0, 0, theme, MarkdownStyle{}, true)
			rendered := strings.Join(markdown.Render(80), "\n")
			if strings.Contains(rendered, "\x1b]8;") {
				t.Fatalf("rejected scheme emitted OSC 8: %q", rendered)
			}
			plain := tui.StripTerminalSequences(rendered)
			if !strings.Contains(plain, "label") || !strings.Contains(plain, target) {
				t.Fatalf("rejected scheme did not render safely: %q", plain)
			}
		})
	}
}

func TestMarkdownHyperlinkDisabledShowsTarget(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("[docs](https://example.com/docs)", 0, 0, theme, MarkdownStyle{}, false)
	rendered := strings.Join(markdown.Render(80), "\n")
	if strings.Contains(rendered, tui.OSC8Hyperlink("", "https://example.com/docs")) {
		t.Fatal("hyperlink should not be emitted when disabled")
	}
	plain := tui.StripTerminalSequences(rendered)
	if !strings.Contains(plain, "(https://example.com/docs)") {
		t.Fatalf("target not shown when hyperlinks disabled: %q", plain)
	}
}

func TestMarkdownAutolinkAndBareURL(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("see <https://example.com> and https://example.org/x", 0, 0, theme, MarkdownStyle{}, true)
	rendered := strings.Join(markdown.Render(80), "\n")
	if !strings.Contains(rendered, tui.OSC8Hyperlink("", "https://example.com")) {
		t.Fatalf("autolink missing: %q", rendered)
	}
	if !strings.Contains(rendered, tui.OSC8Hyperlink("", "https://example.org/x")) {
		t.Fatalf("bare url missing: %q", rendered)
	}
	plain := tui.StripTerminalSequences(rendered)
	if strings.Contains(plain, "(https://example.com)") || strings.Contains(plain, "(https://example.org/x)") {
		t.Fatalf("autolink duplicated its URL: %q", plain)
	}
}

func TestMarkdownCodeHighlightCallback(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("```go\nx := 1\n```", 0, 0, theme, MarkdownStyle{}, false)
	markdown.SetHighlight(func(code, lang string) []string {
		if lang != "go" || code != "x := 1" {
			t.Fatalf("highlight called with (%q, %q)", code, lang)
		}
		return []string{theme.Fg("syntaxKeyword", "x"), theme.Fg("syntaxOperator", ":=")}
	})
	lines := markdown.Render(40)
	joined := tui.StripTerminalSequences(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "x") || !strings.Contains(joined, ":=") {
		t.Fatalf("highlighted code missing: %q", joined)
	}
}

func TestMarkdownWidthBound(t *testing.T) {
	text := strings.Join([]string{
		"# A heading that is quite long and will need wrapping to fit",
		"",
		"A paragraph with **bold** and `code` and a [link](https://example.com/some/long/path) plus more words.",
		"",
		"> a blockquote that is long enough to wrap across several output lines at this width",
		"",
		"- a list item that is long enough to wrap onto more than one line at this width",
		"    - a nested list item that is also long enough to wrap at this narrow width",
		"",
		"```go",
		"func main() { fmt.Println(\"a very long line of code that should wrap\") }",
		"```",
		"",
		"| column one | column two |",
		"| --- | --- |",
		"| a long cell value | another long cell value |",
	}, "\n")
	for _, width := range []int{8, 16, 24, 40} {
		lines := renderMarkdownPlain(t, text, width)
		for index, line := range lines {
			if got := tui.VisibleWidth(line); got > width {
				t.Fatalf("width %d line %d visible %d: %q", width, index, got, line)
			}
		}
	}
}

func TestMarkdownEmptyText(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("   \n  ", 0, 0, theme, MarkdownStyle{}, false)
	if lines := markdown.Render(20); len(lines) != 0 {
		t.Fatalf("blank markdown rendered %d lines", len(lines))
	}
	if markdown.Text() != "   \n  " {
		t.Fatalf("Text() = %q", markdown.Text())
	}
}

func TestMarkdownEmphasisBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "underscore emphasis", input: "_italic_ text", want: "italic text"},
		{name: "intraword underscore", input: "word_with_underscores", want: "word_with_underscores"},
		{name: "underscore before word", input: "_a_b", want: "_a_b"},
		{name: "underscore then punctuation", input: "_em_!", want: "em!"},
		{name: "punctuation before underscore", input: "(_em_)", want: "(em)"},
		{name: "asterisk intraword", input: "a*b*c", want: "abc"},
		{name: "double underscore emphasis", input: "__strong__ text", want: "strong text"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := renderMarkdownPlain(t, testCase.input, 60)
			if got := strings.TrimSpace(strings.Join(lines, " ")); got != testCase.want {
				t.Fatalf("emphasis %q = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestMarkdownMailtoLinkLabel(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("[a@example.com](mailto:a@example.com)", 0, 0, theme, MarkdownStyle{}, false)
	plain := tui.StripTerminalSequences(strings.Join(markdown.Render(80), "\n"))
	if !strings.Contains(plain, "a@example.com") {
		t.Fatalf("mailto label lost: %q", plain)
	}
	if strings.Contains(plain, "(mailto:") {
		t.Fatalf("redundant mailto target shown: %q", plain)
	}
}

func TestMarkdownBareURLStopsAtBoundary(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("see (https://example.com/a) now", 0, 0, theme, MarkdownStyle{}, true)
	rendered := strings.Join(markdown.Render(80), "\n")
	if !strings.Contains(rendered, tui.OSC8Hyperlink("", "https://example.com/a")) {
		t.Fatalf("bracketed bare url not detected: %q", rendered)
	}
}

func TestMarkdownCacheInvalidation(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("first", 0, 0, theme, MarkdownStyle{}, false)
	first := markdown.Render(20)
	second := markdown.Render(20)
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatal("cached render differs")
	}
	markdown.SetText("second")
	third := markdown.Render(20)
	if strings.Join(third, "") == strings.Join(second, "") {
		t.Fatal("cache not invalidated after SetText")
	}
	markdown.SetHighlight(func(code, lang string) []string { return []string{code} })
	if len(markdown.Render(20)) == 0 {
		t.Fatal("render after SetHighlight empty")
	}
}

func TestMarkdownPadding(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("text", 1, 1, theme, MarkdownStyle{BgColor: func(text string) string { return theme.Bg("userMessageBg", text) }}, false)
	lines := markdown.Render(10)
	if len(lines) != 3 {
		t.Fatalf("padded render height = %d, want 3", len(lines))
	}
	for index, line := range lines {
		if got := tui.VisibleWidth(line); got != 10 {
			t.Fatalf("padded line %d visible %d, want 10", index, got)
		}
	}
}

func TestMarkdownTableCenterAlign(t *testing.T) {
	table := "| Name | Value |\n| :---: | --- |\n| ab | 1 |"
	lines := renderMarkdownPlain(t, table, 40)
	structural := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimRight(line, " "); trimmed != "" {
			structural = append(structural, trimmed)
		}
	}
	if len(structural) < 4 {
		t.Fatalf("table structure = %#v", structural)
	}
	if !strings.HasPrefix(structural[3], "│  ab") {
		t.Fatalf("center-aligned cell not rendered as expected: %q", structural[3])
	}
}

func TestMarkdownTableBadSeparatorFallsBack(t *testing.T) {
	text := "| Name |\n| nope |\n| a |"
	lines := renderMarkdownPlain(t, text, 30)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "│") {
		t.Fatalf("invalid separator should render as a paragraph: %q", joined)
	}
	if !strings.Contains(joined, "nope") {
		t.Fatalf("fallback paragraph lost content: %q", joined)
	}
}

func TestMarkdownPaddingWithoutBackground(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("text", 1, 1, theme, MarkdownStyle{}, false)
	lines := markdown.Render(10)
	if len(lines) != 3 {
		t.Fatalf("padded height = %d, want 3", len(lines))
	}
	if got := tui.StripTerminalSequences(lines[0]); got != strings.Repeat(" ", 10) {
		t.Fatalf("padding line = %q", got)
	}
}

func TestMarkdownStylePrefix(t *testing.T) {
	theme := mustTheme(t)
	if got := stylePrefix(func(string) string { return "" }, theme, MarkdownStyle{}); got != "" {
		t.Fatalf("stylePrefix without sentinel = %q", got)
	}
	prefix := stylePrefix(func(text string) string { return text }, theme, MarkdownStyle{Bold: true, Italic: true, Strike: true, Underline: true})
	if prefix == "" {
		t.Fatal("stylePrefix lost the sentinel prefix")
	}
}

func TestMarkdownTaskMarkerEdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "invalid state", input: "- [~] nope", want: "[~] nope"},
		{name: "missing space", input: "- [x]done", want: "[x]done"},
		{name: "empty marker", input: "- [] nope", want: "[] nope"},
		{name: "checked task", input: "- [X] done", want: "[x] done"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := renderMarkdownPlain(t, testCase.input, 30)
			if len(lines) == 0 || !strings.Contains(lines[0], testCase.want) {
				t.Fatalf("task edge %q = %#v", testCase.input, lines)
			}
		})
	}
}

func TestMarkdownListItemContinuation(t *testing.T) {
	lines := renderMarkdownPlain(t, "- first line\n  continued line\n- second", 40)
	if len(lines) != 2 {
		t.Fatalf("continuation rendered %d lines: %#v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "first line continued line") {
		t.Fatalf("continuation not joined: %#v", lines)
	}
}

func TestMarkdownNestedQuoteWrap(t *testing.T) {
	lines := renderMarkdownPlain(t, "> "+strings.Repeat("quote ", 20), 20)
	if len(lines) < 2 {
		t.Fatalf("wrapped quote rendered %d lines", len(lines))
	}
	for _, line := range lines {
		if !strings.Contains(line, "│") && strings.TrimSpace(line) != "" {
			t.Fatalf("quote line missing border: %q", line)
		}
	}
}
