package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func runSlash(t *testing.T, fixture *bridgeFixture, input string, fragment string) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fixture.bridge.handle(input)
		close(done)
	}()
	waitForOutputSettled(t, fixture.terminal, fragment, 3*time.Second)
	return done
}

func TestBridgeModelSelectionChangesNextRequest(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("done")}, nil)
	registry := models.NewRegistry()
	registry.Register("alpha", models.ModelInfo{ID: "alpha", Provider: "prov", ContextWindow: 1000})
	registry.Register("beta", models.ModelInfo{ID: "beta", Provider: "prov", ContextWindow: 2000})
	fixture.bridge.rd.modelRegistry = registry
	done := runSlash(t, fixture, "/model", "Select model")
	fixture.terminal.SendInput("beta")
	fixture.terminal.SendInput("\r")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("model selector did not close")
	}
	if fixture.bridge.rd.model != "beta" {
		t.Fatalf("rd.model = %q, want beta", fixture.bridge.rd.model)
	}
	turned := make(chan struct{})
	fixture.bridge.afterTurn = func() { close(turned) }
	fixture.bridge.submit("hello")
	select {
	case <-turned:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	if len(fixture.client.reqs) == 0 {
		t.Fatal("no turn request was captured")
	}
	if got := fixture.client.reqs[len(fixture.client.reqs)-1].Model; got != "beta" {
		t.Fatalf("TurnRequest.Model = %q, want beta", got)
	}
}

func TestBridgeSettingsApplyAndCancelEffects(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.retryPolicy.Enabled = true
	done := runSlash(t, fixture, "/settings", "Auto retry")
	fixture.terminal.SendInput("\r")
	fixture.terminal.SendInput("\x13")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("settings dialog did not close")
	}
	if fixture.bridge.rd.retryPolicy.Enabled {
		t.Fatal("retry setting was not applied")
	}

	fixture.bridge.rd.retryPolicy.Enabled = true
	cancelled := runSlash(t, fixture, "/settings", "Auto retry")
	fixture.terminal.SendInput("\r")
	fixture.terminal.SendInput("\x1b")
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("settings dialog did not cancel")
	}
	if !fixture.bridge.rd.retryPolicy.Enabled {
		t.Fatal("cancelled settings must not change retry")
	}
}

func TestBridgeSettingsToolAndThinkingExpansion(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.runner.Surface().AddToolExecution("read", nil)
	done := runSlash(t, fixture, "/settings", "Auto retry")
	fixture.terminal.SendInput("\x1b[B")
	fixture.terminal.SendInput("\r")
	fixture.terminal.SendInput("\x1b[B")
	fixture.terminal.SendInput("\r")
	fixture.terminal.SendInput("\x13")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("settings dialog did not close")
	}
	if !fixture.runner.Surface().ToolsExpanded() {
		t.Fatal("tool expansion setting was not applied")
	}
	if !fixture.runner.Surface().ThinkingExpanded() {
		t.Fatal("thinking visibility setting was not applied")
	}
}

func TestBridgeThemeSelectionRecolorsRetainedTranscript(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.runner.Surface().AddUserMessage("retained transcript line")
	before := fixture.runner.ThemeRegistry().ActiveName()
	done := runSlash(t, fixture, "/theme", "Select theme")
	fixture.terminal.SendInput("light")
	fixture.terminal.SendInput("\r")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("theme selector did not close")
	}
	after := fixture.runner.ThemeRegistry().ActiveName()
	if after == before {
		t.Fatalf("theme did not change from %q", before)
	}
	frame := bridgeFrameText(t, fixture)
	if !strings.Contains(frame, "retained transcript line") {
		t.Fatalf("retained transcript disappeared after recolor:\n%s", frame)
	}
}

func TestExtensionCommandReceivesInteractiveUIAndSignal(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	defer runtime.SetContextDecorator(nil)
	runtime.SetContextDecorator(func(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext {
		return fixture.runner.InteractiveHandlerContext(signal, base)
	})
	signal, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlerCtx := runtime.HandlerContext(signal)
	if handlerCtx.Mode() != sdk.ModeInteractive {
		t.Fatalf("Mode = %v, want interactive", handlerCtx.Mode())
	}
	if !handlerCtx.HasUI() {
		t.Fatal("extension handler context has no UI")
	}
	if handlerCtx.Signal() != signal {
		t.Fatal("extension handler context lost the signal")
	}
	done := make(chan error, 1)
	go func() {
		_, err := handlerCtx.UI().Confirm("confirm", "from an extension")
		done <- err
	}()
	waitForOutputSettled(t, fixture.terminal, "from an extension", 3*time.Second)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Confirm error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("extension dialog did not cancel")
	}
}

func TestPrintAndNonTTYDialogsStayUnsupported(t *testing.T) {
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: &strings.Builder{},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, &strings.Builder{}, sdk.ModePrint)
	for name, call := range map[string]func() error{
		"confirm": func() error { _, err := lineUI.Confirm("t", "m"); return err },
		"select":  func() error { _, err := lineUI.Select("t", []string{"a"}); return err },
		"input":   func() error { _, err := lineUI.Input("t", "p"); return err },
		"editor":  func() error { _, err := lineUI.Editor("t", ""); return err },
	} {
		if err := call(); !errors.Is(err, sdk.ErrModeUnsupported) {
			t.Fatalf("%s error = %v, want ErrModeUnsupported", name, err)
		}
	}
}

type interactiveHookExtension struct {
	mu         sync.Mutex
	sessionCtx sdk.HandlerContext
	toolCtx    sdk.HandlerContext
}

func (e *interactiveHookExtension) ID() string { return "interactive-hooks" }

func (e *interactiveHookExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	r.OnSessionStart(func(ctx sdk.HandlerContext, _ sdk.SessionStartEvent) error {
		e.mu.Lock()
		e.sessionCtx = ctx
		e.mu.Unlock()
		_, err := ctx.UI().Confirm("startup", "hook from startup")
		return err
	})
}

func (e *interactiveHookExtension) RegisterToolHooks(r sdk.ToolHookRegistry) {
	r.OnToolCall(func(ctx sdk.HandlerContext, _ sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
		e.mu.Lock()
		e.toolCtx = ctx
		e.mu.Unlock()
		_, err := ctx.UI().Confirm("tool", "hook from tool call")
		return nil, err
	})
}

func (e *interactiveHookExtension) contexts() (sdk.HandlerContext, sdk.HandlerContext) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionCtx, e.toolCtx
}

func TestDispatchedHooksReceiveInteractiveUIAndSignal(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	registry := extensions.NewRegistry()
	extension := &interactiveHookExtension{}
	if err := registry.Register(extension); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(registry)
	runtime.SetContextDecorator(func(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext {
		return fixture.runner.InteractiveHandlerContext(signal, base)
	})
	defer runtime.SetContextDecorator(nil)
	dispatcher := runtime.Dispatcher()

	startupCtx, cancelStartup := context.WithCancel(context.Background())
	startupDone := make(chan error, 1)
	go func() { startupDone <- dispatcher.SessionStart(startupCtx, string(sdk.SessionStartStartup)) }()
	waitForOutputSettled(t, fixture.terminal, "hook from startup", 3*time.Second)
	cancelStartup()
	select {
	case <-startupDone:
	case <-time.After(3 * time.Second):
		t.Fatal("SessionStart hook did not finish")
	}
	sessionCtx, _ := extension.contexts()
	if sessionCtx == nil {
		t.Fatal("SessionStart hook did not receive a handler context")
	}
	if sessionCtx.Mode() != sdk.ModeInteractive || !sessionCtx.HasUI() {
		t.Fatalf("SessionStart hook context mode/ui = %v/%v", sessionCtx.Mode(), sessionCtx.HasUI())
	}
	if sessionCtx.Signal() != startupCtx {
		t.Fatal("SessionStart hook lost its signal")
	}

	toolCtx, cancelTool := context.WithCancel(context.Background())
	toolDone := make(chan error, 1)
	go func() {
		_, err := dispatcher.ToolCall(toolCtx, "read", "call-1", nil)
		toolDone <- err
	}()
	waitForOutputSettled(t, fixture.terminal, "hook from tool", 3*time.Second)
	cancelTool()
	select {
	case <-toolDone:
	case <-time.After(3 * time.Second):
		t.Fatal("ToolCall hook did not finish")
	}
	_, callCtx := extension.contexts()
	if callCtx == nil {
		t.Fatal("ToolCall hook did not receive a handler context")
	}
	if callCtx.Signal() != toolCtx {
		t.Fatal("ToolCall hook lost its signal")
	}
	if callCtx.Mode() != sdk.ModeInteractive {
		t.Fatalf("ToolCall hook mode = %v, want interactive", callCtx.Mode())
	}
}

func TestSelectorHelpersAndErrorPaths(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.inform("")
	fixture.bridge.inform("notice text")
	fixture.bridge.warn(nil)
	if choices := fixture.bridge.modelChoices(); len(choices) != 1 || choices[0].Provider != "current" {
		t.Fatalf("default modelChoices = %+v", choices)
	}
	registry := models.NewRegistry()
	registry.Register("only", models.ModelInfo{ID: "only", Provider: "prov", ContextWindow: 1000})
	registry.Register("broken", models.ModelInfo{ID: "broken", Provider: "prov", ContextWindow: 1000})
	registry.Register("recovered", models.ModelInfo{ID: "recovered", Provider: "prov", ContextWindow: 1000})
	fixture.bridge.rd.modelRegistry = registry
	choices := fixture.bridge.modelChoices()
	foundOnly := false
	for _, choice := range choices {
		if choice.ID == "only" && choice.Provider == "prov" {
			foundOnly = true
		}
	}
	if !foundOnly {
		t.Fatalf("registry modelChoices missing the registered model: %+v", choices)
	}
	entries := fixture.bridge.helpEntries()
	if len(entries) == 0 {
		t.Fatal("helpEntries returned nothing")
	}
	seenModel := false
	for _, entry := range entries {
		if entry.Name == "model" {
			seenModel = true
		}
	}
	if !seenModel {
		t.Fatal("helpEntries missing the model command")
	}

	fixture.bridge.rd.reprepare = func(string, string) (*contextPreparerAdapter, error) {
		return nil, errors.New("window unavailable")
	}
	fixture.bridge.applyModel("broken")
	if fixture.bridge.rd.model == "broken" {
		t.Fatal("applyModel changed the model despite a preparer failure")
	}
	fixture.bridge.rd.reprepare = nil
	fixture.bridge.applyModel("recovered")
	if fixture.bridge.rd.model != "recovered" {
		t.Fatalf("applyModel = %q, want recovered", fixture.bridge.rd.model)
	}

	fixture.runner.CancelDialogs()
	fixture.bridge.showHelp()
	fixture.bridge.selectTheme()
	fixture.bridge.showSettings()
	fixture.bridge.selectModel()
	if fixture.runner.Surface().RenderFrame(80, 24).Lines == nil {
		t.Fatal("surface stopped rendering after selector errors")
	}
}

func TestShutdownHooksCannotOpenDialogs(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	runtime.SetContextDecorator(func(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext {
		return fixture.runner.InteractiveHandlerContext(signal, base)
	})
	defer runtime.SetContextDecorator(nil)
	fixture.runner.CancelDialogs()
	handlerCtx := runtime.HandlerContext(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := handlerCtx.UI().Confirm("shutdown", "should not open")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("shutdown hook opened a dialog after dialogs were cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hook dialog did not fail fast")
	}
}

func TestTransportSupportsModelNormalizesAliases(t *testing.T) {
	cases := []struct {
		transport string
		provider  string
		want      bool
	}{
		{"", "anything", true},
		{"openrouter", "anything", true},
		{"openrouter-oauth", "openai", true},
		{"anthropic", "anthropic", true},
		{"anthropic", "openai", false},
		{"anthropic-oauth", "anthropic", true},
		{"anthropic-oauth", "openai", false},
		{"codex", "openai", true},
		{"codex", "anthropic", false},
		{"azure-openai-responses", "openai", true},
		{"xai-subscription", "xai", true},
		{"kimi-coding-oauth", "kimi", true},
		{"kimi-coding", "kimi", true},
		{"gemini", "google", true},
		{"google", "google", true},
		{"deepseek", "deepseek", true},
		{"deepseek", "openai", false},
	}
	for _, tc := range cases {
		if got := transportSupportsModel(tc.transport, tc.provider); got != tc.want {
			t.Errorf("transportSupportsModel(%q, %q) = %v, want %v", tc.transport, tc.provider, got, tc.want)
		}
	}
}

func modelChoiceIDs(choices []ui.ModelChoice) []string {
	ids := make([]string, 0, len(choices))
	for _, choice := range choices {
		ids = append(ids, choice.ID)
	}
	return ids
}

func verifiedModelChoices(choices []ui.ModelChoice) []ui.ModelChoice {
	out := make([]ui.ModelChoice, 0, len(choices))
	for _, choice := range choices {
		if choice.Provider == currentModelProviderLabel {
			continue
		}
		out = append(out, choice)
	}
	return out
}

func choicesContain(choices []ui.ModelChoice, id string) bool {
	for _, choice := range choices {
		if choice.ID == id {
			return true
		}
	}
	return false
}

func TestBridgeModelChoicesFilterByTransport(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register("kimi/kimi-for-coding", models.ModelInfo{ID: "kimi/kimi-for-coding", Provider: "kimi", ContextWindow: 3000})
	fixture.bridge.rd.modelRegistry = registry

	fixture.bridge.rd.provider = "anthropic"
	anthropicChoices := fixture.bridge.modelChoices()
	if len(anthropicChoices) == 0 {
		t.Fatal("anthropic transport offered no models")
	}
	for _, choice := range verifiedModelChoices(anthropicChoices) {
		if choice.Provider != "anthropic" {
			t.Fatalf("anthropic transport offered %q from provider %q", choice.ID, choice.Provider)
		}
	}
	if !choicesContain(anthropicChoices, "test/model") {
		t.Fatalf("anthropic transport dropped the current model: %v", modelChoiceIDs(anthropicChoices))
	}

	fixture.bridge.rd.provider = "codex"
	codexChoices := fixture.bridge.modelChoices()
	if len(codexChoices) == 0 {
		t.Fatal("codex transport offered no models")
	}
	for _, choice := range verifiedModelChoices(codexChoices) {
		if choice.Provider != "openai" {
			t.Fatalf("codex transport offered %q from provider %q", choice.ID, choice.Provider)
		}
	}
	if !choicesContain(codexChoices, "test/model") {
		t.Fatalf("codex transport dropped the current model: %v", modelChoiceIDs(codexChoices))
	}

	fixture.bridge.rd.provider = "kimi-coding-oauth"
	kimiChoices := fixture.bridge.modelChoices()
	if ids := modelChoiceIDs(verifiedModelChoices(kimiChoices)); len(ids) != 1 || ids[0] != "kimi/kimi-for-coding" {
		t.Fatalf("kimi transport choices = %v, want [kimi/kimi-for-coding]", ids)
	}
	if !choicesContain(kimiChoices, "test/model") {
		t.Fatalf("kimi transport dropped the current model: %v", modelChoiceIDs(kimiChoices))
	}

	fixture.bridge.rd.provider = "openrouter"
	broadChoices := fixture.bridge.modelChoices()
	providers := map[string]bool{}
	for _, choice := range broadChoices {
		providers[choice.Provider] = true
	}
	if !providers["anthropic"] || !providers["openai"] || !providers["kimi"] {
		t.Fatalf("openrouter transport providers = %v, want multiple providers", providers)
	}
	if !choicesContain(broadChoices, "test/model") {
		t.Fatalf("openrouter transport dropped the current model: %v", modelChoiceIDs(broadChoices))
	}
}

func TestBridgeModelChoicesPreserveCurrentModelAndRejectUnverified(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register("anthropic/claude-fable-5", models.ModelInfo{ID: "anthropic/claude-fable-5", Provider: "anthropic", ContextWindow: 1_000_000})
	registry.Register("anthropic/claude-3.5-sonnet", models.ModelInfo{ID: "anthropic/claude-3.5-sonnet", Provider: "anthropic", ContextWindow: 200_000})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "anthropic"
	fixture.bridge.rd.model = "custom/experimental"
	fixture.bridge.rd.wireModel = "experimental"

	choices := fixture.bridge.modelChoices()
	if !choicesContain(choices, "custom/experimental") {
		t.Fatalf("modelChoices dropped the configured current model: %v", modelChoiceIDs(choices))
	}
	for _, choice := range choices {
		if choice.ID == "custom/experimental" && choice.Provider != currentModelProviderLabel {
			t.Fatalf("current model labelled %q, want %q", choice.Provider, currentModelProviderLabel)
		}
	}
	if !choicesContain(choices, "anthropic/claude-fable-5") {
		t.Fatalf("modelChoices dropped the verified native model: %v", modelChoiceIDs(choices))
	}
	if choicesContain(choices, "anthropic/claude-3.5-sonnet") {
		t.Fatalf("modelChoices offered an unverified native model: %v", modelChoiceIDs(choices))
	}
	if !fixture.bridge.modelSelectable("custom/experimental") {
		t.Fatal("the configured current model must stay selectable")
	}
	if fixture.bridge.modelSelectable("anthropic/claude-3.5-sonnet") {
		t.Fatal("an unverified native model must not be selectable")
	}

	fixture.bridge.applyModel("custom/experimental")
	if fixture.bridge.rd.model != "custom/experimental" || fixture.bridge.rd.wireModel != "experimental" {
		t.Fatalf("applyModel mutated the current selection: model=%q wire=%q", fixture.bridge.rd.model, fixture.bridge.rd.wireModel)
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "custom/experimental") {
		t.Fatalf("footer missing the configured current model:\n%s", frame)
	}
}

func TestBridgeApplyModelRejectsIncompatibleTransport(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.runner.Surface().SetModel("test/model")
	registry := models.NewRegistry()
	registry.Register("anthropic/claude-fable-5", models.ModelInfo{ID: "anthropic/claude-fable-5", Provider: "anthropic", ContextWindow: 4096})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "anthropic"

	previous := testPreparer(t)
	fixture.bridge.rd.preparer = previous
	reprepareCalls := 0
	fixture.bridge.rd.reprepare = func(string, string) (*contextPreparerAdapter, error) {
		reprepareCalls++
		return testPreparer(t), nil
	}

	fixture.bridge.applyModel("openai/gpt-5")
	if fixture.bridge.rd.model != "test/model" {
		t.Fatalf("rejected model changed rd.model to %q", fixture.bridge.rd.model)
	}
	if fixture.bridge.rd.preparer != previous {
		t.Fatal("rejected model replaced the preparer")
	}
	if reprepareCalls != 0 {
		t.Fatalf("rejected model rebuilt the preparer %d time(s)", reprepareCalls)
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "test/model") {
		t.Fatalf("rejected model changed the footer:\n%s", frame)
	}

	replacement := testPreparer(t)
	fixture.bridge.rd.reprepare = func(string, string) (*contextPreparerAdapter, error) {
		return replacement, nil
	}
	fixture.bridge.applyModel("anthropic/claude-fable-5")
	if fixture.bridge.rd.model != "anthropic/claude-fable-5" {
		t.Fatalf("rd.model = %q, want anthropic/claude-fable-5", fixture.bridge.rd.model)
	}
	if fixture.bridge.rd.preparer != replacement {
		t.Fatal("accepted model did not install the rebuilt preparer")
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "anthropic/claude-fable-5") {
		t.Fatalf("footer missing the accepted model:\n%s", frame)
	}
}

func TestBridgeApplyModelRollsBackOnFailure(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.runner.Surface().SetModel("test/model")
	registry := models.NewRegistry()
	registry.Register("openai/alpha", models.ModelInfo{ID: "openai/alpha", Provider: "openai", ContextWindow: 4096})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "openrouter"

	previous := testPreparer(t)
	fixture.bridge.rd.preparer = previous
	fixture.bridge.rd.reprepare = func(string, string) (*contextPreparerAdapter, error) {
		return nil, errors.New("window unavailable")
	}
	fixture.bridge.applyModel("openai/alpha")
	if fixture.bridge.rd.model != "test/model" || fixture.bridge.rd.preparer != previous {
		t.Fatal("preparer failure was not rolled back")
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "test/model") {
		t.Fatalf("preparer failure changed the footer:\n%s", frame)
	}

	built := testPreparer(t)
	persistErr := errors.New("profile write failed")
	fixture.bridge.rd.reprepare = func(string, string) (*contextPreparerAdapter, error) {
		return built, nil
	}
	fixture.bridge.rd.persistModel = func(string) error {
		return persistErr
	}
	fixture.bridge.applyModel("openai/alpha")
	if fixture.bridge.rd.model != "test/model" {
		t.Fatalf("profile failure changed rd.model to %q", fixture.bridge.rd.model)
	}
	if fixture.bridge.rd.preparer != previous {
		t.Fatal("profile failure installed the rebuilt preparer")
	}

	fixture.bridge.rd.persistModel = nil
	fixture.bridge.applyModel("openai/alpha")
	if fixture.bridge.rd.model != "openai/alpha" || fixture.bridge.rd.preparer != built {
		t.Fatal("successful apply did not install the model and preparer")
	}
}

func TestNewModelPreparerCarriesSelectedModelAndWindow(t *testing.T) {
	registry := models.NewRegistry()
	registry.Register("vendor/wide", models.ModelInfo{ID: "vendor/wide", Provider: "vendor", ContextWindow: 4096})

	cfg := config.Config{Model: "vendor/stale", ContextEnabled: true}
	preparer, err := newModelPreparer(cfg, registry, "vendor/wide", "vendor/wire", nil)
	if err != nil {
		t.Fatalf("newModelPreparer: %v", err)
	}
	if preparer.contextWindow != 4096 {
		t.Fatalf("contextWindow = %d, want the selected model window 4096", preparer.contextWindow)
	}
	if preparer.selectorModel != "vendor/wire" {
		t.Fatalf("selectorModel = %q, want the wire model", preparer.selectorModel)
	}

	override := cfg
	override.ContextWindowTokens = 9999
	preparer, err = newModelPreparer(override, registry, "vendor/wide", "vendor/wire", nil)
	if err != nil {
		t.Fatalf("newModelPreparer with override: %v", err)
	}
	if preparer.contextWindow != 9999 {
		t.Fatalf("contextWindow = %d, want the explicit override 9999", preparer.contextWindow)
	}
	if preparer.selectorModel != "vendor/wire" {
		t.Fatalf("selectorModel = %q, want the wire model", preparer.selectorModel)
	}
}

func TestBridgeModelSelectionPreparesNextTurnWithSelectedModel(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("done")}, nil)
	registry := models.NewRegistry()
	registry.Register("openai/alpha", models.ModelInfo{ID: "openai/alpha", Provider: "openai", ContextWindow: 1111})
	registry.Register("openai/beta", models.ModelInfo{ID: "openai/beta", Provider: "openai", ContextWindow: 2222})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "openrouter"
	fixture.bridge.rd.preparer = testPreparer(t)

	config := config.Config{Model: "openai/alpha", ContextEnabled: true}
	selected, err := newModelPreparer(config, registry, "openai/beta", "openai/beta", nil)
	if err != nil {
		t.Fatalf("newModelPreparer: %v", err)
	}
	fixture.bridge.rd.reprepare = func(model, wireModel string) (*contextPreparerAdapter, error) {
		if model != "openai/beta" {
			t.Fatalf("reprepare model = %q, want openai/beta", model)
		}
		if wireModel != "openai/beta" {
			t.Fatalf("reprepare wire model = %q, want openai/beta", wireModel)
		}
		return selected, nil
	}

	fixture.bridge.applyModel("openai/beta")
	if fixture.bridge.rd.preparer != selected {
		t.Fatal("applyModel did not install the selected preparer")
	}
	if selected.selectorModel != "openai/beta" || selected.contextWindow != 2222 {
		t.Fatalf("selected preparer window/model = %d/%q", selected.contextWindow, selected.selectorModel)
	}

	turned := make(chan struct{})
	fixture.bridge.afterTurn = func() { close(turned) }
	fixture.bridge.submit("hello")
	select {
	case <-turned:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	if len(fixture.client.reqs) == 0 {
		t.Fatal("no turn request was captured")
	}
	if got := fixture.client.reqs[len(fixture.client.reqs)-1].Model; got != "openai/beta" {
		t.Fatalf("TurnRequest.Model = %q, want openai/beta", got)
	}
	if fixture.bridge.rd.preparer != selected {
		t.Fatal("turn changed the installed preparer")
	}
}
