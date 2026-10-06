package ui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/sdk"
)

type panickingEditorComponent struct {
	mu           sync.Mutex
	text         string
	panicText    bool
	panicSet     bool
	panicInsert  bool
	panicInstall bool
	disposes     atomic.Int32
}

func (e *panickingEditorComponent) Render(width int) []string { return []string{"PANICKING EDITOR"} }

func (e *panickingEditorComponent) Invalidate() {}

func (e *panickingEditorComponent) HandleInput(data string) {}

func (e *panickingEditorComponent) Text() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.panicText {
		panic("editor text boom")
	}
	return e.text
}

func (e *panickingEditorComponent) SetText(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.panicSet {
		panic("editor set boom")
	}
	e.text = text
}

func (e *panickingEditorComponent) InsertTextAtCursor(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.panicInsert {
		panic("editor insert boom")
	}
	e.text += text
}

func (e *panickingEditorComponent) SetOnSubmit(fn func(string)) {
	if e.panicInstall {
		panic("editor install boom")
	}
}

func (e *panickingEditorComponent) SetOnChange(fn func(string)) {}

func (e *panickingEditorComponent) Dispose() { e.disposes.Add(1) }

func (e *panickingEditorComponent) setPanic(apply func(*panickingEditorComponent)) {
	e.mu.Lock()
	apply(e)
	e.mu.Unlock()
}

func TestPanickingEditorTextPreservesLastSafeState(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Surface().Editor().SetText("safe text")
	component := &panickingEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	component.setPanic(func(e *panickingEditorComponent) { e.panicText = true })
	if got := ui.GetEditorText(); got != "safe text" {
		t.Fatalf("GetEditorText = %q, want the last safe editor text", got)
	}
}

func TestPanickingEditorTextDoesNotStrandInputPump(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	component := &panickingEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	component.setPanic(func(e *panickingEditorComponent) { e.panicText = true })
	if got := ui.GetEditorText(); got != "" {
		t.Fatalf("GetEditorText = %q, want the empty fallback", got)
	}
	terminal.SendInput("\x04")
	select {
	case <-runner.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("panicking editor text stranded the input pump")
	}
}

func TestPanickingEditorSetTextIsContained(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Surface().Editor().SetText("safe text")
	component := &panickingEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	component.setPanic(func(e *panickingEditorComponent) { e.panicSet = true })
	boundedCall(t, "panicking editor SetText", func() {
		ui.SetEditorText("replacement")
	})
	if got := ui.GetEditorText(); got != "safe text" {
		t.Fatalf("editor text after a failed set = %q, want the last safe text", got)
	}
}

func TestPanickingEditorInsertIsContained(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	runner.Surface().Editor().SetText("safe text")
	component := &panickingEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return component }); err != nil {
		t.Fatal(err)
	}
	component.setPanic(func(e *panickingEditorComponent) { e.panicInsert = true })
	boundedCall(t, "panicking editor insert", func() {
		ui.PasteToEditor(" more")
	})
	if got := ui.GetEditorText(); got != "safe text" {
		t.Fatalf("editor text after a failed insert = %q, want the last safe text", got)
	}
}

func TestPanickingEditorInstallAccessorsKeepCurrentEditor(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	attachExtensionRegistry(t, runner)
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	good := &plainEditorComponent{}
	if err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return good }); err != nil {
		t.Fatal(err)
	}
	bad := &panickingEditorComponent{panicInstall: true}
	err := ui.SetEditorComponent(func(ctx sdk.EditorContext) sdk.EditorComponent { return bad })
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("SetEditorComponent error = %v, want a panic error", err)
	}
	if ui.GetEditorComponent() != sdk.EditorComponent(good) {
		t.Fatal("failed install replaced the active editor")
	}
	if got := bad.disposes.Load(); got != 1 {
		t.Fatalf("rejected editor dispose count = %d, want 1", got)
	}
}

type panickingModalInstallerComponent struct {
	disposes atomic.Int32
}

func (c *panickingModalInstallerComponent) Render(width int) []string { return []string{"MODAL"} }

func (c *panickingModalInstallerComponent) Invalidate() {}

func (c *panickingModalInstallerComponent) SetModalDone(done func(sdk.ModalResult)) {
	panic("modal install boom")
}

func (c *panickingModalInstallerComponent) Dispose() { c.disposes.Add(1) }

func TestPanickingModalInstallerReturnsError(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	component := &panickingModalInstallerComponent{}
	if err := registry.RegisterComponent("broken-modal", func() sdk.Component { return component }); err != nil {
		t.Fatal(err)
	}
	ui := runner.BoundUI(context.Background()).(sdk.ExtendedUI)
	var modalErr error
	boundedCall(t, "panicking modal installer", func() {
		_, modalErr = ui.ShowComponent("broken-modal")
	})
	if modalErr == nil || !strings.Contains(modalErr.Error(), "panic") {
		t.Fatalf("ShowComponent error = %v, want a panic error", modalErr)
	}
	if got := component.disposes.Load(); got != 1 {
		t.Fatalf("rejected modal dispose count = %d, want 1", got)
	}
}
