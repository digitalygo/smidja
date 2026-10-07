package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func TestTuiStartupThinkingLabelMatchesProviderDefault(t *testing.T) {
	fixture := newHostTuiFixture(t, &capturingClient{}, nil)
	registry := fixture.rd.modelRegistry
	registry.Register("test/model", models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	fixture.host.setModel(registry, "test/model", "test/model", "openrouter")
	fixture.host.setReasoningSeam(true)

	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       fixture.deps.Stdin,
		Stdout:      fixture.deps.Stdout,
		Mode:        ui.TUIModeRegular,
		NewTerminal: bridgeTerminalFactory(fixture.terminal),
	})
	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	defer runner.Stop()

	done := make(chan error, 1)
	go func() {
		done <- runTUI(context.Background(), fixture.deps, fixture.rd, fixture.lineUI, ui.TUIModeRegular, fixture.cwd, fixture.cwd, nil, bridgeTerminalFactory(fixture.terminal), fixture.runtime, &tuiStartup{runner: runner})
	}()
	waitUntil(t, 5*time.Second, "startup footer did not show the provider default", func() bool {
		return runner.Surface() != nil && runner.Surface().Footer().ThinkingLevel() == string(sdk.ThinkingDefault)
	})
	var frame strings.Builder
	for _, line := range runner.Surface().RenderFrame(80, 24).Lines {
		frame.WriteString(tui.StripTerminalSequences(line))
		frame.WriteString("\n")
	}
	if !strings.Contains(frame.String(), "test/model • default") {
		t.Fatalf("initial footer missing the provider default:\n%s", frame.String())
	}
	if strings.Contains(frame.String(), "test/model • off") {
		t.Fatalf("initial footer claimed reasoning was off:\n%s", frame.String())
	}
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the terminal closed")
	}
	for _, entry := range sessionEntriesAt(t, fixture.sess.Path()) {
		if _, ok := entry.(*session.ThinkingLevelChangeEntry); ok {
			t.Fatal("startup persisted a thinking level entry")
		}
	}
	if directive, ok := fixture.host.reasoningDirective(); ok {
		t.Fatalf("provider default must stay off the wire: %+v", directive)
	}
}

func TestTuiThinkingLabelTracksSDKLevelsAndMandatoryReset(t *testing.T) {
	fixture, registry := newTuiHostFixtureWithModels(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
		models.ModelInfo{ID: "mandatory/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, Mandatory: true, EffortSelection: true}},
	)
	registry.Register("test/model", models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	fixture.rd.model = "test/model"
	fixture.rd.wireModel = "test/model"
	fixture.rd.provider = "openrouter"
	fixture.host.setModel(registry, "test/model", "test/model", "openrouter")
	fixture.host.setReasoningSeam(true)
	bindTuiHostModelTransactions(t, fixture, registry, func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
		adopt(handle)
		return nil
	}, nil)
	bridge := newTuiBridgeHarness(t, fixture)
	bridge.runner.Surface().SetModel("test/model")
	fixture.host.setLifecycle(bridge.tuiHostLifecycle(bridge.runner))
	ctx := fixture.host.context()

	if err := ctx.SetThinkingLevel(sdk.ThinkingHigh); err != nil {
		t.Fatalf("SetThinkingLevel(high): %v", err)
	}
	waitFooterThinking(t, bridge, string(sdk.ThinkingHigh))
	if directive, ok := fixture.host.reasoningDirective(); !ok || directive.Disable || directive.Effort != "high" {
		t.Fatalf("high wire directive = %+v ok=%v", directive, ok)
	}

	if err := ctx.SetThinkingLevel(sdk.ThinkingOff); err != nil {
		t.Fatalf("SetThinkingLevel(off): %v", err)
	}
	waitFooterThinking(t, bridge, string(sdk.ThinkingOff))
	if directive, ok := fixture.host.reasoningDirective(); !ok || !directive.Disable {
		t.Fatalf("off wire directive = %+v ok=%v", directive, ok)
	}

	if err := ctx.SetThinkingLevel(sdk.ThinkingDefault); err != nil {
		t.Fatalf("SetThinkingLevel(default): %v", err)
	}
	waitFooterThinking(t, bridge, string(sdk.ThinkingDefault))
	if directive, ok := fixture.host.reasoningDirective(); ok {
		t.Fatalf("provider default must stay off the wire: %+v", directive)
	}

	if err := ctx.SetThinkingLevel(sdk.ThinkingOff); err != nil {
		t.Fatalf("SetThinkingLevel(off): %v", err)
	}
	waitFooterThinking(t, bridge, string(sdk.ThinkingOff))
	if err := ctx.SetModel(sdk.Model{ID: "mandatory/model"}); err != nil {
		t.Fatalf("SetModel(mandatory): %v", err)
	}
	waitFooterThinking(t, bridge, string(sdk.ThinkingDefault))
	fixture.host.applyPendingModel()
	if got := fixture.host.currentThinking(); got != sdk.ThinkingDefault {
		t.Fatalf("thinking after the mandatory reset = %q, want default", got)
	}
	if directive, ok := fixture.host.reasoningDirective(); ok {
		t.Fatalf("mandatory reset must not send a directive: %+v", directive)
	}
	if model := fixture.host.currentModel(); model == nil || model.ID != "mandatory/model" {
		t.Fatalf("host model after the reset = %+v", model)
	}
}

func TestTuiSessionActivationKeepsThinkingLabelOnHostState(t *testing.T) {
	fixture := newHostTuiFixture(t, &capturingClient{}, nil)
	fixture.rd.sess = fixture.sess
	registry := fixture.rd.modelRegistry
	registry.Register("model-b", models.ModelInfo{ID: "model-b", ContextWindow: 4096, Provider: "openrouter"})
	bridge := newTuiBridgeHarness(t, fixture)
	bridge.runner.Surface().SetModel("test/model")
	next, err := fixture.store.Create(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close() })
	if err := next.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: string(sdk.ThinkingLow)}); err != nil {
		t.Fatal(err)
	}
	state := bridge.captureSessionDisplayState()
	bridge.runner.Surface().SetThinkingLevel(string(sdk.ThinkingOff))
	bridge.installSessionDisplayState(&activeSession{
		sess:      next,
		recorder:  &sessionRecorder{next},
		path:      next.Path(),
		model:     "model-b",
		wireModel: "model-b",
	})
	if got := bridge.runner.Surface().Footer().ThinkingLevel(); got != string(sdk.ThinkingDefault) {
		t.Fatalf("label after session activation = %q, want the host provider default", got)
	}
	thinkingEntries := 0
	lastThinking := ""
	for _, entry := range sessionEntriesAt(t, next.Path()) {
		if typed, ok := entry.(*session.ThinkingLevelChangeEntry); ok {
			thinkingEntries++
			lastThinking = typed.ThinkingLevel
		}
	}
	if thinkingEntries != 1 || lastThinking != string(sdk.ThinkingLow) {
		t.Fatalf("session activation rewrote thinking entries: count=%d last=%q", thinkingEntries, lastThinking)
	}
	bridge.runner.Surface().SetThinkingLevel(string(sdk.ThinkingOff))
	bridge.restoreSessionDisplayState(state)
	if got := bridge.runner.Surface().Footer().ThinkingLevel(); got != string(sdk.ThinkingDefault) {
		t.Fatalf("label after session restore = %q, want the host provider default", got)
	}
}

func waitFooterThinking(t *testing.T, bridge *tuiBridge, level string) {
	t.Helper()
	waitUntil(t, 5*time.Second, "footer thinking level did not become "+level, func() bool {
		return bridge.runner.Surface().Footer().ThinkingLevel() == level
	})
}
