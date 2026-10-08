package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func newTuiHostFixtureWithModels(t *testing.T, infos ...models.ModelInfo) (*hostTuiFixture, *models.Registry) {
	t.Helper()
	fixture := newHostTuiFixture(t, &capturingClient{}, nil)
	registry := fixture.rd.modelRegistry
	for _, info := range infos {
		registry.Register(info.ID, info)
	}
	return fixture, registry
}

func bindTuiHostModelTransactions(t *testing.T, fixture *hostTuiFixture, registry *models.Registry, persist func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error, custom agent.Client) {
	t.Helper()
	providers := fixture.rd.providers
	fixture.host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) {
			if providers != nil {
				if entry, ok := providers.FindModel(model); ok {
					return model, entry.Name, true
				}
			}
			return model, "openrouter", true
		},
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return newModelPreparer(config.Config{Model: model, ContextEnabled: true}, registry, model, wire, nil)
		},
		buildClient: func(provider string) (agent.Client, bool, error) {
			if provider != "" && provider != "openrouter" {
				return custom, false, nil
			}
			return fixture.rd.client, false, nil
		},
		persist: persist,
	})
}

func newTuiBridgeHarness(t *testing.T, fixture *hostTuiFixture) *tuiBridge {
	t.Helper()
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       fixture.deps.Stdin,
		Stdout:      fixture.deps.Stdout,
		Mode:        ui.TUIModeRegular,
		NewTerminal: bridgeTerminalFactory(fixture.terminal),
	})
	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.Stop)
	bridge := newTuiBridge(context.Background(), func() {}, fixture.rd, runner, &tuiCaptureWriter{fallback: fixture.deps.Stdout})
	t.Cleanup(func() {
		bridge.shutdown()
		bridge.wait()
	})
	return bridge
}

func tuiBridgeFrameText(t *testing.T, bridge *tuiBridge) string {
	t.Helper()
	frame := bridge.runner.Surface().RenderFrame(80, 24)
	var builder strings.Builder
	for _, line := range frame.Lines {
		builder.WriteString(tui.StripTerminalSequences(line))
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestTuiApplyModelRejectsIncompatibleReasoning(t *testing.T) {
	fixture, registry := newTuiHostFixtureWithModels(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
		models.ModelInfo{ID: "model-b", ContextWindow: 4096, Provider: "openrouter"},
	)
	fixture.rd.model = "model-a"
	fixture.rd.wireModel = "model-a"
	fixture.rd.provider = "openrouter"
	fixture.rd.preparer = testPreparer(t)
	fixture.rd.client = &capturingClient{}
	fixture.host.setModel(registry, "model-a", "model-a", "openrouter")
	fixture.host.setReasoningSeam(true)
	bindTuiHostModelTransactions(t, fixture, registry, func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
		adopt(handle)
		return nil
	}, nil)
	if err := fixture.host.requestThinking(sdk.ThinkingHigh, nil); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	baseClient := fixture.rd.client
	preparer := fixture.rd.preparer
	bridge := newTuiBridgeHarness(t, fixture)
	bridge.runner.Surface().SetModel("model-a")
	bridge.applyModel("model-b")
	if fixture.rd.model != "model-a" || fixture.rd.client != baseClient || fixture.rd.preparer != preparer {
		t.Fatalf("rejected model changed rd state: model=%q", fixture.rd.model)
	}
	if model := fixture.host.currentModel(); model == nil || model.ID != "model-a" {
		t.Fatalf("host model = %+v, want model-a", model)
	}
	if got := fixture.host.currentThinking(); got != sdk.ThinkingHigh {
		t.Fatalf("host thinking = %q, want high", got)
	}
	if fixture.host.takePendingModel() != nil {
		t.Fatal("rejected model queued an intent")
	}
	if frame := tuiBridgeFrameText(t, bridge); !strings.Contains(frame, "model-a") {
		t.Fatalf("footer changed after the rejection:\n%s", frame)
	}
}

func TestTuiApplyModelUpdatesWindowAndContextUsagePercent(t *testing.T) {
	fixture, registry := newTuiHostFixtureWithModels(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter"},
		models.ModelInfo{ID: "model-b", ContextWindow: 8192, Provider: "openrouter"},
	)
	fixture.rd.model = "model-a"
	fixture.rd.wireModel = "model-a"
	fixture.rd.provider = "openrouter"
	fixture.rd.client = &capturingClient{}
	fixture.host.setModel(registry, "model-a", "model-a", "openrouter")
	fixture.host.setWindow(4096)
	usage := agent.Usage{Input: 4096}
	fixture.host.setMessages([]*agent.Message{{Assistant: &agent.AssistantMessage{Usage: usage}}})
	before := fixture.host.contextUsage()
	if before == nil || before.Percent == nil || *before.Percent != 100 {
		t.Fatalf("initial usage = %+v, want 100%%", before)
	}
	bindTuiHostModelTransactions(t, fixture, registry, func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
		adopt(handle)
		return nil
	}, nil)
	bridge := newTuiBridgeHarness(t, fixture)
	bridge.applyModel("model-b")
	after := fixture.host.contextUsage()
	if after == nil || after.ContextWindow != 8192 || after.Percent == nil || *after.Percent != 50 {
		t.Fatalf("usage after model switch = %+v, want window 8192 and 50%%", after)
	}
	if fixture.rd.wireModel != "model-b" {
		t.Fatalf("rd.wireModel = %q", fixture.rd.wireModel)
	}
}

func TestTuiApplyModelCustomProviderFailureRollsBack(t *testing.T) {
	fixture, registry := newTuiHostFixtureWithModels(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter"},
	)
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("proxy", sdk.ProviderConfig{BaseURL: "https://example.com", Models: []sdk.Model{{ID: "proxy/alpha"}}}); err != nil {
		t.Fatal(err)
	}
	fixture.rd.providers = providers
	fixture.rd.provider = "openrouter"
	fixture.rd.model = "model-a"
	fixture.rd.wireModel = "model-a"
	baseClient := &capturingClient{}
	customClient := &capturingClient{}
	fixture.rd.client = baseClient
	fixture.rd.preparer = testPreparer(t)
	fixture.host.setProviders(providers)
	fixture.host.setModel(registry, "model-a", "model-a", "openrouter")
	persistErr := errors.New("profile write failed")
	bindTuiHostModelTransactions(t, fixture, registry, func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
		return persistErr
	}, customClient)
	newTuiBridgeHarness(t, fixture).applyModel("proxy/alpha")
	if fixture.rd.model != "model-a" || fixture.rd.wireModel != "model-a" {
		t.Fatalf("failed switch changed rd model = %q/%q", fixture.rd.model, fixture.rd.wireModel)
	}
	if fixture.rd.client != baseClient {
		t.Fatalf("failed custom-provider switch kept the custom client: %T", fixture.rd.client)
	}
	if model := fixture.host.currentModel(); model == nil || model.ID != "model-a" {
		t.Fatalf("failed switch changed the host model = %+v", model)
	}
	if fixture.host.takePendingModel() != nil {
		t.Fatal("failed switch queued an intent")
	}
	if err := fixture.host.removeProvider("openrouter"); err == nil {
		t.Fatal("the active provider must stay guarded after a failed switch")
	}
	if err := fixture.host.removeProvider("proxy"); err != nil {
		t.Fatalf("inactive provider removal failed: %v", err)
	}
}

func TestTuiApplyModelPersistFailureKeepsEffortAndPreparer(t *testing.T) {
	fixture, registry := newTuiHostFixtureWithModels(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
		models.ModelInfo{ID: "model-b", ContextWindow: 8192, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
	)
	fixture.rd.provider = "openrouter"
	fixture.rd.model = "model-a"
	fixture.rd.wireModel = "model-a"
	baseClient := &capturingClient{}
	fixture.rd.client = baseClient
	oldPreparer := testPreparer(t)
	fixture.rd.preparer = oldPreparer
	fixture.host.setModel(registry, "model-a", "model-a", "openrouter")
	fixture.host.setReasoningSeam(true)
	persistErr := errors.New("profile write failed")
	bindTuiHostModelTransactions(t, fixture, registry, func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
		return persistErr
	}, nil)
	if err := fixture.host.requestThinking(sdk.ThinkingHigh, nil); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	newTuiBridgeHarness(t, fixture).applyModel("model-b")
	if fixture.rd.model != "model-a" || fixture.rd.wireModel != "model-a" {
		t.Fatalf("failed switch changed rd model = %q/%q", fixture.rd.model, fixture.rd.wireModel)
	}
	if fixture.rd.client != baseClient || fixture.rd.preparer != oldPreparer {
		t.Fatal("failed switch changed the client or preparer")
	}
	if got := fixture.host.currentThinking(); got != sdk.ThinkingHigh {
		t.Fatalf("failed switch changed thinking = %q", got)
	}
	if model := fixture.host.currentModel(); model == nil || model.ID != "model-a" {
		t.Fatalf("failed switch changed the host model = %+v", model)
	}
	if fixture.host.takePendingModel() != nil {
		t.Fatal("failed switch queued an intent")
	}
}

func TestTuiHostModelAndThinkingChangesFromHookStayTruthful(t *testing.T) {
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("one"), textStop("two")}}
	var hookErrors []error
	extension := &hostHookExtension{
		id: "tui-r3-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			switch call {
			case 0:
				if err := ctx.SetModel(sdk.Model{ID: "model-b"}); err != nil {
					hookErrors = append(hookErrors, err)
				}
			case 1:
				if err := ctx.SetThinkingLevel(sdk.ThinkingHigh); err != nil {
					hookErrors = append(hookErrors, err)
				}
			}
			return nil, nil
		},
	}
	fixture := newHostTuiFixture(t, client, extension)
	fixture.rd.modelRegistry.Register("test/model", models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	fixture.rd.modelRegistry.Register("model-b", models.ModelInfo{
		ID:            "model-b",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	fixture.host.setModel(fixture.rd.modelRegistry, "test/model", "test/model", "openrouter")
	fixture.host.setReasoningSeam(true)
	fixture.host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return model, "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return newModelPreparer(config.Config{Model: model, ContextEnabled: true}, fixture.rd.modelRegistry, model, wire, nil)
		},
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			adopt(handle)
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fixture.start(ctx)
	select {
	case <-fixture.terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	fixture.terminal.SendInput("first")
	fixture.terminal.SendInput("\r")
	waitUntil(t, 5*time.Second, "first turn did not finish", func() bool {
		return strings.Contains(fixture.terminal.Output(), "one")
	})
	waitUntil(t, 5*time.Second, "model footer did not update", func() bool {
		return strings.Contains(fixture.terminal.Output(), "model-b")
	})
	fixture.terminal.SendInput("second")
	fixture.terminal.SendInput("\r")
	waitUntil(t, 5*time.Second, "thinking footer did not update", func() bool {
		return strings.Contains(fixture.terminal.Output(), "high")
	})
	waitUntil(t, 5*time.Second, "second turn did not finish", func() bool {
		return strings.Contains(fixture.terminal.Output(), "two")
	})
	preparer := fixture.host.currentPreparer()
	if preparer == nil || preparer.selectorModel != "model-b" || preparer.contextWindow != 4096 {
		t.Fatalf("next-turn preparer = %+v, want the model-b selector and window", preparer)
	}
	fixture.terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return")
	}
	if len(client.reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.reqs))
	}
	if client.reqs[0].Model != "test/model" || client.reqs[1].Model != "model-b" {
		t.Fatalf("request models = %q, %q", client.reqs[0].Model, client.reqs[1].Model)
	}
	if len(hookErrors) != 0 {
		t.Fatalf("hook errors = %v", hookErrors)
	}
}
