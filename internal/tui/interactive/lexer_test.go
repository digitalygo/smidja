package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func assertSourcePreserved(t *testing.T, code, lang string) []string {
	t.Helper()
	theme := mustTheme(t)
	lines := SyntaxHighlight(code, lang, theme)
	stripped := make([]string, len(lines))
	for i, line := range lines {
		stripped[i] = tui.StripTerminalSequences(line)
	}
	if strings.Join(stripped, "\n") != code {
		t.Fatalf("highlighted %s source changed:\nwant %q\ngot  %q", lang, code, strings.Join(stripped, "\n"))
	}
	return lines
}

func containsToken(lines []string, token tui.ThemeColor, theme *tui.Theme) bool {
	ansi, ok := theme.GetFgAnsi(token)
	if !ok || ansi == "" {
		return false
	}
	for _, line := range lines {
		if strings.Contains(line, ansi) {
			return true
		}
	}
	return false
}

func TestSyntaxHighlightPreservesEveryLanguage(t *testing.T) {
	samples := map[string]string{
		"go":         "package main\n\n// comment\nfunc main() {\n\tx := 42\n\tprintln(\"hi\")\n}\n",
		"javascript": "const x = 1; // comment\nfunction f(a) { return `t${a}`; }\n/* block\ncomment */\n",
		"typescript": "interface Point { x: number }\nconst p: Point = { x: 1 };\n",
		"json":       "{\"a\": 1, \"b\": [true, null]}\n",
		"yaml":       "key: value\nlist:\n  - item\n  - 42\n# comment\n",
		"bash":       "#!/bin/bash\necho \"hi\" | grep hi\nfor i in 1 2 3; do\n  echo $i\ndone\n",
		"python":     "def f(x):\n    # comment\n    return x ** 2\n",
		"markdown":   "# Title\n\n- item **bold**\n\n```go\nx := 1\n```\n",
		"diff":       "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n",
	}
	for lang, code := range samples {
		lines := assertSourcePreserved(t, code, lang)
		if len(lines) == 0 {
			t.Fatalf("%s produced no lines", lang)
		}
	}
}

func TestSyntaxHighlightUsesNineTokens(t *testing.T) {
	theme := mustTheme(t)
	code := "package main\n\n// note\nfunc Compute(x int) string {\n\tconst limit = 10\n\treturn \"value\" + x\n}\n"
	lines := assertSourcePreserved(t, code, "go")
	for _, token := range []tui.ThemeColor{
		"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxString", "syntaxNumber",
	} {
		if !containsToken(lines, token, theme) {
			t.Fatalf("go highlight missing token %s", token)
		}
	}
}

func TestSyntaxHighlightScriptTokens(t *testing.T) {
	theme := mustTheme(t)
	jsLines := assertSourcePreserved(t, "const value = 1; // note\nfunction run() { return `x${value}`; }\n", "javascript")
	for _, token := range []tui.ThemeColor{"syntaxKeyword", "syntaxNumber", "syntaxComment", "syntaxFunction", "syntaxString"} {
		if !containsToken(jsLines, token, theme) {
			t.Fatalf("javascript highlight missing token %s", token)
		}
	}
	pythonLines := assertSourcePreserved(t, "def run(x):\n    # note\n    return \"a\" + str(2)\n", "python")
	for _, token := range []tui.ThemeColor{"syntaxKeyword", "syntaxFunction", "syntaxComment", "syntaxString", "syntaxNumber"} {
		if !containsToken(pythonLines, token, theme) {
			t.Fatalf("python highlight missing token %s", token)
		}
	}
	bashLines := assertSourcePreserved(t, "for i in 1 2; do\n  echo \"hi\" $i\ndone\n", "bash")
	for _, token := range []tui.ThemeColor{"syntaxKeyword", "syntaxNumber", "syntaxString", "syntaxVariable"} {
		if !containsToken(bashLines, token, theme) {
			t.Fatalf("bash highlight missing token %s", token)
		}
	}
}

func TestSyntaxHighlightMultilineStates(t *testing.T) {
	theme := mustTheme(t)
	code := "/* first\nsecond\nthird */\nconst x = 1\n"
	lines := assertSourcePreserved(t, code, "javascript")
	ansi, _ := theme.GetFgAnsi("syntaxComment")
	for index := 0; index < 3; index++ {
		if !strings.Contains(lines[index], ansi) {
			t.Fatalf("block comment line %d not styled", index)
		}
	}
	python := "text = \"\"\"first\nsecond\nthird\"\"\"\n"
	pyLines := assertSourcePreserved(t, python, "python")
	stringAnsi, _ := theme.GetFgAnsi("syntaxString")
	for index := 0; index < 3; index++ {
		if !strings.Contains(pyLines[index], stringAnsi) {
			t.Fatalf("triple-quoted line %d not styled", index)
		}
	}
}

func TestSyntaxHighlightUnknownAndOversizedPlain(t *testing.T) {
	theme := mustTheme(t)
	lines := SyntaxHighlight("some text", "brainfuck", theme)
	if len(lines) != 1 {
		t.Fatalf("unexpected line count %d", len(lines))
	}
	if tui.StripTerminalSequences(lines[0]) != "some text" {
		t.Fatalf("unknown language altered source: %q", lines[0])
	}
	oversized := strings.Repeat("x", syntaxMaxSource+1)
	plain := SyntaxHighlight(oversized, "go", theme)
	if tui.StripTerminalSequences(plain[0]) != oversized {
		t.Fatal("oversized source altered")
	}
	if strings.Contains(plain[0], "syntax") {
		t.Fatal("oversized source should stay plain")
	}
}

func TestSyntaxHighlightRethemePreservesSource(t *testing.T) {
	code := "func main() {\n\tprintln(\"hi\")\n}\n"
	dark := mustTheme(t)
	lightRegistry := tui.NewThemeRegistry("", "", tui.ColorModeTrueColor)
	light, err := lightRegistry.SetTheme("light")
	if err != nil {
		t.Fatalf("load light theme: %v", err)
	}
	darkLines := SyntaxHighlight(code, "go", dark)
	lightLines := SyntaxHighlight(code, "go", light)
	strip := func(lines []string) string {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = tui.StripTerminalSequences(line)
		}
		return strings.Join(out, "\n")
	}
	if strip(darkLines) != code || strip(lightLines) != code {
		t.Fatal("retheme changed source")
	}
	if strings.Join(darkLines, "") == strings.Join(lightLines, "") {
		t.Fatal("expected different ANSI for different themes")
	}
}

func TestSyntaxHighlightEmptyAndNoTheme(t *testing.T) {
	if lines := SyntaxHighlight("", "go", mustTheme(t)); lines == nil {
		t.Fatal("empty source should return a slice")
	}
	if lines := SyntaxHighlight("x := 1", "go", nil); tui.StripTerminalSequences(lines[0]) != "x := 1" {
		t.Fatal("nil theme should return plain text")
	}
}

func TestMarkdownUsesBuiltInLexerAndKeepsOverride(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("```go\nfunc main() {}\n```\n", 0, 0, theme, MarkdownStyle{}, false)
	rendered := markdown.Render(40)
	foundKeyword := false
	keywordAnsi, _ := theme.GetFgAnsi("syntaxKeyword")
	for _, line := range rendered {
		if strings.Contains(line, keywordAnsi) {
			foundKeyword = true
		}
	}
	if !foundKeyword {
		t.Fatal("markdown code fence did not use the built-in lexer")
	}
	if strings.Join(stripLines(rendered), "") == "" {
		t.Fatal("empty render")
	}
	custom := NewMarkdown("```go\nfunc main() {}\n```\n", 0, 0, theme, MarkdownStyle{}, false)
	custom.SetHighlight(func(code, lang string) []string { return []string{"CUSTOM"} })
	customRendered := custom.Render(40)
	joined := strings.Join(customRendered, "\n")
	if !strings.Contains(joined, "CUSTOM") {
		t.Fatal("SetHighlight override was not retained")
	}
	if strings.Contains(joined, keywordAnsi) {
		t.Fatal("override should suppress built-in lexer")
	}
}

func stripLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = tui.StripTerminalSequences(line)
	}
	return out
}

func TestMarkdownImagePlaceholderAndResolver(t *testing.T) {
	theme := mustTheme(t)
	markdown := NewMarkdown("before ![alt text](pic.png) after", 0, 0, theme, MarkdownStyle{}, false)
	rendered := strings.Join(stripLines(markdown.Render(80)), "\n")
	if !strings.Contains(rendered, "alt text") || !strings.Contains(rendered, "pic.png") {
		t.Fatalf("default image rendering lost information: %q", rendered)
	}
	markdown.SetImageResolver(func(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
		if alt == "" {
			return tui.ResolvedImage{}, false
		}
		return tui.ResolvedImage{CacheKey: source, Columns: 20, Rows: 2, Label: "[image: " + alt + "]", Protocol: tui.GraphicsITerm2}, true
	})
	rich := markdown.RenderRich(80)
	rendered = strings.Join(stripLines(rich.Lines), "\n")
	if !strings.Contains(rendered, "[image: alt text]") {
		t.Fatalf("image resolver not used: %q", rendered)
	}
	if strings.Contains(rendered, "\x1b_G") || strings.Contains(rendered, "\x1b]1337") || strings.Contains(rendered, "\x1b_pi") {
		t.Fatalf("graphics sequences must not appear inside markdown strings: %q", rendered)
	}
	if len(rich.Images) != 1 {
		t.Fatalf("expected one image descriptor, got %d", len(rich.Images))
	}
	descriptor := rich.Images[0]
	if descriptor.RowSpan != 2 || descriptor.Columns != 20 || descriptor.CacheKey != "pic.png" {
		t.Fatalf("unexpected descriptor %+v", descriptor)
	}
	if descriptor.Row+descriptor.RowSpan > len(rich.Lines) {
		t.Fatalf("row reservation out of range: %+v in %d lines", descriptor, len(rich.Lines))
	}
}

func TestLexerPreservesRichSamples(t *testing.T) {
	samples := map[string]string{
		"javascript": "const re = /ab+c/;\nlet s = `a${b + `${c}`}d`;\n/* multi\nline */\nclass Foo extends Bar {}\n",
		"typescript": "interface Point<T> { x: T }\ntype Alias = string | number;\nconst v: Point<number> = { x: 1 };\n",
		"python":     "@decorator\ndef run(self, x):\n    \"\"\"doc\n    string\"\"\"\n    return {'key': 0xFF, 'v': 1e-3}\n",
		"yaml":       "key: |\n  block\n  scalar\nquoted: \"value\"\nlist:\n  - {a: 1}\n  - &anchor item\n  - *anchor\n",
		"bash":       "#!/usr/bin/env bash\ncase \"$1\" in\n  a) echo ${VAR} ;;\n  *) echo $(date) ;;\nesac\n",
		"markdown":   "# Title\n\n> quote **bold**\n\n1. one\n2. two\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n<div>html</div>\n",
		"json":       "// comment\n{\"a\": [1, 2.5e3, -4], /* c */ \"b\": null}\n",
		"diff":       "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n context\n-removed\n+added\n",
		"go":         "package p\n\nimport \"fmt\"\n\ntype T struct{ X int }\n\nfunc (t *T) M() { fmt.Println(t.X, `raw`, 'r') }\n",
	}
	for lang, code := range samples {
		assertSourcePreserved(t, code, lang)
	}
}

func spanTokenAt(spans []syntaxSpan, start int) syntaxToken {
	for _, span := range spans {
		if span.start <= start && start < span.end {
			return span.token
		}
	}
	return syntaxNone
}

func TestLexerDataBranchTokens(t *testing.T) {
	jsonCode := `{"name": myVar + 1}`
	jsonSpans := lexJSON(jsonCode)
	if token := spanTokenAt(jsonSpans, strings.Index(jsonCode, "myVar")); token != syntaxVariableToken {
		t.Fatalf("json identifier token = %v, want variable", token)
	}
	if token := spanTokenAt(jsonSpans, strings.Index(jsonCode, "+")); token != syntaxOperatorToken {
		t.Fatalf("json operator token = %v, want operator", token)
	}

	yamlCode := strings.Join([]string{
		"---",
		"%YAML 1.2",
		"? key",
		"plain: 'single'",
		"double: \"unclosed",
		"keyword: true",
		"operator: a = b",
		"other: @",
		"comment: value # note",
		`"quoted key"  : value`,
		`"quoted" plain`,
		"bad key # note",
		"-",
	}, "\n")
	yamlSpans := lexYAML(yamlCode)
	yamlCases := []struct {
		needle string
		token  syntaxToken
	}{
		{"---", syntaxPunctuationToken},
		{"%YAML", syntaxPunctuationToken},
		{"? key", syntaxPunctuationToken},
		{"'single'", syntaxStringToken},
		{"\"unclosed", syntaxStringToken},
		{"true", syntaxKeywordToken},
		{"= b", syntaxOperatorToken},
		{"# note", syntaxCommentToken},
		{"\"quoted key\"", syntaxVariableToken},
		{"bad key # note", syntaxVariableToken},
		{"\n-", syntaxPunctuationToken},
	}
	for _, testCase := range yamlCases {
		position := strings.Index(yamlCode, testCase.needle)
		if testCase.needle == "\n-" {
			position++
		}
		if token := spanTokenAt(yamlSpans, position); token != testCase.token {
			t.Fatalf("yaml %q token = %v, want %v", testCase.needle, token, testCase.token)
		}
	}
	if token := spanTokenAt(yamlSpans, strings.Index(yamlCode, "@")); token != syntaxNone {
		t.Fatalf("yaml default character token = %v, want none", token)
	}
	if token := spanTokenAt(yamlSpans, strings.Index(yamlCode, "\"quoted\" plain")); token != syntaxStringToken {
		t.Fatalf("yaml quoted non-key token = %v, want string", token)
	}

	markdownCode := strings.Join([]string{
		"***",
		"a `code` b",
		"[label](url)",
		"-x",
		"12x item",
		"``",
	}, "\n")
	markdownSpans := lexMarkdown(markdownCode)
	markdownCases := []struct {
		needle string
		token  syntaxToken
	}{
		{"***", syntaxPunctuationToken},
		{"`code`", syntaxStringToken},
		{"[label]", syntaxPunctuationToken},
		{"(url)", syntaxStringToken},
	}
	for _, testCase := range markdownCases {
		if token := spanTokenAt(markdownSpans, strings.Index(markdownCode, testCase.needle)); token != testCase.token {
			t.Fatalf("markdown %q token = %v, want %v", testCase.needle, token, testCase.token)
		}
	}
	if token := spanTokenAt(markdownSpans, strings.Index(markdownCode, "-x")); token != syntaxNone {
		t.Fatalf("markdown bare dash token = %v, want none", token)
	}
	if token := spanTokenAt(markdownSpans, strings.Index(markdownCode, "12x")); token != syntaxNone {
		t.Fatalf("markdown digit prefix token = %v, want none", token)
	}
}

func TestLexerFenceWithoutTrailingNewline(t *testing.T) {
	closed := "```\ncode\n```"
	closedSpans := lexMarkdown(closed)
	for _, span := range closedSpans {
		if span.token != syntaxStringToken {
			t.Fatalf("closed fence span token = %v, want string", span.token)
		}
	}
	open := "```\ncode"
	openSpans := lexMarkdown(open)
	if len(openSpans) == 0 {
		t.Fatal("unterminated fence lost its content spans")
	}
	for _, span := range openSpans {
		if span.token != syntaxStringToken {
			t.Fatalf("unterminated fence span token = %v, want string", span.token)
		}
	}
	assertSourcePreserved(t, closed, "markdown")
	assertSourcePreserved(t, open, "markdown")
}

func TestLexerHelpers(t *testing.T) {
	if got := scanNumberEnd("0x1F rest", 0); got != 4 {
		t.Fatalf("hex %d", got)
	}
	if got := scanNumberEnd("0b1010 rest", 0); got != 6 {
		t.Fatalf("binary %d", got)
	}
	if got := scanNumberEnd("1_000 rest", 0); got != 5 {
		t.Fatalf("underscore %d", got)
	}
	if got := scanNumberEnd("1.5e-3", 0); got != 6 {
		t.Fatalf("exponent %d", got)
	}
	if got := scanNumberEnd("42j", 0); got != 3 {
		t.Fatalf("imaginary %d", got)
	}
	if got := findTemplateEnd("a${ {b: 1} }c`", 0); got != len("a${ {b: 1} }c`") {
		t.Fatalf("template end %d", got)
	}
	if got := findClose("'unterminated", 1, '\''); got != len("'unterminated") {
		t.Fatalf("unterminated close %d", got)
	}
	if got := scanDollarExpansion("${VAR} rest", 0); got != 6 {
		t.Fatalf("dollar braces %d", got)
	}
	if got := scanDollarExpansion("$(cmd) rest", 0); got != 6 {
		t.Fatalf("dollar paren %d", got)
	}
	if got := scanDollarExpansion("$VAR rest", 0); got != 4 {
		t.Fatalf("dollar name %d", got)
	}
	if got := scanDollarExpansion("$", 0); got != 1 {
		t.Fatalf("lone dollar %d", got)
	}
	if got := markdownListMarkerEnd("1. item", 0, 7); got != 2 {
		t.Fatalf("ordered marker %d", got)
	}
	if got := markdownListMarkerEnd("1) item", 0, 7); got != 2 {
		t.Fatalf("paren marker %d", got)
	}
	if got := markdownListMarkerEnd("- item", 0, 6); got != 1 {
		t.Fatalf("bullet marker %d", got)
	}
	if got := markdownListMarkerEnd("abc", 0, 3); got != 0 {
		t.Fatalf("plain text marker %d", got)
	}
	if got := yamlKeyEnd("'quoted key': value", 0, len("'quoted key': value")); got != len("'quoted key'") {
		t.Fatalf("quoted yaml key %d", got)
	}
	if got := yamlKeyEnd("no colon here", 0, 13); got != 0 {
		t.Fatalf("plain yaml text %d", got)
	}
	if got := yamlKeyEnd("key: value", 0, 10); got != 3 {
		t.Fatalf("plain yaml key %d", got)
	}
	if got := findBlockClose("/* never closed", 2, "/*", "*/"); got != len("/* never closed") {
		t.Fatalf("unterminated block %d", got)
	}
}

func TestMarkdownDefaultImageResolverRegistersDescriptor(t *testing.T) {
	SetDefaultImageResolver(func(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
		return tui.ResolvedImage{CacheKey: source, Columns: 15, Rows: 3, Label: "[image: " + alt + "]", Protocol: tui.GraphicsITerm2}, true
	})
	t.Cleanup(func() { SetDefaultImageResolver(nil) })
	theme := mustTheme(t)
	markdown := NewMarkdown("![logo](assets/logo.png)", 0, 0, theme, MarkdownStyle{}, false)
	rich := markdown.RenderRich(80)
	rendered := strings.Join(rich.Lines, "\n")
	if strings.Contains(rendered, "\x1b_pi") || strings.Contains(rendered, "\x1b_G") {
		t.Fatalf("rendered text must be placeholder-only: %q", rendered)
	}
	if !strings.Contains(tui.StripTerminalSequences(rendered), "[image: logo]") {
		t.Fatalf("placeholder missing: %q", rendered)
	}
	if len(rich.Images) != 1 || rich.Images[0].RowSpan != 3 || rich.Images[0].CacheKey != "assets/logo.png" {
		t.Fatalf("unexpected images %+v", rich.Images)
	}
}

func TestMarkdownImageRowReservation(t *testing.T) {
	theme := mustTheme(t)
	plain := NewMarkdown("before\n\n![alt](pic.png)\n\nafter", 0, 0, theme, MarkdownStyle{}, false)
	plain.SetImageResolver(nil)
	plainLines := len(plain.RenderRich(40).Lines)

	resolved := NewMarkdown("before\n\n![alt](pic.png)\n\nafter", 0, 0, theme, MarkdownStyle{}, false)
	resolved.SetImageResolver(func(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
		return tui.ResolvedImage{CacheKey: source, Columns: 12, Rows: 4, Label: "[image: " + alt + "]", Protocol: tui.GraphicsKitty}, true
	})
	rich := resolved.RenderRich(40)
	if len(rich.Images) != 1 || rich.Images[0].RowSpan != 4 {
		t.Fatalf("unexpected images %+v", rich.Images)
	}
	if len(rich.Lines) != plainLines+3 {
		t.Fatalf("row reservation lines = %d, want %d", len(rich.Lines), plainLines+3)
	}
	descriptor := rich.Images[0]
	for row := descriptor.Row; row < descriptor.Row+descriptor.RowSpan; row++ {
		line := tui.StripTerminalSequences(rich.Lines[row])
		if strings.Contains(line, "\x1b_G") || strings.Contains(line, "\x1b]1337") {
			t.Fatalf("reserved row %d leaked graphics: %q", row, line)
		}
	}
}
