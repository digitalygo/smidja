package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/sdk"
)

var errFlagsGhostSetup = errors.New("ghost setup failed")

type r3AgentTool struct {
	name  string
	calls int
}

func (t *r3AgentTool) Name() string {
	if t.name == "" {
		return "r3-setup-tool"
	}
	return t.name
}
func (t *r3AgentTool) Description() string { return "r3 setup tool" }
func (t *r3AgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *r3AgentTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	t.calls++
	return agent.Result{Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "done"}}}
}

type flagsProbeExtension struct {
	mu       sync.Mutex
	setupErr error
	seen     []map[string]any
	contexts int
	counter  *int
}

func (e *flagsProbeExtension) ID() string { return "flags-probe" }

func (e *flagsProbeExtension) Setup(api sdk.API) error {
	if e.counter != nil {
		*e.counter++
	}
	if err := api.RegisterFlag("ext-text", sdk.FlagOptions{Type: "string", Default: "fallback", Description: "text flag"}); err != nil {
		return err
	}
	if err := api.RegisterFlag("ext-bool", sdk.FlagOptions{Type: "boolean", Description: "bool flag"}); err != nil {
		return err
	}
	return e.setupErr
}

func (e *flagsProbeExtension) RegisterLLMHooks(r sdk.LLMHookRegistry) {
	r.OnContext(func(ctx sdk.HandlerContext, ev sdk.ContextEvent) (*sdk.ContextEventResult, error) {
		e.mu.Lock()
		e.contexts++
		flags := ctx.Flags()
		clone := make(map[string]any, len(flags))
		for key, value := range flags {
			clone[key] = value
		}
		e.seen = append(e.seen, clone)
		e.mu.Unlock()
		return nil, nil
	})
}

func (e *flagsProbeExtension) lastFlags() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seen) == 0 {
		return nil
	}
	return e.seen[len(e.seen)-1]
}

func runFlagsPrompt(t *testing.T, args []string, ext sdk.Extension, script ...*agent.AssistantMessage) (*Deps, *bytes.Buffer, error) {
	t.Helper()
	cwd := t.TempDir()
	client := &capturingClient{script: script}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if ext != nil {
		registry := extensions.NewRegistry()
		if err := registry.Register(ext); err != nil {
			t.Fatal(err)
		}
		deps.ExtensionRuntime = extensions.NewRuntime(registry)
	}
	err := RunWithDeps(args, deps)
	return deps, &stdout, err
}

func TestExtensionFlagsParseAtRootAndRun(t *testing.T) {
	root := &flagsProbeExtension{}
	if _, _, err := runFlagsPrompt(t, []string{"-ext-text=hello", "-ext-bool", "-p", "hi"}, root, textStop("ok")); err != nil {
		t.Fatalf("root RunWithDeps: %v", err)
	}
	if got := root.lastFlags(); got["ext-text"] != "hello" || got["ext-bool"] != true {
		t.Fatalf("root flags = %#v", got)
	}

	defaults := &flagsProbeExtension{}
	if _, _, err := runFlagsPrompt(t, []string{"-p", "hi"}, defaults, textStop("ok")); err != nil {
		t.Fatalf("default RunWithDeps: %v", err)
	}
	if got := defaults.lastFlags(); got["ext-text"] != "fallback" || got["ext-bool"] != false {
		t.Fatalf("default flags = %#v", got)
	}

	run := &flagsProbeExtension{}
	if _, _, err := runFlagsPrompt(t, []string{"run", "-p", "hi", "--ext-text", "spaced value", "--ext-bool=false"}, run, textStop("ok")); err != nil {
		t.Fatalf("run RunWithDeps: %v", err)
	}
	if got := run.lastFlags(); got["ext-text"] != "spaced value" || got["ext-bool"] != false {
		t.Fatalf("run flags = %#v", got)
	}

	positional := &flagsProbeExtension{}
	if _, _, err := runFlagsPrompt(t, []string{"run", "positional prompt", "-ext-bool"}, positional, textStop("ok")); err != nil {
		t.Fatalf("positional RunWithDeps: %v", err)
	}
	if got := positional.lastFlags(); got["ext-bool"] != true {
		t.Fatalf("positional flags = %#v", got)
	}
}

func TestExtensionFlagParserErrorsStayStandard(t *testing.T) {
	probe := &flagsProbeExtension{}
	newDeps := func(t *testing.T) *Deps {
		t.Helper()
		cwd := t.TempDir()
		var stdout, stderr bytes.Buffer
		deps := wiringTestDeps(t.TempDir())
		deps.Getwd = func() (string, error) { return cwd, nil }
		deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
		deps.Config = testConfig(t, cwd)
		deps.Store = wiringStore(t)
		deps.Stdout = &stdout
		deps.Stderr = &stderr
		registry := extensions.NewRegistry()
		if err := registry.Register(probe); err != nil {
			t.Fatal(err)
		}
		deps.ExtensionRuntime = extensions.NewRuntime(registry)
		return deps
	}

	err := RunWithDeps([]string{"-ext-unknown", "x", "-p", "hi"}, newDeps(t))
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("unknown flag error = %v", err)
	}
	err = RunWithDeps([]string{"-ext-bool=not-a-bool", "-p", "hi"}, newDeps(t))
	if err == nil {
		t.Fatal("malformed boolean must fail")
	}
	deps := newDeps(t)
	err = RunWithDeps([]string{"-version"}, deps)
	if err != nil || !strings.Contains(deps.Stdout.(*bytes.Buffer).String(), "smidja ") {
		t.Fatalf("version error = %v", err)
	}
}

func TestExtensionFlagCoreCollisionAndFailedSetupGhosts(t *testing.T) {
	newDeps := func(t *testing.T) (*Deps, *capturingClient) {
		t.Helper()
		cwd := t.TempDir()
		client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
		var stdout, stderr bytes.Buffer
		deps := wiringTestDeps(t.TempDir())
		deps.Getwd = func() (string, error) { return cwd, nil }
		deps.Client = client
		deps.Config = testConfig(t, cwd)
		deps.Store = wiringStore(t)
		deps.Stdout = &stdout
		deps.Stderr = &stderr
		collision := &hostHookExtension{id: "collision", setupFn: func(api sdk.API) error {
			return api.RegisterFlag("model", sdk.FlagOptions{Type: "string"})
		}}
		ghost := &hostHookExtension{id: "ghost", setupFn: func(api sdk.API) error {
			if err := api.RegisterFlag("ghost-flag", sdk.FlagOptions{Type: "string"}); err != nil {
				return err
			}
			return errFlagsGhostSetup
		}}
		registry := extensions.NewRegistry()
		if err := registry.Register(collision); err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(ghost); err != nil {
			t.Fatal(err)
		}
		deps.ExtensionRuntime = extensions.NewRuntime(registry)
		return deps, client
	}

	err := RunWithDeps([]string{"-ghost-flag", "x", "-p", "hi"}, mustDeps(t, newDeps))
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("ghost flag error = %v", err)
	}
	deps, client := newDeps(t)
	err = RunWithDeps([]string{"-model", "test/model", "-p", "hi"}, deps)
	if err != nil {
		t.Fatalf("core model flag failed after collision: %v", err)
	}
	if client.calls == 0 {
		t.Fatal("core flag path never reached the model")
	}
}

func mustDeps(t *testing.T, build func(*testing.T) (*Deps, *capturingClient)) *Deps {
	t.Helper()
	deps, _ := build(t)
	return deps
}

func TestExtensionSetupRunsExactlyOnceWithContributions(t *testing.T) {
	cwd := t.TempDir()
	count := 0
	apiRef := (*sdk.API)(nil)
	probe := &r3AgentTool{}
	ext := &hostHookExtension{
		id: "once",
		setupFn: func(api sdk.API) error {
			count++
			local := api
			apiRef = &local
			if err := api.RegisterFlag("once-flag", sdk.FlagOptions{Type: "boolean"}); err != nil {
				return err
			}
			if err := api.RegisterCommand("r3cmd", sdk.Command{Description: "r3 command"}); err != nil {
				return err
			}
			if registration, ok := api.(sdk.UIRegistrationAPI); ok {
				return registration.RegisterWidget("r3.widget", func() sdk.Component { return nil })
			}
			return nil
		},
	}
	client := &capturingClient{script: []*agent.AssistantMessage{
		toolUse("call_1", "r3-setup-tool", `{}`),
		textStop("done"),
	}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Tools = []agent.Tool{probe}
	registry := extensions.NewRegistry()
	if err := registry.Register(ext); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)

	if err := RunWithDeps([]string{"-once-flag", "-p", "hi"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if count != 1 {
		t.Fatalf("setup runs = %d, want 1", count)
	}
	if probe.calls != 1 {
		t.Fatalf("extension tool calls = %d, want 1", probe.calls)
	}
	if apiRef == nil {
		t.Fatal("setup did not receive the API")
	}
	uiRegistry := extensions.UIRegistryOf(*apiRef)
	if uiRegistry == nil {
		t.Fatal("setup API lost the UI registry")
	}
	if keys := uiRegistry.WidgetKeys(); len(keys) != 1 || keys[0] != "r3.widget" {
		t.Fatalf("widget keys = %v", keys)
	}
	if flags := (*apiRef).Flags(); flags["once-flag"] != true {
		t.Fatalf("setup flag values = %#v", flags)
	}
	if list := (*apiRef).Commands(); len(list) == 0 {
		t.Fatal("setup command was discarded")
	}
}

func TestExtensionSubcommandsDoNotRunSetup(t *testing.T) {
	cwd := t.TempDir()
	count := 0
	ext := &flagsProbeExtension{counter: &count}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	registry := extensions.NewRegistry()
	if err := registry.Register(ext); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)

	if err := RunWithDeps([]string{"version"}, deps); err != nil {
		t.Fatalf("version subcommand: %v", err)
	}
	if count != 0 {
		t.Fatalf("subcommand ran extension setup %d time(s)", count)
	}
	if !strings.Contains(stdout.String(), "smidja") {
		t.Fatalf("version output = %q", stdout.String())
	}
}

func TestExtensionHelpStaysStandard(t *testing.T) {
	cwd := t.TempDir()
	ext := &flagsProbeExtension{}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	registry := extensions.NewRegistry()
	if err := registry.Register(ext); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)

	if err := RunWithDeps([]string{"-h"}, deps); err != nil {
		t.Fatalf("-h error = %v", err)
	}
	if !strings.Contains(stderr.String(), "usage: smidja") {
		t.Fatalf("-h output = %q", stderr.String())
	}

	runDeps := wiringTestDeps(t.TempDir())
	runDeps.Getwd = func() (string, error) { return cwd, nil }
	runDeps.Config = testConfig(t, cwd)
	runDeps.Store = wiringStore(t)
	runDeps.Stdout = &stdout
	runDeps.Stderr = &stderr
	if err := RunWithDeps([]string{"run", "-h"}, runDeps); err != nil {
		t.Fatalf("run -h error = %v", err)
	}
}

func TestExtensionToolOverridesCoreToolAtSetup(t *testing.T) {
	cwd := t.TempDir()
	core := &r3AgentTool{name: "dup"}
	sdkProbe := &sdkToolProbe{name: "dup"}
	ext := &hostHookExtension{
		id: "override-tool",
		setupFn: func(api sdk.API) error {
			return api.RegisterTool(sdkProbe)
		},
	}
	client := &capturingClient{script: []*agent.AssistantMessage{
		toolUse("call_1", "dup", `{}`),
		textStop("done"),
	}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Tools = []agent.Tool{core}
	registry := extensions.NewRegistry()
	if err := registry.Register(ext); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)

	if err := RunWithDeps([]string{"-p", "hi"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if sdkProbe.calls != 1 || core.calls != 0 {
		t.Fatalf("tool calls sdk=%d core=%d, want the setup tool to win", sdkProbe.calls, core.calls)
	}
}

func TestExtensionBootstrapDoubleStartFailsPrecisely(t *testing.T) {
	newDeps := func(t *testing.T) *Deps {
		t.Helper()
		cwd := t.TempDir()
		deps := wiringTestDeps(t.TempDir())
		deps.Getwd = func() (string, error) { return cwd, nil }
		deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
		deps.Config = testConfig(t, cwd)
		deps.Store = wiringStore(t)
		var stdout, stderr bytes.Buffer
		deps.Stdout = &stdout
		deps.Stderr = &stderr
		registry := extensions.NewRegistry()
		if err := registry.Register(&flagsProbeExtension{}); err != nil {
			t.Fatal(err)
		}
		deps.ExtensionRuntime = extensions.NewRuntime(registry)
		return deps
	}

	root := newDeps(t)
	if err := RunWithDeps([]string{"-version"}, root); err != nil {
		t.Fatalf("first root invocation: %v", err)
	}
	if err := RunWithDeps([]string{"-version"}, root); err == nil || !strings.Contains(err.Error(), "setup already run") {
		t.Fatalf("second root invocation error = %v, want setup already run", err)
	}

	run := newDeps(t)
	if err := RunWithDeps([]string{"run", "-p", "hi"}, run); err != nil {
		t.Fatalf("first run invocation: %v", err)
	}
	if err := RunWithDeps([]string{"run", "-p", "hi"}, run); err == nil || !strings.Contains(err.Error(), "setup already run") {
		t.Fatalf("second run invocation error = %v, want setup already run", err)
	}
}

func TestExtensionSetupRunOnlyActionsFailClosed(t *testing.T) {
	cwd := t.TempDir()
	var (
		modelErr    error
		thinkingErr error
		emitErr     error
	)
	ext := &hostHookExtension{
		id: "setup-actions",
		setupFn: func(api sdk.API) error {
			modelErr = api.SetModel(sdk.Model{ID: "test/model"})
			thinkingErr = api.SetThinkingLevel(sdk.ThinkingHigh)
			emitErr = api.EmitCustomEvent("setup.event", nil)
			return nil
		},
	}
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	var stdout, stderr bytes.Buffer
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	registry := extensions.NewRegistry()
	if err := registry.Register(ext); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)
	if err := RunWithDeps([]string{"-p", "hi"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if modelErr != errHostClosed || thinkingErr != errHostClosed {
		t.Fatalf("setup model/thinking errors = %v, %v; want errHostClosed", modelErr, thinkingErr)
	}
	if emitErr != nil {
		t.Fatalf("setup emit on a ready bus = %v", emitErr)
	}
}
