package tui

import (
	"encoding/base64"
	"strings"
)

func (e *Editor) submitValueLocked() (string, func()) {
	expanded := e.buffer.ExpandedText()
	trimmed := strings.TrimSpace(expanded)
	if trimmed == "" {
		return "", nil
	}
	e.buffer.lines = []string{""}
	e.buffer.cursorLine = 0
	e.buffer.cursorCol = 0
	e.buffer.pastes = map[int]string{}
	e.buffer.pasteCounter = 0
	e.buffer.undo = nil
	e.buffer.lastAction = ""
	e.buffer.hasPreferred = false
	e.exitHistoryLocked()
	e.dismissAutocompleteLocked()
	e.selection.active = false
	e.scrollOffset = 0
	e.updateBorderLocked()
	changed := e.buffer.Text()
	history := e.history
	onSubmit := e.onSubmit
	onChange := e.onChange
	if trimmed != "" && history != nil {
		history.Add(trimmed)
		_ = history.Save()
	}
	return trimmed, func() {
		if onChange != nil {
			onChange(changed)
		}
		if onSubmit != nil {
			onSubmit(trimmed)
		}
	}
}

func (e *Editor) handleFollowUpLocked() (bool, func()) {
	expanded := e.buffer.ExpandedText()
	trimmed := strings.TrimSpace(expanded)
	if trimmed == "" {
		return false, nil
	}
	e.queued = append(e.queued, trimmed)
	history := e.history
	if history != nil {
		history.Add(trimmed)
		_ = history.Save()
	}
	e.buffer.lines = []string{""}
	e.buffer.cursorLine = 0
	e.buffer.cursorCol = 0
	e.buffer.pastes = map[int]string{}
	e.buffer.pasteCounter = 0
	e.buffer.undo = nil
	e.buffer.lastAction = ""
	e.buffer.hasPreferred = false
	e.exitHistoryLocked()
	e.dismissAutocompleteLocked()
	e.selection.active = false
	e.scrollOffset = 0
	e.updateBorderLocked()
	changed := e.buffer.Text()
	onChange := e.onChange
	return true, func() {
		if onChange != nil {
			onChange(changed)
		}
	}
}

func (e *Editor) handleDequeueLocked() (int, func()) {
	if len(e.queued) == 0 {
		return 0, nil
	}
	queuedText := strings.Join(e.queued, "\n\n")
	current := strings.TrimSpace(e.buffer.Text())
	combined := queuedText
	if current != "" {
		combined = queuedText + "\n\n" + current
	}
	count := len(e.queued)
	e.queued = nil
	e.buffer.SetText(combined)
	e.buffer.cursorLine = len(e.buffer.lines) - 1
	e.buffer.cursorCol = len(e.buffer.lines[e.buffer.cursorLine])
	e.exitHistoryLocked()
	e.dismissAutocompleteLocked()
	e.selection.active = false
	e.scrollOffset = 0
	e.updateBorderLocked()
	changed := e.buffer.Text()
	onChange := e.onChange
	return count, func() {
		if onChange != nil {
			onChange(changed)
		}
	}
}

func (e *Editor) afterEditLocked() {
	e.selection.active = false
	e.updateBorderLocked()
}

func (e *Editor) HandleInput(data string) {
	e.mu.Lock()
	if e.jumpMode != "" {
		kb := GlobalKeybindings()
		if kb.Matches(data, "tui.editor.jumpForward") || kb.Matches(data, "tui.editor.jumpBackward") {
			e.jumpMode = ""
			e.mu.Unlock()
			return
		}
		if printable, ok := DecodePrintableKey(data); ok {
			direction := e.jumpMode
			e.jumpMode = ""
			e.buffer.JumpToChar(printable, direction == "forward")
			changed := e.buffer.Text()
			onChange := e.onChange
			e.updateBorderLocked()
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
		if len(data) == 1 && data[0] >= 32 && data[0] <= 126 {
			direction := e.jumpMode
			e.jumpMode = ""
			e.buffer.JumpToChar(data, direction == "forward")
			changed := e.buffer.Text()
			onChange := e.onChange
			e.updateBorderLocked()
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
		e.jumpMode = ""
	}
	if strings.Contains(data, BracketedPasteStart) {
		e.isInPaste = true
		e.pasteBuffer = ""
		data = strings.Replace(data, BracketedPasteStart, "", 1)
	}
	if e.isInPaste {
		e.pasteBuffer += data
		endIndex := strings.Index(e.pasteBuffer, BracketedPasteEnd)
		if endIndex == -1 {
			e.mu.Unlock()
			return
		}
		pasteContent := e.pasteBuffer[:endIndex]
		remaining := e.pasteBuffer[endIndex+len(BracketedPasteEnd):]
		e.isInPaste = false
		e.pasteBuffer = ""
		if pasteContent != "" {
			e.buffer.HandlePaste(pasteContent)
			e.exitHistoryLocked()
			e.dismissAutocompleteLocked()
			e.afterEditLocked()
		}
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil && pasteContent != "" {
			onChange(changed)
		}
		if remaining != "" {
			e.HandleInput(remaining)
		}
		return
	}
	kb := GlobalKeybindings()
	if kb.Matches(data, "tui.input.copy") || kb.Matches(data, "app.clear") {
		if e.selection.active {
			text := e.selectedTextLocked()
			supported := e.clipboardSupported
			write := e.clipboardWrite
			e.selection.active = false
			e.mu.Unlock()
			if text != "" && supported && write != nil {
				encoded := base64.StdEncoding.EncodeToString([]byte(text))
				write(OSC52Clipboard(encoded))
			}
			return
		}
		if kb.Matches(data, "app.clear") {
			if e.buffer.Text() != "" {
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
				return
			}
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.undo") {
		e.buffer.Undo()
		e.exitHistoryLocked()
		e.updateBorderLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if e.autocompleteActive && e.autocompleteList != nil {
		if kb.Matches(data, "tui.select.cancel") {
			e.dismissAutocompleteLocked()
			e.mu.Unlock()
			return
		}
		if kb.Matches(data, "tui.select.up") || kb.Matches(data, "tui.select.down") {
			e.autocompleteList.HandleInput(data)
			e.mu.Unlock()
			return
		}
		if kb.Matches(data, "tui.input.tab") {
			e.acceptAutocompleteLocked()
			changed := e.buffer.Text()
			onChange := e.onChange
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
		if kb.Matches(data, "tui.select.confirm") {
			kind := e.autocompleteKind
			prefix := e.autocompletePrefix
			e.acceptAutocompleteLocked()
			if kind == "slash" && strings.HasPrefix(prefix, "/") {
				trimmed, after := e.submitValueLocked()
				_ = trimmed
				e.mu.Unlock()
				if after != nil {
					after()
				}
				return
			}
			changed := e.buffer.Text()
			onChange := e.onChange
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
	}
	if kb.Matches(data, "tui.input.tab") && !e.autocompleteActive {
		e.triggerAutocompleteLocked(true)
		if e.autocompleteActive && e.autocompleteList != nil {
			if items, ok := e.autocompleteList.SelectedItem(); ok && len(e.autocompleteItems) == 1 {
				_ = items
			}
		}
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.deleteToLineEnd") {
		e.buffer.DeleteToLineEnd()
		e.exitHistoryLocked()
		e.afterEditLocked()
		e.updateAutocompleteLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.deleteToLineStart") {
		e.buffer.DeleteToLineStart()
		e.exitHistoryLocked()
		e.afterEditLocked()
		e.updateAutocompleteLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordBackward") {
		e.buffer.DeleteWordBackward()
		e.exitHistoryLocked()
		e.afterEditLocked()
		e.updateAutocompleteLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordForward") {
		e.buffer.DeleteWordForward()
		e.exitHistoryLocked()
		e.afterEditLocked()
		e.updateAutocompleteLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharBackward") || MatchesKey(data, "shift+backspace") {
		e.buffer.DeleteCharBackward()
		e.exitHistoryLocked()
		e.afterEditLocked()
		e.updateAutocompleteLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharForward") || MatchesKey(data, "shift+delete") {
		e.buffer.DeleteCharForward()
		e.exitHistoryLocked()
		e.afterEditLocked()
		e.updateAutocompleteLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.yank") {
		e.buffer.Yank()
		e.exitHistoryLocked()
		e.afterEditLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.yankPop") {
		e.buffer.YankPop()
		e.exitHistoryLocked()
		e.afterEditLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.historyPrevious") {
		e.navigateHistory(-1)
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.historyNext") {
		e.navigateHistory(1)
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorLineStart") {
		if MatchesKey(data, "ctrl+home") {
			e.buffer.MoveBufferStart()
		} else {
			e.buffer.MoveLineStart()
		}
		e.updateAutocompleteLocked()
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.cursorLineEnd") {
		if MatchesKey(data, "ctrl+end") {
			e.buffer.MoveBufferEnd()
		} else {
			e.buffer.MoveLineEnd()
		}
		e.updateAutocompleteLocked()
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordLeft") {
		e.buffer.MoveWordLeft()
		e.updateAutocompleteLocked()
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordRight") {
		e.buffer.MoveWordRight()
		e.updateAutocompleteLocked()
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "app.message.followUp") {
		ok, after := e.handleFollowUpLocked()
		e.mu.Unlock()
		if ok && after != nil {
			after()
		}
		return
	}
	if kb.Matches(data, "app.message.dequeue") {
		_, after := e.handleDequeueLocked()
		e.mu.Unlock()
		if after != nil {
			after()
		}
		return
	}
	if kb.Matches(data, "app.editor.external") {
		e.mu.Unlock()
		_ = e.OpenExternalEditor()
		return
	}
	if kb.Matches(data, "tui.input.newLine") || data == "\n" || data == "\x1b\r" || data == "\x1b[13;2~" {
		e.buffer.Newline()
		e.exitHistoryLocked()
		e.dismissAutocompleteLocked()
		e.afterEditLocked()
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if kb.Matches(data, "tui.input.submit") {
		if e.disableSubmit {
			e.mu.Unlock()
			return
		}
		line := e.buffer.lines[e.buffer.cursorLine]
		if e.buffer.cursorCol > 0 && e.buffer.cursorCol <= len(line) && line[e.buffer.cursorCol-1] == '\\' {
			e.buffer.DeleteCharBackward()
			e.buffer.Newline()
			e.exitHistoryLocked()
			e.dismissAutocompleteLocked()
			e.afterEditLocked()
			changed := e.buffer.Text()
			onChange := e.onChange
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
		_, after := e.submitValueLocked()
		e.mu.Unlock()
		if after != nil {
			after()
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorUp") {
		if e.isOnFirstVisualLineLocked() && (e.isEmptyLocked() || e.historyIndex > -1 || e.buffer.cursorCol == 0) {
			e.navigateHistory(-1)
			changed := e.buffer.Text()
			onChange := e.onChange
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
		if e.isOnFirstVisualLineLocked() {
			e.buffer.MoveLineStart()
			e.mu.Unlock()
			return
		}
		e.buffer.MoveVisualUp(e.lastWidthForNavLocked())
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		}
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.cursorDown") {
		if e.historyIndex > -1 && e.isOnLastVisualLineLocked() {
			e.navigateHistory(1)
			changed := e.buffer.Text()
			onChange := e.onChange
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
		if e.isOnLastVisualLineLocked() {
			e.buffer.MoveLineEnd()
			e.mu.Unlock()
			return
		}
		e.buffer.MoveVisualDown(e.lastWidthForNavLocked())
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		}
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.cursorRight") {
		e.buffer.MoveRight()
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		}
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.cursorLeft") {
		e.buffer.MoveLeft()
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		}
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.pageUp") {
		pageSize := e.maxVisibleLinesLocked()
		e.buffer.MovePageUp(e.lastWidthForNavLocked(), pageSize)
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.pageDown") {
		pageSize := e.maxVisibleLinesLocked()
		e.buffer.MovePageDown(e.lastWidthForNavLocked(), pageSize)
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.jumpForward") {
		e.jumpMode = "forward"
		e.mu.Unlock()
		return
	}
	if kb.Matches(data, "tui.editor.jumpBackward") {
		e.jumpMode = "backward"
		e.mu.Unlock()
		return
	}
	if MatchesKey(data, "shift+space") {
		e.buffer.InsertChar(" ")
		e.exitHistoryLocked()
		e.afterEditLocked()
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		} else {
			e.triggerAutocompleteLocked(false)
		}
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if printable, ok := DecodePrintableKey(data); ok {
		e.buffer.InsertChar(printable)
		e.exitHistoryLocked()
		e.afterEditLocked()
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		} else {
			e.triggerAutocompleteLocked(false)
		}
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if len(data) == 1 && data[0] >= 32 && data[0] <= 126 {
		e.buffer.InsertChar(data)
		e.exitHistoryLocked()
		e.afterEditLocked()
		if e.autocompleteActive {
			e.updateAutocompleteLocked()
		} else {
			e.triggerAutocompleteLocked(false)
		}
		changed := e.buffer.Text()
		onChange := e.onChange
		e.mu.Unlock()
		if onChange != nil {
			onChange(changed)
		}
		return
	}
	if len(data) > 1 && data[0] >= 32 {
		hasControl := false
		for i := 0; i < len(data); i++ {
			if data[i] < 32 || data[i] == 127 {
				hasControl = true
				break
			}
		}
		if !hasControl {
			e.buffer.InsertTextAtCursor(data)
			e.exitHistoryLocked()
			e.afterEditLocked()
			changed := e.buffer.Text()
			onChange := e.onChange
			e.mu.Unlock()
			if onChange != nil {
				onChange(changed)
			}
			return
		}
	}
	e.mu.Unlock()
}
