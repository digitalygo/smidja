package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/sdk"
)

type testEditorComponent struct {
	mu       sync.Mutex
	text     string
	onSubmit func(string)
	onChange func(string)
	inputs   []string
	disposes int
}

func (e *testEditorComponent) Render(width int) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []string{"CUSTOM EDITOR " + e.text}
}

func (e *testEditorComponent) Invalidate() {}

func (e *testEditorComponent) HandleInput(data string) {
	e.mu.Lock()
	e.inputs = append(e.inputs, data)
	e.text += data
	callback := e.onChange
	e.mu.Unlock()
	if callback != nil {
		callback(data)
	}
}

func (e *testEditorComponent) Text() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.text
}

func (e *testEditorComponent) SetText(text string) {
	e.mu.Lock()
	e.text = text
	callback := e.onChange
	e.mu.Unlock()
	if callback != nil {
		callback(text)
	}
}

func (e *testEditorComponent) SetOnSubmit(fn func(string)) {
	e.mu.Lock()
	e.onSubmit = fn
	e.mu.Unlock()
}

func (e *testEditorComponent) SetOnChange(fn func(string)) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

func (e *testEditorComponent) InsertTextAtCursor(text string) {
	e.SetText(e.Text() + text)
}

func (e *testEditorComponent) Dispose() {
	e.mu.Lock()
	e.disposes++
	e.mu.Unlock()
}

func (e *testEditorComponent) DisposeCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.disposes
}

func (e *testEditorComponent) Inputs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.inputs...)
}

func (e *testEditorComponent) submit(text string) {
	e.mu.Lock()
	callback := e.onSubmit
	e.mu.Unlock()
	if callback != nil {
		callback(text)
	}
}

func TestRunnerExtensionEditorFactoryRoundTrip(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Surface().Editor().SetText("default text")
	component := &testEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent {
		if ctx.Theme == nil || ctx.Keybindings == nil || ctx.TerminalRows == 0 {
			t.Errorf("editor context = %+v, want theme, keybindings and rows", ctx)
		}
		return component
	}); err != nil {
		t.Fatalf("SetEditorComponent: %v", err)
	}
	if ui.GetEditorComponent() != sdk.EditorComponent(component) {
		t.Fatal("GetEditorComponent did not return the custom component")
	}
	if component.Text() != "default text" {
		t.Fatalf("custom editor initial text = %q, want the default text", component.Text())
	}
	if runner.Surface().ActiveEditor() != runner.view.FocusedComponent() {
		t.Fatal("custom editor is not focused")
	}
	frame := extensionFrameText(t, runner)
	if !strings.Contains(frame, "CUSTOM EDITOR default text") {
		t.Fatalf("custom editor frame missing:\n%s", frame)
	}
	ui.SetEditorText("only custom")
	if got := runner.Surface().Editor().Text(); got != "default text" {
		t.Fatalf("SetEditorText changed the hidden default editor: %q", got)
	}
	if ui.GetEditorText() != "only custom" {
		t.Fatalf("GetEditorText = %q, want only custom", ui.GetEditorText())
	}
	ui.PasteToEditor(" pasted")
	if ui.GetEditorText() != "only custom pasted" {
		t.Fatalf("PasteToEditor = %q", ui.GetEditorText())
	}
	var submitted string
	runner.SetOnSubmit(func(text string) { submitted = text })
	component.submit("payload")
	if submitted != "payload" {
		t.Fatalf("custom editor submit = %q, want payload", submitted)
	}
	terminal.SendInput("z")
	if !strings.HasSuffix(component.Text(), "z") {
		t.Fatalf("typed input did not reach the custom editor: inputs=%v", component.Inputs())
	}
	if err := ui.SetEditorComponent(nil); err != nil {
		t.Fatalf("restore default: %v", err)
	}
	if component.DisposeCount() != 1 {
		t.Fatalf("custom editor dispose count = %d, want 1", component.DisposeCount())
	}
	if runner.Surface().ActiveEditor() != runner.Surface().Editor() {
		t.Fatal("default editor was not restored")
	}
	if got := runner.Surface().Editor().Text(); got != component.Text() {
		t.Fatalf("restored default editor text = %q, want the custom text %q", got, component.Text())
	}
	defaultComponent := ui.GetEditorComponent()
	if defaultComponent == nil || defaultComponent.Text() != runner.Surface().Editor().Text() {
		t.Fatal("default editor component does not round-trip the text")
	}
	defaultComponent.SetText("via adapter")
	if runner.Surface().Editor().Text() != "via adapter" {
		t.Fatalf("default editor adapter SetText = %q", runner.Surface().Editor().Text())
	}
}

func TestRunnerExtensionEditorFactoryFailureKeepsCurrent(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	good := &testEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return good }); err != nil {
		t.Fatal(err)
	}
	panicking := func(ctx sdk.EditorContext) sdk.EditorComponent { panic("factory boom") }
	if err := ui.SetEditorComponent(panicking); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("panicking factory error = %v", err)
	}
	if ui.GetEditorComponent() != sdk.EditorComponent(good) {
		t.Fatal("failed factory replaced the active editor")
	}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return nil }); !errors.Is(err, ErrExtensionUINilEditor) {
		t.Fatalf("nil component error = %v", err)
	}
	if ui.GetEditorComponent() != sdk.EditorComponent(good) {
		t.Fatal("nil factory result replaced the active editor")
	}
}

func TestRunnerExtensionAutocompleteProvider(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	entered := make(chan struct{}, 1)
	unsubscribe, err := ui.AddAutocompleteProvider(autocompleteFixture{entered: entered})
	if err != nil {
		t.Fatalf("AddAutocompleteProvider: %v", err)
	}
	editor := runner.Surface().Editor()
	editor.SetText("")
	editor.HandleInput("x")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("autocomplete provider was not called")
	}
	if !editor.IsShowingAutocomplete() {
		t.Fatal("external provider suggestions were not merged")
	}
	items := editor.AutocompleteItems()
	if len(items) == 0 || items[0].Value != "provided" {
		t.Fatalf("autocomplete items = %+v, want the external suggestion first", items)
	}
	unsubscribe()
	editor.HandleInput("\x1b")
	editor.SetText("")
	editor.HandleInput("y")
	if len(editor.AutocompleteItems()) != 0 {
		t.Fatalf("unsubscribed provider still returned items: %+v", editor.AutocompleteItems())
	}
	if _, err := ui.AddAutocompleteProvider(nil); err == nil {
		t.Fatal("nil autocomplete provider must fail")
	}
}

type autocompleteFixture struct {
	entered chan struct{}
}

func (p autocompleteFixture) Suggest(ctx sdk.AutocompleteContext) []sdk.AutocompleteSuggestion {
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	return []sdk.AutocompleteSuggestion{{Value: "provided", Label: "provided"}}
}

func TestRunnerExtensionTerminalInputHooks(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	var mu sync.Mutex
	var observed []string
	unsubscribeFirst, err := ui.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		mu.Lock()
		observed = append(observed, "first:"+data)
		mu.Unlock()
		return sdk.TerminalInputResult{}
	})
	if err != nil {
		t.Fatalf("OnTerminalInput: %v", err)
	}
	unsubscribeSecond, err := ui.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		mu.Lock()
		observed = append(observed, "second:"+data)
		mu.Unlock()
		return sdk.TerminalInputResult{Replace: true, Data: "b"}
	})
	if err != nil {
		t.Fatalf("OnTerminalInput: %v", err)
	}
	defer unsubscribeFirst()
	defer unsubscribeSecond()
	runner.Surface().Editor().SetText("")
	terminal.SendInput("a")
	mu.Lock()
	got := append([]string(nil), observed...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "first:a" || got[1] != "second:a" {
		t.Fatalf("hook order = %v, want first then second with the original data", got)
	}
	if runner.Surface().Editor().Text() != "b" {
		t.Fatalf("transformed input did not reach the editor: %q", runner.Surface().Editor().Text())
	}
	terminal.SendInput("\x1b_Gi=1;OK\x1b\\")
	mu.Lock()
	got = append([]string(nil), observed...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("terminal protocol reply reached the hooks: %v", got)
	}
	runner.Surface().Editor().SetText("")
	consumed, err := ui.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		return sdk.TerminalInputResult{Consume: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal.SendInput("q")
	if runner.Surface().Editor().Text() != "" {
		t.Fatalf("consumed input reached the editor: %q", runner.Surface().Editor().Text())
	}
	consumed()
	consumed()
	if _, err := ui.OnTerminalInput(nil); err == nil {
		t.Fatal("nil input hook must fail")
	}
	unsubscribeSecond()
	runner.Surface().Editor().SetText("")
	terminal.SendInput("\x04")
	select {
	case <-runner.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("application exit key did not work with a non-consuming hook installed")
	}
}

func TestRunnerExtensionFullscreenWorkingVisibility(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.SetWorking(true)
	runner.view.RenderNow(true)
	if !strings.Contains(terminal.Output(), "Working") {
		t.Fatalf("fullscreen working row missing:\n%q", terminal.Output())
	}
	ui.SetWorkingIndicator(&sdk.WorkingIndicator{Frames: []string{}})
	runner.view.RenderNow(true)
	mark := terminal.WriteCount()
	runner.view.RenderNow(true)
	if strings.Contains(terminal.OutputSince(mark), "Working") {
		t.Fatalf("empty frames did not hide the fullscreen indicator:\n%q", terminal.OutputSince(mark))
	}
	ui.SetWorkingIndicator(nil)
	runner.SetWorking(false)
}

func TestRunnerExtensionTerminalInputHookSelfUnsubscribe(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	var unsubscribe func()
	calls := 0
	nestedCalls := 0
	stop, err := ui.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		calls++
		unsubscribe()
		if _, nestedErr := ui.OnTerminalInput(func(string) sdk.TerminalInputResult {
			nestedCalls++
			return sdk.TerminalInputResult{}
		}); nestedErr != nil {
			t.Errorf("nested registration: %v", nestedErr)
		}
		return sdk.TerminalInputResult{}
	})
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe = stop
	terminal.SendInput("a")
	terminal.SendInput("b")
	if calls != 1 {
		t.Fatalf("self-unsubscribing hook calls = %d, want 1", calls)
	}
	if nestedCalls != 1 {
		t.Fatalf("nested hook calls = %d, want 1", nestedCalls)
	}
}

func TestRunnerExtensionInputHooksBypassedBySecretModal(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	hooked := 0
	if _, err := ui.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		hooked++
		return sdk.TerminalInputResult{}
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := runner.PromptSecret(context.Background(), "token")
		done <- err
	}()
	waitFor(t, "secret dialog", func() bool { return runner.dialogs.Active() })
	terminal.SendInput("s")
	terminal.SendInput("e")
	if hooked != 0 {
		t.Fatalf("terminal hooks observed %d secret keystrokes", hooked)
	}
	runner.CancelDialogs()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("PromptSecret did not return after dialog shutdown")
	}
}

func TestRunnerExtensionInputHookRegistrationAndCleanup(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	calls := 0
	if err := registry.RegisterTerminalInputHook("setup-hook", func(data string) sdk.TerminalInputResult {
		calls++
		return sdk.TerminalInputResult{}
	}); err != nil {
		t.Fatal(err)
	}
	terminal.SendInput("a")
	if calls != 1 {
		t.Fatalf("registry hook calls = %d, want 1", calls)
	}
	runner.Stop()
	if hooks := runner.extensionInputHooks(); len(hooks) != 0 {
		t.Fatalf("hooks after stop = %d, want 0", len(hooks))
	}
	terminal.SendInput("b")
	if calls != 1 {
		t.Fatalf("stopped runner still dispatched hooks: %d", calls)
	}
	second, terminalTwo := startTestRunner(t, TUIModeRegular, nil)
	secondRegistry := attachExtensionRegistry(t, second)
	if err := secondRegistry.RegisterTerminalInputHook("other", func(data string) sdk.TerminalInputResult {
		calls++
		return sdk.TerminalInputResult{}
	}); err != nil {
		t.Fatal(err)
	}
	terminalTwo.SendInput("c")
	if calls != 2 {
		t.Fatalf("next runner did not get exactly its own hook: %d", calls)
	}
}

func TestRunnerExtensionThemesAndToolsExpanded(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	themes := ui.AllThemes()
	names := map[string]bool{}
	for _, info := range themes {
		names[info.Name] = true
	}
	if !names["dark"] || !names["light"] {
		t.Fatalf("themes = %+v, want dark and light", themes)
	}
	active := ui.ActiveTheme()
	if active == nil || active.Name() != runner.ThemeRegistry().ActiveName() {
		t.Fatalf("active theme = %v", active)
	}
	loaded, ok := ui.GetTheme("light")
	if !ok || loaded == nil || loaded.Name() != "light" {
		t.Fatalf("GetTheme(light) = %v %v", loaded, ok)
	}
	if runner.ThemeRegistry().ActiveName() != active.Name() {
		t.Fatalf("GetTheme activated a theme: active = %q", runner.ThemeRegistry().ActiveName())
	}
	if styled := loaded.Fg("accent", "text"); !strings.Contains(styled, "text") {
		t.Fatalf("theme handle Fg = %q", styled)
	}
	if _, ok := ui.GetTheme("missing-theme"); ok {
		t.Fatal("GetTheme returned a missing theme")
	}
	if err := ui.SetTheme("light"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	if runner.ThemeRegistry().ActiveName() != "light" {
		t.Fatalf("SetTheme did not switch the runner theme: %q", runner.ThemeRegistry().ActiveName())
	}
	if err := ui.SetTheme("missing-theme"); err == nil {
		t.Fatal("SetTheme with a missing theme must fail")
	}
	if ui.ToolsExpanded() {
		t.Fatal("tools must start collapsed")
	}
	ui.SetToolsExpanded(true)
	if !ui.ToolsExpanded() || !runner.Surface().ToolsExpanded() {
		t.Fatal("SetToolsExpanded did not reach the surface")
	}
}

func TestRunnerExtensionEditorExternalSync(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &testEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	ui.SetEditorText("external draft")
	runner.syncEditorForExternalEditor()
	if runner.Surface().Editor().Text() != "external draft" {
		t.Fatalf("external editor did not receive the custom text: %q", runner.Surface().Editor().Text())
	}
	runner.Surface().Editor().SetText("edited outside")
	runner.syncEditorFromExternalEditor()
	if component.Text() != "edited outside" {
		t.Fatalf("custom editor did not receive the external result: %q", component.Text())
	}
	_ = tui.StopOptions{}
}

func TestDefaultEditorRestoreRunsReentrantChangeCallbacks(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	defaultComponent := ui.GetEditorComponent()
	if defaultComponent == nil {
		t.Fatal("GetEditorComponent returned nil")
	}
	var changes []string
	defaultComponent.SetOnChange(func(text string) {
		changes = append(changes, text)
		if got := ui.GetEditorText(); got != text {
			t.Errorf("reentrant GetEditorText = %q, want %q", got, text)
		}
		ui.SetHeader(nil)
	})
	runner.Surface().Editor().SetText("seed")
	custom := &testEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return custom }); err != nil {
		t.Fatal(err)
	}
	ui.SetEditorText("custom draft")
	changes = nil
	if !boundedCall(t, "restore default with a reentrant change callback", func() {
		if err := ui.SetEditorComponent(nil); err != nil {
			t.Errorf("restore default: %v", err)
		}
	}) {
		return
	}
	if runner.Surface().ActiveEditor() != runner.Surface().Editor() {
		t.Fatal("default editor was not restored")
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("restored default editor was not focused")
	}
	if got := runner.Surface().Editor().Text(); got != "custom draft" {
		t.Fatalf("restored default text = %q, want the custom draft", got)
	}
	if got := ui.GetEditorText(); got != "custom draft" {
		t.Fatalf("GetEditorText after restore = %q, want the custom draft", got)
	}
	if len(changes) == 0 || changes[len(changes)-1] != "custom draft" {
		t.Fatalf("reentrant change callbacks = %v, want the custom draft", changes)
	}
}

func TestDefaultEditorRestoreKeepsCallbackReplacement(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	defaultComponent := ui.GetEditorComponent()
	customOne := &testEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return customOne }); err != nil {
		t.Fatal(err)
	}
	ui.SetEditorText("custom draft")
	customTwo := &testEditorComponent{}
	var replaced atomic.Bool
	defaultComponent.SetOnChange(func(text string) {
		if replaced.CompareAndSwap(false, true) {
			if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return customTwo }); err != nil {
				t.Errorf("reentrant editor replacement: %v", err)
			}
		}
	})
	if !boundedCall(t, "restore default with a replacing change callback", func() {
		if err := ui.SetEditorComponent(nil); err != nil {
			t.Errorf("restore default: %v", err)
		}
	}) {
		return
	}
	if !replaced.Load() {
		t.Fatal("the installed default change callback never ran")
	}
	if ui.GetEditorComponent() != sdk.EditorComponent(customTwo) {
		t.Fatal("the concurrent custom install was overwritten by the restore")
	}
	if runner.Surface().ActiveEditor() == runner.Surface().Editor() {
		t.Fatal("restore replaced the concurrently installed custom editor")
	}
	if got := ui.GetEditorText(); got != "custom draft" {
		t.Fatalf("custom editor text after the replacement = %q, want custom draft", got)
	}
	if got := customOne.DisposeCount(); got != 1 {
		t.Fatalf("replaced custom editor dispose count = %d, want 1", got)
	}
}

func TestDefaultEditorRestoreTransfersEmptyCustomText(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Surface().Editor().SetText("stale draft")
	var submitted []string
	runner.SetOnSubmit(func(text string) { submitted = append(submitted, text) })
	custom := &testEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return custom }); err != nil {
		t.Fatal(err)
	}
	if got := custom.Text(); got != "stale draft" {
		t.Fatalf("custom initial text = %q, want the default draft", got)
	}
	ui.SetEditorText("")
	if err := ui.SetEditorComponent(nil); err != nil {
		t.Fatal(err)
	}
	if got := runner.Surface().Editor().Text(); got != "" {
		t.Fatalf("restored default editor resurrected the stale draft: %q", got)
	}
	if got := ui.GetEditorText(); got != "" {
		t.Fatalf("GetEditorText after the empty restore = %q, want empty", got)
	}
	terminal.SendInput("x")
	terminal.SendInput("\r")
	if len(submitted) != 1 || submitted[0] != "x" {
		t.Fatalf("submissions after restore = %v, want exactly [x] with no stale draft", submitted)
	}
}

func TestDefaultEditorCallbackPanicsAreContained(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	defaultComponent := ui.GetEditorComponent()
	if defaultComponent == nil {
		t.Fatal("GetEditorComponent returned nil")
	}
	var changes atomic.Int32
	var submits atomic.Int32
	defaultComponent.SetOnChange(func(text string) {
		changes.Add(1)
		panic("default change boom")
	})
	defaultComponent.SetOnSubmit(func(text string) {
		submits.Add(1)
		panic("default submit boom")
	})
	boundedCall(t, "panicking default change during input", func() {
		terminal.SendInput("a")
	})
	if got := runner.Surface().Editor().Text(); !strings.Contains(got, "a") {
		t.Fatalf("input did not continue after a default change panic: %q", got)
	}
	boundedCall(t, "panicking default submit during input", func() {
		terminal.SendInput("\r")
	})
	if got := submits.Load(); got != 1 {
		t.Fatalf("default submit callbacks = %d, want 1", got)
	}
	if got := changes.Load(); got == 0 {
		t.Fatal("default change callback was never invoked")
	}
	runner.Surface().Editor().SetText("")
	terminal.SendInput("b")
	if got := runner.Surface().Editor().Text(); !strings.Contains(got, "b") {
		t.Fatalf("input did not recover after default callback panics: %q", got)
	}
	custom := &testEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return custom }); err != nil {
		t.Fatal(err)
	}
	ui.SetEditorText("after panics")
	if !boundedCall(t, "restore after default callback panics", func() {
		if err := ui.SetEditorComponent(nil); err != nil {
			t.Errorf("restore default: %v", err)
		}
	}) {
		return
	}
	if got := runner.Surface().Editor().Text(); got != "after panics" {
		t.Fatalf("restored default text after panics = %q, want after panics", got)
	}
	if !boundedCall(t, "stop after default callback panics", func() { runner.Stop() }) {
		return
	}
	if terminal.StopCalls() == 0 {
		t.Fatal("terminal was not restored after default callback panics")
	}
}

func TestSDKEditorAdapterDispatchPanicsAreContained(t *testing.T) {
	component := &testEditorComponent{}
	var reported []string
	adapter, err := newSDKEditorAdapter(component, func(kind string, recovered any) {
		reported = append(reported, kind)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter.SetOnChange(func(string) { panic("adapter change boom") })
	adapter.SetOnSubmit(func(string) { panic("adapter submit boom") })
	adapter.dispatchChange("changed")
	adapter.dispatchSubmit("submitted")
	if len(reported) != 2 || reported[0] != "editor-change" || reported[1] != "editor-submit" {
		t.Fatalf("adapter panic reports = %v, want editor-change then editor-submit", reported)
	}
}
