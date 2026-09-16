package interactive

import (
	"strconv"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
)

func padLineToWidth(line string, width int) string {
	return line + strings.Repeat(" ", maxInt(0, width-tui.VisibleWidth(line)))
}

func blockContentWidth(width, paddingX int) int {
	return maxInt(1, width-paddingX*2)
}

func wrapBlockContent(content []string, width int) []string {
	safeWidth := maxInt(1, width)
	result := make([]string, 0, len(content))
	for _, line := range content {
		result = append(result, tui.WrapTextWithANSI(line, safeWidth)...)
	}
	return result
}

func applyBlockBackground(content []string, width, paddingX, paddingY int, bgFn func(string) string) []string {
	safeWidth := maxInt(0, width)
	left := strings.Repeat(" ", maxInt(0, paddingX))
	lines := make([]string, 0, len(content)+paddingY*2)
	for i := 0; i < paddingY; i++ {
		lines = append(lines, bgFn(strings.Repeat(" ", safeWidth)))
	}
	for _, line := range content {
		lines = append(lines, bgFn(padLineToWidth(tui.TruncateToWidth(left+line, safeWidth, "", false), safeWidth)))
	}
	for i := 0; i < paddingY; i++ {
		lines = append(lines, bgFn(strings.Repeat(" ", safeWidth)))
	}
	return lines
}

func thinkingLevelToken(level string) tui.ThemeColor {
	switch level {
	case "minimal":
		return "thinkingMinimal"
	case "low":
		return "thinkingLow"
	case "medium":
		return "thinkingMedium"
	case "high":
		return "thinkingHigh"
	case "xhigh":
		return "thinkingXhigh"
	case "max":
		return "thinkingMax"
	default:
		return "thinkingOff"
	}
}

type UserMessage struct {
	mu       sync.Mutex
	markdown *Markdown
	theme    *tui.Theme
}

func NewUserMessage(text string, theme *tui.Theme, hyperlinks bool) *UserMessage {
	style := MarkdownStyle{TextColor: func(text string) string { return theme.Fg("userMessageText", text) }}
	return &UserMessage{markdown: NewMarkdown(text, 0, 0, theme, style, hyperlinks), theme: theme}
}

func (u *UserMessage) SetText(text string) { u.markdown.SetText(text) }

func (u *UserMessage) Text() string { return u.markdown.Text() }

func (u *UserMessage) Invalidate() {
	u.mu.Lock()
	u.markdown.Invalidate()
	u.mu.Unlock()
}

func (u *UserMessage) Render(width int) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	contentWidth := blockContentWidth(width, 1)
	content := wrapBlockContent(u.markdown.Render(contentWidth), contentWidth)
	if len(content) == 0 {
		return nil
	}
	return applyBlockBackground(content, width, 1, 1, func(text string) string {
		return u.theme.Bg("userMessageBg", text)
	})
}

type assistantSegment struct {
	thinking bool
	text     string
}

type AssistantMessagePart struct {
	Thinking bool
	Text     string
}

type AssistantMessage struct {
	mu               sync.Mutex
	theme            *tui.Theme
	hyperlinks       bool
	segments         []assistantSegment
	thinkingLevel    string
	thinkingExpanded bool
	thinkingKey      string
	stopReason       string
	errorMessage     string
	version          int
	cacheValid       bool
	cacheWidth       int
	cacheRender      []string
}

func NewAssistantMessage(theme *tui.Theme, hyperlinks bool) *AssistantMessage {
	return &AssistantMessage{theme: theme, hyperlinks: hyperlinks}
}

func (a *AssistantMessage) Invalidate() {
	a.mu.Lock()
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) AppendText(delta string) {
	if delta == "" {
		return
	}
	a.mu.Lock()
	if len(a.segments) > 0 && !a.segments[len(a.segments)-1].thinking {
		a.segments[len(a.segments)-1].text += delta
	} else {
		a.segments = append(a.segments, assistantSegment{text: delta})
	}
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) AppendThinking(delta string) {
	if delta == "" {
		return
	}
	a.mu.Lock()
	if len(a.segments) > 0 && a.segments[len(a.segments)-1].thinking {
		a.segments[len(a.segments)-1].text += delta
	} else {
		a.segments = append(a.segments, assistantSegment{thinking: true, text: delta})
	}
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) SetThinkingLevel(level string) {
	a.mu.Lock()
	a.thinkingLevel = level
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) SetThinkingExpanded(expanded bool) {
	a.mu.Lock()
	a.thinkingExpanded = expanded
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) SetThinkingKeyDisplay(key string) {
	a.mu.Lock()
	a.thinkingKey = key
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) ReconcileContent(parts []AssistantMessagePart) {
	a.mu.Lock()
	segments := make([]assistantSegment, 0, len(parts))
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		if len(segments) > 0 && segments[len(segments)-1].thinking == part.Thinking {
			segments[len(segments)-1].text += part.Text
			continue
		}
		segments = append(segments, assistantSegment{thinking: part.Thinking, text: part.Text})
	}
	a.segments = segments
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) SetStopReason(reason, errorMessage string) {
	a.mu.Lock()
	a.stopReason = reason
	a.errorMessage = SanitizeSingleLine(errorMessage)
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (a *AssistantMessage) Text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var b strings.Builder
	for _, segment := range a.segments {
		if segment.thinking {
			continue
		}
		b.WriteString(segment.text)
	}
	return b.String()
}

func (a *AssistantMessage) Render(width int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cacheValid && a.cacheWidth == width {
		return a.cacheRender
	}
	lines := a.render(width)
	a.cacheRender = lines
	a.cacheWidth = width
	a.cacheValid = true
	return lines
}

func (a *AssistantMessage) render(width int) []string {
	safeWidth := maxInt(1, width)
	lines := make([]string, 0, len(a.segments)+2)
	hasContent := false
	for _, segment := range a.segments {
		if strings.TrimSpace(segment.text) != "" {
			hasContent = true
			break
		}
	}
	if hasContent {
		lines = append(lines, "")
	}
	for _, segment := range a.segments {
		if strings.TrimSpace(segment.text) == "" {
			continue
		}
		if segment.thinking {
			lines = append(lines, a.renderThinking(segment.text, safeWidth)...)
			continue
		}
		markdown := NewMarkdown(segment.text, 1, 0, a.theme, MarkdownStyle{}, a.hyperlinks)
		lines = append(lines, markdown.Render(safeWidth)...)
	}
	lines = append(lines, a.renderStopLines(safeWidth)...)
	return lines
}

func (a *AssistantMessage) renderThinking(text string, width int) []string {
	if !a.thinkingExpanded {
		token := thinkingLevelToken(a.thinkingLevel)
		label := a.theme.Fg(token, a.theme.Italic("Thinking..."))
		if a.thinkingKey != "" {
			label += a.theme.Fg("dim", " ("+a.thinkingKey+" to expand)")
		}
		return wrapBlockContent([]string{" " + label}, maxInt(1, width))
	}
	style := MarkdownStyle{
		TextColor: func(text string) string { return a.theme.Fg("thinkingText", text) },
		Italic:    true,
	}
	header := ""
	if a.thinkingKey != "" {
		header = a.theme.Fg("dim", "Thinking ("+a.thinkingKey+" to collapse)")
	}
	markdown := NewMarkdown(text, 1, 0, a.theme, style, a.hyperlinks)
	rendered := markdown.Render(maxInt(1, width))
	if header == "" {
		return rendered
	}
	return append(wrapBlockContent([]string{" " + header}, maxInt(1, width)), rendered...)
}

func (a *AssistantMessage) renderStopLines(width int) []string {
	var message string
	switch a.stopReason {
	case "length":
		message = "Response was truncated before completion."
	case "aborted":
		message = "Operation aborted"
		if a.errorMessage != "" && a.errorMessage != "Request was aborted" {
			message = a.errorMessage
		}
	case "error":
		message = a.errorMessage
		if message == "" {
			message = "Unknown error"
		}
		message = "Error: " + message
	default:
		return nil
	}
	return append([]string{""}, wrapBlockContent([]string{a.theme.Fg("error", message)}, maxInt(1, width))...)
}

type NoticeKind int

const (
	NoticeInfo NoticeKind = iota
	NoticeWarning
	NoticeError
)

type Notice struct {
	kind  NoticeKind
	text  string
	theme *tui.Theme
}

func NewNotice(kind NoticeKind, text string, theme *tui.Theme) *Notice {
	return &Notice{kind: kind, text: SanitizeDisplayText(text), theme: theme}
}

func (n *Notice) Invalidate() {}

func (n *Notice) Render(width int) []string {
	color := tui.ThemeColor("muted")
	switch n.kind {
	case NoticeWarning:
		color = "warning"
	case NoticeError:
		color = "error"
	}
	theme := n.theme
	var lines []string
	for _, wrapped := range tui.WrapTextWithANSI(theme.Fg(color, n.text), maxInt(1, width)) {
		lines = append(lines, padLineToWidth(wrapped, width))
	}
	return append([]string{""}, lines...)
}

type SkillBlock struct {
	mu         sync.Mutex
	name       string
	content    string
	expanded   bool
	theme      *tui.Theme
	hyperlinks bool
	keyDisplay string
}

func NewSkillBlock(name, content string, theme *tui.Theme, hyperlinks bool, keyDisplay string) *SkillBlock {
	return &SkillBlock{
		name:       SanitizeSingleLine(name),
		content:    content,
		theme:      theme,
		hyperlinks: hyperlinks,
		keyDisplay: keyDisplay,
	}
}

func (s *SkillBlock) SetExpanded(expanded bool) {
	s.mu.Lock()
	s.expanded = expanded
	s.mu.Unlock()
}

func (s *SkillBlock) IsExpanded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expanded
}

func (s *SkillBlock) Invalidate() {}

func (s *SkillBlock) Render(width int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	theme := s.theme
	contentWidth := blockContentWidth(width, 1)
	label := theme.Fg("customMessageLabel", theme.Bold("[skill]"))
	var content []string
	if s.expanded {
		markdown := NewMarkdown("**"+s.name+"**\n\n"+s.content, 0, 0, theme, MarkdownStyle{
			TextColor: func(text string) string { return theme.Fg("customMessageText", text) },
		}, s.hyperlinks)
		content = append(content, label)
		content = append(content, markdown.Render(contentWidth)...)
	} else {
		line := label + " " + theme.Fg("customMessageText", s.name)
		if s.keyDisplay != "" {
			line += theme.Fg("dim", " ("+s.keyDisplay+" to expand)")
		}
		content = append(content, line)
	}
	return applyBlockBackground(wrapBlockContent(content, contentWidth), width, 1, 1, func(text string) string {
		return theme.Bg("customMessageBg", text)
	})
}

type CompactionBlock struct {
	mu           sync.Mutex
	summary      string
	tokensBefore int64
	expanded     bool
	theme        *tui.Theme
	hyperlinks   bool
	keyDisplay   string
}

func NewCompactionBlock(summary string, tokensBefore int64, theme *tui.Theme, hyperlinks bool, keyDisplay string) *CompactionBlock {
	return &CompactionBlock{
		summary:      summary,
		tokensBefore: tokensBefore,
		theme:        theme,
		hyperlinks:   hyperlinks,
		keyDisplay:   keyDisplay,
	}
}

func (c *CompactionBlock) SetExpanded(expanded bool) {
	c.mu.Lock()
	c.expanded = expanded
	c.mu.Unlock()
}

func (c *CompactionBlock) IsExpanded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expanded
}

func (c *CompactionBlock) Invalidate() {}

func (c *CompactionBlock) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	theme := c.theme
	contentWidth := blockContentWidth(width, 1)
	label := theme.Fg("customMessageLabel", theme.Bold("[compaction]"))
	tokens := formatTokenCount(c.tokensBefore)
	var content []string
	if c.expanded {
		markdown := NewMarkdown("**Compacted from "+tokens+" tokens**\n\n"+c.summary, 0, 0, theme, MarkdownStyle{
			TextColor: func(text string) string { return theme.Fg("customMessageText", text) },
		}, c.hyperlinks)
		content = append(content, label)
		content = append(content, markdown.Render(contentWidth)...)
		if c.keyDisplay != "" {
			content = append(content, theme.Fg("dim", "("+c.keyDisplay+" to collapse)"))
		}
	} else {
		line := theme.Fg("customMessageText", "Compacted from "+tokens+" tokens")
		if c.keyDisplay != "" {
			line += theme.Fg("dim", " ("+c.keyDisplay+" to expand)")
		}
		content = append(content, label, "", line)
	}
	return applyBlockBackground(wrapBlockContent(content, contentWidth), width, 1, 1, func(text string) string {
		return theme.Bg("customMessageBg", text)
	})
}

func itoa(value int) string { return strconv.Itoa(value) }
