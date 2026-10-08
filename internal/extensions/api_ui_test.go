package extensions

import (
	"errors"
	"testing"

	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/sdk"
)

type registeringExtension struct{}

func (registeringExtension) ID() string { return "registering" }

func (registeringExtension) Setup(api sdk.API) error {
	registration, ok := api.(sdk.UIRegistrationAPI)
	if !ok {
		return errors.New("api does not expose UIRegistrationAPI")
	}
	if err := registration.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return nil
	}); err != nil {
		return err
	}
	if err := registration.RegisterEntryRenderer("audit", func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component {
		return nil
	}); err != nil {
		return err
	}
	if err := registration.RegisterComponent("dialog", func() sdk.Component { return nil }); err != nil {
		return err
	}
	return registration.RegisterTerminalInputHook("hook", func(data string) sdk.TerminalInputResult {
		return sdk.TerminalInputResult{}
	})
}

func TestAPIUIRegistrationReachableFromSetup(t *testing.T) {
	registry := extensionui.NewRegistry()
	api := NewAPI(APIOptions{UI: registry})
	extensionRegistry := NewRegistry()
	if err := extensionRegistry.Register(registeringExtension{}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(extensionRegistry)
	runtime.SetAPI(func() sdk.API { return api })
	if err := runtime.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := registry.MessageRenderer("note"); !ok {
		t.Fatal("message renderer registration did not reach the registry")
	}
	if _, ok := registry.EntryRenderer("audit"); !ok {
		t.Fatal("entry renderer registration did not reach the registry")
	}
	if _, ok := registry.Component("dialog"); !ok {
		t.Fatal("component registration did not reach the registry")
	}
	if got := registry.TerminalInputHooks(); len(got) != 1 {
		t.Fatalf("terminal input hooks = %d, want 1", len(got))
	}
	typed, ok := api.(sdk.UIRegistrationAPI)
	if !ok {
		t.Fatal("api does not implement UIRegistrationAPI")
	}
	if err := typed.RegisterComponent("", nil); !errors.Is(err, extensionui.ErrEmptyKey) {
		t.Fatalf("empty component key = %v, want ErrEmptyKey", err)
	}
	if UIRegistryOf(api) != registry {
		t.Fatal("UIRegistryOf did not return the attached registry")
	}
	if UIRegistryOf(NewAPI(APIOptions{})) == nil {
		t.Fatal("an API without an explicit registry must still hold an inert one")
	}
}

func TestAPIUIDelegatesEveryRegistration(t *testing.T) {
	registry := extensionui.NewRegistry()
	api := NewAPI(APIOptions{UI: registry}).(sdk.UIRegistrationAPI)
	component := func() sdk.Component { return nil }
	if err := api.RegisterComponent("component", component); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterWidget("widget", component); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterMessageRenderer("message", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterEntryRenderer("entry", func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterMarkdownTransformer("markdown", func(markdown string, ctx sdk.MarkdownTransformContext) string { return markdown }); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterTerminalInputHook("input", func(data string) sdk.TerminalInputResult { return sdk.TerminalInputResult{} }); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Component("component"); !ok {
		t.Fatal("component registration did not delegate")
	}
	if _, ok := registry.Widgets()["widget"]; !ok {
		t.Fatal("widget registration did not delegate")
	}
	if _, ok := registry.MessageRenderer("message"); !ok {
		t.Fatal("message renderer registration did not delegate")
	}
	if _, ok := registry.EntryRenderer("entry"); !ok {
		t.Fatal("entry renderer registration did not delegate")
	}
	if got := registry.MarkdownTransformers(); len(got) != 1 {
		t.Fatal("markdown transformer registration did not delegate")
	}
	if got := registry.TerminalInputHooks(); len(got) != 1 {
		t.Fatal("terminal input hook registration did not delegate")
	}
	if err := api.UnregisterTerminalInputHook("input"); err != nil {
		t.Fatal(err)
	}
	if err := api.UnregisterMarkdownTransformer("markdown"); err != nil {
		t.Fatal(err)
	}
	if err := api.UnregisterEntryRenderer("entry"); err != nil {
		t.Fatal(err)
	}
	if err := api.UnregisterMessageRenderer("message"); err != nil {
		t.Fatal(err)
	}
	if err := api.UnregisterWidget("widget"); err != nil {
		t.Fatal(err)
	}
	if err := api.UnregisterComponent("component"); err != nil {
		t.Fatal(err)
	}
}

func TestAPIUIWithoutRegistryIsUnsupported(t *testing.T) {
	api := &api{}
	if err := api.RegisterComponent("k", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("RegisterComponent = %v", err)
	}
	if err := api.UnregisterComponent("k"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("UnregisterComponent = %v", err)
	}
	if err := api.RegisterWidget("k", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("RegisterWidget = %v", err)
	}
	if err := api.UnregisterWidget("k"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("UnregisterWidget = %v", err)
	}
	if err := api.RegisterMessageRenderer("k", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("RegisterMessageRenderer = %v", err)
	}
	if err := api.UnregisterMessageRenderer("k"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("UnregisterMessageRenderer = %v", err)
	}
	if err := api.RegisterEntryRenderer("k", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("RegisterEntryRenderer = %v", err)
	}
	if err := api.UnregisterEntryRenderer("k"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("UnregisterEntryRenderer = %v", err)
	}
	if err := api.RegisterMarkdownTransformer("k", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("RegisterMarkdownTransformer = %v", err)
	}
	if err := api.UnregisterMarkdownTransformer("k"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("UnregisterMarkdownTransformer = %v", err)
	}
	if err := api.RegisterTerminalInputHook("k", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("RegisterTerminalInputHook = %v", err)
	}
	if err := api.UnregisterTerminalInputHook("k"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("UnregisterTerminalInputHook = %v", err)
	}
	if UIRegistryOf(nil) != nil {
		t.Fatal("UIRegistryOf(nil) must be nil")
	}
	if UIRegistryOf(NewAPI(APIOptions{})) == nil {
		t.Fatal("implicit registry must be present")
	}
}

func TestRuntimeCarriesUIRegistry(t *testing.T) {
	runtime := NewRuntime(NewRegistry())
	if runtime.UIRegistry() != nil {
		t.Fatal("runtime must start without a UI registry")
	}
	registry := extensionui.NewRegistry()
	if runtime.SetUIRegistry(registry) != runtime {
		t.Fatal("SetUIRegistry must return the runtime")
	}
	if runtime.UIRegistry() != registry {
		t.Fatal("runtime did not keep the UI registry")
	}
	runtime.SetUIRegistry(nil)
	if runtime.UIRegistry() != nil {
		t.Fatal("runtime did not clear the UI registry")
	}
}

func TestNoopUIExtendedSemantics(t *testing.T) {
	ui := noopUI{}
	extended, ok := sdk.UI(ui).(sdk.ExtendedUI)
	if !ok {
		t.Fatal("noopUI must implement ExtendedUI")
	}
	if _, err := extended.ShowModal(nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("ShowModal = %v, want ErrModeUnsupported", err)
	}
	if _, err := extended.ShowComponent("key"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("ShowComponent = %v, want ErrModeUnsupported", err)
	}
	if err := extended.SetEditorComponent(nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("SetEditorComponent = %v, want ErrModeUnsupported", err)
	}
	if err := extended.SetWidgetComponent("k", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("SetWidgetComponent = %v, want ErrModeUnsupported", err)
	}
	if _, err := extended.AddAutocompleteProvider(nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("AddAutocompleteProvider = %v, want ErrModeUnsupported", err)
	}
	if _, err := extended.OnTerminalInput(nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("OnTerminalInput = %v, want ErrModeUnsupported", err)
	}
	if err := extended.SetTheme("dark"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("SetTheme = %v, want ErrModeUnsupported", err)
	}
	extended.SetHeader(nil)
	extended.SetFooter(nil)
	extended.SetWorkingVisible(false)
	extended.SetWorkingIndicator(nil)
	extended.SetHiddenThinkingLabel("label")
	extended.PasteToEditor("text")
	extended.SetEditorText("text")
	extended.SetToolsExpanded(true)
	if extended.GetEditorComponent() != nil {
		t.Fatal("print mode must not report an editor component")
	}
	if extended.GetEditorText() != "" {
		t.Fatalf("print mode editor text = %q", extended.GetEditorText())
	}
	if extended.AllThemes() != nil {
		t.Fatal("print mode must not report themes")
	}
	if _, ok := extended.GetTheme("dark"); ok {
		t.Fatal("print mode must not resolve themes")
	}
	if extended.ActiveTheme() != nil {
		t.Fatal("print mode must not report an active theme")
	}
	if extended.ToolsExpanded() {
		t.Fatal("print mode tools must be collapsed")
	}
	context := &defaultContext{}
	if _, ok := context.UI().(sdk.ExtendedUI); !ok {
		t.Fatal("the default handler context must expose ExtendedUI")
	}
}
