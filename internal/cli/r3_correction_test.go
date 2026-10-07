package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func TestSetupRunOnlyActionsFailClosedOnEarlyExit(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"help", []string{"-h"}, false},
		{"unknown flag", []string{"-r3-unknown-flag"}, true},
		{"version", []string{"-version"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "exec-marker")
			var (
				execErr error
				apiRef  sdk.API
			)
			ext := &hostHookExtension{
				id: "early-exec",
				setupFn: func(api sdk.API) error {
					apiRef = api
					_, execErr = api.Exec("/bin/sh", []string{"-c", "touch " + marker}, sdk.ExecOptions{})
					return nil
				},
			}
			deps := wiringTestDeps(t.TempDir())
			home := t.TempDir()
			deps.Getwd = func() (string, error) { return dir, nil }
			deps.Home = func() string { return home }
			deps.Config = testConfig(t, dir)
			deps.Store = wiringStore(t)
			registry := extensions.NewRegistry()
			if err := registry.Register(ext); err != nil {
				t.Fatal(err)
			}
			deps.ExtensionRuntime = extensions.NewRuntime(registry)
			err := RunWithDeps(tc.args, deps)
			if tc.wantErr && err == nil {
				t.Fatal("malformed arguments must fail")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("RunWithDeps: %v", err)
			}
			if execErr == nil {
				t.Fatal("Setup Exec must fail closed before the host is ready")
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("Setup Exec created %s (stat err %v)", marker, statErr)
			}
			if apiRef == nil {
				t.Fatal("setup did not capture the API")
			}
			if err := apiRef.EmitCustomEvent("r3.early", nil); !errors.Is(err, extensions.ErrCustomEventClosed) {
				t.Fatalf("early return left the event bus open: %v", err)
			}
			if _, err := apiRef.Exec("/bin/sh", []string{"-c", "true"}, sdk.ExecOptions{}); err == nil {
				t.Fatal("closed host must refuse Exec")
			}
			if _, statErr := os.Stat(filepath.Join(home, ".smidja", "auth.json")); !os.IsNotExist(statErr) {
				t.Fatalf("early return wrote a credential store (stat err %v)", statErr)
			}
		})
	}
}

func TestSetupExecBecomesAvailableAfterValidatedHost(t *testing.T) {
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "hook-marker")
	var (
		setupErr error
		hookErr  error
		hookDone bool
	)
	ext := &hostHookExtension{
		id: "late-exec",
		setupFn: func(api sdk.API) error {
			_, setupErr = api.Exec("/bin/sh", []string{"-c", "touch " + filepath.Join(cwd, "setup-marker")}, sdk.ExecOptions{})
			return nil
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			_, hookErr = ctx.Exec("/bin/sh", []string{"-c", "touch " + marker}, sdk.ExecOptions{})
			hookDone = true
			return nil, nil
		},
	}
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps := r3ReplDeps(t, cwd, client, r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"}), "first\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if setupErr == nil {
		t.Fatal("Setup Exec must fail before the host is ready")
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "setup-marker")); !os.IsNotExist(statErr) {
		t.Fatal("Setup Exec created a file before the host was ready")
	}
	if !hookDone || hookErr != nil {
		t.Fatalf("post-boundary hook Exec = %v done=%v", hookErr, hookDone)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("post-boundary Exec did not run: %v", statErr)
	}
}

func TestMalformedUserConfigDoesNotBlockHelpAndVersion(t *testing.T) {
	newDeps := func(t *testing.T) *Deps {
		t.Helper()
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".smidja"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".smidja", "settings.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		deps := wiringTestDeps(t.TempDir())
		deps.Getwd = func() (string, error) { return t.TempDir(), nil }
		deps.Home = func() string { return home }
		deps.Store = wiringStore(t)
		deps.Stderr = &strings.Builder{}
		return deps
	}
	helpDeps := newDeps(t)
	if err := RunWithDeps([]string{"-h"}, helpDeps); err != nil {
		t.Fatalf("help must ignore malformed user config: %v", err)
	}
	if !strings.Contains(helpDeps.Stderr.(*strings.Builder).String(), "usage: smidja") {
		t.Fatalf("help output = %q", helpDeps.Stderr.(*strings.Builder).String())
	}
	versionDeps := newDeps(t)
	versionDeps.Stdout = &strings.Builder{}
	if err := RunWithDeps([]string{"-version"}, versionDeps); err != nil {
		t.Fatalf("version must ignore malformed user config: %v", err)
	}
	if !strings.Contains(versionDeps.Stdout.(*strings.Builder).String(), "smidja ") {
		t.Fatalf("version output = %q", versionDeps.Stdout.(*strings.Builder).String())
	}
	runDeps := newDeps(t)
	runDeps.Stdout = &strings.Builder{}
	err := RunWithDeps([]string{"-p", "hi"}, runDeps)
	if err == nil || !strings.Contains(err.Error(), "settings") {
		t.Fatalf("run with malformed user config error = %v", err)
	}
}

func r3CorrectionHost(t *testing.T, cwd string) (*hostRuntime, *session.Session, *models.Registry, string) {
	t.Helper()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter"},
		models.ModelInfo{ID: "model-b", ContextWindow: 8192, Provider: "openrouter"},
	)
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.setModel(registry, "model-a", "model-a", "openrouter")
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	return host, sess, registry, store.Root()
}

func TestApplyPendingModelAttachesPreparerWindowAndClient(t *testing.T) {
	cwd := t.TempDir()
	host, _, registry, _ := r3CorrectionHost(t, cwd)
	oldPreparer := testPreparer(t)
	host.attachPreparer(oldPreparer)
	newPreparer, err := newModelPreparer(config.Config{Model: "model-b", ContextEnabled: true}, registry, "model-b", "model-b-wire", nil)
	if err != nil {
		t.Fatal(err)
	}
	newClient := &capturingClient{}
	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return model + "-wire", "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return newPreparer, nil
		},
		buildClient: func(provider string) (agent.Client, bool, error) { return newClient, false, nil },
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			adopt(handle)
			return nil
		},
	})
	baseClient := &capturingClient{}
	rd := &runDeps{host: host, preparer: oldPreparer, client: baseClient, wireModel: "model-a-wire"}
	if err := host.requestModel(sdk.Model{ID: "model-b"}, host.snapshot()); err != nil {
		t.Fatalf("requestModel: %v", err)
	}
	if host.currentPreparer() != oldPreparer {
		t.Fatal("the preparer changed before the turn boundary")
	}
	rd.applyPendingModel()
	if host.currentPreparer() != newPreparer {
		t.Fatal("the boundary did not attach the new preparer for the next compaction")
	}
	if rd.preparer != newPreparer || rd.wireModel != "model-b-wire" || rd.client != newClient {
		t.Fatalf("boundary state = preparer %v wire %q client %T", rd.preparer == newPreparer, rd.wireModel, rd.client)
	}
	if usage := host.contextUsage(); usage == nil || usage.ContextWindow != newPreparer.contextWindow {
		t.Fatalf("context usage window = %+v, want %d", usage, newPreparer.contextWindow)
	}
	if model := host.currentModel(); model == nil || model.ID != "model-b" || model.Provider != "openrouter" {
		t.Fatalf("host model = %+v", model)
	}
}

func TestRequestModelTransactionRacesSessionSwitch(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter"},
		models.ModelInfo{ID: "model-b", ContextWindow: 8192, Provider: "openrouter"},
	)
	newHost := func(t *testing.T) (*hostRuntime, *session.Session) {
		t.Helper()
		sess, err := store.Create(cwd)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { sess.Close() })
		host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
		host.setModelRegistry(registry)
		host.setModel(registry, "model-a", "model-a", "openrouter")
		host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
		return host, sess
	}
	bindRacing := func(t *testing.T, host *hostRuntime) (chan struct{}, chan error) {
		t.Helper()
		built := make(chan struct{})
		release := make(chan struct{})
		preparer := testPreparer(t)
		host.bindModelBindings(hostModelBindings{
			resolveWire: func(model string) (string, string, bool) { return model, "openrouter", true },
			buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
				close(built)
				<-release
				return preparer, nil
			},
			persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
				return host.commitSession(handle, func(sess *session.Session) error {
					return sess.AppendEntry(&session.CustomEntry{CustomType: "r3.race.marker", Data: json.RawMessage(`"` + intent.model + `"`)})
				}, adopt)
			},
		})
		done := make(chan error, 1)
		expected := host.snapshot()
		go func() { done <- host.requestModel(sdk.Model{ID: "model-b"}, expected) }()
		<-built
		return release, done
	}
	t.Run("session switch wins", func(t *testing.T) {
		host, sessA := newHost(t)
		release, done := bindRacing(t, host)
		sessB, err := store.Create(cwd)
		if err != nil {
			t.Fatal(err)
		}
		defer sessB.Close()
		host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "")
		close(release)
		if err := <-done; !errors.Is(err, errHostStaleSession) {
			t.Fatalf("error = %v, want errHostStaleSession", err)
		}
		if host.takePendingModel() != nil {
			t.Fatal("stale model change queued a pending intent")
		}
		if raw := sessionFileString(t, sessA.Path()); strings.Contains(raw, "r3.race.marker") {
			t.Fatalf("stale transaction wrote metadata to the old session: %s", raw)
		}
	})
	t.Run("transaction wins", func(t *testing.T) {
		host, sessA := newHost(t)
		release, done := bindRacing(t, host)
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("requestModel: %v", err)
		}
		if raw := sessionFileString(t, sessA.Path()); !strings.Contains(raw, "r3.race.marker") {
			t.Fatalf("accepted transaction did not reach the session: %s", raw)
		}
		sessB, err := store.Create(cwd)
		if err != nil {
			t.Fatal(err)
		}
		defer sessB.Close()
		host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "")
		if host.takePendingModel() != nil {
			t.Fatal("session switch kept a stale model intent")
		}
	})
	t.Run("shutdown wins", func(t *testing.T) {
		host, sessA := newHost(t)
		release, done := bindRacing(t, host)
		host.shutdown()
		close(release)
		if err := <-done; !errors.Is(err, errHostClosed) && !errors.Is(err, errHostStaleSession) {
			t.Fatalf("closed host error = %v", err)
		}
		if host.takePendingModel() != nil {
			t.Fatal("closed host queued a pending intent")
		}
		if raw := sessionFileString(t, sessA.Path()); strings.Contains(raw, "r3.race.marker") {
			t.Fatalf("shutdown race wrote metadata: %s", raw)
		}
	})
}

func TestRequestModelPersisterRespectsSessionSwitch(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter"},
		models.ModelInfo{ID: "model-b", ContextWindow: 8192, Provider: "openrouter"},
	)
	cfg := config.Config{Model: "model-a", WorkspaceRoot: cwd}
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.setModel(registry, "model-a", "model-a", "openrouter")
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "")
	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return model, "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return testPreparer(t), nil
		},
		persist: newHostModelPersister(host, &cfg, "sys", extensions.NewToolCatalog(), nil, cwd, func() string { return "fp" }),
	})
	staleCtx := host.context()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "")
	if err := staleCtx.SetModel(sdk.Model{ID: "model-b"}); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("stale persister error = %v", err)
	}
	for _, entry := range sessionEntriesAt(t, sessA.Path()) {
		if custom, ok := entry.(*session.CustomEntry); ok && custom.CustomType == session.RuntimeProfileCustomType {
			var profile session.RuntimeProfile
			if err := json.Unmarshal(custom.Data, &profile); err == nil && profile.ModelID == "model-b" {
				t.Fatal("stale transaction persisted the new profile")
			}
		}
	}
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "")
	if err := host.requestModel(sdk.Model{ID: "model-b"}, host.snapshot()); err != nil {
		t.Fatalf("requestModel: %v", err)
	}
	profile, ok := sessionProfileFromEntries(sessionEntriesAt(t, sessA.Path()))
	if !ok || profile.ModelID != "model-b" {
		t.Fatalf("persisted profile = %+v ok=%v", profile, ok)
	}
}

func sessionEntriesAt(t *testing.T, path string) []session.Entry {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	return loader.Entries()
}

func TestThinkingDefaultResetsToTruthfulState(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	registry := r3ModelRegistry(t, models.ModelInfo{
		ID:            "model-a",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.setModel(registry, "model-a", "model-a", "openrouter")
	host.setReasoningSeam(true)
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	ctx := host.context()
	if err := ctx.SetThinkingLevel(sdk.ThinkingHigh); err != nil {
		t.Fatalf("SetThinkingLevel(high): %v", err)
	}
	if err := ctx.SetThinkingLevel(sdk.ThinkingDefault); err != nil {
		t.Fatalf("SetThinkingLevel(default): %v", err)
	}
	if got := host.currentThinking(); got != sdk.ThinkingDefault {
		t.Fatalf("thinking = %q, want default", got)
	}
	host.mu.Lock()
	set := host.thinkingSet
	host.mu.Unlock()
	if set {
		t.Fatal("provider default must not look explicitly set")
	}
	last := sdk.ThinkingLevel("")
	for _, entry := range sessionEntriesAt(t, sess.Path()) {
		if typed, ok := entry.(*session.ThinkingLevelChangeEntry); ok {
			last = sdk.ThinkingLevel(typed.ThinkingLevel)
		}
	}
	if last != sdk.ThinkingDefault {
		t.Fatalf("last thinking entry = %q, want default", last)
	}
	if directive, ok := host.reasoningDirective(); ok {
		t.Fatalf("provider default must omit the wire directive: %+v", directive)
	}
}

func TestRequestThinkingTransactionGuardsStaleSession(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	registry := r3ModelRegistry(t, models.ModelInfo{
		ID:            "model-a",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.setModel(registry, "model-a", "model-a", "openrouter")
	host.setReasoningSeam(true)
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "")
	ctxA := host.context()
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "")
	if err := ctxA.SetThinkingLevel(sdk.ThinkingHigh); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("stale thinking error = %v", err)
	}
	if raw := sessionFileString(t, sessA.Path()); strings.Contains(raw, "high") {
		t.Fatalf("stale thinking change was persisted: %s", raw)
	}
	if got := host.currentThinking(); got != sdk.ThinkingDefault {
		t.Fatalf("stale thinking change mutated the host: %q", got)
	}
}

func sessionFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func TestHostModelPersisterGuardsAndResetPersistence(t *testing.T) {
	cwd := t.TempDir()
	host, sess, _, _ := r3CorrectionHost(t, cwd)
	cfg := config.Config{Model: "model-a", WorkspaceRoot: cwd}
	persist := newHostModelPersister(host, &cfg, "sys", extensions.NewToolCatalog(), nil, cwd, func() string { return "fp" })
	if err := persist(nil, &hostModelIntent{model: "model-b"}, nil); !errors.Is(err, errHostClosed) {
		t.Fatalf("nil handle error = %v", err)
	}
	if err := persist(host.snapshot(), nil, nil); !errors.Is(err, errHostClosed) {
		t.Fatalf("nil intent error = %v", err)
	}
	adopted := false
	err := persist(host.snapshot(), &hostModelIntent{model: "model-b", provider: "openrouter", resetThinking: true}, func(*hostSessionHandle) {
		adopted = true
	})
	if err != nil || !adopted {
		t.Fatalf("persist reset = %v adopted=%v", err, adopted)
	}
	last := sdk.ThinkingLevel("")
	for _, entry := range sessionEntriesAt(t, sess.Path()) {
		if typed, ok := entry.(*session.ThinkingLevelChangeEntry); ok {
			last = sdk.ThinkingLevel(typed.ThinkingLevel)
		}
	}
	if last != sdk.ThinkingDefault {
		t.Fatalf("reset entry = %q, want default", last)
	}
}

func TestBootstrapHelpersGuardEdges(t *testing.T) {
	var bootstrap *extensionBootstrap
	bootstrap.close()
	fallback := fallbackChatConfig(&Deps{Getwd: func() (string, error) { return "", errors.New("no cwd") }})
	if fallback.WorkspaceRoot != "." {
		t.Fatalf("fallback workspace root = %q", fallback.WorkspaceRoot)
	}
	fallback = fallbackChatConfig(&Deps{Getwd: func() (string, error) { return "", nil }})
	if fallback.WorkspaceRoot != "." {
		t.Fatalf("empty cwd fallback = %q", fallback.WorkspaceRoot)
	}
}
