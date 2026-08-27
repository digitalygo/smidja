package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/sdk"
)

func TestProjectInstructionsInjectedIntoSystem(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("# workspace rules\nno comments in code"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = client
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if len(client.reqs) == 0 {
		t.Fatal("client received no requests")
	}
	system := client.reqs[0].System
	if !strings.Contains(system, "[project instructions]") {
		t.Errorf("system prompt missing the project marker:\n%s", system)
	}
	if !strings.Contains(system, "# workspace rules") || !strings.Contains(system, "no comments in code") {
		t.Errorf("system prompt missing the AGENTS.md content:\n%s", system)
	}
	if !strings.Contains(system, "You are smidja") {
		t.Error("instructions must be appended after the base system prompt")
	}
	if i := strings.Index(system, "You are smidja"); i > strings.Index(system, "[project instructions]") {
		t.Error("project instructions must come after the base prompt")
	}
}

func TestGlobalInstructionsAppendedAfterProject(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("project rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".smidja"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".smidja", "AGENTS.md"), []byte("global rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Home = func() string { return home }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = client
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	system := client.reqs[0].System
	if !strings.Contains(system, "[user instructions]") || !strings.Contains(system, "global rules") {
		t.Errorf("system prompt missing the global section:\n%s", system)
	}
	if i := strings.Index(system, "[project instructions]"); i > strings.Index(system, "[user instructions]") {
		t.Error("global instructions must come after the project section")
	}
}

func TestAgentsContentNotExposedAsCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("/help\n/quit\n")
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	deps.Bundle = sdk.Bundle{
		ID: "digitalygo",
		FS: fstest.MapFS{
			"content/skills/quick.md":   {Data: []byte("# quick")},
			"content/agents/planner.md": {Data: []byte("# planner")},
		},
	}
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "/skill - inject a skill into the conversation") {
		t.Errorf("help missing the skill command:\n%s", out)
	}
	if strings.Contains(out, "/agents") {
		t.Errorf("agents must not be exposed as a command:\n%s", out)
	}
}

func TestWorkspaceSkillLoadedIntoCatalog(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".smidja", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".smidja", "skills", "project.md"), []byte("project skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = client
	deps.Stdin = strings.NewReader("/skill project\n/quit\n")
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	injected := client.lastUserText()
	if !strings.Contains(injected, "[skill workspace/project]") {
		t.Errorf("injected user message = %q, want the workspace skill", injected)
	}
	if !strings.Contains(injected, "project skill") {
		t.Errorf("injected user message = %q, want the skill content", injected)
	}
}

func TestModelsCatalogMergedIntoRegistry(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "models", "testdata", "pi-models-sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixture"`)
		w.WriteHeader(http.StatusOK)
		w.Write(fixture)
	}))
	defer srv.Close()

	reg := models.NewRegistry()
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.ModelRegistry = reg
	deps.ModelsCatalog = &models.CatalogSource{BaseURL: srv.URL}
	deps.Config.SessionDir = filepath.Join(t.TempDir(), "sessions")

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	m, ok := reg.Get("anthropic/claude-fable-5")
	if !ok || m.ContextWindow != 1_000_000 || m.Provider != "anthropic" {
		t.Errorf("unified catalog model missing from registry: %+v ok=%v", m, ok)
	}
	if _, ok := reg.Get("openai/gpt-4.1"); !ok {
		t.Error("second provider model missing from registry")
	}
	if _, ok := reg.Get("openai/gpt-5"); !ok {
		t.Error("built-in fallback must survive the unified merge")
	}
}

func TestLocalOverridesBeatStoreInRegistry(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "models", "testdata", "pi-models-sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixture"`)
		w.WriteHeader(http.StatusOK)
		w.Write(fixture)
	}))
	defer srv.Close()

	reg := models.NewRegistry()
	cwd := t.TempDir()
	wsModels := filepath.Join(cwd, ".smidja", "models.json")
	if err := os.MkdirAll(filepath.Dir(wsModels), 0o755); err != nil {
		t.Fatal(err)
	}
	overrides := `{"anthropic":{"claude-fable-5":{"id":"claude-fable-5","provider":"anthropic","contextWindow":555}}}`
	if err := os.WriteFile(wsModels, []byte(overrides), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.ModelRegistry = reg
	deps.ModelsCatalog = &models.CatalogSource{BaseURL: srv.URL}
	deps.Config.SessionDir = filepath.Join(t.TempDir(), "sessions")

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	m, ok := reg.Get("anthropic/claude-fable-5")
	if !ok || m.ContextWindow != 555 {
		t.Errorf("local override must beat the store, got %+v ok=%v", m, ok)
	}
}

func TestCorruptPackagesIndexFailsChat(t *testing.T) {
	pkgRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pkgRoot, ".staging"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "index.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(pkgRoot)
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)

	if err := RunWithDeps(nil, deps); err == nil {
		t.Fatalf("RunWithDeps with a corrupt packages index succeeded (stderr %q)", stderr.String())
	}
	if !strings.Contains(stderr.String(), "read index") {
		t.Errorf("stderr = %q, want the corrupt-index error", stderr.String())
	}
}

func TestRunVersionOffline(t *testing.T) {
	t.Setenv("SMIDJA_OFFLINE", "1")
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()
	runErr := Run([]string{"-version"})
	w.Close()
	io.Copy(io.Discard, r)
	r.Close()
	if runErr != nil {
		t.Fatalf("Run(-version): %v", runErr)
	}
}

func TestOfflineSkipsModelsNetwork(t *testing.T) {
	t.Setenv("SMIDJA_OFFLINE", "1")
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()

	reg := models.NewRegistry()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps.Stdin = strings.NewReader("hello\n/quit\n")
	cwd := t.TempDir()
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.ModelRegistry = reg
	deps.ModelsCatalog = &models.CatalogSource{BaseURL: srv.URL}
	deps.Config.SessionDir = filepath.Join(t.TempDir(), "sessions")

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if hit {
		t.Error("offline mode still hit the models endpoint")
	}
	if _, ok := reg.Get("anthropic/claude-fable-5"); ok {
		t.Error("offline mode must not add remote models")
	}
	if d := reg.Default(); d.ID != models.DefaultModelID {
		t.Errorf("built-in default = %q, want %q", d.ID, models.DefaultModelID)
	}
}
