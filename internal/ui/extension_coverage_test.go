package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type plainEditorComponent struct {
	mu   sync.Mutex
	text string
}

func (e *plainEditorComponent) Render(width int) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []string{e.text}
}

func (e *plainEditorComponent) Invalidate() {}

func (e *plainEditorComponent) HandleInput(data string) {
	e.mu.Lock()
	e.text += data
	e.mu.Unlock()
}

func (e *plainEditorComponent) Text() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.text
}

func (e *plainEditorComponent) SetText(text string) {
	e.mu.Lock()
	e.text = text
	e.mu.Unlock()
}

func (e *plainEditorComponent) SetOnSubmit(func(string)) {}

func (e *plainEditorComponent) SetOnChange(func(string)) {}

func TestExtensionComponentAdapters(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	empty := sdkTheme{}
	if empty.Name() != "" || empty.Fg("accent", "plain") != "plain" || empty.Bg("accent", "plain") != "plain" {
		t.Fatal("nil theme handle must be inert")
	}
	active := runner.ThemeRegistry().Active()
	theme := sdkTheme{theme: active}
	if theme.Name() != active.Name || theme.Fg("accent", "x") == "x" || theme.Bg("userMessageBg", "x") == "x" {
		t.Fatalf("theme handle did not style: name=%q fg=%q", theme.Name(), theme.Fg("accent", "x"))
	}
	if (sdkKeybindings{}).Keys("app.exit") != nil {
		t.Fatal("nil keybindings must return no keys")
	}
	keys := sdkKeybindings{manager: runner.keys}
	if len(keys.Keys("app.exit")) == 0 {
		t.Fatal("runner keybindings did not resolve an action")
	}
	component := &extensionTestComponent{text: "frame"}
	wrapped := wrapExtensionComponent(component)
	if wrapExtensionComponent(wrapped) != wrapped {
		t.Fatal("wrapping a wrapper must be idempotent")
	}

	editor := runner.Surface().Editor()
	defaultAdapter := newDefaultEditorComponent(editor, nil)
	editor.SetText("adapter text")
	if defaultAdapter.Text() != "adapter text" {
		t.Fatalf("default adapter text = %q", defaultAdapter.Text())
	}
	rendered := defaultAdapter.Render(40)
	if len(rendered) == 0 {
		t.Fatal("default adapter rendered nothing")
	}
	defaultAdapter.Invalidate()
	var observed string
	defaultAdapter.SetOnChange(func(text string) { observed = text })
	defaultAdapter.SetOnSubmit(func(text string) { observed = "submit:" + text })
	defaultAdapter.HandleInput("!")
	if defaultAdapter.Text() != "adapter text!" {
		t.Fatalf("default adapter input = %q", defaultAdapter.Text())
	}
	if observed != defaultAdapter.Text() {
		t.Fatalf("combined change callback = %q, want %q", observed, defaultAdapter.Text())
	}
	editor.SetOnSubmit(func(text string) { observed = "existing:" + text })
	defaultAdapter.SetOnSubmit(func(text string) { observed += ":new" })
	editor.SetOnSubmit(nil)
}

func TestSDKEditorAdapterAccessors(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Surface().Editor().SetText("adapter")
	custom := &plainEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return custom }); err != nil {
		t.Fatal(err)
	}
	active, ok := runner.Surface().ActiveEditor().(*sdkEditorAdapter)
	if !ok {
		t.Fatalf("active editor = %T, want the sdk adapter", runner.Surface().ActiveEditor())
	}
	if active.Text() != "adapter" {
		t.Fatalf("adapter text = %q", active.Text())
	}
	active.SetText("updated")
	if custom.Text() != "updated" {
		t.Fatalf("adapter SetText = %q", custom.Text())
	}
	active.Invalidate()
}

func TestRunnerExtensionAttachErrorsAndFactoryFailures(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	if err := runner.AttachExtensionUI(nil); err == nil {
		t.Fatal("nil registry must be rejected")
	}
	attachExtensionRegistry(t, runner)
	if err := runner.AttachExtensionUI(extensionui.NewRegistry()); !errors.Is(err, ErrExtensionUIUnexpected) {
		t.Fatalf("second attach = %v, want ErrExtensionUIUnexpected", err)
	}
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	ui.SetHeader(func() sdk.Component { panic("header boom") })
	ui.SetFooter(func() sdk.Component { panic("footer boom") })
	runner.view.RenderNow(true)
	if _, ok := ui.GetTheme(""); ok {
		t.Fatal("empty theme name must not resolve")
	}
	cleaned := &Runner{surface: runner.surface}
	cleanedRunner := newRunnerExtensions(cleaned)
	cleaned.ext = cleanedRunner
	cleanedRunner.cleanup()
	if renderer := cleaned.extensionMessageRenderer("missing"); renderer(interactive.CustomRenderContext{}, interactive.CustomEntryView{}) != nil {
		t.Fatal("renderer lookup without a registry must return nil")
	}
	if renderer := runner.extensionMessageRenderer("missing"); renderer(interactive.CustomRenderContext{}, interactive.CustomEntryView{}) != nil {
		t.Fatal("missing renderer must return nil")
	}
	if _, err := ui.ShowModal(nil); err == nil {
		t.Fatal("nil modal factory must fail")
	}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent {
		return &plainEditorComponent{}
	}); err != nil {
		t.Fatal(err)
	}
	ui.PasteToEditor(" appended")
	if !strings.Contains(ui.GetEditorText(), "appended") {
		t.Fatalf("paste without an inserter = %q", ui.GetEditorText())
	}
}

func TestRunnerExtensionBlocksWithoutSurface(t *testing.T) {
	runner := &Runner{}
	extensions := newRunnerExtensions(runner)
	runner.ext = extensions
	if themes := runner.AllThemes(); themes != nil {
		t.Fatalf("themes without registry = %v", themes)
	}
	if theme := runner.ActiveTheme(); theme == nil || theme.Name() != "" {
		t.Fatalf("empty active theme = %v", theme)
	}
	if _, ok := runner.GetTheme("dark"); ok {
		t.Fatal("theme resolution without a registry must fail")
	}
	if runner.ToolsExpanded() {
		t.Fatal("tools without a surface must be collapsed")
	}
	runner.SetToolsExpanded(true)
	runner.SetWorkingVisible(false)
	runner.SetHiddenThinkingLabel("x")
	runner.SetEditorText("x")
	if runner.GetEditorText() != "" {
		t.Fatal("editor text without a surface must be empty")
	}
	extensions.cleanup()
	if extensions.cleaned != true {
		t.Fatal("cleanup did not mark the state closed")
	}
}
