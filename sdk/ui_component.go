package sdk

type Component interface {
	Render(width int) []string
	Invalidate()
}

type InputHandler interface {
	HandleInput(data string)
}

type Disposable interface {
	Dispose()
}

type ComponentFactory func() Component

type Theme interface {
	Name() string
	Fg(token string, text string) string
	Bg(token string, text string) string
}

type ThemeInfo struct {
	Name string
	Path string
}

type ModalResult struct {
	Value    any
	Canceled bool
}

type ModalFactory func(done func(ModalResult)) Component

type ModalComponent interface {
	Component
	SetModalDone(done func(ModalResult))
}

type WorkingIndicator struct {
	Frames   []string
	Interval int
}

type Keybindings interface {
	Keys(action string) []string
}

type EditorComponent interface {
	Component
	HandleInput(data string)
	Text() string
	SetText(text string)
	SetOnSubmit(fn func(string))
	SetOnChange(fn func(string))
}

type EditorInserter interface {
	InsertTextAtCursor(text string)
}

type EditorContext struct {
	Theme        Theme
	Keybindings  Keybindings
	TerminalRows int
	Width        int
}

type EditorFactory func(ctx EditorContext) EditorComponent

type AutocompleteSuggestion struct {
	Value       string
	Label       string
	Description string
}

type AutocompleteContext struct {
	Text  string
	Line  int
	Col   int
	Token string
	Kind  string
	Width int
}

type AutocompleteProvider interface {
	Suggest(ctx AutocompleteContext) []AutocompleteSuggestion
}

type TerminalInputResult struct {
	Consume bool
	Data    string
	Replace bool
}

type TerminalInputHandler func(data string) TerminalInputResult

type ExtendedUI interface {
	UI

	ShowModal(factory ModalFactory) (ModalResult, error)
	ShowComponent(key string) (ModalResult, error)

	SetHeader(factory ComponentFactory)
	SetFooter(factory ComponentFactory)
	SetWidgetComponent(key string, factory ComponentFactory) error

	SetWorkingVisible(visible bool)
	SetWorkingIndicator(indicator *WorkingIndicator)
	SetHiddenThinkingLabel(label string)

	SetEditorComponent(factory EditorFactory) error
	GetEditorComponent() EditorComponent
	PasteToEditor(text string)
	SetEditorText(text string)
	GetEditorText() string

	AddAutocompleteProvider(provider AutocompleteProvider) (func(), error)
	OnTerminalInput(handler TerminalInputHandler) (func(), error)

	AllThemes() []ThemeInfo
	GetTheme(name string) (Theme, bool)
	ActiveTheme() Theme
	SetTheme(name string) error

	ToolsExpanded() bool
	SetToolsExpanded(expanded bool)
}
