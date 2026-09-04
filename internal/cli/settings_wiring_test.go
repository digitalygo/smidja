package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/gateway"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

type errorStopClient struct {
	mu    sync.Mutex
	calls int
}

func (c *errorStopClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		Content:      []agent.ContentBlock{},
		API:          "openai-completions",
		Provider:     "openrouter",
		Model:        req.Model,
		StopReason:   "error",
		ErrorMessage: "503 service unavailable",
		Timestamp:    1,
	}, nil
}

func (c *errorStopClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type retryRecorder struct {
	mu      sync.Mutex
	delays  []int64
	maxima  []int
	started bool
}

func (r *retryRecorder) extension() sdk.Extension {
	return &retryRecorderExtension{rec: r}
}

type retryRecorderExtension struct {
	rec *retryRecorder
}

func (e *retryRecorderExtension) ID() string { return "retry-recorder" }

func (e *retryRecorderExtension) RegisterLLMHooks(r sdk.LLMHookRegistry) {
	r.OnAutoRetryStart(func(ctx sdk.HandlerContext, ev sdk.AutoRetryStartEvent) error {
		e.rec.mu.Lock()
		defer e.rec.mu.Unlock()
		e.rec.started = true
		e.rec.delays = append(e.rec.delays, ev.DelayMs)
		e.rec.maxima = append(e.rec.maxima, ev.MaxAttempts)
		return nil
	})
}

func settingsWiringConfig(t *testing.T, env map[string]string, cwd string) *config.Config {
	t.Helper()
	cfg, err := config.Load(
		envFrom(env),
		func() (string, error) { return cwd, nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSelectChatClient(t *testing.T) {
	injected := &capturingClient{}
	cfg := settingsWiringConfig(t, map[string]string{"DEEPSEEK_API_KEY": "sk-deepseek"}, "/work")
	depsWithHome := func(env map[string]string) *Deps {
		return &Deps{Client: injected, Env: envFrom(env), Home: func() string { return t.TempDir() }}
	}

	client, selected, err := selectChatClient(depsWithHome(map[string]string{"DEEPSEEK_API_KEY": "sk-deepseek"}), cfg, "deepseek")
	if err != nil {
		t.Fatalf("selectChatClient(deepseek): %v", err)
	}
	if selected != "deepseek" {
		t.Errorf("selected = %q, want deepseek", selected)
	}
	if client == nil || client == agent.Client(injected) {
		t.Error("a selected provider must build a dedicated client")
	}

	client, selected, err = selectChatClient(depsWithHome(nil), cfg, "")
	if err != nil {
		t.Fatalf("selectChatClient(injected): %v", err)
	}
	if selected != "" || client != agent.Client(injected) {
		t.Errorf("selected = %q, want empty and the injected client to stay authoritative", selected)
	}

	client, _, err = selectChatClient(&Deps{Env: envFrom(nil), Home: func() string { return t.TempDir() }}, cfg, "")
	if err != nil {
		t.Fatalf("selectChatClient(fallback): %v", err)
	}
	if client == nil {
		t.Error("without provider or injected client the openrouter fallback must be built")
	}

	if _, _, err := selectChatClient(depsWithHome(nil), cfg, "bogus"); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("flag provider error = %v, want unknown provider", err)
	}

	bogusCfg := settingsWiringConfig(t, map[string]string{"SMIDJA_PROVIDER": "bogus"}, "/work")
	if _, _, err := selectChatClient(depsWithHome(nil), bogusCfg, ""); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("config provider error = %v, want unknown provider", err)
	}
}

func TestProviderSelectionFromConfigReachesChat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Env = envFrom(map[string]string{"SMIDJA_PROVIDER": "bogus", "SMIDJA_PACKAGES_DIR": t.TempDir()})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("")
	deps.Store = wiringStore(t)

	if err := RunWithDeps([]string{"-p", "hello"}, deps); err == nil {
		t.Fatal("RunWithDeps: expected the provider selection error")
	} else if !strings.Contains(err.Error(), `unknown provider "bogus"`) {
		t.Errorf("error = %v, want unknown provider", err)
	}
}

func TestRetryPolicyFromConfigReachesChatLoop(t *testing.T) {
	rec := &retryRecorder{}
	runtime := extensions.NewRuntime(mustRegister(t, rec.extension()))
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Env = envFrom(map[string]string{
		"SMIDJA_RETRY_MAX_RETRIES":   "2",
		"SMIDJA_RETRY_BASE_DELAY_MS": "1",
	})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	client := &errorStopClient{}
	deps.Client = client
	deps.Stdin = strings.NewReader("")
	deps.Config = settingsWiringConfig(t, map[string]string{"SMIDJA_RETRY_MAX_RETRIES": "2", "SMIDJA_RETRY_BASE_DELAY_MS": "1"}, t.TempDir())
	deps.Store = wiringStore(t)
	deps.ExtensionRuntime = runtime

	if err := RunWithDeps([]string{"-p", "hello"}, deps); err == nil {
		t.Fatal("RunWithDeps: expected the provider error after exhausting retries")
	}
	if got := client.callCount(); got != 3 {
		t.Errorf("client calls = %d, want 3 (first attempt plus 2 configured retries)", got)
	}
	rec.mu.Lock()
	delays := append([]int64(nil), rec.delays...)
	maxima := append([]int(nil), rec.maxima...)
	started := rec.started
	rec.mu.Unlock()
	if !started {
		t.Fatal("no retry started hook fired")
	}
	if len(delays) != 2 || delays[0] != 1 || delays[1] != 2 {
		t.Errorf("retry delays = %v, want [1 2] from the configured base delay", delays)
	}
	if len(maxima) != 2 || maxima[0] != 2 {
		t.Errorf("retry maxima = %v, want the configured 2", maxima)
	}
}

func TestRetryDisabledFromConfigStopsFirstFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Env = envFrom(map[string]string{"SMIDJA_RETRY": "false"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	client := &errorStopClient{}
	deps.Client = client
	deps.Stdin = strings.NewReader("")
	deps.Config = settingsWiringConfig(t, map[string]string{"SMIDJA_RETRY": "false"}, t.TempDir())
	deps.Store = wiringStore(t)

	if err := RunWithDeps([]string{"-p", "hello"}, deps); err == nil {
		t.Fatal("RunWithDeps: expected the provider error")
	}
	if got := client.callCount(); got != 1 {
		t.Errorf("client calls = %d, want 1 with retry disabled", got)
	}
}

func TestGatewayRetryPolicyFromConfig(t *testing.T) {
	cfg := settingsWiringConfig(t, map[string]string{"SMIDJA_RETRY_MAX_RETRIES": "2", "SMIDJA_RETRY_BASE_DELAY_MS": "1"}, t.TempDir())
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := &errorStopClient{}
	bindings, err := loadBindings(filepath.Join(t.TempDir(), "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	runner := newGatewayRunner(gatewayRunnerConfig{
		cfg:            cfg,
		providerID:     "openrouter",
		model:          "test/model",
		store:          store,
		bindings:       bindings,
		client:         client,
		catalog:        extensions.NewToolCatalog(),
		retry:          retryAdapter,
		retryPolicy:    agent.RetryPolicy{Enabled: cfg.RetryEnabled, MaxRetries: cfg.RetryMaxRetries, BaseDelayMs: cfg.RetryBaseDelayMs},
		retryPolicySet: true,
	})
	if _, err := runner.Run(context.Background(), gateway.WorkItem{Text: "hello"}); err == nil {
		t.Fatal("gateway run: expected the provider error")
	}
	if got := client.callCount(); got != 3 {
		t.Errorf("gateway client calls = %d, want 3 (first attempt plus 2 configured retries)", got)
	}
}

func fixtureCatalogServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("..", "models", "testdata", "pi-models-sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("ETag", `"fixture"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestModelsCatalogURLEnvironmentSeam(t *testing.T) {
	srv, hits := fixtureCatalogServer(t)
	reg := models.NewRegistry()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = settingsWiringConfig(t, map[string]string{"SMIDJA_MODELS_CATALOG_URL": srv.URL}, t.TempDir())
	deps.Config.SessionDir = filepath.Join(t.TempDir(), "sessions")
	deps.Store = wiringStore(t)
	deps.ModelRegistry = reg
	deps.ModelsCatalog = &models.CatalogSource{}

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if *hits == 0 {
		t.Error("SMIDJA_MODELS_CATALOG_URL was never fetched")
	}
	if _, ok := reg.Get("anthropic/claude-fable-5"); !ok {
		t.Error("catalog model missing from the registry")
	}
}

func TestModelsCatalogURLFromUserSettings(t *testing.T) {
	srv, hits := fixtureCatalogServer(t)
	home := t.TempDir()
	withUserSettingsFile(t, home, fmt.Sprintf(`{"modelsCatalogUrl": %q, "sessionDir": %q}`, srv.URL, filepath.Join(t.TempDir(), "sessions")))
	reg := models.NewRegistry()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Env = envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": t.TempDir()})
	deps.Home = func() string { return home }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Store = wiringStore(t)
	deps.ModelRegistry = reg
	deps.ModelsCatalog = &models.CatalogSource{}

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if *hits == 0 {
		t.Error("the user settings modelsCatalogUrl was never fetched")
	}
	if _, ok := reg.Get("anthropic/claude-fable-5"); !ok {
		t.Error("catalog model missing from the registry")
	}
}

func TestInjectedCatalogBaseURLAuthoritative(t *testing.T) {
	srvA, hitsA := fixtureCatalogServer(t)
	srvB, hitsB := fixtureCatalogServer(t)
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = settingsWiringConfig(t, map[string]string{"SMIDJA_MODELS_CATALOG_URL": srvB.URL}, t.TempDir())
	deps.Config.SessionDir = filepath.Join(t.TempDir(), "sessions")
	deps.Store = wiringStore(t)
	deps.ModelsCatalog = &models.CatalogSource{BaseURL: srvA.URL}

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if *hitsA == 0 {
		t.Error("the injected BaseURL must stay authoritative and be used")
	}
	if *hitsB != 0 {
		t.Error("the env URL must be ignored when a non-empty BaseURL is injected")
	}
}

func TestBundleModelsBeatWorkspaceOverrides(t *testing.T) {
	srv, _ := fixtureCatalogServer(t)
	cwd := t.TempDir()
	wsModels := filepath.Join(cwd, ".smidja", "models.json")
	if err := os.MkdirAll(filepath.Dir(wsModels), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsModels, []byte(`{"anthropic":{"claude-fable-5":{"id":"claude-fable-5","provider":"anthropic","contextWindow":222}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := models.NewRegistry()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = settingsWiringConfig(t, map[string]string{"SMIDJA_MODELS_CATALOG_URL": srv.URL}, cwd)
	deps.Config.SessionDir = filepath.Join(t.TempDir(), "sessions")
	deps.Store = wiringStore(t)
	deps.ModelRegistry = reg
	deps.ModelsCatalog = &models.CatalogSource{}
	deps.Bundle = sdk.Bundle{
		ID: "digitalygo",
		FS: fstest.MapFS{"models.json": {Data: []byte(`{"anthropic":{"claude-fable-5":{"id":"claude-fable-5","provider":"anthropic","contextWindow":333}}}`)}},
	}

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	m, ok := reg.Get("anthropic/claude-fable-5")
	if !ok || m.ContextWindow != 333 {
		t.Errorf("claude-fable-5 = %+v ok=%v, want the bundle override 333 above workspace 222", m, ok)
	}
}

func TestBundleModelsInvalidJSONFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("")
	deps.Config = settingsWiringConfig(t, nil, t.TempDir())
	deps.Store = wiringStore(t)
	deps.Bundle = sdk.Bundle{ID: "digitalygo", FS: fstest.MapFS{"models.json": {Data: []byte("{bad")}}}

	if err := RunWithDeps(nil, deps); err == nil {
		t.Fatal("RunWithDeps: expected an error for an invalid bundle models.json")
	}
}

func TestBundleInstructionsPrecedeProjectInstructions(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("project rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Client = client
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = settingsWiringConfig(t, map[string]string{"OPENROUTER_API_KEY": "sk-test"}, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = sdk.Bundle{
		ID: "digitalygo",
		FS: fstest.MapFS{"AGENTS.md": {Data: []byte("bundle rules")}},
	}

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	system := client.reqs[0].System
	bundleIdx := strings.Index(system, "[bundle instructions]")
	projectIdx := strings.Index(system, "[project instructions]")
	if bundleIdx < 0 || projectIdx < 0 {
		t.Fatalf("system prompt missing markers:\n%s", system)
	}
	if bundleIdx > projectIdx {
		t.Errorf("bundle instructions must precede project instructions:\n%s", system)
	}
	if !strings.Contains(system, "bundle rules") || !strings.Contains(system, "project rules") {
		t.Errorf("system prompt missing content:\n%s", system)
	}
}

func TestWorkspaceSettingsIgnoredByChat(t *testing.T) {
	cwd := t.TempDir()
	withUserSettingsFile(t, cwd, `{"defaultModel": "workspace/model"}`)
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Client = client
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = settingsWiringConfig(t, map[string]string{"OPENROUTER_API_KEY": "sk-test"}, cwd)
	deps.Store = wiringStore(t)

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if len(client.reqs) == 0 {
		t.Fatal("client received no requests")
	}
	if model := client.reqs[0].Model; model == "workspace/model" {
		t.Errorf("model = %q, workspace settings must never be read", model)
	}
}

func TestSkillUnqualifiedLookupAfterCollision(t *testing.T) {
	cwd := t.TempDir()
	skillsDir := filepath.Join(cwd, ".smidja", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "quick.md"), []byte("workspace quick"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Client = client
	deps.Stdin = strings.NewReader("/skill quick\n/quit\n")
	deps.Config = settingsWiringConfig(t, map[string]string{"OPENROUTER_API_KEY": "sk-test"}, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = sdk.Bundle{
		ID: "digitalygo",
		FS: fstest.MapFS{"skills/quick.md": {Data: []byte("bundle quick")}},
	}

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	injected := client.lastUserText()
	if !strings.Contains(injected, "[skill digitalygo/quick]") {
		t.Errorf("injected = %q, want the bundle winner provenance", injected)
	}
	if !strings.Contains(injected, "bundle quick") {
		t.Errorf("injected = %q, want the bundle content to win the collision", injected)
	}
	if strings.Contains(injected, "workspace quick") {
		t.Errorf("injected = %q, the losing workspace duplicate must not appear", injected)
	}
	if strings.Contains(stderr.String(), "no skill named") || strings.Contains(stderr.String(), "ambiguous") {
		t.Errorf("stderr = %q, unqualified lookup must resolve without ambiguity", stderr.String())
	}
}

func TestGatewayProviderSelectionFromConfig(t *testing.T) {
	var stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Env = envFrom(map[string]string{"SMIDJA_PROVIDER": "bogus", "SMIDJA_PACKAGES_DIR": t.TempDir()})
	deps.Stdout = &bytes.Buffer{}
	deps.Stderr = &stderr

	if err := runGatewayServer(deps, gatewayServerOptions{noWeb: true}); err == nil {
		t.Fatal("runGatewayServer: expected the provider selection error")
	} else if !strings.Contains(err.Error(), `unknown provider "bogus"`) {
		t.Errorf("error = %v, want unknown provider", err)
	}
}

func withUserSettingsFile(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".smidja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRegister(t *testing.T, ext sdk.Extension) *extensions.Registry {
	t.Helper()
	reg := extensions.NewRegistry()
	if err := reg.Register(ext); err != nil {
		t.Fatal(err)
	}
	return reg
}
