package ui

import "github.com/digitalygo/smidja/sdk"

var _ sdk.ExtendedUI = (*boundUI)(nil)

func (u *boundUI) ShowModal(factory sdk.ModalFactory) (sdk.ModalResult, error) {
	return u.runner.showModalWithContext(u.signal, factory)
}

func (u *boundUI) ShowComponent(key string) (sdk.ModalResult, error) {
	return u.runner.showComponentWithContext(u.signal, key)
}

func (u *boundUI) SetHeader(factory sdk.ComponentFactory) { u.runner.SetHeader(factory) }

func (u *boundUI) SetFooter(factory sdk.ComponentFactory) { u.runner.SetFooter(factory) }

func (u *boundUI) SetWidgetComponent(key string, factory sdk.ComponentFactory) error {
	return u.runner.SetWidgetComponent(key, factory)
}

func (u *boundUI) SetWorkingVisible(visible bool) { u.runner.SetWorkingVisible(visible) }

func (u *boundUI) SetWorkingIndicator(indicator *sdk.WorkingIndicator) {
	u.runner.SetWorkingIndicator(indicator)
}

func (u *boundUI) SetHiddenThinkingLabel(label string) { u.runner.SetHiddenThinkingLabel(label) }

func (u *boundUI) SetEditorComponent(factory sdk.EditorFactory) error {
	return u.runner.SetEditorComponent(factory)
}

func (u *boundUI) GetEditorComponent() sdk.EditorComponent { return u.runner.GetEditorComponent() }

func (u *boundUI) PasteToEditor(text string) { u.runner.PasteToEditor(text) }

func (u *boundUI) SetEditorText(text string) { u.runner.SetEditorText(text) }

func (u *boundUI) GetEditorText() string { return u.runner.GetEditorText() }

func (u *boundUI) AddAutocompleteProvider(provider sdk.AutocompleteProvider) (func(), error) {
	return u.runner.AddAutocompleteProvider(provider)
}

func (u *boundUI) OnTerminalInput(handler sdk.TerminalInputHandler) (func(), error) {
	return u.runner.OnTerminalInput(handler)
}

func (u *boundUI) AllThemes() []sdk.ThemeInfo { return u.runner.AllThemes() }

func (u *boundUI) GetTheme(name string) (sdk.Theme, bool) { return u.runner.GetTheme(name) }

func (u *boundUI) ActiveTheme() sdk.Theme { return u.runner.ActiveTheme() }

func (u *boundUI) SetTheme(name string) error { return u.runner.SetTheme(name) }

func (u *boundUI) ToolsExpanded() bool { return u.runner.ToolsExpanded() }

func (u *boundUI) SetToolsExpanded(expanded bool) { u.runner.SetToolsExpanded(expanded) }
