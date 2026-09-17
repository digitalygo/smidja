package tui

import (
	"encoding/base64"
	"strings"
	"sync"
)

type EditorOptions struct {
	PaddingX               int
	AutocompleteMaxVisible int
	WorkspaceRoot          string
	SmidjaHome             string
	ProjectPath            string
	History                *HistoryStore
	Provider               *AutocompleteProvider
	Theme                  *Theme
	ThinkingLevel          string
	ClipboardSupported     bool
	ClipboardWrite         func(string)
	ExternalCommand        string
	ExternalRunner         ExternalRunner
	TerminalRows           int
	DisableAutocomplete    bool
}

type editorSelection struct {
	active              bool
	startLine, startCol int
	endLine, endCol     int
}

type editorHistoryDraft struct {
	lines []string
	line  int
	col   int
}

type Editor struct {
	mu                         sync.Mutex
	buffer                     *editorBuffer
	history                    *HistoryStore
	historyIndex               int
	historyDraft               *editorHistoryDraft
	provider                   *AutocompleteProvider
	autocompleteList           *SelectList
	autocompleteKind           string
	autocompletePrefix         string
	autocompleteActive         bool
	autocompleteItems          []AutocompleteItem
	autocompleteMaxVisible     int
	paddingX                   int
	focused                    bool
	theme                      *Theme
	thinkingLevel              string
	borderColor                func(string) string
	bashMode                   bool
	selection                  editorSelection
	clipboardSupported         bool
	clipboardWrite             func(string)
	externalCommand            string
	externalRunner             ExternalRunner
	queued                     []string
	onSubmit                   func(string)
	onChange                   func(string)
	disableSubmit              bool
	jumpMode                   string
	pasteBuffer                string
	isInPaste                  bool
	scrollOffset               int
	lastWidth                  int
	terminalRows               int
	renderedVisible            int
	renderedAutocompleteHeight int
	mouseAnchorLine            int
	mouseAnchorCol             int
	mouseAnchorSet             bool
}

func NewEditor(opts EditorOptions) *Editor {
	padding := opts.PaddingX
	if padding < 0 {
		padding = 0
	}
	maxVisible := opts.AutocompleteMaxVisible
	if maxVisible <= 0 {
		maxVisible = 5
	}
	if maxVisible < 3 {
		maxVisible = 3
	}
	if maxVisible > 20 {
		maxVisible = 20
	}
	provider := opts.Provider
	if opts.DisableAutocomplete {
		provider = nil
	} else if provider == nil {
		provider = NewAutocompleteProvider(opts.WorkspaceRoot)
	}
	history := opts.History
	if history == nil && opts.SmidjaHome != "" {
		history = NewHistoryStore(opts.SmidjaHome, opts.ProjectPath)
		_ = history.Load()
	}
	rows := opts.TerminalRows
	if rows <= 0 {
		rows = 24
	}
	e := &Editor{
		buffer:                 newEditorBuffer(),
		history:                history,
		historyIndex:           -1,
		provider:               provider,
		autocompleteMaxVisible: maxVisible,
		paddingX:               padding,
		theme:                  opts.Theme,
		thinkingLevel:          opts.ThinkingLevel,
		clipboardSupported:     opts.ClipboardSupported,
		clipboardWrite:         opts.ClipboardWrite,
		externalCommand:        opts.ExternalCommand,
		externalRunner:         opts.ExternalRunner,
		terminalRows:           rows,
	}
	e.updateBorderLocked()
	return e
}

func (e *Editor) SetOnSubmit(fn func(string)) {
	e.mu.Lock()
	e.onSubmit = fn
	e.mu.Unlock()
}

func (e *Editor) SetOnChange(fn func(string)) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

func (e *Editor) SetFocused(focused bool) {
	e.mu.Lock()
	e.focused = focused
	e.mu.Unlock()
}

func (e *Editor) Invalidate() {}

func (e *Editor) SetTheme(theme *Theme) {
	e.mu.Lock()
	e.theme = theme
	e.updateBorderLocked()
	e.mu.Unlock()
}

func (e *Editor) SetThinkingLevel(level string) {
	e.mu.Lock()
	e.thinkingLevel = level
	e.updateBorderLocked()
	e.mu.Unlock()
}

func (e *Editor) SetWorkspaceRoot(root string) {
	e.mu.Lock()
	if e.provider != nil {
		e.provider.SetWorkspaceRoot(root)
	}
	e.mu.Unlock()
}

func (e *Editor) SetCommandCatalog(commands []AutocompleteItem) {
	e.mu.Lock()
	if e.provider != nil {
		e.provider.SetExtraCommands(commands)
	}
	e.mu.Unlock()
}

func (e *Editor) SetClipboard(supported bool, write func(string)) {
	e.mu.Lock()
	e.clipboardSupported = supported
	e.clipboardWrite = write
	e.mu.Unlock()
}

func (e *Editor) SetExternalCommand(command string, runner ExternalRunner) {
	e.mu.Lock()
	e.externalCommand = command
	e.externalRunner = runner
	e.mu.Unlock()
}

func (e *Editor) SetTerminalRows(rows int) {
	e.mu.Lock()
	if rows > 0 {
		e.terminalRows = rows
	}
	e.mu.Unlock()
}

func (e *Editor) SetDisableSubmit(disabled bool) {
	e.mu.Lock()
	e.disableSubmit = disabled
	e.mu.Unlock()
}

func thinkingBorderToken(level string) ThemeColor {
	switch level {
	case "off", "":
		return ThemeColor("thinkingOff")
	case "minimal":
		return ThemeColor("thinkingMinimal")
	case "low":
		return ThemeColor("thinkingLow")
	case "medium":
		return ThemeColor("thinkingMedium")
	case "high":
		return ThemeColor("thinkingHigh")
	case "xhigh":
		return ThemeColor("thinkingXhigh")
	case "max":
		return ThemeColor("thinkingMax")
	}
	return ThemeColor("thinkingOff")
}

func (e *Editor) updateBorderLocked() {
	if e.theme == nil {
		e.borderColor = func(s string) string { return s }
		e.bashMode = strings.HasPrefix(strings.TrimLeft(e.buffer.Text(), " \t\n"), "!")
		return
	}
	text := ""
	if e.buffer != nil {
		text = e.buffer.Text()
	}
	e.bashMode = strings.HasPrefix(strings.TrimLeft(text, " \t\n"), "!")
	theme := e.theme
	if e.bashMode {
		e.borderColor = func(s string) string { return theme.Fg(ThemeColor("bashMode"), s) }
		return
	}
	token := thinkingBorderToken(e.thinkingLevel)
	e.borderColor = func(s string) string { return theme.Fg(token, s) }
}

func (e *Editor) IsBashMode() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bashMode
}

func (e *Editor) BorderColor() func(string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.borderColor
}

func (e *Editor) Text() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buffer.Text()
}

func (e *Editor) ExpandedText() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buffer.ExpandedText()
}

func (e *Editor) Lines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buffer.Lines()
}

func (e *Editor) Cursor() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buffer.Cursor()
}

func (e *Editor) SetText(text string) {
	e.mu.Lock()
	e.buffer.SetText(text)
	e.exitHistoryLocked()
	e.dismissAutocompleteLocked()
	e.selection.active = false
	e.updateBorderLocked()
	changed := e.buffer.Text()
	onChange := e.onChange
	e.mu.Unlock()
	if onChange != nil {
		onChange(changed)
	}
}

func (e *Editor) QueuedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.queued)
}

func (e *Editor) QueuedMessages() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.queued...)
}

func (e *Editor) HasSelection() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.selection.active
}

func (e *Editor) SetSelection(startLine, startCol, endLine, endCol int) {
	e.mu.Lock()
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(e.buffer.lines) {
		endLine = len(e.buffer.lines) - 1
	}
	if startCol < 0 {
		startCol = 0
	}
	if endCol < 0 {
		endCol = 0
	}
	if startCol > len(e.buffer.lines[startLine]) {
		startCol = len(e.buffer.lines[startLine])
	}
	if endCol > len(e.buffer.lines[endLine]) {
		endCol = len(e.buffer.lines[endLine])
	}
	if startLine == endLine && startCol == endCol {
		e.selection.active = false
		e.mu.Unlock()
		return
	}
	e.selection = editorSelection{active: true, startLine: startLine, startCol: startCol, endLine: endLine, endCol: endCol}
	e.mu.Unlock()
}

func (e *Editor) ClearSelection() {
	e.mu.Lock()
	e.selection.active = false
	e.mu.Unlock()
}

func (e *Editor) SelectedText() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.selectedTextLocked()
}

func (e *Editor) selectedTextLocked() string {
	if !e.selection.active {
		return ""
	}
	sl, sc := e.selection.startLine, e.selection.startCol
	el, ec := e.selection.endLine, e.selection.endCol
	if sl >= len(e.buffer.lines) || el >= len(e.buffer.lines) {
		return ""
	}
	if sl == el {
		line := e.buffer.lines[sl]
		if sc > len(line) {
			sc = len(line)
		}
		if ec > len(line) {
			ec = len(line)
		}
		return line[sc:ec]
	}
	var b strings.Builder
	b.WriteString(e.buffer.lines[sl][sc:])
	for i := sl + 1; i < el; i++ {
		b.WriteString("\n")
		b.WriteString(e.buffer.lines[i])
	}
	b.WriteString("\n")
	b.WriteString(e.buffer.lines[el][:ec])
	return b.String()
}

func (e *Editor) CopySelection() bool {
	e.mu.Lock()
	text := e.selectedTextLocked()
	supported := e.clipboardSupported
	write := e.clipboardWrite
	e.mu.Unlock()
	if text == "" {
		return false
	}
	if !supported || write == nil {
		return false
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	write(OSC52Clipboard(encoded))
	return true
}

func (e *Editor) HandleClear() bool {
	e.mu.Lock()
	hasSelection := e.selection.active
	selected := e.selectedTextLocked()
	supported := e.clipboardSupported
	write := e.clipboardWrite
	e.mu.Unlock()
	if hasSelection {
		if selected != "" && supported && write != nil {
			encoded := base64.StdEncoding.EncodeToString([]byte(selected))
			write(OSC52Clipboard(encoded))
		}
		e.ClearSelection()
		return true
	}
	e.mu.Lock()
	if e.buffer.Text() == "" {
		e.mu.Unlock()
		return false
	}
	e.buffer.pushUndo()
	e.buffer.lines = []string{""}
	e.buffer.cursorLine = 0
	e.buffer.cursorCol = 0
	e.buffer.lastAction = ""
	e.buffer.hasPreferred = false
	e.exitHistoryLocked()
	e.dismissAutocompleteLocked()
	e.updateBorderLocked()
	changed := e.buffer.Text()
	onChange := e.onChange
	e.mu.Unlock()
	if onChange != nil {
		onChange(changed)
	}
	return false
}

func (e *Editor) AddToHistory(text string) {
	e.mu.Lock()
	history := e.history
	e.mu.Unlock()
	if history == nil {
		return
	}
	if history.Add(text) {
		_ = history.Save()
	}
}

func (e *Editor) exitHistoryLocked() {
	e.historyIndex = -1
	e.historyDraft = nil
}

func (e *Editor) navigateHistory(direction int) {
	if e.history == nil {
		return
	}
	entries := e.history.Entries()
	if len(entries) == 0 {
		return
	}
	newIndex := e.historyIndex - direction
	if newIndex < -1 || newIndex >= len(entries) {
		return
	}
	if e.historyIndex == -1 && newIndex >= 0 {
		lines := append([]string(nil), e.buffer.lines...)
		line, col := e.buffer.Cursor()
		e.historyDraft = &editorHistoryDraft{lines: lines, line: line, col: col}
		e.buffer.pushUndo()
	}
	e.historyIndex = newIndex
	e.dismissAutocompleteLocked()
	e.selection.active = false
	if e.historyIndex == -1 {
		if e.historyDraft != nil {
			e.buffer.lines = e.historyDraft.lines
			e.buffer.cursorLine = e.historyDraft.line
			e.buffer.cursorCol = e.historyDraft.col
			if e.buffer.cursorLine >= len(e.buffer.lines) {
				e.buffer.cursorLine = len(e.buffer.lines) - 1
			}
			if e.buffer.cursorCol > len(e.buffer.lines[e.buffer.cursorLine]) {
				e.buffer.cursorCol = len(e.buffer.lines[e.buffer.cursorLine])
			}
			e.historyDraft = nil
			e.buffer.hasPreferred = false
			e.scrollOffset = 0
		} else {
			e.buffer.lines = []string{""}
			e.buffer.cursorLine = 0
			e.buffer.cursorCol = 0
		}
	} else {
		text := entries[e.historyIndex]
		normalized := normalizeEditorText(text)
		if normalized == "" {
			e.buffer.lines = []string{""}
		} else {
			e.buffer.lines = strings.Split(normalized, "\n")
		}
		if direction == -1 {
			e.buffer.cursorLine = 0
			e.buffer.cursorCol = 0
		} else {
			e.buffer.cursorLine = len(e.buffer.lines) - 1
			e.buffer.cursorCol = len(e.buffer.lines[e.buffer.cursorLine])
		}
		e.buffer.hasPreferred = false
		e.scrollOffset = 0
	}
	e.updateBorderLocked()
}

func (e *Editor) OpenExternalEditor() error {
	e.mu.Lock()
	content := e.buffer.ExpandedText()
	command := e.externalCommand
	runner := e.externalRunner
	e.mu.Unlock()
	if strings.TrimSpace(command) == "" {
		command = ResolveExternalCommand("", nil)
	}
	result, err := EditInExternalEditor(content, command, runner)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.buffer.SetText(result)
	e.exitHistoryLocked()
	e.dismissAutocompleteLocked()
	e.selection.active = false
	e.updateBorderLocked()
	changed := e.buffer.Text()
	onChange := e.onChange
	e.mu.Unlock()
	if onChange != nil {
		onChange(changed)
	}
	return nil
}
