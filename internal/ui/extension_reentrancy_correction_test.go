package ui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/sdk"
)

type callbackProbeComponent struct {
	text     string
	render   func()
	input    func()
	dispose  func()
	inFlight atomic.Bool
	after    atomic.Bool
	disposes atomic.Int32
}

func (c *callbackProbeComponent) Render(width int) []string {
	c.inFlight.Store(true)
	defer c.inFlight.Store(false)
	if c.render != nil {
		c.render()
	}
	return []string{c.text}
}

func (c *callbackProbeComponent) Invalidate() {}

func (c *callbackProbeComponent) HandleInput(data string) {
	c.inFlight.Store(true)
	defer c.inFlight.Store(false)
	if c.input != nil {
		c.input()
	}
}

func (c *callbackProbeComponent) Text() string { return c.text }

func (c *callbackProbeComponent) SetText(text string) { c.text = text }

func (c *callbackProbeComponent) SetOnSubmit(func(string)) {}

func (c *callbackProbeComponent) SetOnChange(func(string)) {}

func (c *callbackProbeComponent) Dispose() {
	c.disposes.Add(1)
	if c.inFlight.Load() {
		c.after.Store(true)
	}
	if c.dispose != nil {
		c.dispose()
	}
}

func TestWidgetSelfClearDuringRenderDoesNotDeadlock(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &callbackProbeComponent{text: "SELF CLEAR"}
	component.render = func() { _ = ui.SetWidgetComponent("self-clear", nil) }
	if err := ui.SetWidgetComponent("self-clear", func() sdk.Component { return component }); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "widget self clear during render", func() {
		extensionFrameText(t, runner)
	})
	waitFor(t, "widget self clear disposal", func() bool { return component.disposes.Load() == 1 })
	if component.after.Load() {
		t.Fatal("widget was disposed while its render callback was still running")
	}
	if text := extensionFrameText(t, runner); strings.Contains(text, "SELF CLEAR") {
		t.Fatalf("retired widget output was published:\n%s", text)
	}
	runner.Stop()
	if got := component.disposes.Load(); got != 1 {
		t.Fatalf("widget dispose count after stop = %d, want 1", got)
	}
}

func TestWidgetSelfReplaceDuringRenderDoesNotDeadlock(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	first := &callbackProbeComponent{text: "FIRST SELF"}
	second := &callbackProbeComponent{text: "SECOND SELF"}
	first.render = func() {
		_ = ui.SetWidgetComponent("self-replace", func() sdk.Component { return second })
	}
	if err := ui.SetWidgetComponent("self-replace", func() sdk.Component { return first }); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "widget self replace during render", func() {
		extensionFrameText(t, runner)
	})
	waitFor(t, "replaced widget disposal", func() bool { return first.disposes.Load() == 1 })
	if first.after.Load() {
		t.Fatal("replaced widget was disposed while its render callback was still running")
	}
	waitFor(t, "widget replacement frame", func() bool {
		text := extensionFrameText(t, runner)
		return strings.Contains(text, "SECOND SELF") && !strings.Contains(text, "FIRST SELF")
	})
	runner.Stop()
	waitFor(t, "active widget disposal", func() bool { return second.disposes.Load() == 1 })
}

func TestHeaderSelfClearDuringRenderDoesNotDeadlock(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	header := &callbackProbeComponent{text: "HEADER SELF"}
	header.render = func() { ui.SetHeader(nil) }
	ui.SetHeader(func() sdk.Component { return header })
	boundedCall(t, "header self clear during render", func() {
		extensionFrameText(t, runner)
	})
	waitFor(t, "header self clear disposal", func() bool { return header.disposes.Load() == 1 })
	if header.after.Load() {
		t.Fatal("header was disposed while its render callback was still running")
	}
	if text := extensionFrameText(t, runner); strings.Contains(text, "HEADER SELF") {
		t.Fatalf("retired header output was published:\n%s", text)
	}
}

func TestFooterSelfReplaceDuringRenderDoesNotDeadlock(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	first := &callbackProbeComponent{text: "FIRST FOOTER"}
	second := &callbackProbeComponent{text: "SECOND FOOTER"}
	first.render = func() {
		ui.SetFooter(func() sdk.Component { return second })
	}
	ui.SetFooter(func() sdk.Component { return first })
	boundedCall(t, "footer self replace during render", func() {
		extensionFrameText(t, runner)
	})
	waitFor(t, "replaced footer disposal", func() bool { return first.disposes.Load() == 1 })
	if first.after.Load() {
		t.Fatal("replaced footer was disposed while its render callback was still running")
	}
	waitFor(t, "footer replacement frame", func() bool {
		text := extensionFrameText(t, runner)
		return strings.Contains(text, "SECOND FOOTER") && !strings.Contains(text, "FIRST FOOTER")
	})
	runner.Stop()
	waitFor(t, "active footer disposal", func() bool { return second.disposes.Load() == 1 })
}

func TestEditorSelfClearDuringInputDoesNotDeadlock(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &callbackProbeComponent{text: "CUSTOM EDITOR"}
	component.input = func() { _ = ui.SetEditorComponent(nil) }
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	boundedCall(t, "editor self clear during input", func() {
		terminal.SendInput("z")
	})
	if got := component.disposes.Load(); got != 1 {
		t.Fatalf("editor dispose count = %d, want 1", got)
	}
	if component.after.Load() {
		t.Fatal("editor was disposed while its input callback was still running")
	}
	if runner.Surface().ActiveEditor() != runner.Surface().Editor() {
		t.Fatal("default editor was not restored after the self clear")
	}
}

func TestSlowEditorInputRacingStopDefersDisposal(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	entered := make(chan struct{})
	release := make(chan struct{})
	component := &callbackProbeComponent{text: "SLOW INPUT"}
	var firstCall atomic.Bool
	component.input = func() {
		if firstCall.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		terminal.SendInput("z")
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("slow editor input never started")
	}
	stopDone := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked on a slow input callback")
	}
	if got := component.disposes.Load(); got != 0 {
		t.Fatalf("editor disposed %d times before its callback returned", got)
	}
	close(release)
	select {
	case <-inputDone:
	case <-time.After(3 * time.Second):
		t.Fatal("slow editor input did not finish after release")
	}
	waitFor(t, "slow editor disposal", func() bool { return component.disposes.Load() == 1 })
	if component.after.Load() {
		t.Fatal("editor was disposed while its input callback was still running")
	}
}
