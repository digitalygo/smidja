package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestParseMathSpanInline(t *testing.T) {
	cases := []struct {
		source   string
		expected string
		consumed int
	}{
		{"$x^2$", "x²", 5},
		{"$x_1$", "x₁", 5},
		{"$\\alpha + \\beta$", "α + β", 16},
		{"$\\frac{a}{b}$", "a/b", 12},
		{"$\\frac{ab}{cd}$", "(ab)/(cd)", 14},
		{"$$E = mc^2$$", "E = mc²", 11},
		{"$\\sqrt{x}$", "√x", 9},
		{"$x_{i}$", "xᵢ", 6},
		{"$\\frac{1}{2}$", "1/2", 12},
	}
	for _, testCase := range cases {
		rendered, consumed, ok := parseMathSpan(testCase.source, 0)
		if !ok {
			t.Fatalf("%q was not parsed", testCase.source)
		}
		if rendered != testCase.expected {
			t.Fatalf("%q rendered as %q want %q", testCase.source, rendered, testCase.expected)
		}
		if consumed != len(testCase.source) {
			t.Fatalf("%q consumed %d want %d", testCase.source, consumed, len(testCase.source))
		}
	}
}

func TestParseMathSpanCurrencyAndMalformedRejected(t *testing.T) {
	rejected := []string{
		"$5 and $10",
		"$5",
		"$x$5",
		"$x",
		"$\\unknown{x}$",
		"$$unbalanced",
		"$x^$",
		"$ $",
		"$x^{2$",
	}
	for _, source := range rejected {
		if _, _, ok := parseMathSpan(source, 0); ok {
			t.Fatalf("%q should not be treated as math", source)
		}
	}
}

func TestParseMathSpanBudget(t *testing.T) {
	oversized := "$" + strings.Repeat("x", mathMaxSource+1) + "$"
	if _, _, ok := parseMathSpan(oversized, 0); ok {
		t.Fatal("oversized math should be rejected")
	}
	deep := "$" + strings.Repeat("{", mathMaxDepth+2) + "x" + strings.Repeat("}", mathMaxDepth+2) + "$"
	if _, _, ok := parseMathSpan(deep, 0); ok {
		t.Fatal("overly nested math should be rejected")
	}
}

func TestMarkdownRendersMathAndKeepsRawFallback(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("The value $x^2$ grows and $\\alpha$ too.", 0, 0, theme, MarkdownStyle{}, false)
	rendered := strings.Join(stripLines(markdown.Render(80)), "\n")
	if !strings.Contains(rendered, "x²") || !strings.Contains(rendered, "α") {
		t.Fatalf("math not rendered: %q", rendered)
	}
	fallback := NewMarkdown("Costs $5 and $10 today.", 0, 0, theme, MarkdownStyle{}, false)
	fallbackRendered := strings.Join(stripLines(fallback.Render(80)), "\n")
	if !strings.Contains(fallbackRendered, "$5 and $10") {
		t.Fatalf("currency was misparsed: %q", fallbackRendered)
	}
	unknown := NewMarkdown("Bad $\\nope{x}$ here.", 0, 0, theme, MarkdownStyle{}, false)
	unknownRendered := strings.Join(stripLines(unknown.Render(80)), "\n")
	if !strings.Contains(unknownRendered, "$\\nope{x}$") {
		t.Fatalf("malformed math lost delimiters: %q", unknownRendered)
	}
	if !strings.Contains(unknownRendered, mathWarningText()) {
		t.Fatalf("malformed math did not warn: %q", unknownRendered)
	}
}

func mathWarningText() string {
	return "math: " + mathFallbackWarning
}

func TestMarkdownDisplayMathRenders(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("$$\\frac{a}{b}$$", 0, 0, theme, MarkdownStyle{}, false)
	rendered := strings.Join(stripLines(markdown.Render(40)), "\n")
	if !strings.Contains(rendered, "a/b") {
		t.Fatalf("display math not rendered: %q", rendered)
	}
}

func TestConvertScriptUnsupported(t *testing.T) {
	if _, ok := convertScript("~", true); ok {
		t.Fatal("unsupported superscript should fail")
	}
	if _, ok := convertScript("Q", false); ok {
		t.Fatal("unsupported subscript should fail")
	}
	if value, ok := convertScript("12", true); !ok || value != "¹²" {
		t.Fatalf("superscript digits: %q %v", value, ok)
	}
}

func TestMathThemeToken(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("$x^2$", 0, 0, theme, MarkdownStyle{}, false)
	ansi, _ := theme.GetFgAnsi("mdCode")
	rendered := strings.Join(markdown.Render(20), "")
	if !strings.Contains(rendered, ansi) {
		t.Fatal("math should use the mdCode token")
	}
	_ = tui.VisibleWidth
}

func TestMarkdownInlineMathFailedSpanPreservesSource(t *testing.T) {
	cases := []string{
		"$\\unknown{a*b*c}$",
		"$\\unknown{a `code` b}$",
		"$\\unknown{[link](https://example.com)*em*}$",
		"$x^$",
		"$\\frac{a}{$",
		"$\\unknown{a}_{\\text{~}}$",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			lines := trimTrailingBlanks(renderMarkdownPlain(t, input, 120))
			if len(lines) != 2 {
				t.Fatalf("failed span rendered %#v, want source plus one warning", lines)
			}
			if lines[0] != input {
				t.Fatalf("failed span rendered as %q, want exact %q", lines[0], input)
			}
			if lines[1] != mathWarningText() {
				t.Fatalf("failed span warning = %q, want %q", lines[1], mathWarningText())
			}
		})
	}
}

func TestMarkdownInlineMathFallbackWarnsOncePerBlock(t *testing.T) {
	input := "$\\bad{a}$ and $\\bad{b}$ and $\\bad{c}$"
	lines := trimTrailingBlanks(renderMarkdownPlain(t, input, 120))
	if len(lines) != 2 {
		t.Fatalf("multiple failed spans rendered %#v, want source plus one warning", lines)
	}
	if lines[0] != input {
		t.Fatalf("failed spans rendered as %q, want exact %q", lines[0], input)
	}
	if count := strings.Count(strings.Join(lines, "\n"), "math: "); count != 1 {
		t.Fatalf("warning repeated %d times: %#v", count, lines)
	}
}

func TestMarkdownInlineMathFailedSpanSkipsMarkdown(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("$\\unknown{a*b*c}$", 0, 0, theme, MarkdownStyle{}, false)
	plain := tui.StripTerminalSequences(strings.Join(markdown.Render(80), ""))
	if strings.Contains(plain, "abc") {
		t.Fatalf("emphasis parsed inside a failed span: %q", plain)
	}
	if !strings.Contains(plain, "*b*") {
		t.Fatalf("failed span lost its markers: %q", plain)
	}
	if !strings.Contains(plain, mathWarningText()) {
		t.Fatalf("failed span did not warn: %q", plain)
	}
}

func TestMarkdownMathOutsideCodeAndLinks(t *testing.T) {
	code := strings.Join(trimTrailingBlanks(renderMarkdownPlain(t, "`$x^2$`", 40)), "\n")
	if code != "$x^2$" {
		t.Fatalf("code span math rendered as %q", code)
	}
	link := strings.Join(trimTrailingBlanks(renderMarkdownPlain(t, "[label](https://example.com/$x^2$)", 80)), "\n")
	if !strings.Contains(link, "$x^2$") || strings.Contains(link, "x²") {
		t.Fatalf("link target math was parsed: %q", link)
	}
	label := strings.Join(trimTrailingBlanks(renderMarkdownPlain(t, "[$x^2$](https://example.com)", 80)), "\n")
	if !strings.Contains(label, "x²") {
		t.Fatalf("link label math was not rendered: %q", label)
	}
}

func TestMarkdownMathInsideFenceStaysLiteral(t *testing.T) {
	text := "```\n$$\n\\unknown{a}\n$$\n```"
	joined := strings.Join(trimTrailingBlanks(renderMarkdownPlain(t, text, 40)), "\n")
	if !strings.Contains(joined, "$$") || !strings.Contains(joined, "\\unknown{a}") {
		t.Fatalf("fenced math lost source: %q", joined)
	}
}

func TestMarkdownDisplayMathMultiline(t *testing.T) {
	accepted := "$$\na + b\n= c\n$$"
	lines := trimTrailingBlanks(renderMarkdownPlain(t, accepted, 40))
	if len(lines) != 2 || !strings.Contains(lines[0], "a + b") || !strings.Contains(lines[1], "= c") {
		t.Fatalf("multiline display math = %#v", lines)
	}
}

func TestMarkdownDisplayMathSameLine(t *testing.T) {
	lines := trimTrailingBlanks(renderMarkdownPlain(t, "$$\\frac{a}{b}$$", 40))
	if len(lines) != 1 || !strings.Contains(lines[0], "a/b") {
		t.Fatalf("same-line display math = %#v", lines)
	}
}

func TestMarkdownDisplayMathInlineInParagraph(t *testing.T) {
	lines := trimTrailingBlanks(renderMarkdownPlain(t, "before $$x^2$$ after", 40))
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "before x² after") {
		t.Fatalf("inline display math = %q", got)
	}
}

func TestMarkdownDisplayMathMalformedFallbackPreservesSource(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
		warn bool
	}{
		{
			name: "multiline unknown command",
			text: "$$\n\\unknown{a*b*c}\n$$",
			want: []string{"$$", "\\unknown{a*b*c}", "$$", mathWarningText()},
			warn: true,
		},
		{
			name: "same line unknown command",
			text: "$$\\unknown{a*b*c}$$",
			want: []string{"$$\\unknown{a*b*c}$$", mathWarningText()},
			warn: true,
		},
		{
			name: "unmatched open",
			text: "$$\nstill open",
			want: []string{"$$", "still open"},
		},
		{
			name: "unmatched open inline",
			text: "$$x^2",
			want: []string{"$$x^2"},
		},
		{
			name: "unbalanced braces",
			text: "$$\n\\frac{a}{\n$$",
			want: []string{"$$", "\\frac{a}{", "$$", mathWarningText()},
			warn: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := trimTrailingBlanks(renderMarkdownPlain(t, testCase.text, 80))
			if len(lines) != len(testCase.want) {
				t.Fatalf("rendered %#v, want %#v", lines, testCase.want)
			}
			for index, want := range testCase.want {
				if lines[index] != want {
					t.Fatalf("line %d = %q, want %q (all %#v)", index, lines[index], want, lines)
				}
			}
			warned := len(lines) > 0 && strings.Contains(lines[len(lines)-1], mathWarningText())
			if warned != testCase.warn {
				t.Fatalf("warning presence = %v, want %v (all %#v)", warned, testCase.warn, lines)
			}
		})
	}
}

func TestMarkdownCurrencyStaysLiteral(t *testing.T) {
	cases := []string{
		"$5 and $10",
		"$5.00",
		"Cost $5 and $10 today.",
		"$100$200",
		"$ 5 $",
		"$5",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			lines := trimTrailingBlanks(renderMarkdownPlain(t, input, 80))
			if got := strings.Join(lines, "\n"); got != input {
				t.Fatalf("currency rendered as %q, want exact %q", got, input)
			}
		})
	}
}

func TestMarkdownMathBudgetFallbackPreservesSource(t *testing.T) {
	oversized := "$" + strings.Repeat("x", mathMaxSource+1) + "$"
	width := mathMaxSource + 10
	lines := trimTrailingBlanks(renderMarkdownPlain(t, oversized, width))
	if len(lines) != 2 {
		t.Fatalf("oversized inline math rendered %d lines, want source plus one warning", len(lines))
	}
	if lines[0] != oversized {
		t.Fatalf("oversized inline math lost source: got %d bytes want %d", len(lines[0]), len(oversized))
	}
	if lines[1] != mathWarningText() {
		t.Fatalf("oversized inline math warning = %q, want %q", lines[1], mathWarningText())
	}
	deep := "$" + strings.Repeat("{", mathMaxDepth+2) + "x*y" + strings.Repeat("}", mathMaxDepth+2) + "$"
	deepLines := trimTrailingBlanks(renderMarkdownPlain(t, deep, 80))
	if len(deepLines) != 2 {
		t.Fatalf("deep inline math rendered %#v, want source plus one warning", deepLines)
	}
	if deepLines[0] != deep {
		t.Fatalf("deep inline math rendered as %q, want exact %q", deepLines[0], deep)
	}
	if deepLines[1] != mathWarningText() {
		t.Fatalf("deep inline math warning = %q, want %q", deepLines[1], mathWarningText())
	}
}

func TestMarkdownTableMathFallbackWarns(t *testing.T) {
	input := "| a | b |\n| --- | --- |\n| $\\bad{x}$ | ok |"
	lines := trimTrailingBlanks(renderMarkdownPlain(t, input, 80))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "$\\bad{x}$") {
		t.Fatalf("table cell lost the failed span: %q", joined)
	}
	if count := strings.Count(joined, "math: "); count != 1 {
		t.Fatalf("table math warning count = %d, want 1: %q", count, joined)
	}
}

func TestMarkdownDisplayMathBudgetFallback(t *testing.T) {
	deep := "$$\n" + strings.Repeat("{", mathMaxDepth+2) + "x*y" + strings.Repeat("}", mathMaxDepth+2) + "\n$$"
	lines := trimTrailingBlanks(renderMarkdownPlain(t, deep, 120))
	if len(lines) != 4 {
		t.Fatalf("deep display math = %#v", lines)
	}
	if !strings.Contains(lines[1], "x*y") {
		t.Fatalf("deep display math lost source: %#v", lines)
	}
	if lines[3] != mathWarningText() {
		t.Fatalf("deep display math warning = %q, want %q", lines[3], mathWarningText())
	}
}

func TestAssistantMessageStreamsMathIncompleteThenComplete(t *testing.T) {
	theme := mustTheme(t)
	message := NewAssistantMessage(theme, false)
	message.AppendText("$$")
	incomplete := strings.Join(plainLines(message.Render(60)), "\n")
	if !strings.Contains(incomplete, "$$") {
		t.Fatalf("incomplete display math lost source: %q", incomplete)
	}
	message.AppendText("\n\\frac{a}{b}\n$$")
	complete := strings.Join(plainLines(message.Render(60)), "\n")
	if !strings.Contains(complete, "a/b") {
		t.Fatalf("complete display math not rendered: %q", complete)
	}
	if strings.Contains(complete, "$$") {
		t.Fatalf("complete display math retained delimiters: %q", complete)
	}
}

func TestAssistantMessageStreamsInlineMathIncompleteThenComplete(t *testing.T) {
	theme := mustTheme(t)
	message := NewAssistantMessage(theme, false)
	message.AppendText("$x^2")
	incomplete := strings.Join(plainLines(message.Render(60)), "\n")
	if !strings.Contains(incomplete, "$x^2") {
		t.Fatalf("incomplete inline math lost source: %q", incomplete)
	}
	message.AppendText("$")
	complete := strings.Join(plainLines(message.Render(60)), "\n")
	if !strings.Contains(complete, "x²") {
		t.Fatalf("complete inline math not rendered: %q", complete)
	}
}

func TestFindMathSpanDelimiterRules(t *testing.T) {
	valid := []string{"$x$", "$$x$$", "$\\alpha$", "$a*b*c$"}
	for _, source := range valid {
		span, ok := findMathSpan(source, 0)
		if !ok || span.end != len(source) {
			t.Fatalf("%q should be a math candidate: %+v %v", source, span, ok)
		}
	}
	invalid := []string{"$5$", "$ 5$", "$5 $", "$x$5", "$x$y", "$", "$5 and $10"}
	for _, source := range invalid {
		if _, ok := findMathSpan(source, 0); ok {
			t.Fatalf("%q should not be a math candidate", source)
		}
	}
}
