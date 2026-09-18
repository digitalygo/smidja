package tui

import (
	"strings"
	"sync"
)

type InputOptions struct {
	Prompt           string
	Placeholder      string
	PlaceholderStyle func(string) string
}

type InputState struct {
	value  string
	cursor int
}

type Input struct {
	mu                  sync.Mutex
	value               string
	cursor              int
	prompt              string
	placeholder         string
	placeholderStyle    func(string) string
	renderedStartColumn int

	onSubmit func(string)
	onEscape func()

	focused bool

	pasteBuffer strings.Builder
	isInPaste   bool

	killRing   killRing
	lastAction string

	undo undoStack[InputState]
}

func NewInput(options InputOptions) *Input {
	input := &Input{
		prompt:           options.Prompt,
		placeholder:      options.Placeholder,
		placeholderStyle: options.PlaceholderStyle,
	}
	if input.prompt == "" && options.Prompt == "" {
		input.prompt = "> "
	}
	if input.placeholderStyle == nil {
		input.placeholderStyle = func(text string) string { return text }
	}
	return input
}

func (i *Input) SetOnSubmit(callback func(string)) { i.onSubmit = callback }
func (i *Input) SetOnEscape(callback func())       { i.onEscape = callback }

func (i *Input) Value() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.value
}

func (i *Input) SetValue(value string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.value = value
	if i.cursor > len(value) {
		i.cursor = len(value)
	}
}

func (i *Input) Cursor() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.cursor
}

func (i *Input) SetFocused(focused bool) {
	i.mu.Lock()
	i.focused = focused
	i.mu.Unlock()
}

func (i *Input) SetPlaceholderStyle(style func(string) string) {
	if style == nil {
		style = func(text string) string { return text }
	}
	i.mu.Lock()
	i.placeholderStyle = style
	i.mu.Unlock()
}

func (i *Input) Invalidate() {}

func (i *Input) HandleInput(data string) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if strings.Contains(data, BracketedPasteStart) {
		i.isInPaste = true
		i.pasteBuffer.Reset()
		data = strings.Replace(data, BracketedPasteStart, "", 1)
	}

	if i.isInPaste {
		i.pasteBuffer.WriteString(data)
		buffered := i.pasteBuffer.String()
		endIndex := strings.Index(buffered, BracketedPasteEnd)
		if endIndex == -1 {
			return
		}
		pasteContent := buffered[:endIndex]
		remaining := buffered[endIndex+len(BracketedPasteEnd):]
		i.isInPaste = false
		i.pasteBuffer.Reset()
		i.handlePasteLocked(pasteContent)
		i.mu.Unlock()
		if remaining != "" {
			i.HandleInput(remaining)
		}
		i.mu.Lock()
		return
	}

	keybindings := GlobalKeybindings()

	if keybindings.Matches(data, "tui.select.cancel") {
		if i.onEscape != nil {
			i.onEscape()
		}
		return
	}
	if keybindings.Matches(data, "tui.editor.undo") {
		i.undoLocked()
		return
	}
	if keybindings.Matches(data, "tui.input.submit") || data == "\n" {
		if i.onSubmit != nil {
			i.onSubmit(i.value)
		}
		return
	}
	if keybindings.Matches(data, "tui.editor.deleteCharBackward") {
		i.handleBackspaceLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.deleteCharForward") {
		i.handleForwardDeleteLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.deleteWordBackward") {
		i.deleteWordBackwardLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.deleteWordForward") {
		i.deleteWordForwardLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.deleteToLineStart") {
		i.deleteToLineStartLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.deleteToLineEnd") {
		i.deleteToLineEndLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.yank") {
		i.yankLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.yankPop") {
		i.yankPopLocked()
		return
	}
	if keybindings.Matches(data, "tui.editor.cursorLeft") {
		i.lastAction = ""
		if i.cursor > 0 {
			before := i.value[:i.cursor]
			graphemes := splitGraphemes(before)
			if len(graphemes) > 0 {
				i.cursor -= len(graphemes[len(graphemes)-1].text)
			}
		}
		return
	}
	if keybindings.Matches(data, "tui.editor.cursorRight") {
		i.lastAction = ""
		if i.cursor < len(i.value) {
			after := i.value[i.cursor:]
			graphemes := splitGraphemes(after)
			if len(graphemes) > 0 {
				i.cursor += len(graphemes[0].text)
			}
		}
		return
	}
	if keybindings.Matches(data, "tui.editor.cursorLineStart") {
		i.lastAction = ""
		i.cursor = 0
		return
	}
	if keybindings.Matches(data, "tui.editor.cursorLineEnd") {
		i.lastAction = ""
		i.cursor = len(i.value)
		return
	}
	if keybindings.Matches(data, "tui.editor.cursorWordLeft") {
		i.lastAction = ""
		if i.cursor > 0 {
			i.cursor = findWordBackward(i.value, i.cursor)
		}
		return
	}
	if keybindings.Matches(data, "tui.editor.cursorWordRight") {
		i.lastAction = ""
		if i.cursor < len(i.value) {
			i.cursor = findWordForward(i.value, i.cursor)
		}
		return
	}

	if char, ok := DecodePrintableKey(data); ok {
		i.insertCharacterLocked(char)
		return
	}

	if hasControlChars(data) {
		return
	}
	i.insertCharacterLocked(data)
}

func hasControlChars(data string) bool {
	for _, r := range data {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

func (i *Input) insertCharacterLocked(char string) {
	if isWhitespaceRune(firstRuneOf(char)) || i.lastAction != "type-word" {
		i.pushUndoLocked()
	}
	i.lastAction = "type-word"
	i.value = i.value[:i.cursor] + char + i.value[i.cursor:]
	i.cursor += len(char)
}

func firstRuneOf(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func (i *Input) handleBackspaceLocked() {
	i.lastAction = ""
	if i.cursor == 0 {
		return
	}
	i.pushUndoLocked()
	before := i.value[:i.cursor]
	graphemes := splitGraphemes(before)
	graphemeLength := 1
	if len(graphemes) > 0 {
		graphemeLength = len(graphemes[len(graphemes)-1].text)
	}
	i.value = i.value[:i.cursor-graphemeLength] + i.value[i.cursor:]
	i.cursor -= graphemeLength
}

func (i *Input) handleForwardDeleteLocked() {
	i.lastAction = ""
	if i.cursor >= len(i.value) {
		return
	}
	i.pushUndoLocked()
	after := i.value[i.cursor:]
	graphemes := splitGraphemes(after)
	graphemeLength := 1
	if len(graphemes) > 0 {
		graphemeLength = len(graphemes[0].text)
	}
	i.value = i.value[:i.cursor] + i.value[i.cursor+graphemeLength:]
}

func (i *Input) deleteToLineStartLocked() {
	if i.cursor == 0 {
		return
	}
	i.pushUndoLocked()
	deleted := i.value[:i.cursor]
	i.killRing.push(deleted, true, i.lastAction == "kill")
	i.lastAction = "kill"
	i.value = i.value[i.cursor:]
	i.cursor = 0
}

func (i *Input) deleteToLineEndLocked() {
	if i.cursor >= len(i.value) {
		return
	}
	i.pushUndoLocked()
	deleted := i.value[i.cursor:]
	i.killRing.push(deleted, false, i.lastAction == "kill")
	i.lastAction = "kill"
	i.value = i.value[:i.cursor]
}

func (i *Input) deleteWordBackwardLocked() {
	if i.cursor == 0 {
		return
	}
	wasKill := i.lastAction == "kill"
	i.pushUndoLocked()
	oldCursor := i.cursor
	i.cursor = findWordBackward(i.value, oldCursor)
	deleted := i.value[i.cursor:oldCursor]
	i.killRing.push(deleted, true, wasKill)
	i.lastAction = "kill"
	i.value = i.value[:i.cursor] + i.value[oldCursor:]
}

func (i *Input) deleteWordForwardLocked() {
	if i.cursor >= len(i.value) {
		return
	}
	wasKill := i.lastAction == "kill"
	i.pushUndoLocked()
	oldCursor := i.cursor
	deleteTo := findWordForward(i.value, oldCursor)
	deleted := i.value[oldCursor:deleteTo]
	i.killRing.push(deleted, false, wasKill)
	i.lastAction = "kill"
	i.value = i.value[:oldCursor] + i.value[deleteTo:]
}

func (i *Input) yankLocked() {
	text, ok := i.killRing.peek()
	if !ok {
		return
	}
	i.pushUndoLocked()
	i.value = i.value[:i.cursor] + text + i.value[i.cursor:]
	i.cursor += len(text)
	i.lastAction = "yank"
}

func (i *Input) yankPopLocked() {
	if i.lastAction != "yank" || i.killRing.length() <= 1 {
		return
	}
	i.pushUndoLocked()
	previous, _ := i.killRing.peek()
	i.value = i.value[:i.cursor-len(previous)] + i.value[i.cursor:]
	i.cursor -= len(previous)
	i.killRing.rotate()
	text, _ := i.killRing.peek()
	i.value = i.value[:i.cursor] + text + i.value[i.cursor:]
	i.cursor += len(text)
	i.lastAction = "yank"
}

func (i *Input) pushUndoLocked() {
	i.undo.push(InputState{value: i.value, cursor: i.cursor})
}

func (i *Input) undoLocked() {
	state, ok := i.undo.pop()
	if !ok {
		return
	}
	i.value = state.value
	i.cursor = state.cursor
	i.lastAction = ""
}

func (i *Input) handlePasteLocked(pasted string) {
	i.lastAction = ""
	i.pushUndoLocked()
	cleaned := pasted
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "")
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	cleaned = strings.ReplaceAll(cleaned, "\n", "")
	cleaned = strings.ReplaceAll(cleaned, "\t", "    ")
	i.value = i.value[:i.cursor] + cleaned + i.value[i.cursor:]
	i.cursor += len(cleaned)
}

func (i *Input) HandleMouse(event MouseEvent) *MouseEventResult {
	i.mu.Lock()
	defer i.mu.Unlock()
	if event.Type != MousePress || event.Button != MouseButtonLeft || event.Y != 0 {
		return nil
	}
	visibleColumn := maxInt(0, event.X-2)
	targetColumn := i.renderedStartColumn + visibleColumn
	currentColumn := 0
	i.cursor = len(i.value)
	offset := 0
	for _, g := range splitGraphemes(i.value) {
		nextColumn := currentColumn + g.width
		if targetColumn < nextColumn {
			i.cursor = offset
			break
		}
		currentColumn = nextColumn
		offset += len(g.text)
	}
	i.lastAction = ""
	return &MouseEventResult{Handled: true, Focus: true}
}

func (i *Input) Render(width int) []string {
	i.mu.Lock()
	defer i.mu.Unlock()

	availableWidth := width - VisibleWidth(i.prompt)
	if availableWidth <= 0 {
		return []string{TruncateToWidth(i.prompt, width, "", false)}
	}
	rawCursor := i.cursor
	if rawCursor < 0 {
		rawCursor = 0
	}
	if rawCursor > len(i.value) {
		rawCursor = len(i.value)
	}
	sanitizedValue, sanitizedCursor := sanitizeSingleLineWithMapping(i.value, rawCursor)
	sanitizedPlaceholder := sanitizeSelectSingleLine(i.placeholder)

	if len(sanitizedValue) == 0 && sanitizedPlaceholder != "" {
		placeholder := TruncateToWidth(sanitizedPlaceholder, availableWidth, "", false)
		graphemes := splitGraphemes(placeholder)
		atCursor := " "
		afterCursor := placeholder
		if len(graphemes) > 0 {
			atCursor = graphemes[0].text
			afterCursor = placeholder[len(atCursor):]
		}
		marker := ""
		if i.focused {
			marker = CursorMarker
		}
		cursorChar := SGRInverse + i.placeholderStyle(atCursor) + SGRInverseOff
		textWithCursor := marker + cursorChar + i.placeholderStyle(afterCursor)
		padding := strings.Repeat(" ", maxInt(0, availableWidth-VisibleWidth(textWithCursor)))
		return []string{i.prompt + textWithCursor + padding}
	}

	visibleText := ""
	cursorDisplay := sanitizedCursor
	i.renderedStartColumn = 0
	totalWidth := VisibleWidth(sanitizedValue)

	if totalWidth < availableWidth {
		visibleText = sanitizedValue
	} else {
		scrollWidth := availableWidth
		if sanitizedCursor == len(sanitizedValue) {
			scrollWidth = availableWidth - 1
		}
		cursorCol := VisibleWidth(sanitizedValue[:sanitizedCursor])
		if scrollWidth > 0 {
			halfWidth := scrollWidth / 2
			startCol := 0
			if cursorCol < halfWidth {
				startCol = 0
			} else if cursorCol > totalWidth-halfWidth {
				startCol = maxInt(0, totalWidth-scrollWidth)
			} else {
				startCol = maxInt(0, cursorCol-halfWidth)
			}
			i.renderedStartColumn = startCol
			visibleText = SliceByColumn(sanitizedValue, startCol, scrollWidth, true)
			beforeCursor := SliceByColumn(sanitizedValue, startCol, maxInt(0, cursorCol-startCol), true)
			cursorDisplay = len(beforeCursor)
		} else {
			visibleText = ""
			cursorDisplay = 0
		}
	}

	cursorStart := minInt(cursorDisplay, len(visibleText))
	afterSlice := splitGraphemes(visibleText[cursorStart:])
	cursorGrapheme := ""
	cursorFromText := false
	if len(afterSlice) > 0 {
		cursorGrapheme = afterSlice[0].text
		cursorFromText = true
	} else {
		cursorGrapheme = " "
	}
	beforeCursor := visibleText[:cursorStart]
	afterCursor := ""
	if cursorFromText {
		afterCursor = visibleText[cursorStart+len(cursorGrapheme):]
	}

	marker := ""
	if i.focused {
		marker = CursorMarker
	}
	cursorChar := SGRInverse + cursorGrapheme + SGRInverseOff
	textWithCursor := beforeCursor + marker + cursorChar + afterCursor
	visualLength := VisibleWidth(textWithCursor)
	padding := strings.Repeat(" ", maxInt(0, availableWidth-visualLength))
	return []string{i.prompt + textWithCursor + padding}
}
