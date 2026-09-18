package interactive

import (
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
)

type diffKind int

const (
	diffContext diffKind = iota
	diffAdded
	diffRemoved
	diffHunk
	diffMeta
)

type diffLine struct {
	kind    diffKind
	number  string
	content string
}

func parseDiffLine(line string) diffLine {
	if strings.HasPrefix(line, "@@") {
		return diffLine{kind: diffHunk, content: line}
	}
	if strings.HasPrefix(line, "\\") {
		return diffLine{kind: diffMeta, content: line}
	}
	if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
		return diffLine{kind: diffMeta, content: line}
	}
	if line == "" {
		return diffLine{kind: diffContext}
	}
	prefix := line[0]
	switch prefix {
	case '+', '-', ' ':
	default:
		return diffLine{kind: diffContext, content: line}
	}
	rest := line[1:]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	kind := diffKindForPrefix(prefix)
	if digits > 0 && digits < len(rest) && (rest[digits] == ' ' || rest[digits] == '\t') {
		return diffLine{kind: kind, number: rest[:digits], content: rest[digits+1:]}
	}
	if digits > 0 && digits == len(rest) {
		return diffLine{kind: kind, number: rest, content: ""}
	}
	return diffLine{kind: kind, content: rest}
}

func diffKindForPrefix(prefix byte) diffKind {
	switch prefix {
	case '+':
		return diffAdded
	case '-':
		return diffRemoved
	default:
		return diffContext
	}
}

func replaceDiffTabs(text string) string {
	return strings.ReplaceAll(text, "\t", tabReplacement)
}

func RenderDiff(diffText string, theme *tui.Theme) []string {
	lines := strings.Split(SanitizeDisplayText(diffText), "\n")
	result := make([]string, 0, len(lines))
	for _, raw := range lines {
		result = append(result, renderDiffLine(parseDiffLine(raw), theme))
	}
	return result
}

func renderDiffLine(line diffLine, theme *tui.Theme) string {
	content := replaceDiffTabs(line.content)
	separator := ""
	if line.number != "" {
		separator = " "
	}
	switch line.kind {
	case diffAdded:
		return theme.Fg("toolDiffAdded", "+"+line.number+separator+content)
	case diffRemoved:
		return theme.Fg("toolDiffRemoved", "-"+line.number+separator+content)
	case diffHunk:
		return theme.Fg("toolDiffContext", theme.Bold(content))
	default:
		return theme.Fg("toolDiffContext", " "+line.number+separator+content)
	}
}
