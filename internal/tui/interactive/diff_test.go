package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestParseDiffLineForms(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		kind    diffKind
		number  string
		content string
	}{
		{name: "numbered added", input: "+12 new line", kind: diffAdded, number: "12", content: "new line"},
		{name: "numbered removed", input: "-7 old line", kind: diffRemoved, number: "7", content: "old line"},
		{name: "numbered context", input: " 3 kept", kind: diffContext, number: "3", content: "kept"},
		{name: "numbered without content", input: "-9", kind: diffRemoved, number: "9", content: ""},
		{name: "unified added", input: "+added", kind: diffAdded, content: "added"},
		{name: "unified removed", input: "-removed", kind: diffRemoved, content: "removed"},
		{name: "unified context", input: " context", kind: diffContext, content: "context"},
		{name: "digits only added", input: "+123", kind: diffAdded, number: "123", content: ""},
		{name: "hunk", input: "@@ -1,3 +1,4 @@", kind: diffHunk, content: "@@ -1,3 +1,4 @@"},
		{name: "file header", input: "+++ b/main.go", kind: diffMeta, content: "+++ b/main.go"},
		{name: "old header", input: "--- a/main.go", kind: diffMeta, content: "--- a/main.go"},
		{name: "no newline marker", input: "\\ No newline at end of file", kind: diffMeta, content: "\\ No newline at end of file"},
		{name: "plain", input: "plain text", kind: diffContext, content: "plain text"},
		{name: "empty", input: "", kind: diffContext, content: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := parseDiffLine(testCase.input)
			if got.kind != testCase.kind || got.number != testCase.number || got.content != testCase.content {
				t.Fatalf("parseDiffLine(%q) = %+v, want kind=%v number=%q content=%q", testCase.input, got, testCase.kind, testCase.number, testCase.content)
			}
		})
	}
}

func TestRenderDiffAppliesTokens(t *testing.T) {
	theme := mustTheme(t)
	diff := strings.Join([]string{
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,3 +1,3 @@",
		"-1 old line",
		"+1 new line",
		" 2 context",
		"+unified addition",
		"-unified removal",
		" unified context",
	}, "\n")
	lines := RenderDiff(diff, theme)
	if len(lines) != 9 {
		t.Fatalf("expected 9 diff lines, got %d", len(lines))
	}
	addedToken, ok := theme.GetFgAnsi("toolDiffAdded")
	if !ok || addedToken == "" {
		t.Fatal("toolDiffAdded token missing from theme")
	}
	removedToken, ok := theme.GetFgAnsi("toolDiffRemoved")
	if !ok || removedToken == "" {
		t.Fatal("toolDiffRemoved token missing from theme")
	}
	contextToken, ok := theme.GetFgAnsi("toolDiffContext")
	if !ok || contextToken == "" {
		t.Fatal("toolDiffContext token missing from theme")
	}
	if !strings.Contains(lines[3], removedToken) {
		t.Fatalf("numbered removal missing removal token: %q", lines[3])
	}
	if !strings.Contains(lines[4], addedToken) {
		t.Fatalf("numbered addition missing addition token: %q", lines[4])
	}
	if !strings.Contains(lines[6], addedToken) {
		t.Fatalf("unified addition missing addition token: %q", lines[6])
	}
	if !strings.Contains(lines[7], removedToken) {
		t.Fatalf("unified removal missing removal token: %q", lines[7])
	}
	if !strings.Contains(lines[5], contextToken) {
		t.Fatalf("numbered context missing context token: %q", lines[5])
	}
	if !strings.Contains(lines[8], contextToken) {
		t.Fatalf("unified context missing context token: %q", lines[8])
	}
	plain := tui.StripTerminalSequences(lines[3])
	if plain != "-1 old line" {
		t.Fatalf("numbered removal text = %q", plain)
	}
	if got := tui.StripTerminalSequences(lines[6]); got != "+unified addition" {
		t.Fatalf("unified addition text = %q", got)
	}
	if got := tui.StripTerminalSequences(lines[8]); got != " unified context" {
		t.Fatalf("unified context text = %q", got)
	}
}

func TestRenderDiffReplacesTabsAndSanitizes(t *testing.T) {
	theme := mustTheme(t)
	lines := RenderDiff("+1\tvalue\x1b[31m", theme)
	if len(lines) != 1 {
		t.Fatalf("expected one line, got %d", len(lines))
	}
	plain := tui.StripTerminalSequences(lines[0])
	if strings.Contains(plain, "\t") {
		t.Fatalf("tab was not replaced: %q", plain)
	}
	if !strings.HasPrefix(plain, "+1 ") || !strings.HasSuffix(plain, "value") {
		t.Fatalf("tab replacement = %q", plain)
	}
}

func TestRenderDiffEmptyInput(t *testing.T) {
	theme := mustTheme(t)
	lines := RenderDiff("", theme)
	if len(lines) != 1 {
		t.Fatalf("expected single line, got %d", len(lines))
	}
	if got := tui.StripTerminalSequences(lines[0]); got != " " {
		t.Fatalf("empty diff line = %q, want a single space", got)
	}
}
