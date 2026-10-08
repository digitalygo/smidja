package interactive

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type ToolStatus int

const (
	ToolPending ToolStatus = iota
	ToolSuccess
	ToolError
)

const previewOutputLines = 10

const bashPreviewLines = 20

func expandHint(keyDisplay, verb string) string {
	if keyDisplay == "" {
		return ""
	}
	return ", " + keyDisplay + " to " + verb
}

func renderOutputPreview(output string, expanded bool, width int, theme *tui.Theme, keyDisplay string) []string {
	if output == "" {
		return nil
	}
	safeWidth := maxInt(1, width)
	lines := strings.Split(output, "\n")
	display := lines
	if !expanded && len(lines) > previewOutputLines {
		display = lines[:previewOutputLines]
	}
	result := make([]string, 0, len(display)+2)
	for _, line := range display {
		styled := theme.Fg("toolOutput", line)
		result = append(result, tui.WrapTextWithANSI(styled, safeWidth)...)
	}
	if remaining := len(lines) - len(display); remaining > 0 {
		counter := theme.Fg("muted", "... ("+itoa(remaining)+" more lines"+expandHint(keyDisplay, "expand")+")")
		result = append(result, tui.WrapTextWithANSI(counter, safeWidth)...)
	}
	if expanded && keyDisplay != "" {
		result = append(result, tui.WrapTextWithANSI(theme.Fg("dim", "("+keyDisplay+" to collapse)"), safeWidth)...)
	}
	return result
}

func toolBackground(status ToolStatus) tui.ThemeColor {
	switch status {
	case ToolSuccess:
		return "toolSuccessBg"
	case ToolError:
		return "toolErrorBg"
	default:
		return "toolPendingBg"
	}
}

type ToolExecution struct {
	mu         sync.Mutex
	name       string
	argsText   string
	output     string
	diff       string
	hasDiff    bool
	status     ToolStatus
	expanded   bool
	theme      *tui.Theme
	keyDisplay string
	onChange   func()
	cacheValid bool
	cacheWidth int
	cacheLines []string
}

func NewToolExecution(name string, argsJSON json.RawMessage, theme *tui.Theme, keyDisplay string, onChange func()) *ToolExecution {
	return &ToolExecution{
		name:       SanitizeSingleLine(name),
		argsText:   formatToolArgs(argsJSON),
		theme:      theme,
		keyDisplay: keyDisplay,
		onChange:   onChange,
	}
}

func formatToolArgs(argsJSON json.RawMessage) string {
	if len(argsJSON) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, argsJSON, "", "  "); err != nil {
		return SanitizeDisplayText(string(argsJSON))
	}
	return SanitizeDisplayText(buf.String())
}

func (t *ToolExecution) SetResult(output string, status ToolStatus) {
	t.mu.Lock()
	t.output = SanitizeDisplayText(output)
	t.status = status
	t.cacheValid = false
	t.mu.Unlock()
	t.notify()
}

func (t *ToolExecution) SetDiff(diffText string) {
	t.mu.Lock()
	t.diff = diffText
	t.hasDiff = true
	t.cacheValid = false
	t.mu.Unlock()
	t.notify()
}

func (t *ToolExecution) SetExpanded(expanded bool) {
	t.mu.Lock()
	t.expanded = expanded
	t.cacheValid = false
	t.mu.Unlock()
}

func (t *ToolExecution) IsExpanded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expanded
}

func (t *ToolExecution) Status() ToolStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *ToolExecution) Output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.output
}

func (t *ToolExecution) Invalidate() {
	t.mu.Lock()
	t.cacheValid = false
	t.mu.Unlock()
}

func (t *ToolExecution) notify() {
	if t.onChange != nil {
		t.onChange()
	}
}

func (t *ToolExecution) Render(width int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cacheValid && t.cacheWidth == width {
		return t.cacheLines
	}
	contentWidth := blockContentWidth(width, 1)
	content := []string{t.theme.Fg("toolTitle", t.theme.Bold(t.name))}
	if t.argsText != "" {
		content = append(content, "")
		content = append(content, t.argsText)
	}
	if t.hasDiff && t.diff != "" {
		content = append(content, "")
		content = append(content, RenderDiff(t.diff, t.theme)...)
	}
	if t.output != "" {
		content = append(content, "")
		content = append(content, renderOutputPreview(t.output, t.expanded, contentWidth, t.theme, t.keyDisplay)...)
	}
	background := toolBackground(t.status)
	lines := applyBlockBackground(wrapBlockContent(content, contentWidth), width, 1, 1, func(text string) string {
		return t.theme.Bg(background, text)
	})
	t.cacheLines = append([]string{""}, lines...)
	t.cacheWidth = width
	t.cacheValid = true
	return t.cacheLines
}

type SubagentBlock struct {
	mu         sync.Mutex
	name       string
	output     string
	status     ToolStatus
	expanded   bool
	theme      *tui.Theme
	keyDisplay string
	onChange   func()
}

func NewSubagentBlock(name string, theme *tui.Theme, keyDisplay string, onChange func()) *SubagentBlock {
	return &SubagentBlock{name: SanitizeSingleLine(name), theme: theme, keyDisplay: keyDisplay, onChange: onChange}
}

func (s *SubagentBlock) SetOutput(output string) {
	s.mu.Lock()
	s.output = SanitizeDisplayText(output)
	s.mu.Unlock()
	s.notify()
}

func (s *SubagentBlock) SetStatus(status ToolStatus) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	s.notify()
}

func (s *SubagentBlock) SetExpanded(expanded bool) {
	s.mu.Lock()
	s.expanded = expanded
	s.mu.Unlock()
}

func (s *SubagentBlock) IsExpanded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expanded
}

func (s *SubagentBlock) Invalidate() {}

func (s *SubagentBlock) notify() {
	if s.onChange != nil {
		s.onChange()
	}
}

func (s *SubagentBlock) Render(width int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	contentWidth := blockContentWidth(width, 1)
	content := []string{s.theme.Fg("toolTitle", s.theme.Bold("[subagent] "+s.name))}
	if s.output != "" {
		content = append(content, "")
		content = append(content, renderOutputPreview(s.output, s.expanded, contentWidth, s.theme, s.keyDisplay)...)
	}
	background := toolBackground(s.status)
	lines := applyBlockBackground(wrapBlockContent(content, contentWidth), width, 1, 1, func(text string) string {
		return s.theme.Bg(background, text)
	})
	return append([]string{""}, lines...)
}

type bashStatus int

const (
	bashRunning bashStatus = iota
	bashComplete
	bashCancelled
	bashError
)

type BashExecution struct {
	mu              sync.Mutex
	command         string
	outputLines     []string
	status          bashStatus
	exitCode        int
	hasExitCode     bool
	expanded        bool
	theme           *tui.Theme
	keyDisplay      string
	cancelKey       string
	frames          []string
	interval        time.Duration
	frame           int
	runtime         *tui.Runtime
	cancelAnimation func() bool
	onChange        func()
	onCancel        func()
}

func NewBashExecution(command string, theme *tui.Theme, keyDisplay, cancelKey string, indicator *tui.LoaderIndicator, runtime *tui.Runtime, onChange func()) *BashExecution {
	frames := defaultSpinnerFrames
	interval := defaultSpinnerInterval
	if indicator != nil {
		if len(indicator.Frames) > 0 {
			frames = sanitizeFrames(indicator.Frames)
		}
		if indicator.IntervalMs > 0 {
			interval = time.Duration(indicator.IntervalMs) * time.Millisecond
		}
	}
	block := &BashExecution{
		command:    SanitizeSingleLine(command),
		theme:      theme,
		keyDisplay: keyDisplay,
		cancelKey:  cancelKey,
		frames:     frames,
		interval:   interval,
		runtime:    runtime,
		onChange:   onChange,
	}
	block.mu.Lock()
	block.startAnimationLocked()
	block.mu.Unlock()
	return block
}

func (b *BashExecution) startAnimationLocked() {
	b.stopAnimationLocked()
	if b.runtime == nil || len(b.frames) <= 1 {
		return
	}
	b.cancelAnimation = b.runtime.Every(b.interval, b.advance)
}

func (b *BashExecution) stopAnimationLocked() {
	if b.cancelAnimation != nil {
		b.cancelAnimation()
		b.cancelAnimation = nil
	}
}

func (b *BashExecution) advance() {
	b.mu.Lock()
	if b.status != bashRunning {
		b.stopAnimationLocked()
		b.mu.Unlock()
		return
	}
	if len(b.frames) > 0 {
		b.frame = (b.frame + 1) % len(b.frames)
	}
	b.mu.Unlock()
	b.notify()
}

func (b *BashExecution) notify() {
	if b.onChange != nil {
		b.onChange()
	}
}

func (b *BashExecution) SetOnCancel(onCancel func()) {
	b.mu.Lock()
	b.onCancel = onCancel
	b.mu.Unlock()
}

func (b *BashExecution) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status == bashRunning
}

func (b *BashExecution) Cancel() bool {
	b.mu.Lock()
	if b.status != bashRunning {
		b.mu.Unlock()
		return false
	}
	onCancel := b.onCancel
	b.mu.Unlock()
	if onCancel == nil {
		return false
	}
	onCancel()
	return true
}

func (b *BashExecution) AppendOutput(chunk string) {
	clean := SanitizeDisplayText(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(chunk))
	b.mu.Lock()
	newLines := strings.Split(clean, "\n")
	if len(b.outputLines) > 0 && len(newLines) > 0 {
		b.outputLines[len(b.outputLines)-1] += newLines[0]
		b.outputLines = append(b.outputLines, newLines[1:]...)
	} else {
		b.outputLines = append(b.outputLines, newLines...)
	}
	b.mu.Unlock()
	b.notify()
}

func (b *BashExecution) SetComplete(exitCode int, hasExitCode bool, cancelled bool) {
	b.mu.Lock()
	b.exitCode = exitCode
	b.hasExitCode = hasExitCode
	switch {
	case cancelled:
		b.status = bashCancelled
	case hasExitCode && exitCode != 0:
		b.status = bashError
	default:
		b.status = bashComplete
	}
	b.stopAnimationLocked()
	b.mu.Unlock()
	b.notify()
}

func (b *BashExecution) SetExpanded(expanded bool) {
	b.mu.Lock()
	b.expanded = expanded
	b.mu.Unlock()
}

func (b *BashExecution) IsExpanded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.expanded
}

func (b *BashExecution) Invalidate() {}

func (b *BashExecution) Render(width int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	safeWidth := maxInt(1, width)
	contentWidth := blockContentWidth(width, 1)
	border := b.theme.Fg("bashMode", strings.Repeat("─", safeWidth))
	lines := []string{"", border}
	lines = append(lines, wrapBlockContent([]string{" " + b.theme.Fg("bashMode", b.theme.Bold("$ "+b.command))}, safeWidth)...)
	if b.status == bashRunning {
		spinner := b.spinnerFrame() + " " + b.theme.Fg("muted", "Running... ("+b.cancelKey+" to cancel)")
		lines = append(lines, wrapBlockContent([]string{" " + spinner}, safeWidth)...)
	}
	hidden := b.hiddenLines(contentWidth)
	output := strings.Join(b.outputLines, "\n")
	if output != "" {
		styled := b.theme.Fg("muted", output)
		wrapped := tui.WrapTextWithANSI(styled, contentWidth)
		display := wrapped
		if !b.expanded && len(wrapped) > bashPreviewLines {
			display = wrapped[len(wrapped)-bashPreviewLines:]
		}
		for _, line := range display {
			lines = append(lines, " "+line)
		}
	}
	if b.status != bashRunning {
		if !b.expanded && hidden > 0 {
			counter := "... " + itoa(hidden) + " more lines" + expandHint(b.keyDisplay, "expand")
			lines = append(lines, wrapBlockContent([]string{" " + b.theme.Fg("muted", counter)}, safeWidth)...)
		}
		if b.expanded && hidden > 0 && b.keyDisplay != "" {
			lines = append(lines, wrapBlockContent([]string{" " + b.theme.Fg("dim", "("+b.keyDisplay+" to collapse)")}, safeWidth)...)
		}
		switch b.status {
		case bashCancelled:
			lines = append(lines, wrapBlockContent([]string{" " + b.theme.Fg("warning", "(cancelled)")}, safeWidth)...)
		case bashError:
			lines = append(lines, wrapBlockContent([]string{" " + b.theme.Fg("error", "(exit "+itoa(b.exitCode)+")")}, safeWidth)...)
		}
	}
	lines = append(lines, border)
	return lines
}

func (b *BashExecution) hiddenLines(contentWidth int) int {
	if len(b.outputLines) == 0 {
		return 0
	}
	styled := b.theme.Fg("muted", strings.Join(b.outputLines, "\n"))
	wrapped := tui.WrapTextWithANSI(styled, contentWidth)
	if len(wrapped) <= bashPreviewLines {
		return 0
	}
	return len(wrapped) - bashPreviewLines
}

func (b *BashExecution) spinnerFrame() string {
	frame := ""
	if len(b.frames) > 0 {
		frame = b.frames[b.frame%len(b.frames)]
	}
	return b.theme.Fg("bashMode", frame)
}
