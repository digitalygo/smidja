package tui

import (
	"strings"
	"sync"
	"unicode/utf8"
)

type SearchOverlayOptions struct {
	Keybindings *KeybindingsManager
	Search      *TranscriptSearch
	OnChange    func()
	OnSelect    func()
	OnClose     func()
	PromptStyle func(string) string
}

const searchPasteMaxBytes = searchMaxQueryLength

type SearchOverlay struct {
	mu          sync.Mutex
	keybindings *KeybindingsManager
	search      *TranscriptSearch
	onChange    func()
	onSelect    func()
	onClose     func()
	promptStyle func(string) string
	cursor      int
	inPaste     bool
	pasteBuffer strings.Builder
	pasteWindow string
}

func NewSearchOverlay(options SearchOverlayOptions) *SearchOverlay {
	keybindings := options.Keybindings
	if keybindings == nil {
		keybindings = GlobalKeybindings()
	}
	overlay := &SearchOverlay{
		keybindings: keybindings,
		search:      options.Search,
		onChange:    options.OnChange,
		onSelect:    options.OnSelect,
		onClose:     options.OnClose,
		promptStyle: options.PromptStyle,
	}
	if options.Search != nil {
		overlay.cursor = len([]rune(options.Search.Query()))
	}
	return overlay
}

func (o *SearchOverlay) SetPromptStyle(style func(string) string) {
	if style == nil {
		return
	}
	o.mu.Lock()
	o.promptStyle = style
	o.mu.Unlock()
}

func (o *SearchOverlay) Invalidate() {}

func (o *SearchOverlay) WantsKeyRelease() bool { return false }

func (o *SearchOverlay) Render(width int) []string {
	o.mu.Lock()
	query := ""
	if o.search != nil {
		query = o.search.Query()
	}
	runes := []rune(query)
	if o.cursor > len(runes) {
		o.cursor = len(runes)
	}
	prompt := "/ "
	if o.promptStyle != nil {
		prompt = o.promptStyle(prompt)
	}
	status := ""
	if o.search != nil {
		status = o.search.Status()
	}
	o.mu.Unlock()
	line := prompt + string(runes) + "  " + status
	return []string{TruncateToWidth(padLine(line, width), width, "", true)}
}

func padLine(line string, width int) string {
	if VisibleWidth(line) >= width {
		return line
	}
	return line + strings.Repeat(" ", width-VisibleWidth(line))
}

func (o *SearchOverlay) Reset() {
	o.mu.Lock()
	o.resetPasteLocked()
	o.mu.Unlock()
}

func (o *SearchOverlay) resetPasteLocked() {
	o.inPaste = false
	o.pasteBuffer.Reset()
	o.pasteWindow = ""
}

func (o *SearchOverlay) HandleInput(data string) {
	if data == "" || IsKeyRelease(data) {
		return
	}
	if strings.Contains(data, BracketedPasteStart) {
		o.mu.Lock()
		o.resetPasteLocked()
		o.inPaste = true
		o.mu.Unlock()
		data = strings.Replace(data, BracketedPasteStart, "", 1)
	}
	o.mu.Lock()
	if o.inPaste {
		content, remaining, done := o.consumePasteLocked(data)
		o.mu.Unlock()
		if !done {
			return
		}
		o.insertText(sanitizeSearchPaste(content))
		if remaining != "" {
			o.HandleInput(remaining)
		}
		return
	}
	o.mu.Unlock()

	switch {
	case o.matches(data, "tui.altScreen.searchClose") || MatchesKey(data, "escape"):
		o.close()
		return
	case o.matches(data, "tui.altScreen.searchPrevious"):
		if o.search != nil && o.search.SelectPrevious() {
			o.notifySelect()
		} else {
			o.notifyChange()
		}
		return
	case o.matches(data, "tui.altScreen.searchNext"):
		if o.search != nil && o.search.SelectNext() {
			o.notifySelect()
		} else {
			o.notifyChange()
		}
		return
	}

	switch {
	case o.matches(data, "tui.editor.deleteToLineStart"):
		o.setQuery("", 0)
	case o.matches(data, "tui.editor.deleteCharBackward"):
		o.deleteBackward()
	case o.matches(data, "tui.editor.cursorLineStart"):
		o.moveCursorTo(0)
	case o.matches(data, "tui.editor.cursorLineEnd"):
		o.moveCursorTo(len([]rune(o.queryText())))
	case o.matches(data, "tui.editor.cursorLeft"):
		o.moveCursor(-1)
	case o.matches(data, "tui.editor.cursorRight"):
		o.moveCursor(1)
	default:
		if printable, ok := DecodePrintableKey(data); ok {
			o.insertText(printable)
			return
		}
		if len(data) == 1 && data[0] >= 32 && data[0] <= 126 {
			o.insertText(data)
			return
		}
		if len(data) > 1 && data[0] >= 32 && !containsControlBytes(data) {
			o.insertText(data)
		}
	}
}

func (o *SearchOverlay) consumePasteLocked(data string) (string, string, bool) {
	pending := o.pasteWindow + data
	o.pasteWindow = ""
	if endIndex := strings.Index(pending, BracketedPasteEnd); endIndex >= 0 {
		o.appendPasteLocked(pending[:endIndex])
		content := o.pasteBuffer.String()
		remaining := pending[endIndex+len(BracketedPasteEnd):]
		o.resetPasteLocked()
		return content, remaining, true
	}
	window := len(BracketedPasteEnd) - 1
	if len(pending) > window {
		o.appendPasteLocked(pending[:len(pending)-window])
		pending = pending[len(pending)-window:]
	}
	o.pasteWindow = pending
	return "", "", false
}

func (o *SearchOverlay) appendPasteLocked(text string) {
	for len(text) > 0 {
		remaining := searchPasteMaxBytes - o.pasteBuffer.Len()
		if remaining <= 0 {
			return
		}
		_, size := utf8.DecodeRuneInString(text)
		if size > remaining {
			return
		}
		o.pasteBuffer.WriteString(text[:size])
		text = text[size:]
	}
}

func containsControlBytes(data string) bool {
	for index := 0; index < len(data); index++ {
		if data[index] < 32 || data[index] == 127 {
			return true
		}
	}
	return false
}

func sanitizeSearchPaste(content string) string {
	var builder strings.Builder
	for _, r := range content {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			builder.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func (o *SearchOverlay) matches(data, action string) bool {
	if o.keybindings == nil {
		return false
	}
	return o.keybindings.Matches(data, action)
}

func (o *SearchOverlay) close() {
	if o.onClose != nil {
		o.onClose()
	}
}

func (o *SearchOverlay) notifyChange() {
	if o.onChange != nil {
		o.onChange()
	}
}

func (o *SearchOverlay) notifySelect() {
	if o.onSelect != nil {
		o.onSelect()
	} else {
		o.notifyChange()
	}
}

func (o *SearchOverlay) queryText() string {
	if o.search == nil {
		return ""
	}
	return o.search.Query()
}

func (o *SearchOverlay) setQuery(query string, cursor int) {
	o.mu.Lock()
	if o.search != nil {
		o.search.SetQuery(query)
	}
	o.cursor = clamp(cursor, 0, len([]rune(query)))
	o.mu.Unlock()
	o.notifyChange()
}

func (o *SearchOverlay) insertText(text string) {
	if text == "" {
		return
	}
	o.mu.Lock()
	query := o.queryText()
	runes := []rune(query)
	cursor := clamp(o.cursor, 0, len(runes))
	inserted := []rune(text)
	next := append(append(append([]rune(nil), runes[:cursor]...), inserted...), runes[cursor:]...)
	o.cursor = cursor + len(inserted)
	if o.search != nil {
		o.search.SetQuery(string(next))
	}
	o.mu.Unlock()
	o.notifyChange()
}

func (o *SearchOverlay) deleteBackward() {
	o.mu.Lock()
	query := o.queryText()
	runes := []rune(query)
	cursor := clamp(o.cursor, 0, len(runes))
	if cursor == 0 {
		o.mu.Unlock()
		return
	}
	start := graphemeStartBefore(query, cursor)
	next := append(append([]rune(nil), runes[:start]...), runes[cursor:]...)
	o.cursor = start
	if o.search != nil {
		o.search.SetQuery(string(next))
	}
	o.mu.Unlock()
	o.notifyChange()
}

func (o *SearchOverlay) moveCursor(delta int) {
	o.mu.Lock()
	query := o.queryText()
	runes := []rune(query)
	cursor := clamp(o.cursor, 0, len(runes))
	switch {
	case delta < 0:
		cursor = graphemeStartBefore(query, cursor)
	case delta > 0:
		cursor = graphemeEndAfter(query, cursor)
	}
	o.cursor = cursor
	o.mu.Unlock()
	o.notifyChange()
}

func (o *SearchOverlay) moveCursorTo(cursor int) {
	o.mu.Lock()
	o.cursor = clamp(cursor, 0, len([]rune(o.queryText())))
	o.mu.Unlock()
	o.notifyChange()
}

func graphemeStartBefore(text string, runeCursor int) int {
	runes := []rune(text)
	if runeCursor <= 0 {
		return 0
	}
	if runeCursor > len(runes) {
		runeCursor = len(runes)
	}
	clusters := splitGraphemes(string(runes[:runeCursor]))
	if len(clusters) == 0 {
		return runeCursor
	}
	return runeCursor - len([]rune(clusters[len(clusters)-1].text))
}

func graphemeEndAfter(text string, runeCursor int) int {
	runes := []rune(text)
	if runeCursor >= len(runes) {
		return len(runes)
	}
	if runeCursor < 0 {
		runeCursor = 0
	}
	clusters := splitGraphemes(string(runes[runeCursor:]))
	if len(clusters) == 0 {
		return runeCursor
	}
	return runeCursor + len([]rune(clusters[0].text))
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func isPrintableText(data string) bool {
	if data == "" {
		return false
	}
	if strings.HasPrefix(data, "\x1b") {
		return false
	}
	for _, r := range data {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return utf8.ValidString(data)
}
