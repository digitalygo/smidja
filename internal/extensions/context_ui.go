package extensions

import "github.com/digitalygo/smidja/sdk"

var _ sdk.ExtendedUI = noopUI{}

func (noopUI) ShowModal(sdk.ModalFactory) (sdk.ModalResult, error) {
	return sdk.ModalResult{}, sdk.ErrModeUnsupported
}

func (noopUI) ShowComponent(string) (sdk.ModalResult, error) {
	return sdk.ModalResult{}, sdk.ErrModeUnsupported
}

func (noopUI) SetHeader(sdk.ComponentFactory) {}

func (noopUI) SetFooter(sdk.ComponentFactory) {}

func (noopUI) SetWidgetComponent(string, sdk.ComponentFactory) error {
	return sdk.ErrModeUnsupported
}

func (noopUI) SetWorkingVisible(bool) {}

func (noopUI) SetWorkingIndicator(*sdk.WorkingIndicator) {}

func (noopUI) SetHiddenThinkingLabel(string) {}

func (noopUI) SetEditorComponent(sdk.EditorFactory) error {
	return sdk.ErrModeUnsupported
}

func (noopUI) GetEditorComponent() sdk.EditorComponent { return nil }

func (noopUI) PasteToEditor(string) {}

func (noopUI) SetEditorText(string) {}

func (noopUI) GetEditorText() string { return "" }

func (noopUI) AddAutocompleteProvider(sdk.AutocompleteProvider) (func(), error) {
	return nil, sdk.ErrModeUnsupported
}

func (noopUI) OnTerminalInput(sdk.TerminalInputHandler) (func(), error) {
	return nil, sdk.ErrModeUnsupported
}

func (noopUI) AllThemes() []sdk.ThemeInfo { return nil }

func (noopUI) GetTheme(string) (sdk.Theme, bool) { return nil, false }

func (noopUI) ActiveTheme() sdk.Theme { return nil }

func (noopUI) SetTheme(string) error { return sdk.ErrModeUnsupported }

func (noopUI) ToolsExpanded() bool { return false }

func (noopUI) SetToolsExpanded(bool) {}
