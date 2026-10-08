package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

type KeybindingDefinition struct {
	DefaultKeys []string
	Description string
}

type KeybindingsConfig map[string][]string

type keybindingConflict struct {
	Key     string
	Actions []string
}

func tuiKeybindingDefaults() map[string]KeybindingDefinition {
	darwinTreeFold := []string{"ctrl+left", "alt+left"}
	darwinTreeUnfold := []string{"ctrl+right", "alt+right"}
	if runtime.GOOS == "darwin" {
		darwinTreeFold = []string{"alt+left", "ctrl+left"}
		darwinTreeUnfold = []string{"alt+right", "ctrl+right"}
	}
	return map[string]KeybindingDefinition{
		"tui.editor.cursorUp":           {DefaultKeys: []string{"up"}, Description: "Move cursor up"},
		"tui.editor.cursorDown":         {DefaultKeys: []string{"down"}, Description: "Move cursor down"},
		"tui.editor.historyPrevious":    {DefaultKeys: nil, Description: "Select previous prompt history entry"},
		"tui.editor.historyNext":        {DefaultKeys: nil, Description: "Select next prompt history entry"},
		"tui.editor.cursorLeft":         {DefaultKeys: []string{"left", "ctrl+b"}, Description: "Move cursor left"},
		"tui.editor.cursorRight":        {DefaultKeys: []string{"right", "ctrl+f"}, Description: "Move cursor right"},
		"tui.editor.cursorWordLeft":     {DefaultKeys: []string{"alt+left", "ctrl+left", "alt+b"}, Description: "Move cursor word left"},
		"tui.editor.cursorWordRight":    {DefaultKeys: []string{"alt+right", "ctrl+right", "alt+f"}, Description: "Move cursor word right"},
		"tui.editor.cursorLineStart":    {DefaultKeys: []string{"home", "ctrl+home", "ctrl+a"}, Description: "Move to line start"},
		"tui.editor.cursorLineEnd":      {DefaultKeys: []string{"end", "ctrl+end", "ctrl+e"}, Description: "Move to line end"},
		"tui.editor.jumpForward":        {DefaultKeys: []string{"ctrl+]"}, Description: "Jump forward to character"},
		"tui.editor.jumpBackward":       {DefaultKeys: []string{"ctrl+alt+]"}, Description: "Jump backward to character"},
		"tui.editor.pageUp":             {DefaultKeys: []string{"pageUp", "ctrl+pageUp"}, Description: "Page up"},
		"tui.editor.pageDown":           {DefaultKeys: []string{"pageDown", "ctrl+pageDown"}, Description: "Page down"},
		"tui.editor.deleteCharBackward": {DefaultKeys: []string{"backspace"}, Description: "Delete character backward"},
		"tui.editor.deleteCharForward":  {DefaultKeys: []string{"delete", "ctrl+d"}, Description: "Delete character forward"},
		"tui.editor.deleteWordBackward": {DefaultKeys: []string{"ctrl+w", "alt+backspace"}, Description: "Delete word backward"},
		"tui.editor.deleteWordForward":  {DefaultKeys: []string{"alt+d", "alt+delete"}, Description: "Delete word forward"},
		"tui.editor.deleteToLineStart":  {DefaultKeys: []string{"ctrl+u"}, Description: "Delete to line start"},
		"tui.editor.deleteToLineEnd":    {DefaultKeys: []string{"ctrl+k"}, Description: "Delete to line end"},
		"tui.editor.yank":               {DefaultKeys: []string{"ctrl+y"}, Description: "Yank"},
		"tui.editor.yankPop":            {DefaultKeys: []string{"alt+y"}, Description: "Yank pop"},
		"tui.editor.undo":               {DefaultKeys: []string{"ctrl+-"}, Description: "Undo"},
		"tui.input.newLine":             {DefaultKeys: []string{"shift+enter", "ctrl+j"}, Description: "Insert newline"},
		"tui.input.submit":              {DefaultKeys: []string{"enter"}, Description: "Submit input"},
		"tui.input.tab":                 {DefaultKeys: []string{"tab"}, Description: "Tab / autocomplete"},
		"tui.input.copy":                {DefaultKeys: []string{"ctrl+c"}, Description: "Copy selection"},
		"tui.select.up":                 {DefaultKeys: []string{"up"}, Description: "Move selection up"},
		"tui.select.down":               {DefaultKeys: []string{"down"}, Description: "Move selection down"},
		"tui.select.pageUp":             {DefaultKeys: []string{"pageUp"}, Description: "Selection page up"},
		"tui.select.pageDown":           {DefaultKeys: []string{"pageDown"}, Description: "Selection page down"},
		"tui.select.confirm":            {DefaultKeys: []string{"enter"}, Description: "Confirm selection"},
		"tui.select.cancel":             {DefaultKeys: []string{"escape", "ctrl+c"}, Description: "Cancel selection"},
		"tui.altScreen.pageUp":          {DefaultKeys: []string{"pageUp"}, Description: "Scroll viewport up one page"},
		"tui.altScreen.pageDown":        {DefaultKeys: []string{"pageDown"}, Description: "Scroll viewport down one page"},
		"tui.altScreen.halfPageUp":      {DefaultKeys: nil, Description: "Scroll viewport up half a page"},
		"tui.altScreen.halfPageDown":    {DefaultKeys: nil, Description: "Scroll viewport down half a page"},
		"tui.altScreen.lineUp":          {DefaultKeys: nil, Description: "Scroll viewport up one line"},
		"tui.altScreen.lineDown":        {DefaultKeys: nil, Description: "Scroll viewport down one line"},
		"tui.altScreen.previousPrompt":  {DefaultKeys: []string{"ctrl+shift+up", "ctrl+up"}, Description: "Jump to previous semantic prompt"},
		"tui.altScreen.nextPrompt":      {DefaultKeys: []string{"ctrl+shift+down", "ctrl+down"}, Description: "Jump to next semantic prompt"},
		"tui.altScreen.search":          {DefaultKeys: []string{"ctrl+shift+f"}, Description: "Search the primary scroll view"},
		"tui.altScreen.searchNext":      {DefaultKeys: []string{"enter", "ctrl+g"}, Description: "Select the next search match"},
		"tui.altScreen.searchPrevious":  {DefaultKeys: []string{"shift+enter", "ctrl+shift+g"}, Description: "Select the previous search match"},
		"tui.altScreen.searchClose":     {DefaultKeys: []string{"escape"}, Description: "Close transcript search"},
		"tui.altScreen.top":             {DefaultKeys: []string{"home"}, Description: "Scroll viewport to top"},
		"tui.altScreen.bottom":          {DefaultKeys: []string{"end"}, Description: "Scroll viewport to bottom"},
		"app.interrupt":                 {DefaultKeys: []string{"escape"}, Description: "Cancel or abort"},
		"app.clear":                     {DefaultKeys: []string{"ctrl+c"}, Description: "Clear editor"},
		"app.exit":                      {DefaultKeys: []string{"ctrl+d"}, Description: "Exit when editor is empty"},
		"app.suspend":                   {DefaultKeys: []string{"ctrl+z"}, Description: "Suspend to background"},
		"app.thinking.cycle":            {DefaultKeys: []string{"shift+tab"}, Description: "Cycle thinking level"},
		"app.thinking.save":             {DefaultKeys: []string{"ctrl+s"}, Description: "Save thinking level"},
		"app.model.cycleForward":        {DefaultKeys: []string{"ctrl+p"}, Description: "Cycle to next model"},
		"app.model.cycleBackward":       {DefaultKeys: []string{"shift+ctrl+p"}, Description: "Cycle to previous model"},
		"app.model.select":              {DefaultKeys: []string{"ctrl+l"}, Description: "Open model selector"},
		"app.tools.expand":              {DefaultKeys: []string{"ctrl+o"}, Description: "Toggle tool output"},
		"app.thinking.toggle":           {DefaultKeys: []string{"ctrl+t"}, Description: "Toggle thinking blocks"},
		"app.session.toggleNamedFilter": {DefaultKeys: []string{"ctrl+n"}, Description: "Toggle named session filter"},
		"app.editor.external":           {DefaultKeys: []string{"ctrl+g"}, Description: "Open external editor"},
		"app.message.copy":              {DefaultKeys: []string{"ctrl+x"}, Description: "Copy message to clipboard"},
		"app.message.followUp":          {DefaultKeys: []string{"alt+enter"}, Description: "Queue follow-up message"},
		"app.message.dequeue":           {DefaultKeys: []string{"alt+up"}, Description: "Restore queued messages"},
		"app.clipboard.pasteImage":      {DefaultKeys: []string{"ctrl+v"}, Description: "Paste image from clipboard (text fallback)"},
		"app.session.new":               {DefaultKeys: nil, Description: "Start a new session"},
		"app.session.tree":              {DefaultKeys: nil, Description: "Open session tree"},
		"app.session.fork":              {DefaultKeys: nil, Description: "Fork current session"},
		"app.session.resume":            {DefaultKeys: nil, Description: "Resume a session"},
		"app.tree.foldOrUp":             {DefaultKeys: darwinTreeFold, Description: "Fold tree branch or move up"},
		"app.tree.unfoldOrDown":         {DefaultKeys: darwinTreeUnfold, Description: "Unfold tree branch or move down"},
		"app.tree.editLabel":            {DefaultKeys: []string{"shift+l"}, Description: "Edit tree label"},
		"app.tree.toggleLabelTimestamp": {DefaultKeys: []string{"shift+t"}, Description: "Toggle tree label timestamps"},
		"app.session.togglePath":        {DefaultKeys: []string{"ctrl+p"}, Description: "Toggle session path display"},
		"app.session.toggleSort":        {DefaultKeys: []string{"ctrl+s"}, Description: "Toggle session sort mode"},
		"app.session.rename":            {DefaultKeys: []string{"ctrl+r"}, Description: "Rename session"},
		"app.session.delete":            {DefaultKeys: []string{"ctrl+d"}, Description: "Delete session"},
		"app.session.deleteNoninvasive": {DefaultKeys: []string{"ctrl+backspace"}, Description: "Delete session when query is empty"},
		"app.models.save":               {DefaultKeys: []string{"ctrl+s"}, Description: "Save model selection"},
		"app.models.enableAll":          {DefaultKeys: []string{"ctrl+a"}, Description: "Enable all models"},
		"app.models.clearAll":           {DefaultKeys: []string{"ctrl+x"}, Description: "Clear all models"},
		"app.models.toggleProvider":     {DefaultKeys: []string{"ctrl+p"}, Description: "Toggle all models for provider"},
		"app.models.reorderUp":          {DefaultKeys: []string{"alt+up"}, Description: "Move model up in order"},
		"app.models.reorderDown":        {DefaultKeys: []string{"alt+down"}, Description: "Move model down in order"},
		"app.tree.filter.default":       {DefaultKeys: []string{"ctrl+d"}, Description: "Tree filter: default view"},
		"app.tree.filter.noTools":       {DefaultKeys: []string{"ctrl+t"}, Description: "Tree filter: hide tool results"},
		"app.tree.filter.userOnly":      {DefaultKeys: []string{"ctrl+u"}, Description: "Tree filter: user messages only"},
		"app.tree.filter.labeledOnly":   {DefaultKeys: []string{"ctrl+l"}, Description: "Tree filter: labeled entries only"},
		"app.tree.filter.all":           {DefaultKeys: []string{"ctrl+a"}, Description: "Tree filter: show all entries"},
		"app.tree.filter.cycleForward":  {DefaultKeys: []string{"ctrl+o"}, Description: "Tree filter: cycle forward"},
		"app.tree.filter.cycleBackward": {DefaultKeys: []string{"shift+ctrl+o"}, Description: "Tree filter: cycle backward"},
	}
}

var legacyKeybindingNames = map[string]string{
	"cursorUp": "tui.editor.cursorUp", "cursorDown": "tui.editor.cursorDown",
	"cursorLeft": "tui.editor.cursorLeft", "cursorRight": "tui.editor.cursorRight",
	"cursorWordLeft": "tui.editor.cursorWordLeft", "cursorWordRight": "tui.editor.cursorWordRight",
	"cursorLineStart": "tui.editor.cursorLineStart", "cursorLineEnd": "tui.editor.cursorLineEnd",
	"jumpForward": "tui.editor.jumpForward", "jumpBackward": "tui.editor.jumpBackward",
	"pageUp": "tui.editor.pageUp", "pageDown": "tui.editor.pageDown",
	"deleteCharBackward": "tui.editor.deleteCharBackward", "deleteCharForward": "tui.editor.deleteCharForward",
	"deleteWordBackward": "tui.editor.deleteWordBackward", "deleteWordForward": "tui.editor.deleteWordForward",
	"deleteToLineStart": "tui.editor.deleteToLineStart", "deleteToLineEnd": "tui.editor.deleteToLineEnd",
	"yank": "tui.editor.yank", "yankPop": "tui.editor.yankPop", "undo": "tui.editor.undo",
	"newLine": "tui.input.newLine", "submit": "tui.input.submit", "tab": "tui.input.tab", "copy": "tui.input.copy",
	"selectUp": "tui.select.up", "selectDown": "tui.select.down",
	"selectPageUp": "tui.select.pageUp", "selectPageDown": "tui.select.pageDown",
	"selectConfirm": "tui.select.confirm", "selectCancel": "tui.select.cancel",
	"interrupt": "app.interrupt", "clear": "app.clear", "exit": "app.exit", "suspend": "app.suspend",
	"cycleThinkingLevel": "app.thinking.cycle", "cycleModelForward": "app.model.cycleForward",
	"cycleModelBackward": "app.model.cycleBackward", "selectModel": "app.model.select",
	"expandTools": "app.tools.expand", "toggleThinking": "app.thinking.toggle",
	"toggleSessionNamedFilter": "app.session.toggleNamedFilter", "externalEditor": "app.editor.external",
	"followUp": "app.message.followUp", "dequeue": "app.message.dequeue", "pasteImage": "app.clipboard.pasteImage",
	"newSession": "app.session.new", "tree": "app.session.tree", "fork": "app.session.fork",
	"resume": "app.session.resume", "treeFoldOrUp": "app.tree.foldOrUp",
	"treeUnfoldOrDown": "app.tree.unfoldOrDown", "treeEditLabel": "app.tree.editLabel",
	"treeToggleLabelTimestamp": "app.tree.toggleLabelTimestamp", "toggleSessionPath": "app.session.togglePath",
	"toggleSessionSort": "app.session.toggleSort", "renameSession": "app.session.rename",
	"deleteSession": "app.session.delete", "deleteSessionNoninvasive": "app.session.deleteNoninvasive",
}

type KeybindingsManager struct {
	definitions map[string]KeybindingDefinition
	user        KeybindingsConfig
	keysByID    map[string][]string
	conflicts   []keybindingConflict
}

func NewKeybindingsManager(definitions map[string]KeybindingDefinition, user KeybindingsConfig) *KeybindingsManager {
	if user == nil {
		user = KeybindingsConfig{}
	}
	manager := &KeybindingsManager{definitions: definitions, user: user}
	manager.rebuild()
	return manager
}

func NewDefaultKeybindingsManager(user KeybindingsConfig) *KeybindingsManager {
	return NewKeybindingsManager(tuiKeybindingDefaults(), user)
}

func (m *KeybindingsManager) rebuild() {
	m.keysByID = make(map[string][]string, len(m.definitions))
	m.conflicts = nil

	userClaims := make(map[string]map[string]struct{})
	for action, keys := range m.user {
		if _, known := m.definitions[action]; !known {
			continue
		}
		for _, key := range normalizeKeyList(keys) {
			claimants, ok := userClaims[key]
			if !ok {
				claimants = make(map[string]struct{})
				userClaims[key] = claimants
			}
			claimants[action] = struct{}{}
		}
	}
	for key, actions := range userClaims {
		if len(actions) > 1 {
			names := make([]string, 0, len(actions))
			for action := range actions {
				names = append(names, action)
			}
			m.conflicts = append(m.conflicts, keybindingConflict{Key: key, Actions: names})
		}
	}

	for id, definition := range m.definitions {
		if userKeys, overridden := m.user[id]; overridden {
			m.keysByID[id] = normalizeKeyList(userKeys)
			continue
		}
		m.keysByID[id] = normalizeKeyList(definition.DefaultKeys)
	}
}

func normalizeKeyList(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

func (m *KeybindingsManager) Matches(data string, action string) bool {
	for _, key := range m.keysByID[action] {
		if MatchesKey(data, key) {
			return true
		}
	}
	return false
}

func (m *KeybindingsManager) Keys(action string) []string {
	keys := m.keysByID[action]
	result := make([]string, len(keys))
	copy(result, keys)
	return result
}

func (m *KeybindingsManager) Definition(action string) (KeybindingDefinition, bool) {
	definition, ok := m.definitions[action]
	return definition, ok
}

func (m *KeybindingsManager) Conflicts() []keybindingConflict {
	result := make([]keybindingConflict, len(m.conflicts))
	for i, conflict := range m.conflicts {
		actions := make([]string, len(conflict.Actions))
		copy(actions, conflict.Actions)
		result[i] = keybindingConflict{Key: conflict.Key, Actions: actions}
	}
	return result
}

func (m *KeybindingsManager) SetUserBindings(user KeybindingsConfig) {
	m.user = user
	m.rebuild()
}

func (m *KeybindingsManager) UserBindings() KeybindingsConfig {
	result := make(KeybindingsConfig, len(m.user))
	for action, keys := range m.user {
		result[action] = append([]string(nil), keys...)
	}
	return result
}

func (m *KeybindingsManager) ResolvedBindings() KeybindingsConfig {
	result := make(KeybindingsConfig, len(m.definitions))
	for id := range m.definitions {
		result[id] = m.Keys(id)
	}
	return result
}

func KeybindingsFilePath(home string) string {
	return filepath.Join(home, ".smidja", "keybindings.json")
}

func LoadKeybindingsConfig(path string) (KeybindingsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return KeybindingsConfig{}, nil
		}
		return nil, err
	}
	return ParseKeybindingsConfig(string(data))
}

func ParseKeybindingsConfig(content string) (KeybindingsConfig, error) {
	cleaned := strings.TrimLeft(content, "\ufeff\t \r\n")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, err
	}
	config := make(KeybindingsConfig, len(raw))
	for key, value := range raw {
		action := key
		if renamed, ok := legacyKeybindingNames[key]; ok {
			action = renamed
		}
		var single string
		if err := json.Unmarshal(value, &single); err == nil {
			config[action] = []string{single}
			continue
		}
		var multiple []string
		if err := json.Unmarshal(value, &multiple); err == nil {
			config[action] = multiple
		}
	}
	return config, nil
}

var globalKeybindings = func() *atomic.Pointer[KeybindingsManager] {
	pointer := &atomic.Pointer[KeybindingsManager]{}
	pointer.Store(NewDefaultKeybindingsManager(nil))
	return pointer
}()

func SetGlobalKeybindings(manager *KeybindingsManager) {
	globalKeybindings.Store(manager)
}

func GlobalKeybindings() *KeybindingsManager {
	return globalKeybindings.Load()
}
