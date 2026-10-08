package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type extensionTestComponent struct {
	text string

	mu          sync.Mutex
	disposes    int
	invalidates int
	done        func(sdk.ModalResult)
}

func (c *extensionTestComponent) Render(width int) []string { return []string{c.text} }

func (c *extensionTestComponent) Invalidate() {
	c.mu.Lock()
	c.invalidates++
	c.mu.Unlock()
}

func (c *extensionTestComponent) Dispose() {
	c.mu.Lock()
	c.disposes++
	c.mu.Unlock()
}

func (c *extensionTestComponent) DisposeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disposes
}

func (c *extensionTestComponent) InvalidateCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invalidates
}

func (c *extensionTestComponent) SetModalDone(done func(sdk.ModalResult)) {
	c.mu.Lock()
	c.done = done
	c.mu.Unlock()
}

func (c *extensionTestComponent) finish(result sdk.ModalResult) {
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done != nil {
		done(result)
	}
}

func extensionFrameText(t *testing.T, runner *Runner) string {
	t.Helper()
	frame := runner.Surface().RenderFrame(80, 24)
	var builder strings.Builder
	for _, line := range frame.Lines {
		builder.WriteString(tui.StripTerminalSequences(line))
		builder.WriteString("\n")
	}
	return builder.String()
}

func attachExtensionRegistry(t *testing.T, runner *Runner) *extensionui.Registry {
	t.Helper()
	registry := extensionui.NewRegistry()
	if err := runner.AttachExtensionUI(registry); err != nil {
		t.Fatalf("AttachExtensionUI: %v", err)
	}
	return registry
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRunnerExtensionRegistriesReachTheSurface(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	if err := registry.RegisterWidget("panel", func() sdk.Component {
		return &extensionTestComponent{text: "WIDGET FRAME"}
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "widget frame", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "WIDGET FRAME")
	})
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return &extensionTestComponent{text: "MESSAGE " + message.Content}
	}); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "payload"})
	text := extensionFrameText(t, runner)
	if !strings.Contains(text, "MESSAGE payload") {
		t.Fatalf("message renderer output missing:\n%s", text)
	}
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return &extensionTestComponent{text: "REPLACED " + message.Content}
	}); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "second"})
	text = extensionFrameText(t, runner)
	if !strings.Contains(text, "REPLACED second") {
		t.Fatalf("replaced message renderer did not take effect:\n%s", text)
	}
	if err := registry.RegisterEntryRenderer("audit", func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component {
		return &extensionTestComponent{text: "ENTRY RENDER"}
	}); err != nil {
		t.Fatal(err)
	}
	runner.DeliverCustomEntry(interactive.CustomEntryView{CustomType: "audit", Data: `{"x":1}`})
	if text := extensionFrameText(t, runner); !strings.Contains(text, "ENTRY RENDER") {
		t.Fatalf("entry renderer output missing:\n%s", text)
	}
	runner.DeliverCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "delivered"})
	if text := extensionFrameText(t, runner); !strings.Contains(text, "REPLACED delivered") {
		t.Fatalf("delivery seam dropped the custom message:\n%s", text)
	}
	if err := registry.UnregisterWidget("panel"); err != nil {
		t.Fatal(err)
	}
	if text := extensionFrameText(t, runner); strings.Contains(text, "WIDGET FRAME") {
		t.Fatalf("unregistered widget still rendered:\n%s", text)
	}
	if err := registry.RegisterMarkdownTransformer("first", func(markdown string, ctx sdk.MarkdownTransformContext) string {
		return markdown + "a"
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterMarkdownTransformer("second", func(markdown string, ctx sdk.MarkdownTransformContext) string {
		return markdown + "b"
	}); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddUserMessage("order")
	if text := extensionFrameText(t, runner); !strings.Contains(text, "orderab") {
		t.Fatalf("transformers did not run in registration order:\n%s", text)
	}
}

func TestRunnerExtensionHeaderFooterLifecycle(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Surface().AddUserMessage("body text")
	header := &extensionTestComponent{text: "EXT HEADER"}
	ui.SetHeader(func() sdk.Component { return header })
	frame := extensionFrameText(t, runner)
	if !strings.Contains(frame, "EXT HEADER") {
		t.Fatalf("header missing:\n%s", frame)
	}
	headerRow := strings.Index(frame, "EXT HEADER")
	bodyRow := strings.Index(frame, "body text")
	if headerRow < 0 || bodyRow < 0 || headerRow > bodyRow {
		t.Fatalf("header must render before the transcript:\n%s", frame)
	}
	replaced := &extensionTestComponent{text: "EXT HEADER 2"}
	ui.SetHeader(func() sdk.Component { return replaced })
	if header.DisposeCount() != 1 {
		t.Fatalf("replaced header dispose count = %d, want 1", header.DisposeCount())
	}
	footer := &extensionTestComponent{text: "EXT FOOTER"}
	ui.SetFooter(func() sdk.Component { return footer })
	frame = extensionFrameText(t, runner)
	if !strings.Contains(frame, "EXT FOOTER") {
		t.Fatalf("custom footer missing:\n%s", frame)
	}
	if strings.Contains(frame, "ctrl+o tools") {
		t.Fatalf("built-in footer still visible:\n%s", frame)
	}
	ui.SetFooter(nil)
	frame = extensionFrameText(t, runner)
	if strings.Contains(frame, "EXT FOOTER") {
		t.Fatalf("custom footer still visible after reset:\n%s", frame)
	}
	if !strings.Contains(frame, "ctrl+o tools") {
		t.Fatalf("built-in footer was not restored:\n%s", frame)
	}
	ui.SetHeader(nil)
}

func TestRunnerExtensionsDisposeOnStop(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	header := &extensionTestComponent{text: "HEADER"}
	widget := &extensionTestComponent{text: "WIDGET"}
	footer := &extensionTestComponent{text: "FOOTER"}
	ui.SetHeader(func() sdk.Component { return header })
	ui.SetFooter(func() sdk.Component { return footer })
	if err := ui.SetWidgetComponent("w", func() sdk.Component { return widget }); err != nil {
		t.Fatal(err)
	}
	runner.Stop()
	if header.DisposeCount() != 1 || widget.DisposeCount() != 1 || footer.DisposeCount() != 1 {
		t.Fatalf("dispose counts after stop = header %d widget %d footer %d, want 1 each",
			header.DisposeCount(), widget.DisposeCount(), footer.DisposeCount())
	}
	runner.Stop()
	if header.DisposeCount() != 1 || widget.DisposeCount() != 1 || footer.DisposeCount() != 1 {
		t.Fatal("components disposed more than once")
	}
}

type modalOutcome struct {
	result sdk.ModalResult
	err    error
}

func startModal(t *testing.T, ui sdk.ExtendedUI, component *extensionTestComponent) chan modalOutcome {
	t.Helper()
	outcomes := make(chan modalOutcome, 1)
	go func() {
		result, err := ui.ShowModal(func(done func(sdk.ModalResult)) sdk.Component {
			component.SetModalDone(done)
			return component
		})
		outcomes <- modalOutcome{result: result, err: err}
	}()
	return outcomes
}

func TestRunnerExtensionModalDoneAndDispose(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ui := runner.BoundUI(ctx).(sdk.ExtendedUI)
	component := &extensionTestComponent{text: "MODAL"}
	outcomes := startModal(t, ui, component)
	waitFor(t, "modal focus", func() bool { return runner.dialogs.overlayFocused() })
	if !runner.dialogs.Active() {
		t.Fatal("modal is not active")
	}
	if runner.Surface().ActiveEditor() == runner.view.FocusedComponent() {
		t.Fatal("focus stayed on the editor while a modal is open")
	}
	component.finish(sdk.ModalResult{Value: "chosen"})
	select {
	case outcome := <-outcomes:
		if outcome.err != nil {
			t.Fatalf("ShowModal = %v", outcome.err)
		}
		if outcome.result.Value != "chosen" || outcome.result.Canceled {
			t.Fatalf("modal result = %+v", outcome.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ShowModal did not return")
	}
	waitFor(t, "dispose", func() bool { return component.DisposeCount() == 1 })
	component.finish(sdk.ModalResult{Value: "late"})
	if component.DisposeCount() != 1 {
		t.Fatalf("late callback changed dispose count: %d", component.DisposeCount())
	}
	if resumed := runner.Surface().ActiveEditor(); resumed != nil && runner.view.FocusedComponent() != resumed {
		t.Fatal("editor focus was not restored after the modal closed")
	}
}

func TestRunnerExtensionModalCanceledBySignal(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	ui := runner.BoundUI(ctx).(sdk.ExtendedUI)
	component := &extensionTestComponent{text: "MODAL"}
	outcomes := startModal(t, ui, component)
	waitFor(t, "modal active", func() bool { return runner.dialogs.Active() })
	cancel()
	select {
	case outcome := <-outcomes:
		if outcome.err != nil {
			t.Fatalf("ShowModal = %v", outcome.err)
		}
		if !outcome.result.Canceled {
			t.Fatalf("canceled modal result = %+v", outcome.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ShowModal did not return after signal cancellation")
	}
	waitFor(t, "dispose", func() bool { return component.DisposeCount() == 1 })
}

func TestRunnerExtensionModalCanceledByStopAndEOF(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		runner, _ := startTestRunner(t, TUIModeRegular, nil)
		attachExtensionRegistry(t, runner)
		component := &extensionTestComponent{text: "MODAL"}
		outcomes := startModal(t, runner.BoundUI(context.Background()).(sdk.ExtendedUI), component)
		waitFor(t, "modal active", func() bool { return runner.dialogs.Active() })
		runner.Stop()
		select {
		case outcome := <-outcomes:
			if outcome.err != nil || !outcome.result.Canceled {
				t.Fatalf("stop modal outcome = %+v err=%v", outcome.result, outcome.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("ShowModal did not return after Stop")
		}
		waitFor(t, "dispose", func() bool { return component.DisposeCount() == 1 })
	})
	t.Run("eof", func(t *testing.T) {
		runner, terminal := startTestRunner(t, TUIModeRegular, nil)
		attachExtensionRegistry(t, runner)
		component := &extensionTestComponent{text: "MODAL"}
		outcomes := startModal(t, runner.BoundUI(context.Background()).(sdk.ExtendedUI), component)
		waitFor(t, "modal active", func() bool { return runner.dialogs.Active() })
		terminal.FireEOF()
		select {
		case outcome := <-outcomes:
			if outcome.err != nil || !outcome.result.Canceled {
				t.Fatalf("eof modal outcome = %+v err=%v", outcome.result, outcome.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("ShowModal did not return after EOF")
		}
	})
}

func TestRunnerExtensionModalDoneBeforeShowReturns(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	component := &extensionTestComponent{text: "MODAL"}
	result, err := runner.BoundUI(context.Background()).(sdk.ExtendedUI).ShowModal(func(done func(sdk.ModalResult)) sdk.Component {
		done(sdk.ModalResult{Value: 42})
		return component
	})
	if err != nil {
		t.Fatalf("ShowModal = %v", err)
	}
	if result.Value != 42 || result.Canceled {
		t.Fatalf("early done result = %+v", result)
	}
	if component.DisposeCount() != 1 {
		t.Fatalf("dispose count = %d, want 1", component.DisposeCount())
	}
}

func TestRunnerExtensionModalQueueAndRetheme(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	first := &extensionTestComponent{text: "FIRST"}
	second := &extensionTestComponent{text: "SECOND"}
	firstOutcomes := startModal(t, runner.BoundUI(firstCtx).(sdk.ExtendedUI), first)
	waitFor(t, "first modal", func() bool { return runner.dialogs.Active() })
	secondOutcomes := startModal(t, runner.BoundUI(secondCtx).(sdk.ExtendedUI), second)
	waitFor(t, "queued modal", func() bool { return runner.dialogs.waitingCount() == 1 })
	first.finish(sdk.ModalResult{})
	select {
	case outcome := <-firstOutcomes:
		if outcome.err != nil {
			t.Fatalf("first modal = %v", outcome.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first modal did not finish")
	}
	waitFor(t, "second modal admitted", func() bool { return runner.dialogs.Active() })
	waitFor(t, "second modal focus", func() bool { return runner.dialogs.overlayFocused() })
	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	terminal.SetSize(120, 40)
	runner.view.RenderNow(true)
	if second.InvalidateCount() == 0 {
		t.Fatal("retheme did not invalidate the active modal component")
	}
	if !runner.dialogs.overlayFocused() {
		t.Fatal("resize dropped the modal focus")
	}
	cancelSecond()
	select {
	case outcome := <-secondOutcomes:
		if !outcome.result.Canceled {
			t.Fatalf("second modal result = %+v", outcome.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second modal did not finish")
	}
}

func TestRunnerExtensionShowComponent(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &extensionTestComponent{text: "REGISTERED"}
	if err := registry.RegisterComponent("dialog", func() sdk.Component { return component }); err != nil {
		t.Fatal(err)
	}
	outcomes := make(chan modalOutcome, 1)
	go func() {
		result, err := ui.ShowComponent("dialog")
		outcomes <- modalOutcome{result: result, err: err}
	}()
	waitFor(t, "registered modal", func() bool { return runner.dialogs.Active() })
	component.finish(sdk.ModalResult{Value: "registry-value"})
	select {
	case outcome := <-outcomes:
		if outcome.err != nil || outcome.result.Value != "registry-value" {
			t.Fatalf("ShowComponent = %+v err=%v", outcome.result, outcome.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ShowComponent did not return")
	}
	if _, err := ui.ShowComponent("missing"); err == nil {
		t.Fatal("ShowComponent with an unknown key must fail")
	}
	if _, err := ui.ShowComponent(""); !errors.Is(err, ErrExtensionUIKeyEmpty) {
		t.Fatalf("ShowComponent empty key = %v", err)
	}
	if err := registry.RegisterComponent("nil-factory", func() sdk.Component { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := ui.ShowComponent("nil-factory"); !errors.Is(err, ErrExtensionUINilFactory) {
		t.Fatalf("nil factory = %v", err)
	}
	if err := registry.RegisterComponent("panic", func() sdk.Component { panic("boom") }); err != nil {
		t.Fatal(err)
	}
	if _, err := ui.ShowComponent("panic"); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("panic factory = %v, want a recovered error", err)
	}
	if err := registry.RegisterComponent("bad key", nil); !errors.Is(err, extensionui.ErrNilValue) {
		t.Fatalf("nil registration = %v", err)
	}
}

func TestRunnerExtensionRegistriesAreIsolated(t *testing.T) {
	first, _ := startTestRunner(t, TUIModeRegular, nil)
	second, _ := startTestRunner(t, TUIModeRegular, nil)
	firstRegistry := attachExtensionRegistry(t, first)
	attachExtensionRegistry(t, second)
	if err := firstRegistry.RegisterWidget("panel", func() sdk.Component {
		return &extensionTestComponent{text: "FIRST ONLY"}
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first widget", func() bool {
		return strings.Contains(extensionFrameText(t, first), "FIRST ONLY")
	})
	if text := extensionFrameText(t, second); strings.Contains(text, "FIRST ONLY") {
		t.Fatalf("widget leaked into the second runner:\n%s", text)
	}
	second.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "second"})
	if text := extensionFrameText(t, first); strings.Contains(text, "second") {
		t.Fatalf("message leaked into the first runner:\n%s", text)
	}
}

func TestRunnerExtensionWidgetComponentErrors(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	if err := ui.SetWidgetComponent("", func() sdk.Component { return &extensionTestComponent{text: "x"} }); !errors.Is(err, ErrExtensionUIKeyEmpty) {
		t.Fatalf("empty widget key = %v", err)
	}
	if err := ui.SetWidgetComponent("key", nil); err != nil {
		t.Fatalf("nil widget factory should clear: %v", err)
	}
	if err := ui.SetWidgetComponent("key", func() sdk.Component { panic("boom") }); err == nil {
		t.Fatal("panicking widget factory must fail")
	}
	if err := ui.SetWidgetComponent("key", func() sdk.Component { return nil }); !errors.Is(err, ErrExtensionUINilFactory) {
		t.Fatalf("nil widget component = %v", err)
	}
}
