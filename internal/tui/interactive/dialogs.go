package interactive

import (
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
)

type DialogTheme struct {
	Base       *tui.Theme
	Accent     func(string) string
	Border     func(string) string
	Text       func(string) string
	Muted      func(string) string
	Dim        func(string) string
	SelectedBg func(string) string
}

func identityStyle(text string) string { return text }

func NewDialogTheme(theme *tui.Theme) DialogTheme {
	if theme == nil {
		return DialogTheme{
			Accent:     identityStyle,
			Border:     identityStyle,
			Text:       identityStyle,
			Muted:      identityStyle,
			Dim:        identityStyle,
			SelectedBg: identityStyle,
		}
	}
	return DialogTheme{
		Base:       theme,
		Accent:     func(text string) string { return theme.Fg("accent", text) },
		Border:     func(text string) string { return theme.Fg("border", text) },
		Text:       func(text string) string { return theme.Fg("text", text) },
		Muted:      func(text string) string { return theme.Fg("muted", text) },
		Dim:        func(text string) string { return theme.Fg("dim", text) },
		SelectedBg: func(text string) string { return theme.Bg("selectedBg", text) },
	}
}

func dialogInnerWidth(width int) int {
	if width < 6 {
		width = 6
	}
	return width - 2
}

func dialogBoxLine(content string, width int, theme DialogTheme) string {
	inner := dialogInnerWidth(width)
	available := maxInt(0, inner-2)
	content = tui.TruncateToWidth(content, available, "", false)
	padding := strings.Repeat(" ", maxInt(0, available-tui.VisibleWidth(content)))
	return theme.Border("│") + " " + content + padding + " " + theme.Border("│")
}

func dialogBoxTop(title string, width int, theme DialogTheme) string {
	inner := dialogInnerWidth(width)
	title = tui.TruncateToWidth(title, maxInt(0, inner-4), "…", false)
	dashes := maxInt(0, inner-tui.VisibleWidth(title)-3)
	return theme.Border("╭─ " + title + " " + strings.Repeat("─", dashes) + "╮")
}

func dialogBoxBottom(width int, theme DialogTheme) string {
	return theme.Border("╰" + strings.Repeat("─", dialogInnerWidth(width)) + "╯")
}

func renderDialogFrame(title, hint string, body []string, width int, theme DialogTheme) []string {
	lines := make([]string, 0, len(body)+4)
	lines = append(lines, dialogBoxTop(title, width, theme))
	for _, line := range body {
		lines = append(lines, dialogBoxLine(line, width, theme))
	}
	if hint != "" {
		lines = append(lines, dialogBoxLine("", width, theme))
		lines = append(lines, dialogBoxLine(theme.Dim(hint), width, theme))
	}
	lines = append(lines, dialogBoxBottom(width, theme))
	return lines
}

func dialogBodyWidth(width int) int {
	return maxInt(1, dialogInnerWidth(width)-2)
}

func ConfirmDialogDefaultHint() string {
	return "Enter confirm · n decline · Esc cancel"
}

type ConfirmDialog struct {
	mu       sync.Mutex
	theme    DialogTheme
	title    string
	message  string
	hint     string
	focused  bool
	onResult func(bool)
	settled  bool
}

func NewConfirmDialog(title, message string, theme DialogTheme, onResult func(bool)) *ConfirmDialog {
	return &ConfirmDialog{theme: theme, title: SanitizeSingleLine(title), message: SanitizeDisplayText(message), hint: ConfirmDialogDefaultHint(), onResult: onResult}
}

func (d *ConfirmDialog) SetHint(hint string) {
	d.mu.Lock()
	d.hint = SanitizeSingleLine(hint)
	d.mu.Unlock()
}

func (d *ConfirmDialog) SetFocused(focused bool) {
	d.mu.Lock()
	d.focused = focused
	d.mu.Unlock()
}

func (d *ConfirmDialog) SetTheme(theme DialogTheme) {
	d.mu.Lock()
	d.theme = theme
	d.mu.Unlock()
}

func (d *ConfirmDialog) Invalidate() {}

func (d *ConfirmDialog) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	bodyWidth := dialogBodyWidth(width)
	var body []string
	if d.message != "" {
		body = append(body, tui.WrapTextWithANSI(SanitizeDisplayText(d.message), bodyWidth)...)
	}
	return renderDialogFrame(SanitizeSingleLine(d.title), SanitizeSingleLine(d.hint), body, width, d.theme)
}

func (d *ConfirmDialog) HandleInput(data string) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	keybindings := tui.GlobalKeybindings()
	switch {
	case keybindings.Matches(data, "tui.select.confirm") || data == "y" || data == "Y":
		d.settled = true
		callback := d.onResult
		d.mu.Unlock()
		if callback != nil {
			callback(true)
		}
	case data == "n" || data == "N":
		d.settled = true
		callback := d.onResult
		d.mu.Unlock()
		if callback != nil {
			callback(false)
		}
	case keybindings.Matches(data, "tui.select.cancel"):
		d.settled = true
		callback := d.onResult
		d.mu.Unlock()
		if callback != nil {
			callback(false)
		}
	default:
		d.mu.Unlock()
	}
}

type TextDialog struct {
	mu      sync.Mutex
	theme   DialogTheme
	title   string
	body    []string
	hint    string
	focused bool
	onClose func()
	settled bool
}

func NewTextDialog(title string, body []string, hint string, theme DialogTheme, onClose func()) *TextDialog {
	sanitized := make([]string, 0, len(body))
	for _, line := range body {
		sanitized = append(sanitized, SanitizeDisplayText(line))
	}
	return &TextDialog{theme: theme, title: SanitizeSingleLine(title), body: sanitized, hint: SanitizeSingleLine(hint), onClose: onClose}
}

func (d *TextDialog) SetFocused(focused bool) {
	d.mu.Lock()
	d.focused = focused
	d.mu.Unlock()
}

func (d *TextDialog) SetTheme(theme DialogTheme) {
	d.mu.Lock()
	d.theme = theme
	d.mu.Unlock()
}

func (d *TextDialog) Invalidate() {}

func (d *TextDialog) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	bodyWidth := dialogBodyWidth(width)
	var body []string
	for _, line := range d.body {
		body = append(body, tui.WrapTextWithANSI(SanitizeDisplayText(line), bodyWidth)...)
	}
	return renderDialogFrame(SanitizeSingleLine(d.title), SanitizeSingleLine(d.hint), body, width, d.theme)
}

func (d *TextDialog) HandleInput(data string) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	keybindings := tui.GlobalKeybindings()
	if keybindings.Matches(data, "tui.select.confirm") || keybindings.Matches(data, "tui.select.cancel") {
		d.settled = true
		callback := d.onClose
		d.mu.Unlock()
		if callback != nil {
			callback()
		}
		return
	}
	d.mu.Unlock()
}

type InputDialog struct {
	mu       sync.Mutex
	theme    DialogTheme
	title    string
	input    *tui.Input
	focused  bool
	onSubmit func(string)
	onCancel func()
	settled  bool
}

func NewInputDialog(title, placeholder string, theme DialogTheme, onSubmit func(string), onCancel func()) *InputDialog {
	input := tui.NewInput(tui.InputOptions{
		Prompt:           "> ",
		Placeholder:      SanitizeSingleLine(placeholder),
		PlaceholderStyle: theme.Dim,
	})
	return &InputDialog{theme: theme, title: SanitizeSingleLine(title), input: input, onSubmit: onSubmit, onCancel: onCancel}
}

func (d *InputDialog) SetFocused(focused bool) {
	d.mu.Lock()
	d.focused = focused
	if d.input != nil {
		d.input.SetFocused(focused)
	}
	d.mu.Unlock()
}

func (d *InputDialog) SetTheme(theme DialogTheme) {
	d.mu.Lock()
	d.theme = theme
	input := d.input
	d.mu.Unlock()
	if input != nil {
		input.SetPlaceholderStyle(theme.Dim)
	}
}

func (d *InputDialog) Invalidate() {
	if d.input != nil {
		d.input.Invalidate()
	}
}

func (d *InputDialog) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	bodyWidth := dialogBodyWidth(width)
	var body []string
	if d.input != nil {
		body = append(body, d.input.Render(bodyWidth)...)
	}
	return renderDialogFrame(SanitizeSingleLine(d.title), "Enter accept · Esc cancel", body, width, d.theme)
}

func (d *InputDialog) HandleInput(data string) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	keybindings := tui.GlobalKeybindings()
	switch {
	case keybindings.Matches(data, "tui.select.cancel"):
		d.settled = true
		callback := d.onCancel
		d.mu.Unlock()
		if callback != nil {
			callback()
		}
	case keybindings.Matches(data, "tui.input.submit"):
		d.settled = true
		value := ""
		if d.input != nil {
			value = d.input.Value()
		}
		callback := d.onSubmit
		d.mu.Unlock()
		if callback != nil {
			callback(value)
		}
	default:
		input := d.input
		d.mu.Unlock()
		if input != nil {
			input.HandleInput(data)
		}
	}
}

type MaskedInput struct {
	mu       sync.Mutex
	theme    DialogTheme
	title    string
	hint     string
	value    []rune
	cursor   int
	focused  bool
	onSubmit func(string)
	onCancel func()
	settled  bool
	pasting  bool
	paste    []rune
}

func NewMaskedInput(title string, theme DialogTheme, onSubmit func(string), onCancel func()) *MaskedInput {
	return &MaskedInput{
		theme:    theme,
		title:    SanitizeSingleLine(title),
		hint:     "Enter accept · Esc cancel",
		onSubmit: onSubmit,
		onCancel: onCancel,
	}
}

func (m *MaskedInput) SetFocused(focused bool) {
	m.mu.Lock()
	m.focused = focused
	m.mu.Unlock()
}

func (m *MaskedInput) SetTheme(theme DialogTheme) {
	m.mu.Lock()
	m.theme = theme
	m.mu.Unlock()
}

func (m *MaskedInput) Invalidate() {}

func (m *MaskedInput) Value() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.value)
}

func (m *MaskedInput) Render(width int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	available := maxInt(1, dialogBodyWidth(width)-2)
	visible := available
	if m.cursor >= len(m.value) {
		visible = maxInt(1, available-1)
	}
	start := 0
	if m.cursor > visible {
		start = m.cursor - visible
	}
	end := minInt(len(m.value), start+visible)
	bullets := make([]rune, maxInt(0, end-start))
	for index := range bullets {
		bullets[index] = '•'
	}
	marker := ""
	if m.focused {
		marker = tui.CursorMarker
	}
	offset := m.cursor - start
	cursorCell := " "
	cursorWidth := 0
	if offset >= 0 && offset < len(bullets) {
		cursorCell = "•"
		cursorWidth = 1
	}
	before := ""
	if offset > 0 {
		before = string(bullets[:minInt(offset, len(bullets))])
	}
	after := ""
	if offset >= 0 && offset+cursorWidth <= len(bullets) {
		after = string(bullets[offset+cursorWidth:])
	}
	text := before + marker + tui.SGRInverse + cursorCell + tui.SGRInverseOff + after
	body := []string{"> " + text}
	return renderDialogFrame(SanitizeSingleLine(m.title), SanitizeSingleLine(m.hint), body, width, m.theme)
}

func (m *MaskedInput) HandleInput(data string) {
	for _, token := range tokenizeInput(data) {
		if token == "" {
			continue
		}
		m.mu.Lock()
		if m.settled {
			m.mu.Unlock()
			return
		}
		if m.pasting {
			m.consumePasteTokenLocked(token)
			m.mu.Unlock()
			continue
		}
		if token == tui.BracketedPasteStart {
			m.pasting = true
			m.paste = m.paste[:0]
			m.mu.Unlock()
			continue
		}
		if tui.IsKeyRelease(token) {
			m.mu.Unlock()
			continue
		}
		keybindings := tui.GlobalKeybindings()
		switch {
		case keybindings.Matches(token, "tui.select.cancel"):
			m.settled = true
			callback := m.onCancel
			m.mu.Unlock()
			if callback != nil {
				callback()
			}
			return
		case keybindings.Matches(token, "tui.input.submit"):
			m.settled = true
			value := string(m.value)
			callback := m.onSubmit
			m.mu.Unlock()
			if callback != nil {
				callback(value)
			}
			return
		case keybindings.Matches(token, "tui.editor.deleteCharBackward"):
			if m.cursor > 0 {
				m.value = append(m.value[:m.cursor-1], m.value[m.cursor:]...)
				m.cursor--
			}
			m.mu.Unlock()
		case keybindings.Matches(token, "tui.editor.deleteCharForward"):
			if m.cursor < len(m.value) {
				m.value = append(m.value[:m.cursor], m.value[m.cursor+1:]...)
			}
			m.mu.Unlock()
		case keybindings.Matches(token, "tui.editor.cursorLeft"):
			if m.cursor > 0 {
				m.cursor--
			}
			m.mu.Unlock()
		case keybindings.Matches(token, "tui.editor.cursorRight"):
			if m.cursor < len(m.value) {
				m.cursor++
			}
			m.mu.Unlock()
		case keybindings.Matches(token, "tui.editor.cursorLineStart"):
			m.cursor = 0
			m.mu.Unlock()
		case keybindings.Matches(token, "tui.editor.cursorLineEnd"):
			m.cursor = len(m.value)
			m.mu.Unlock()
		default:
			if printable, ok := tui.DecodePrintableKey(token); ok {
				m.insertLocked(sanitizeSecret([]rune(printable)))
			} else if isPrintableSecretText(token) {
				m.insertLocked(sanitizeSecret([]rune(token)))
			}
			m.mu.Unlock()
		}
	}
}

func (m *MaskedInput) consumePasteTokenLocked(token string) {
	switch token {
	case tui.BracketedPasteEnd:
		m.pasting = false
		content := m.paste
		m.paste = m.paste[:0]
		m.insertLocked(sanitizeSecret(content))
	case tui.BracketedPasteStart:
	default:
		m.paste = append(m.paste, []rune(token)...)
	}
}

func (m *MaskedInput) insertLocked(runes []rune) {
	if len(runes) == 0 {
		return
	}
	next := make([]rune, 0, len(m.value)+len(runes))
	next = append(next, m.value[:m.cursor]...)
	next = append(next, runes...)
	next = append(next, m.value[m.cursor:]...)
	m.value = next
	m.cursor += len(runes)
}

func tokenizeInput(data string) []string {
	if data == "" {
		return nil
	}
	runes := []rune(data)
	tokens := make([]string, 0, len(runes))
	for index := 0; index < len(runes); {
		if runes[index] != '\x1b' {
			tokens = append(tokens, string(runes[index]))
			index++
			continue
		}
		end := escapeSequenceEnd(runes, index)
		tokens = append(tokens, string(runes[index:end]))
		index = end
	}
	return tokens
}

func escapeSequenceEnd(runes []rune, start int) int {
	if start+1 >= len(runes) {
		return start + 1
	}
	switch runes[start+1] {
	case '[':
		if start+2 < len(runes) && runes[start+2] == 'M' {
			return minInt(start+6, len(runes))
		}
		for index := start + 2; index < len(runes); index++ {
			if runes[index] >= 0x40 && runes[index] <= 0x7e {
				return index + 1
			}
		}
		return len(runes)
	case ']':
		return stringSequenceEnd(runes, start+2, true)
	case 'P', '_':
		return stringSequenceEnd(runes, start+2, false)
	case 'O':
		return minInt(start+3, len(runes))
	default:
		return minInt(start+2, len(runes))
	}
}

func stringSequenceEnd(runes []rune, start int, bellTerminated bool) int {
	for index := start; index < len(runes); index++ {
		if bellTerminated && runes[index] == 0x07 {
			return index + 1
		}
		if runes[index] == '\x1b' && index+1 < len(runes) && runes[index+1] == '\\' {
			return index + 2
		}
	}
	return len(runes)
}

func isPrintableSecretText(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return false
		}
	}
	return true
}

func sanitizeSecret(runes []rune) []rune {
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		out = append(out, r)
	}
	return out
}
