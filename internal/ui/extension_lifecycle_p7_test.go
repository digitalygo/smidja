package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type renderCallbackComponent struct {
	text     string
	callback func()
}

func (c *renderCallbackComponent) Render(width int) []string {
	if c.callback != nil {
		c.callback()
	}
	return []string{c.text}
}

func (c *renderCallbackComponent) Invalidate() {}

type panicComponent struct {
	text      string
	disposes  atomic.Int32
	invalid   atomic.Int32
	panicMode atomic.Bool
}

func (c *panicComponent) Render(width int) []string {
	if c.panicMode.Load() {
		panic("render boom")
	}
	return []string{c.text}
}

func (c *panicComponent) Invalidate() { c.invalid.Add(1) }

func (c *panicComponent) Dispose() { c.disposes.Add(1) }

func boundedCall(t *testing.T, what string, action func()) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		action()
	}()
	select {
	case <-done:
		return true
	case <-time.After(3 * time.Second):
		t.Errorf("%s did not complete; a callback is running under a held host lock", what)
		return false
	}
}

func TestWidgetComponentRenderReentrancy(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &renderCallbackComponent{
		text: "REENTRANT WIDGET",
		callback: func() {
			ui.SetWorkingVisible(false)
			ui.SetWorkingVisible(true)
			_ = ui.ToolsExpanded()
			ui.SetEditorText("changed from render")
		},
	}
	if err := ui.SetWidgetComponent("reentrant", func() sdk.Component { return component }); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "reentrant widget render", func() {
		extensionFrameText(t, runner)
	})
	if text := extensionFrameText(t, runner); !strings.Contains(text, "REENTRANT WIDGET") {
		t.Fatalf("reentrant widget frame missing:\n%s", text)
	}
}

func TestHeaderAndFooterRenderReentrancy(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	header := &renderCallbackComponent{text: "HEADER CALLBACK", callback: func() { ui.SetWorkingVisible(false) }}
	footer := &renderCallbackComponent{text: "FOOTER CALLBACK", callback: func() { ui.SetToolsExpanded(true) }}
	ui.SetHeader(func() sdk.Component { return header })
	ui.SetFooter(func() sdk.Component { return footer })
	boundedCall(t, "reentrant header and footer render", func() {
		extensionFrameText(t, runner)
	})
	text := extensionFrameText(t, runner)
	if !strings.Contains(text, "HEADER CALLBACK") || !strings.Contains(text, "FOOTER CALLBACK") {
		t.Fatalf("reentrant header/footer frame missing:\n%s", text)
	}
}

type disposeOpensModalComponent struct {
	ui       sdk.ExtendedUI
	once     sync.Once
	finished chan struct{}
	result   atomic.Value
	err      atomic.Value
}

func (c *disposeOpensModalComponent) Render(width int) []string { return []string{"FIRST MODAL"} }

func (c *disposeOpensModalComponent) Invalidate() {}

func (c *disposeOpensModalComponent) SetModalDone(done func(sdk.ModalResult)) {}

func (c *disposeOpensModalComponent) Dispose() {
	c.once.Do(func() {
		result, err := c.ui.ShowModal(func(done func(sdk.ModalResult)) sdk.Component {
			done(sdk.ModalResult{Value: "second"})
			return &extensionTestComponent{text: "SECOND MODAL"}
		})
		c.result.Store(result)
		if err != nil {
			c.err.Store(err)
		}
		close(c.finished)
	})
}

func TestModalDisposeCanOpenAnotherModal(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &disposeOpensModalComponent{ui: ui, finished: make(chan struct{})}
	outcomes := make(chan modalOutcome, 1)
	go func() {
		result, err := ui.ShowModal(func(done func(sdk.ModalResult)) sdk.Component {
			done(sdk.ModalResult{Value: "first"})
			return component
		})
		outcomes <- modalOutcome{result: result, err: err}
	}()
	select {
	case outcome := <-outcomes:
		if outcome.err != nil || outcome.result.Value != "first" {
			t.Fatalf("first modal = %+v err=%v", outcome.result, outcome.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first modal did not return")
	}
	select {
	case <-component.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("component Dispose blocked opening a second modal; the modal session was not released first")
	}
	if stored := component.err.Load(); stored != nil {
		t.Fatalf("second modal from Dispose = %v", stored)
	}
	if stored := component.result.Load(); stored == nil || stored.(sdk.ModalResult).Value != "second" {
		t.Fatalf("second modal result = %v", stored)
	}
}

func TestWidgetRegistryRefreshDoesNotRebuildUnchangedWidgets(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	var builds atomic.Int32
	widget := &panicComponent{text: "STABLE WIDGET"}
	if err := registry.RegisterWidget("stable", func() sdk.Component {
		builds.Add(1)
		return widget
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stable widget", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "STABLE WIDGET")
	})
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return &extensionTestComponent{text: "NOTE"}
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterMarkdownTransformer("x", func(markdown string, ctx sdk.MarkdownTransformContext) string {
		return markdown
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterTerminalInputHook("hook", func(data string) sdk.TerminalInputResult {
		return sdk.TerminalInputResult{}
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "post-refresh widget", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "STABLE WIDGET")
	})
	if got := builds.Load(); got != 1 {
		t.Fatalf("unrelated registry changes rebuilt the widget %d times, want 1", got)
	}
	if widget.disposes.Load() != 0 {
		t.Fatalf("unchanged widget was disposed %d times", widget.disposes.Load())
	}
	if err := registry.RegisterWidget("stable", func() sdk.Component {
		builds.Add(1)
		return widget
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "replaced widget build", func() bool { return builds.Load() == 2 })
}

func TestSetWidgetSameInstanceIsNotDisposed(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	widget := &panicComponent{text: "SAME INSTANCE"}
	factory := func() sdk.Component { return widget }
	if err := ui.SetWidgetComponent("same", factory); err != nil {
		t.Fatal(err)
	}
	if err := ui.SetWidgetComponent("same", factory); err != nil {
		t.Fatal(err)
	}
	if widget.disposes.Load() != 0 {
		t.Fatalf("same instance disposed %d times, want 0", widget.disposes.Load())
	}
	if text := extensionFrameText(t, runner); !strings.Contains(text, "SAME INSTANCE") {
		t.Fatalf("widget missing after same-instance replacement:\n%s", text)
	}
}

func TestLateWidgetAfterStopIsRejected(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Stop()
	late := &panicComponent{text: "LATE"}
	err := ui.SetWidgetComponent("late", func() sdk.Component { return late })
	if !errors.Is(err, ErrExtensionUIUnavailable) {
		t.Fatalf("late SetWidgetComponent = %v, want ErrExtensionUIUnavailable", err)
	}
	if late.disposes.Load() != 1 {
		t.Fatalf("late widget dispose count = %d, want 1", late.disposes.Load())
	}
}

func TestSlowWidgetFactoryRacingStopDisposesExactlyOnce(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	late := &panicComponent{text: "SLOW"}
	go func() {
		result <- ui.SetWidgetComponent("slow", func() sdk.Component {
			close(entered)
			<-release
			return late
		})
	}()
	<-entered
	runner.Stop()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrExtensionUIUnavailable) {
			t.Fatalf("racing SetWidgetComponent = %v, want ErrExtensionUIUnavailable", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("racing SetWidgetComponent did not return")
	}
	if late.disposes.Load() != 1 {
		t.Fatalf("slow widget dispose count = %d, want 1", late.disposes.Load())
	}
}

func TestSlowHeaderFooterFactoryRacingStopDisposesOnce(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	headerEntered := make(chan struct{})
	footerEntered := make(chan struct{})
	release := make(chan struct{})
	header := &panicComponent{text: "SLOW HEADER"}
	footer := &panicComponent{text: "SLOW FOOTER"}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ui.SetHeader(func() sdk.Component {
			close(headerEntered)
			<-release
			return header
		})
	}()
	go func() {
		defer wg.Done()
		ui.SetFooter(func() sdk.Component {
			close(footerEntered)
			<-release
			return footer
		})
	}()
	<-headerEntered
	<-footerEntered
	runner.Stop()
	close(release)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("slow header/footer factories did not return after Stop")
	}
	if header.disposes.Load() != 1 || footer.disposes.Load() != 1 {
		t.Fatalf("slow header/footer dispose counts = %d/%d, want 1/1", header.disposes.Load(), footer.disposes.Load())
	}
}

func TestSlowEditorFactoryRacingStopAlignsLifecycle(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	entered := make(chan struct{})
	release := make(chan struct{})
	component := &testEditorComponent{}
	result := make(chan error, 1)
	go func() {
		result <- ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent {
			close(entered)
			<-release
			return component
		})
	}()
	<-entered
	runner.Stop()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrExtensionUIUnavailable) {
			t.Fatalf("racing SetEditorComponent = %v, want ErrExtensionUIUnavailable", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("racing SetEditorComponent did not return")
	}
	if component.DisposeCount() != 1 {
		t.Fatalf("slow editor dispose count = %d, want 1", component.DisposeCount())
	}
}

func TestAttachExtensionUIAfterStopIsRejected(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	runner.Stop()
	registry := extensionui.NewRegistry()
	if err := runner.AttachExtensionUI(registry); !errors.Is(err, ErrExtensionUIUnavailable) {
		t.Fatalf("AttachExtensionUI after Stop = %v, want ErrExtensionUIUnavailable", err)
	}
}

func TestCustomTranscriptComponentsDisposedOnStop(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	component := &panicComponent{text: "TRANSCRIPT CUSTOM"}
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return component
	}); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "payload"})
	if text := extensionFrameText(t, runner); !strings.Contains(text, "TRANSCRIPT CUSTOM") {
		t.Fatalf("custom transcript component missing:\n%s", text)
	}
	runner.Stop()
	if component.disposes.Load() != 1 {
		t.Fatalf("custom transcript dispose count after Stop = %d, want 1", component.disposes.Load())
	}
	runner.Stop()
	if component.disposes.Load() != 1 {
		t.Fatalf("custom transcript disposed more than once: %d", component.disposes.Load())
	}
}

func TestWidgetRenderPanicIsContained(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	broken := &panicComponent{text: "BROKEN"}
	broken.panicMode.Store(true)
	if err := ui.SetWidgetComponent("broken", func() sdk.Component { return broken }); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "panicking widget render", func() {
		extensionFrameText(t, runner)
	})
	boundedCall(t, "panicking widget render again", func() {
		extensionFrameText(t, runner)
	})
	good := &panicComponent{text: "RECOVERED WIDGET"}
	if err := ui.SetWidgetComponent("broken", func() sdk.Component { return good }); err != nil {
		t.Fatal(err)
	}
	if text := extensionFrameText(t, runner); !strings.Contains(text, "RECOVERED WIDGET") {
		t.Fatalf("widget did not recover after a render panic:\n%s", text)
	}
	runner.Stop()
	if terminal.StopCalls() == 0 {
		t.Fatal("terminal was never stopped after a render panic")
	}
}

func TestTransformerPanicIsContained(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	if err := registry.RegisterMarkdownTransformer("boom", func(markdown string, ctx sdk.MarkdownTransformContext) string {
		panic("transform boom")
	}); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "user message with a panicking transformer", func() {
		runner.Surface().AddUserMessage("plain body")
	})
	boundedCall(t, "render with a panicking transformer", func() {
		extensionFrameText(t, runner)
	})
	if text := extensionFrameText(t, runner); !strings.Contains(text, "plain body") {
		t.Fatalf("original content lost after a transformer panic:\n%s", text)
	}
	if err := registry.UnregisterMarkdownTransformer("boom"); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddUserMessage("second body")
	if text := extensionFrameText(t, runner); !strings.Contains(text, "second body") {
		t.Fatalf("render did not recover after the transformer was removed:\n%s", text)
	}
}

func TestInputHookPanicIsContained(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	if _, err := ui.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		panic("hook boom")
	}); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "panicking input hook", func() {
		terminal.SendInput("a")
	})
	if got := runner.Surface().Editor().Text(); !strings.Contains(got, "a") {
		t.Fatalf("input did not continue after a hook panic: %q", got)
	}
	runner.Surface().Editor().SetText("")
	if _, err := ui.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		return sdk.TerminalInputResult{}
	}); err != nil {
		t.Fatal(err)
	}
	terminal.SendInput("b")
	if got := runner.Surface().Editor().Text(); !strings.Contains(got, "b") {
		t.Fatalf("subsequent input did not work after a hook panic: %q", got)
	}
}

func TestCustomRendererPanicFallsBack(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		panic("renderer boom")
	}); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "panicking custom renderer", func() {
		runner.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "fallback payload"})
	})
	runner.Surface().SetToolsExpanded(true)
	if text := extensionFrameText(t, runner); !strings.Contains(text, "fallback payload") {
		t.Fatalf("fallback block missing after a renderer panic:\n%s", text)
	}
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return &extensionTestComponent{text: "RECOVERED RENDERER"}
	}); err != nil {
		t.Fatal(err)
	}
	if text := extensionFrameText(t, runner); !strings.Contains(text, "RECOVERED RENDERER") {
		t.Fatalf("renderer did not recover after replacement:\n%s", text)
	}
}

func TestWidgetReplacementDisposesOldExactlyOnce(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	first := &panicComponent{text: "FIRST WIDGET"}
	second := &panicComponent{text: "SECOND WIDGET"}
	if err := registry.RegisterWidget("replaceable", func() sdk.Component { return first }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first widget", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "FIRST WIDGET")
	})
	if err := registry.RegisterWidget("replaceable", func() sdk.Component { return second }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second widget", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "SECOND WIDGET")
	})
	if first.disposes.Load() != 1 {
		t.Fatalf("replaced widget dispose count = %d, want 1", first.disposes.Load())
	}
	if second.disposes.Load() != 0 {
		t.Fatalf("active widget disposed %d times before stop", second.disposes.Load())
	}
	runner.Stop()
	runner.Stop()
	if first.disposes.Load() != 1 || second.disposes.Load() != 1 {
		t.Fatalf("widget dispose counts after stop = %d/%d, want 1/1", first.disposes.Load(), second.disposes.Load())
	}
}

func TestAutocompleteProviderPanicIsContained(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	if _, err := ui.AddAutocompleteProvider(panickingAutocomplete{}); err != nil {
		t.Fatal(err)
	}
	editor := runner.Surface().Editor()
	editor.SetText("")
	boundedCall(t, "panicking autocomplete provider", func() {
		editor.HandleInput("x")
	})
	editor.SetText("")
	if _, err := ui.AddAutocompleteProvider(autocompleteFixture{}); err != nil {
		t.Fatal(err)
	}
	editor.HandleInput("y")
	items := editor.AutocompleteItems()
	if len(items) == 0 || items[0].Value != "provided" {
		t.Fatalf("autocomplete did not recover after a provider panic: %+v", items)
	}
}

type panickingAutocomplete struct{}

func (panickingAutocomplete) Suggest(ctx sdk.AutocompleteContext) []sdk.AutocompleteSuggestion {
	panic("autocomplete boom")
}

type offThreadEditor struct {
	callbacks chan struct{}
	mu        sync.Mutex
	onSubmit  func(string)
	onChange  func(string)
	text      string
	disposes  atomic.Int32
}

func (e *offThreadEditor) Render(width int) []string { return []string{"OFF THREAD EDITOR"} }

func (e *offThreadEditor) Invalidate() {}

func (e *offThreadEditor) HandleInput(data string) { e.SetText(e.Text() + data) }

func (e *offThreadEditor) Text() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.text
}

func (e *offThreadEditor) SetText(text string) {
	e.mu.Lock()
	e.text = text
	callback := e.onChange
	e.mu.Unlock()
	if callback != nil {
		callback(text)
	}
}

func (e *offThreadEditor) SetOnSubmit(fn func(string)) {
	e.mu.Lock()
	e.onSubmit = fn
	e.mu.Unlock()
	close(e.callbacks)
}

func (e *offThreadEditor) SetOnChange(fn func(string)) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

func (e *offThreadEditor) Dispose() { e.disposes.Add(1) }

func TestEditorAdapterCallbackRaceDuringInstall(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &offThreadEditor{callbacks: make(chan struct{})}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-component.callbacks
		for {
			select {
			case <-stop:
				return
			default:
				component.mu.Lock()
				callback := component.onSubmit
				component.mu.Unlock()
				if callback != nil {
					callback("racy")
				}
			}
		}
	}()
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	close(stop)
	<-done
	if err := ui.SetEditorComponent(nil); err != nil {
		t.Fatal(err)
	}
	if component.disposes.Load() != 1 {
		t.Fatalf("editor dispose count = %d, want 1", component.disposes.Load())
	}
}

func TestRegistryRendererReplacementRerendersTranscript(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	first := &panicComponent{text: "RENDERER ONE"}
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return first
	}); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "payload"})
	waitFor(t, "first renderer", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "RENDERER ONE")
	})
	second := &panicComponent{text: "RENDERER TWO"}
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return second
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second renderer", func() bool {
		text := extensionFrameText(t, runner)
		return strings.Contains(text, "RENDERER TWO") && !strings.Contains(text, "RENDERER ONE")
	})
	if first.disposes.Load() != 1 {
		t.Fatalf("replaced renderer component dispose count = %d, want 1", first.disposes.Load())
	}
	if err := registry.UnregisterMessageRenderer("note"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "renderer fallback", func() bool {
		text := extensionFrameText(t, runner)
		return !strings.Contains(text, "RENDERER TWO") && strings.Contains(text, "note")
	})
	if second.disposes.Load() != 1 {
		t.Fatalf("unregistered renderer component dispose count = %d, want 1", second.disposes.Load())
	}
}

func TestDefaultEditorComponentIsStable(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	first := ui.GetEditorComponent()
	second := ui.GetEditorComponent()
	if first == nil || first != second {
		t.Fatalf("repeated GetEditorComponent returned different adapters: %v vs %v", first, second)
	}
	var submits atomic.Int32
	first.SetOnSubmit(func(text string) { submits.Add(1) })
	second.SetOnSubmit(func(text string) { submits.Add(1) })
	editor := runner.Surface().Editor()
	editor.SetText("submit me")
	editor.HandleInput("\r")
	if got := submits.Load(); got != 1 {
		t.Fatalf("default editor submit callback ran %d times, want 1", got)
	}
}
