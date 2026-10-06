package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/ui"
)

func TestRunTUIReportsExtensionAttachFailure(t *testing.T) {
	fixture := newRunTUIFixture(t, newRunTUITurnClient(&runTUIEventLog{}, "unused"), nil)
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       fixture.deps.Stdin,
		Stdout:      fixture.deps.Stdout,
		Home:        fixture.deps.Home(),
		Mode:        ui.TUIModeRegular,
		NewTerminal: bridgeTerminalFactory(fixture.terminal),
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("runner.Start: %v", err)
	}
	if err := runner.AttachExtensionUI(extensionui.NewRegistry()); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	fixture.rd.uiRegistry = extensionui.NewRegistry()
	done := make(chan error, 1)
	go func() {
		done <- runTUI(context.Background(), fixture.deps, fixture.rd, fixture.lineUI, ui.TUIModeRegular, fixture.cwd, fixture.cwd, nil, bridgeTerminalFactory(fixture.terminal), nil, &tuiStartup{runner: runner})
	}()
	output := waitForOutputSettled(t, fixture.terminal, "extensions:", 5*time.Second)
	if !strings.Contains(output, "already attached") {
		t.Fatalf("attach failure notice missing:\n%s", output)
	}
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after EOF")
	}
}
