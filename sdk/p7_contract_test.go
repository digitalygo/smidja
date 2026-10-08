package sdk

import "testing"

type p7Theme struct{}

func (p7Theme) Name() string { return "fixture" }

func (p7Theme) Fg(token string, text string) string { return text }

func (p7Theme) Bg(token string, text string) string { return text }

type p7Component struct{ id string }

func (c p7Component) Render(width int) []string { return []string{c.id} }

func (c p7Component) Invalidate() {}

type p7Registration struct{}

var (
	_ RendererRegistry  = p7Registration{}
	_ UIRegistrationAPI = p7Registration{}
)

func (p7Registration) RegisterComponent(key string, factory ComponentFactory) error { return nil }

func (p7Registration) UnregisterComponent(key string) error { return nil }

func (p7Registration) RegisterWidget(key string, factory ComponentFactory) error { return nil }

func (p7Registration) UnregisterWidget(key string) error { return nil }

func (p7Registration) RegisterTerminalInputHook(key string, handler TerminalInputHandler) error {
	return nil
}

func (p7Registration) UnregisterTerminalInputHook(key string) error { return nil }

func (p7Registration) RegisterMessageRenderer(customType string, renderer MessageRenderer) error {
	return nil
}

func (p7Registration) UnregisterMessageRenderer(customType string) error { return nil }

func (p7Registration) RegisterEntryRenderer(customType string, renderer EntryRenderer) error {
	return nil
}

func (p7Registration) UnregisterEntryRenderer(customType string) error { return nil }

func (p7Registration) RegisterMarkdownTransformer(name string, transformer MarkdownTransformer) error {
	return nil
}

func (p7Registration) UnregisterMarkdownTransformer(name string) error { return nil }

type p7ExtendedUI struct{ printModeUI }

var _ ExtendedUI = p7ExtendedUI{}

func (p7ExtendedUI) ShowModal(factory ModalFactory) (ModalResult, error) {
	return ModalResult{}, nil
}

func (p7ExtendedUI) ShowComponent(key string) (ModalResult, error) { return ModalResult{}, nil }

func (p7ExtendedUI) SetHeader(factory ComponentFactory) {}

func (p7ExtendedUI) SetFooter(factory ComponentFactory) {}

func (p7ExtendedUI) SetWidgetComponent(key string, factory ComponentFactory) error { return nil }

func (p7ExtendedUI) SetWorkingVisible(visible bool) {}

func (p7ExtendedUI) SetWorkingIndicator(indicator *WorkingIndicator) {}

func (p7ExtendedUI) SetHiddenThinkingLabel(label string) {}

func (p7ExtendedUI) SetEditorComponent(factory EditorFactory) error { return nil }

func (p7ExtendedUI) GetEditorComponent() EditorComponent { return nil }

func (p7ExtendedUI) PasteToEditor(text string) {}

func (p7ExtendedUI) SetEditorText(text string) {}

func (p7ExtendedUI) GetEditorText() string { return "" }

func (p7ExtendedUI) AddAutocompleteProvider(provider AutocompleteProvider) (func(), error) {
	return nil, nil
}

func (p7ExtendedUI) OnTerminalInput(handler TerminalInputHandler) (func(), error) {
	return nil, nil
}

func (p7ExtendedUI) AllThemes() []ThemeInfo { return nil }

func (p7ExtendedUI) GetTheme(name string) (Theme, bool) { return nil, false }

func (p7ExtendedUI) ActiveTheme() Theme { return nil }

func (p7ExtendedUI) SetTheme(name string) error { return nil }

func (p7ExtendedUI) ToolsExpanded() bool { return false }

func (p7ExtendedUI) SetToolsExpanded(expanded bool) {}

func TestP7ContractsAreOptional(t *testing.T) {
	var legacy UI = printModeUI{}
	if _, ok := legacy.(ExtendedUI); ok {
		t.Fatal("the frozen UI interface must not implement ExtendedUI")
	}
	if _, ok := legacy.(UIRegistrationAPI); ok {
		t.Fatal("the frozen UI interface must not implement UIRegistrationAPI")
	}
	var extended ExtendedUI = p7ExtendedUI{}
	extended.SetWorkingVisible(false)
	var registration UIRegistrationAPI = p7Registration{}
	if err := registration.RegisterComponent("key", func() Component { return p7Component{} }); err != nil {
		t.Fatalf("RegisterComponent = %v", err)
	}
	var renderers RendererRegistry = registration
	if err := renderers.RegisterMarkdownTransformer("name", func(markdown string, ctx MarkdownTransformContext) string {
		return markdown
	}); err != nil {
		t.Fatalf("RegisterMarkdownTransformer = %v", err)
	}
	component := p7Component{id: "frame"}
	if lines := component.Render(10); len(lines) != 1 || lines[0] != "frame" {
		t.Fatalf("component render = %v", lines)
	}
	theme := p7Theme{}
	if theme.Name() != "fixture" || theme.Fg("accent", "x") != "x" {
		t.Fatal("theme handle does not round-trip")
	}
}
