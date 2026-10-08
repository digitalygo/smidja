package tui

import "strings"

type Text struct {
	text     string
	paddingX int
	paddingY int
	customBg func(string) string

	cachedText  string
	cachedWidth int
	cachedValid bool
	cachedLines []string
}

func NewText(text string, paddingX, paddingY int, customBg func(string) string) *Text {
	return &Text{text: text, paddingX: paddingX, paddingY: paddingY, customBg: customBg}
}

func (t *Text) SetText(text string) {
	t.text = text
	t.cachedValid = false
}

func (t *Text) GetText() string { return t.text }

func (t *Text) SetCustomBg(customBg func(string) string) {
	t.customBg = customBg
	t.cachedValid = false
}

func (t *Text) Invalidate() {
	t.cachedValid = false
}

func (t *Text) Render(width int) []string {
	if t.cachedValid && t.cachedText == t.text && t.cachedWidth == width {
		return t.cachedLines
	}

	if strings.TrimSpace(t.text) == "" {
		t.cachedText = t.text
		t.cachedWidth = width
		t.cachedValid = true
		t.cachedLines = nil
		return nil
	}

	normalized := strings.ReplaceAll(t.text, "\t", "   ")
	paddingX := minInt(t.paddingX, maxInt(0, (width-1)/2))
	contentWidth := maxInt(1, width-paddingX*2)

	wrapped := WrapTextWithANSI(normalized, contentWidth)
	leftMargin := strings.Repeat(" ", paddingX)
	rightMargin := strings.Repeat(" ", paddingX)

	contentLines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		lineWithMargins := leftMargin + line + rightMargin
		if t.customBg != nil {
			contentLines = append(contentLines, ApplyBackgroundToLine(lineWithMargins, width, t.customBg))
		} else {
			visible := VisibleWidth(lineWithMargins)
			contentLines = append(contentLines, lineWithMargins+strings.Repeat(" ", maxInt(0, width-visible)))
		}
	}

	emptyLine := strings.Repeat(" ", width)
	emptyLines := make([]string, 0, t.paddingY)
	for i := 0; i < t.paddingY; i++ {
		if t.customBg != nil {
			emptyLines = append(emptyLines, ApplyBackgroundToLine(emptyLine, width, t.customBg))
		} else {
			emptyLines = append(emptyLines, emptyLine)
		}
	}

	result := make([]string, 0, len(emptyLines)*2+len(contentLines))
	result = append(result, emptyLines...)
	result = append(result, contentLines...)
	result = append(result, emptyLines...)

	t.cachedText = t.text
	t.cachedWidth = width
	t.cachedValid = true
	t.cachedLines = result
	return result
}

type TruncatedText struct {
	text     string
	paddingX int
	paddingY int
}

func NewTruncatedText(text string, paddingX, paddingY int) *TruncatedText {
	return &TruncatedText{text: text, paddingX: paddingX, paddingY: paddingY}
}

func (t *TruncatedText) SetText(text string) { t.text = text }

func (t *TruncatedText) Invalidate() {}

func (t *TruncatedText) Render(width int) []string {
	result := make([]string, 0, t.paddingY*2+1)
	emptyLine := strings.Repeat(" ", width)
	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}

	availableWidth := maxInt(1, width-t.paddingX*2)
	singleLine := t.text
	if newlineIndex := strings.IndexByte(t.text, '\n'); newlineIndex != -1 {
		singleLine = t.text[:newlineIndex]
	}
	display := TruncateToWidth(singleLine, availableWidth, "...", false)
	lineWithPadding := strings.Repeat(" ", t.paddingX) + display + strings.Repeat(" ", t.paddingX)
	visible := VisibleWidth(lineWithPadding)
	result = append(result, lineWithPadding+strings.Repeat(" ", maxInt(0, width-visible)))

	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}
	return result
}

type Spacer struct {
	lines int
}

func NewSpacer(lines int) *Spacer { return &Spacer{lines: lines} }

func (s *Spacer) SetLines(lines int) { s.lines = lines }

func (s *Spacer) Invalidate() {}

func (s *Spacer) Render(int) []string {
	return make([]string, s.lines)
}
