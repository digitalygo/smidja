# Smidja keybindings

The TUI has a namespaced action registry with defaults for the editor, selectors, transcript viewport, and application commands. Override any action from `~/.smidja/keybindings.json`.

## File and loading

Bindings are read once when the TUI runner starts from `~/.smidja/keybindings.json`. A missing file keeps the defaults. A file that is not valid JSON produces a `keybindings: ...` warning notice and the defaults stay in effect.

## Format

The file is a JSON object that maps an action ID to one key or an array of keys:

```json
{
  "tui.input.submit": ["enter", "ctrl+enter"],
  "app.tools.expand": "ctrl+o"
}
```

Rules:

- User bindings replace the default key list for that action. They do not append to it.
- An empty string or an empty array removes every key from the action, so the action no longer matches.
- Empty entries and duplicate keys inside one list are dropped.
- Values that are neither a string nor an array of strings are ignored.
- Unknown action IDs are ignored. Unknown entries do not fail the file.
- Setting one key for several actions is allowed. Each action matches independently, and the focused component decides which action applies.

## Key syntax

A key ID is a `+`-joined list of modifiers followed by one key name. Modifier names are `ctrl`, `shift`, `alt`, and `super`; key IDs are case-insensitive. Named keys include `escape` (or `esc`), `space`, `tab`, `enter` (or `return`), `backspace`, `delete`, `insert`, `clear`, `home`, `end`, `pageUp`, `pageDown`, `up`, `down`, `left`, `right`, and `f1` through `f12`. Single characters from `a` to `z`, digits, and common symbols work too. A binding that names an unknown modifier never matches.

Examples: `enter`, `shift+enter`, `ctrl+shift+up`, `alt+backspace`, `f5`.

## Action IDs and defaults

These are the exact action IDs and the default keys shipped with the TUI. `none` means the action has no default binding and must be bound in the user file to be usable.

On macOS, the two tree actions keep both keys but reverse their order, because the first entry is the primary binding.

| Action | Default keys | Description |
|---|---|---|
| `tui.editor.cursorUp` | `up` | Move cursor up |
| `tui.editor.cursorDown` | `down` | Move cursor down |
| `tui.editor.historyPrevious` | none | Select previous prompt history entry |
| `tui.editor.historyNext` | none | Select next prompt history entry |
| `tui.editor.cursorLeft` | `left`, `ctrl+b` | Move cursor left |
| `tui.editor.cursorRight` | `right`, `ctrl+f` | Move cursor right |
| `tui.editor.cursorWordLeft` | `alt+left`, `ctrl+left`, `alt+b` | Move cursor word left |
| `tui.editor.cursorWordRight` | `alt+right`, `ctrl+right`, `alt+f` | Move cursor word right |
| `tui.editor.cursorLineStart` | `home`, `ctrl+home`, `ctrl+a` | Move to line start |
| `tui.editor.cursorLineEnd` | `end`, `ctrl+end`, `ctrl+e` | Move to line end |
| `tui.editor.jumpForward` | `ctrl+]` | Jump forward to character |
| `tui.editor.jumpBackward` | `ctrl+alt+]` | Jump backward to character |
| `tui.editor.pageUp` | `pageUp`, `ctrl+pageUp` | Page up |
| `tui.editor.pageDown` | `pageDown`, `ctrl+pageDown` | Page down |
| `tui.editor.deleteCharBackward` | `backspace` | Delete character backward |
| `tui.editor.deleteCharForward` | `delete`, `ctrl+d` | Delete character forward |
| `tui.editor.deleteWordBackward` | `ctrl+w`, `alt+backspace` | Delete word backward |
| `tui.editor.deleteWordForward` | `alt+d`, `alt+delete` | Delete word forward |
| `tui.editor.deleteToLineStart` | `ctrl+u` | Delete to line start |
| `tui.editor.deleteToLineEnd` | `ctrl+k` | Delete to line end |
| `tui.editor.yank` | `ctrl+y` | Yank |
| `tui.editor.yankPop` | `alt+y` | Yank pop |
| `tui.editor.undo` | `ctrl+-` | Undo |
| `tui.input.newLine` | `shift+enter`, `ctrl+j` | Insert newline |
| `tui.input.submit` | `enter` | Submit input |
| `tui.input.tab` | `tab` | Tab / autocomplete |
| `tui.input.copy` | `ctrl+c` | Copy selection |
| `tui.select.up` | `up` | Move selection up |
| `tui.select.down` | `down` | Move selection down |
| `tui.select.pageUp` | `pageUp` | Selection page up |
| `tui.select.pageDown` | `pageDown` | Selection page down |
| `tui.select.confirm` | `enter` | Confirm selection |
| `tui.select.cancel` | `escape`, `ctrl+c` | Cancel selection |
| `tui.altScreen.pageUp` | `pageUp` | Scroll viewport up one page |
| `tui.altScreen.pageDown` | `pageDown` | Scroll viewport down one page |
| `tui.altScreen.halfPageUp` | none | Scroll viewport up half a page |
| `tui.altScreen.halfPageDown` | none | Scroll viewport down half a page |
| `tui.altScreen.lineUp` | none | Scroll viewport up one line |
| `tui.altScreen.lineDown` | none | Scroll viewport down one line |
| `tui.altScreen.previousPrompt` | `ctrl+shift+up`, `ctrl+up` | Jump to previous semantic prompt |
| `tui.altScreen.nextPrompt` | `ctrl+shift+down`, `ctrl+down` | Jump to next semantic prompt |
| `tui.altScreen.search` | `ctrl+shift+f` | Search the primary scroll view |
| `tui.altScreen.searchNext` | `enter`, `ctrl+g` | Select the next search match |
| `tui.altScreen.searchPrevious` | `shift+enter`, `ctrl+shift+g` | Select the previous search match |
| `tui.altScreen.searchClose` | `escape` | Close transcript search |
| `tui.altScreen.top` | `home` | Scroll viewport to top |
| `tui.altScreen.bottom` | `end` | Scroll viewport to bottom |
| `app.interrupt` | `escape` | Cancel or abort |
| `app.clear` | `ctrl+c` | Clear editor |
| `app.exit` | `ctrl+d` | Exit when editor is empty |
| `app.suspend` | `ctrl+z` | Suspend to background |
| `app.thinking.cycle` | `shift+tab` | Cycle thinking level |
| `app.thinking.save` | `ctrl+s` | Save thinking level |
| `app.model.cycleForward` | `ctrl+p` | Cycle to next model |
| `app.model.cycleBackward` | `shift+ctrl+p` | Cycle to previous model |
| `app.model.select` | `ctrl+l` | Open model selector |
| `app.tools.expand` | `ctrl+o` | Toggle tool output |
| `app.thinking.toggle` | `ctrl+t` | Toggle thinking blocks |
| `app.session.toggleNamedFilter` | `ctrl+n` | Toggle named session filter |
| `app.editor.external` | `ctrl+g` | Open external editor |
| `app.message.copy` | `ctrl+x` | Copy message to clipboard |
| `app.message.followUp` | `alt+enter` | Queue follow-up message |
| `app.message.dequeue` | `alt+up` | Restore queued messages |
| `app.clipboard.pasteImage` | `ctrl+v` | Paste image from clipboard (text fallback) |
| `app.session.new` | none | Start a new session |
| `app.session.tree` | none | Open session tree |
| `app.session.fork` | none | Fork current session |
| `app.session.resume` | none | Resume a session |
| `app.tree.foldOrUp` | `ctrl+left`, `alt+left` (`alt+left`, `ctrl+left` on macOS) | Fold tree branch or move up |
| `app.tree.unfoldOrDown` | `ctrl+right`, `alt+right` (`alt+right`, `ctrl+right` on macOS) | Unfold tree branch or move down |
| `app.tree.editLabel` | `shift+l` | Edit tree label |
| `app.tree.toggleLabelTimestamp` | `shift+t` | Toggle tree label timestamps |
| `app.session.togglePath` | `ctrl+p` | Toggle session path display |
| `app.session.toggleSort` | `ctrl+s` | Toggle session sort mode |
| `app.session.rename` | `ctrl+r` | Rename session |
| `app.session.delete` | `ctrl+d` | Delete session |
| `app.session.deleteNoninvasive` | `ctrl+backspace` | Delete session when query is empty |
| `app.models.save` | `ctrl+s` | Save model selection |
| `app.models.enableAll` | `ctrl+a` | Enable all models |
| `app.models.clearAll` | `ctrl+x` | Clear all models |
| `app.models.toggleProvider` | `ctrl+p` | Toggle all models for provider |
| `app.models.reorderUp` | `alt+up` | Move model up in order |
| `app.models.reorderDown` | `alt+down` | Move model down in order |
| `app.tree.filter.default` | `ctrl+d` | Tree filter: default view |
| `app.tree.filter.noTools` | `ctrl+t` | Tree filter: hide tool results |
| `app.tree.filter.userOnly` | `ctrl+u` | Tree filter: user messages only |
| `app.tree.filter.labeledOnly` | `ctrl+l` | Tree filter: labeled entries only |
| `app.tree.filter.all` | `ctrl+a` | Tree filter: show all entries |
| `app.tree.filter.cycleForward` | `ctrl+o` | Tree filter: cycle forward |
| `app.tree.filter.cycleBackward` | `shift+ctrl+o` | Tree filter: cycle backward |

## Legacy action names

Earlier builds used short action names. The loader still accepts them and remaps each one to its namespaced ID. Prefer the namespaced IDs in new files.

- `cursorUp` -> `tui.editor.cursorUp`
- `cursorDown` -> `tui.editor.cursorDown`
- `cursorLeft` -> `tui.editor.cursorLeft`
- `cursorRight` -> `tui.editor.cursorRight`
- `cursorWordLeft` -> `tui.editor.cursorWordLeft`
- `cursorWordRight` -> `tui.editor.cursorWordRight`
- `cursorLineStart` -> `tui.editor.cursorLineStart`
- `cursorLineEnd` -> `tui.editor.cursorLineEnd`
- `jumpForward` -> `tui.editor.jumpForward`
- `jumpBackward` -> `tui.editor.jumpBackward`
- `pageUp` -> `tui.editor.pageUp`
- `pageDown` -> `tui.editor.pageDown`
- `deleteCharBackward` -> `tui.editor.deleteCharBackward`
- `deleteCharForward` -> `tui.editor.deleteCharForward`
- `deleteWordBackward` -> `tui.editor.deleteWordBackward`
- `deleteWordForward` -> `tui.editor.deleteWordForward`
- `deleteToLineStart` -> `tui.editor.deleteToLineStart`
- `deleteToLineEnd` -> `tui.editor.deleteToLineEnd`
- `yank` -> `tui.editor.yank`
- `yankPop` -> `tui.editor.yankPop`
- `undo` -> `tui.editor.undo`
- `newLine` -> `tui.input.newLine`
- `submit` -> `tui.input.submit`
- `tab` -> `tui.input.tab`
- `copy` -> `tui.input.copy`
- `selectUp` -> `tui.select.up`
- `selectDown` -> `tui.select.down`
- `selectPageUp` -> `tui.select.pageUp`
- `selectPageDown` -> `tui.select.pageDown`
- `selectConfirm` -> `tui.select.confirm`
- `selectCancel` -> `tui.select.cancel`
- `interrupt` -> `app.interrupt`
- `clear` -> `app.clear`
- `exit` -> `app.exit`
- `suspend` -> `app.suspend`
- `cycleThinkingLevel` -> `app.thinking.cycle`
- `cycleModelForward` -> `app.model.cycleForward`
- `cycleModelBackward` -> `app.model.cycleBackward`
- `selectModel` -> `app.model.select`
- `expandTools` -> `app.tools.expand`
- `toggleThinking` -> `app.thinking.toggle`
- `toggleSessionNamedFilter` -> `app.session.toggleNamedFilter`
- `externalEditor` -> `app.editor.external`
- `followUp` -> `app.message.followUp`
- `dequeue` -> `app.message.dequeue`
- `pasteImage` -> `app.clipboard.pasteImage`
- `newSession` -> `app.session.new`
- `tree` -> `app.session.tree`
- `fork` -> `app.session.fork`
- `resume` -> `app.session.resume`
- `treeFoldOrUp` -> `app.tree.foldOrUp`
- `treeUnfoldOrDown` -> `app.tree.unfoldOrDown`
- `treeEditLabel` -> `app.tree.editLabel`
- `treeToggleLabelTimestamp` -> `app.tree.toggleLabelTimestamp`
- `toggleSessionPath` -> `app.session.togglePath`
- `toggleSessionSort` -> `app.session.toggleSort`
- `renameSession` -> `app.session.rename`
- `deleteSession` -> `app.session.delete`
- `deleteSessionNoninvasive` -> `app.session.deleteNoninvasive`
