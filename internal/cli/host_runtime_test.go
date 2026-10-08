package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/subagent"
	"github.com/digitalygo/smidja/sdk"
)

type hostHookExtension struct {
	id string

	mu            sync.Mutex
	contexts      []sdk.HandlerContext
	contextCalls  int
	contextFn     func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error)
	startFn       func(ctx sdk.HandlerContext) error
	toolResultFn  func(ctx sdk.HandlerContext, name string) (*sdk.ToolResultEventResult, error)
	setupFn       func(api sdk.API) error
	entryRenderer sdk.EntryRenderer
}

func (e *hostHookExtension) ID() string {
	if e.id == "" {
		return "host-hooks"
	}
	return e.id
}

func (e *hostHookExtension) Setup(api sdk.API) error {
	if e.setupFn != nil {
		return e.setupFn(api)
	}
	if e.entryRenderer != nil {
		registration, ok := api.(sdk.UIRegistrationAPI)
		if !ok {
			return nil
		}
		return registration.RegisterEntryRenderer("note", e.entryRenderer)
	}
	return nil
}

func (e *hostHookExtension) RegisterLLMHooks(r sdk.LLMHookRegistry) {
	if e.contextFn != nil || e.contexts != nil {
		r.OnContext(func(ctx sdk.HandlerContext, ev sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			e.mu.Lock()
			call := e.contextCalls
			e.contextCalls++
			e.contexts = append(e.contexts, ctx)
			e.mu.Unlock()
			if e.contextFn != nil {
				return e.contextFn(call, ctx)
			}
			return nil, nil
		})
	}
}

func (e *hostHookExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	if e.startFn == nil {
		return
	}
	r.OnSessionStart(func(ctx sdk.HandlerContext, _ sdk.SessionStartEvent) error {
		return e.startFn(ctx)
	})
}

func (e *hostHookExtension) RegisterToolHooks(r sdk.ToolHookRegistry) {
	if e.toolResultFn == nil {
		return
	}
	r.OnToolResult(func(ctx sdk.HandlerContext, ev sdk.ToolResultEvent) (*sdk.ToolResultEventResult, error) {
		return e.toolResultFn(ctx, ev.Name)
	})
}

func (e *hostHookExtension) contextAt(index int) sdk.HandlerContext {
	e.mu.Lock()
	defer e.mu.Unlock()
	if index < 0 || index >= len(e.contexts) {
		return nil
	}
	return e.contexts[index]
}

func (e *hostHookExtension) contextCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.contexts)
}

func runHostPrompt(t *testing.T, cwd string, ext sdk.Extension, script ...*agent.AssistantMessage) (*Deps, *capturingClient, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
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
	return deps, client, &stdout, &stderr
}

func firstSessionPath(t *testing.T, root string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatalf("no session file under %s", root)
	}
	return found
}

func TestComposedHookContextExposesRealState(t *testing.T) {
	cwd := t.TempDir()
	extension := &hostHookExtension{contexts: []sdk.HandlerContext{}}
	var (
		mode     sdk.Mode
		hasUI    bool
		gotCwd   string
		session  sdk.SessionView
		modelReg sdk.ModelRegistry
		model    *sdk.Model
		system   string
		usage    *sdk.ContextUsage
		signal   context.Context
		messages []sdk.Message
	)
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		mode = ctx.Mode()
		hasUI = ctx.HasUI()
		gotCwd = ctx.Cwd()
		session = ctx.SessionManager()
		modelReg = ctx.ModelRegistry()
		model = ctx.Model()
		system = ctx.SystemPrompt()
		usage = ctx.ContextUsage()
		signal = ctx.Signal()
		if session != nil {
			messages = session.Messages()
		}
		return nil, nil
	}
	deps, client, stdout, stderr := runHostPrompt(t, cwd, extension, textStop("done"))
	if err := RunWithDeps([]string{"-p", "hello context", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
	if extension.contextCount() != 1 {
		t.Fatalf("context hook calls = %d, want 1", extension.contextCount())
	}
	if mode != sdk.ModePrint || hasUI {
		t.Fatalf("mode = %q hasUI = %v, want print without UI", mode, hasUI)
	}
	if gotCwd != cwd {
		t.Fatalf("cwd = %q, want %q", gotCwd, cwd)
	}
	if session == nil {
		t.Fatal("session view is nil")
	}
	if session.ID() == "" || session.Path() == "" || session.Cwd() != cwd {
		t.Fatalf("session view = %q/%q/%q", session.ID(), session.Path(), session.Cwd())
	}
	if len(messages) != 1 || messages[0].Role != string(agent.RoleUser) {
		t.Fatalf("session messages = %+v, want the live user message", messages)
	}
	if len(messages[0].Content) != 1 || messages[0].Content[0].Text != "hello context" {
		t.Fatalf("user message content = %+v", messages[0].Content)
	}
	if modelReg == nil || model == nil {
		t.Fatal("model registry or current model is nil")
	}
	if model.ID != "test/model" {
		t.Fatalf("model ID = %q, want test/model", model.ID)
	}
	if modelReg.Model() == nil || modelReg.Model().ID != "test/model" {
		t.Fatalf("registry model = %+v", modelReg.Model())
	}
	available := modelReg.Available()
	if len(available) == 0 {
		t.Fatal("model registry has no available models")
	}
	if found, ok := modelReg.Find(available[0].Provider, available[0].ID); !ok || found.ID != available[0].ID {
		t.Fatalf("Find(%q, %q) = %+v, %v", available[0].Provider, available[0].ID, found, ok)
	}
	if _, ok := modelReg.Find("missing", "missing"); ok {
		t.Fatal("Find must not invent missing models")
	}
	if !strings.Contains(system, "smidja") {
		t.Fatalf("system prompt = %q, want the composed prompt", system)
	}
	if strings.Contains(system, "sk-test") || strings.Contains(system, "OPENROUTER") {
		t.Fatalf("system prompt leaks provider credentials: %q", system)
	}
	if usage == nil || usage.ContextWindow <= 0 {
		t.Fatalf("usage = %+v, want a context window", usage)
	}
	if signal == nil {
		t.Fatal("signal is nil")
	}
	if !strings.Contains(stdout.String(), "done") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestComposedHookContextSnapshotsAreIsolated(t *testing.T) {
	cwd := t.TempDir()
	probe := &sdkToolProbe{name: "ext-probe"}
	extension := &hostHookExtension{
		id: "snapshot-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				if session := ctx.SessionManager(); session != nil {
					messages := session.Messages()
					if len(messages) > 0 && len(messages[0].Content) > 0 {
						messages[0].Content[0].Text = "mutated"
					}
				}
				if reg := ctx.ModelRegistry(); reg != nil {
					if available := reg.Available(); len(available) > 0 {
						available[0].ID = "mutated"
					}
				}
				if model := ctx.Model(); model != nil {
					model.ID = "mutated"
				}
			}
			return nil, nil
		},
	}
	setup := &sdkSetupExtension{apiTool: probe}
	client := &capturingClient{script: []*agent.AssistantMessage{
		toolUse("call_1", "ext-probe", `{"x":1}`),
		textStop("done"),
	}}
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &bytes.Buffer{}
	deps.Stderr = &bytes.Buffer{}
	registry := extensions.NewRegistry()
	if err := registry.Register(extension); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(setup); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if extension.contextCount() != 2 {
		t.Fatalf("context calls = %d, want 2", extension.contextCount())
	}
	last := extension.contextAt(1)
	if last == nil {
		t.Fatal("missing second context")
	}
	if model := last.Model(); model == nil || model.ID != "test/model" {
		t.Fatalf("model after mutation = %+v, want the unmutated snapshot", model)
	}
	messages := last.SessionManager().Messages()
	if len(messages) == 0 || len(messages[0].Content) == 0 || messages[0].Content[0].Text == "mutated" {
		t.Fatalf("session messages leaked a mutation: %+v", messages)
	}
	available := last.ModelRegistry().Available()
	if len(available) == 0 || available[0].ID == "mutated" {
		t.Fatalf("available models leaked a mutation: %+v", available)
	}
}

func TestComposedSessionMetadataWritesRoundtrip(t *testing.T) {
	cwd := t.TempDir()
	extension := &hostHookExtension{
		id: "metadata-hooks",
		startFn: func(ctx sdk.HandlerContext) error {
			if err := ctx.AppendEntry("note", map[string]string{"kind": "startup"}); err != nil {
				return err
			}
			return ctx.SetSessionName("renamed session")
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call != 0 {
				return nil, nil
			}
			if name := ctx.SessionManager().Name(); name != "renamed session" {
				return nil, errors.New("later dispatch sees stale session name: " + name)
			}
			return nil, nil
		},
	}
	deps, _, _, stderr := runHostPrompt(t, cwd, extension, textStop("done"))
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	path := firstSessionPath(t, deps.Store.Root())
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if name := sessionDisplayName(loader); name != "renamed session" {
		t.Fatalf("session name = %q, want renamed session", name)
	}
	var found bool
	for _, entry := range loader.Entries() {
		custom, ok := entry.(*session.CustomEntry)
		if !ok || custom.CustomType != "note" {
			continue
		}
		found = true
		if string(custom.Data) != `{"kind":"startup"}` {
			t.Fatalf("custom entry data = %s", custom.Data)
		}
	}
	if !found {
		t.Fatal("custom entry was not persisted")
	}
	transcript, _ := projectTranscript(loader)
	var replayed bool
	for _, item := range transcript {
		if item.Kind == "notice" && strings.Contains(item.Text, "renamed session") {
			replayed = true
		}
	}
	if !replayed {
		t.Fatal("renamed session notice missing from the replay transcript")
	}
}

func TestLabelEntryWritesProjectedLabel(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello"`)}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(reopened, &sessionRecorder{reopened}, reopened.ID(), reopened.Path(), cwd, "")
	loader, err := session.LoadWithOptions(reopened.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	entries := loader.Entries()
	if len(entries) == 0 {
		t.Fatal("session has no entries")
	}
	target := session.EntryID(entries[0])
	if err := host.labelEntry(host.snapshot(), target, "reviewed"); err != nil {
		t.Fatalf("LabelEntry: %v", err)
	}
	reloaded, err := session.LoadWithOptions(reopened.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := buildTreeNodes(reloaded, false, false)
	for _, node := range nodes {
		if node.ID == target && node.Label == "reviewed" {
			return
		}
	}
	t.Fatalf("label for %s missing from the tree projection: %+v", target, nodes)
}

func TestHostSessionActionsRejectInvalidStaleAndClosed(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	first, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(first, &sessionRecorder{first}, first.ID(), first.Path(), cwd, "")
	stale := host.snapshot()
	host.bindSession(second, &sessionRecorder{second}, second.ID(), second.Path(), cwd, "")
	if err := host.appendEntry(stale, "note", map[string]int{"x": 1}); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("stale AppendEntry = %v, want errHostStaleSession", err)
	}
	if err := host.appendEntry(host.snapshot(), "", nil); !errors.Is(err, errHostCustomType) {
		t.Fatalf("empty custom type = %v, want errHostCustomType", err)
	}
	if err := host.appendEntry(host.snapshot(), "note", make(chan int)); err == nil {
		t.Fatal("unmarshalable data must fail")
	}
	if err := host.setSessionName(host.snapshot(), "  "); !errors.Is(err, errHostSessionName) {
		t.Fatalf("empty session name = %v, want errHostSessionName", err)
	}
	if err := host.labelEntry(host.snapshot(), " ", "tag"); !errors.Is(err, errHostEntryID) {
		t.Fatalf("empty entry id = %v, want errHostEntryID", err)
	}
	if err := host.setSessionName(host.snapshot(), "current"); err != nil {
		t.Fatalf("current SetSessionName: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := host.appendEntry(host.snapshot(), "note", nil); err == nil {
		t.Fatal("AppendEntry on a closed session must fail")
	}
}

func TestComposedExecThroughHook(t *testing.T) {
	workspace := t.TempDir()
	subdir := filepath.Join(workspace, "project")
	if err := os.MkdirAll(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SMIDJA_EXEC_TEST_SECRET", "hidden")
	t.Setenv("EXEC_VISIBLE", "visible")
	var result *sdk.ExecResult
	var pwd string
	var timeoutResult *sdk.ExecResult
	var execErr error
	extension := &hostHookExtension{
		id: "exec-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call != 0 {
				return nil, nil
			}
			result, execErr = ctx.Exec("/bin/sh", []string{"-c", `printf out; printf err >&2; printf "|%s|%s" "$SMIDJA_EXEC_TEST_SECRET" "$EXEC_VISIBLE"; exit 5`}, sdk.ExecOptions{})
			if _, err := ctx.Exec("", nil, sdk.ExecOptions{}); err == nil {
				return nil, errors.New("empty command must fail")
			}
			timeoutResult, execErr = ctx.Exec("/bin/sh", []string{"-c", "sleep 30"}, sdk.ExecOptions{Timeout: 150 * time.Millisecond})
			if execErr != nil {
				return nil, execErr
			}
			pwdResult, pwdErr := ctx.Exec("/bin/sh", []string{"-c", "pwd"}, sdk.ExecOptions{})
			if pwdErr != nil {
				return nil, pwdErr
			}
			pwd = strings.TrimSpace(pwdResult.Stdout)
			return nil, nil
		},
	}
	deps, _, _, stderr := runHostPrompt(t, subdir, extension, textStop("done"))
	deps.Config.WorkspaceRoot = workspace
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if execErr != nil {
		t.Fatalf("Exec: %v", execErr)
	}
	if result == nil {
		t.Fatal("Exec returned no result")
	}
	if result.Stdout != "out||visible" {
		t.Fatalf("stdout = %q, want separated redacted stdout", result.Stdout)
	}
	if result.Stderr != "err" {
		t.Fatalf("stderr = %q", result.Stderr)
	}
	if result.Code != 5 || result.Killed {
		t.Fatalf("code = %d killed = %v, want 5 and false", result.Code, result.Killed)
	}
	if timeoutResult == nil || !timeoutResult.Killed {
		t.Fatalf("timeout result = %+v, want killed", timeoutResult)
	}
	wantPwd, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	gotPwd, err := filepath.EvalSymlinks(pwd)
	if err != nil {
		t.Fatal(err)
	}
	if gotPwd != wantPwd {
		t.Fatalf("exec cwd = %q, want the workspace root %q", gotPwd, wantPwd)
	}
}

func TestComposedActiveToolsDisableAdvertisedAndCallable(t *testing.T) {
	cwd := t.TempDir()
	keep := &sdkToolProbe{name: "keep"}
	drop := &sdkToolProbe{name: "drop"}
	var registered sdk.API
	extension := &hostHookExtension{
		id: "tools-hooks",
		setupFn: func(api sdk.API) error {
			registered = api
			if err := api.RegisterTool(keep); err != nil {
				return err
			}
			return api.RegisterTool(drop)
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				return nil, ctx.SetActiveTools([]string{"keep"})
			}
			return nil, nil
		},
	}
	client := &capturingClient{script: []*agent.AssistantMessage{
		toolUse("call_1", "drop", `"malformed"`),
		textStop("done"),
	}}
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &bytes.Buffer{}
	deps.Stderr = &bytes.Buffer{}
	registry := extensions.NewRegistry()
	if err := registry.Register(extension); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if drop.calls != 0 {
		t.Fatalf("disabled tool executed %d times, want 0", drop.calls)
	}
	if len(client.reqs) < 1 {
		t.Fatal("client received no request")
	}
	var advertised []string
	for _, tool := range client.reqs[0].Tools {
		if tool != nil {
			advertised = append(advertised, tool.Name())
		}
	}
	if len(advertised) != 1 || advertised[0] != "keep" {
		t.Fatalf("advertised tools = %v, want only the active view", advertised)
	}
	if extension.contextCount() != 2 {
		t.Fatalf("context calls = %d, want 2", extension.contextCount())
	}
	if keep.calls != 0 {
		t.Fatalf("keep executed %d times, want 0 for a malformed request", keep.calls)
	}
	if registered == nil {
		t.Fatal("setup captured no API")
	}
	if active := registered.ActiveTools(); len(active) != 1 || active[0] != "keep" {
		t.Fatalf("ActiveTools() = %v, want the active view", active)
	}
	all := registered.AllTools()
	names := map[string]bool{}
	for _, info := range all {
		names[info.Name] = true
	}
	if !names["keep"] || !names["drop"] || len(all) <= len(registered.ActiveTools()) {
		t.Fatalf("AllTools() = %v, want every registered tool retained", all)
	}
}

func TestToolsetFingerprintTracksActiveView(t *testing.T) {
	catalog := extensions.NewToolCatalog()
	catalog.Register(fingerprintTool{name: "alpha"})
	catalog.Register(fingerprintTool{name: "beta"})
	before := toolsetFingerprint(catalog, nil)
	if err := catalog.SetActive([]string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	after := toolsetFingerprint(catalog, nil)
	if before == after {
		t.Fatal("active tool view change must change the fingerprint")
	}
	if err := catalog.SetActive([]string{"alpha", "beta"}); err != nil {
		t.Fatal(err)
	}
	if restored := toolsetFingerprint(catalog, nil); restored != before {
		t.Fatalf("restoring the view must restore the fingerprint: %q vs %q", restored, before)
	}
}

func TestComposedShutdownCancelsOwnedPrintRun(t *testing.T) {
	cwd := t.TempDir()
	client := &blockingContextClient{entered: make(chan struct{})}
	extension := &hostHookExtension{
		id: "shutdown-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				ctx.Shutdown()
			}
			return nil, nil
		},
	}
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &bytes.Buffer{}
	deps.Stderr = &bytes.Buffer{}
	registry := extensions.NewRegistry()
	if err := registry.Register(extension); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)
	done := make(chan error, 1)
	go func() { done <- RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps) }()
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never reached the client")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Shutdown must cancel the owned run")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not end the owned run")
	}
}

func TestComposedAbortCancelsOwnedPrintTurn(t *testing.T) {
	cwd := t.TempDir()
	client := &blockingContextClient{entered: make(chan struct{})}
	extension := &hostHookExtension{
		id: "abort-print-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				ctx.Abort()
			}
			return nil, nil
		},
	}
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = &bytes.Buffer{}
	deps.Stderr = &bytes.Buffer{}
	registry := extensions.NewRegistry()
	if err := registry.Register(extension); err != nil {
		t.Fatal(err)
	}
	deps.ExtensionRuntime = extensions.NewRuntime(registry)
	done := make(chan error, 1)
	go func() { done <- RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps) }()
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never reached the client")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Abort must cancel the owned turn")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Abort did not end the owned turn")
	}
}

func TestHostCancelPendingCompactReportsFailure(t *testing.T) {
	host, _, adapter, _, _ := newCompactHost(t, 1)
	host.beginTurn()
	defer host.endTurn()
	failure := make(chan error, 1)
	if !adapter.requestCompact(sdk.CompactOptions{OnError: func(err error) { failure <- err }}) {
		t.Fatal("pending request was not accepted")
	}
	host.cancelPendingCompact()
	select {
	case err := <-failure:
		if !errors.Is(err, errHostCompactCanceled) {
			t.Fatalf("error = %v, want errHostCompactCanceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceling a pending compaction produced no callback")
	}
}

type blockingContextClient struct {
	entered chan struct{}
	once    sync.Once
}

func (c *blockingContextClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.once.Do(func() { close(c.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func seedCompactableSession(t *testing.T, sess *session.Session, pairs int) ([]*agent.Message, []string) {
	t.Helper()
	big := strings.Repeat("token ", 2000)
	for i := 0; i < pairs; i++ {
		if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"question ` + strings.Repeat("x", 2000) + `"`)}); err != nil {
			t.Fatal(err)
		}
		if err := sess.AppendAssistant(&agent.AssistantMessage{
			Role:    string(agent.RoleAssistant),
			Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: big}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	return history, entryIDs
}

func newCompactHost(t *testing.T, keepRecent int) (*hostRuntime, *session.Session, *contextPreparerAdapter, []*agent.Message, []string) {
	t.Helper()
	return newCompactHostWithSelector(t, keepRecent, nil)
}

func newCompactHostWithSelector(t *testing.T, keepRecent int, selector subagent.Selector) (*hostRuntime, *session.Session, *contextPreparerAdapter, []*agent.Message, []string) {
	t.Helper()
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	history, entryIDs := seedCompactableSession(t, sess, 4)
	cmCfg := contextmanager.Config{
		Enabled:                true,
		ContextWindowTokens:    2000,
		CompactTarget:          0.1,
		KeepRecentMessages:     keepRecent,
		PruneThreshold:         0.7,
		CompactThreshold:       0.85,
		SafetyCompactThreshold: 0.95,
	}
	live, err := contextmanager.New(cmCfg, selector)
	if err != nil {
		t.Fatal(err)
	}
	adapter := newContextPreparerAdapter(live, cmCfg)
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	host.attachPreparer(adapter)
	host.setMessages(history)
	host.setEntryIDs(entryIDs)
	host.setSystem("system")
	return host, sess, adapter, history, entryIDs
}

func waitCompact(t *testing.T, result chan sdk.CompactionResult, failure chan error) (sdk.CompactionResult, error) {
	t.Helper()
	select {
	case res := <-result:
		return res, nil
	case err := <-failure:
		return sdk.CompactionResult{}, err
	case <-time.After(5 * time.Second):
		t.Fatal("compaction callbacks were never invoked")
	}
	return sdk.CompactionResult{}, nil
}

func TestHostCompactIdlePersistsVerbatimEntry(t *testing.T) {
	host, sess, _, _, _ := newCompactHost(t, 1)
	result := make(chan sdk.CompactionResult, 1)
	failure := make(chan error, 1)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { result <- res },
		OnError:    func(err error) { failure <- err },
	})
	res, err := waitCompact(t, result, failure)
	if err != nil {
		t.Fatalf("OnError: %v", err)
	}
	if !strings.Contains(res.Summary, "strategy") {
		t.Fatalf("summary = %q, want the context manager strategy summary", res.Summary)
	}
	if !strings.Contains(res.Summary, "smidja-verbatim-v1") && !strings.Contains(res.Summary, "smidja-fallback-v1") {
		t.Fatalf("summary = %q, want a known strategy", res.Summary)
	}
	if res.FirstKeptEntryID == "" || res.TokensBefore <= 0 {
		t.Fatalf("result = %+v, want a kept anchor and token count", res)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	var persisted *session.CompactionEntry
	for _, entry := range loader.Entries() {
		if compaction, ok := entry.(*session.CompactionEntry); ok {
			persisted = compaction
		}
	}
	if persisted == nil {
		t.Fatal("compaction entry was not persisted")
	}
	if persisted.FirstKeptEntryID != res.FirstKeptEntryID || persisted.TokensBefore != res.TokensBefore {
		t.Fatalf("persisted = %+v, callbacks = %+v", persisted, res)
	}
}

func TestHostCompactCustomInstructionsArePreciseUnsupported(t *testing.T) {
	host, sess, _, _, _ := newCompactHost(t, 1)
	failure := make(chan error, 1)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		CustomInstructions: "focus on the API",
		OnError:            func(err error) { failure <- err },
	})
	select {
	case err := <-failure:
		if !errors.Is(err, errHostCompactInstructions) {
			t.Fatalf("error = %v, want errHostCompactInstructions", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError was not invoked")
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loader.Entries() {
		if _, ok := entry.(*session.CompactionEntry); ok {
			t.Fatal("unsupported instructions must not persist a compaction entry")
		}
	}
}

func TestHostCompactCanceledContextFailsTruthfully(t *testing.T) {
	host, _, _, _, _ := newCompactHost(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failure := make(chan error, 1)
	host.requestCompact(ctx, sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError was not invoked for a canceled context")
	}
}

func TestHostCompactEmptyHistoryFailsTruthfully(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	cmCfg := contextmanager.Config{Enabled: true, ContextWindowTokens: 1000, KeepRecentMessages: 1}
	live, err := contextmanager.New(cmCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.attachPreparer(newContextPreparerAdapter(live, cmCfg))
	failure := make(chan error, 1)
	host.requestCompact(context.Background(), sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, errHostNothingToCompact) {
			t.Fatalf("error = %v, want errHostNothingToCompact", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError was not invoked for an empty history")
	}
}

func TestHostCompactAtPrepareBoundaryDuringActiveTurn(t *testing.T) {
	host, sess, adapter, history, entryIDs := newCompactHost(t, 1)
	host.beginTurn()
	defer host.endTurn()
	result := make(chan sdk.CompactionResult, 1)
	failure := make(chan error, 1)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { result <- res },
		OnError:    func(err error) { failure <- err },
	})
	select {
	case res := <-result:
		t.Fatalf("compaction completed before the prepare boundary: %+v", res)
	case err := <-failure:
		t.Fatalf("compaction failed before the prepare boundary: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	res, err := adapter.Prepare(context.Background(), agent.ContextRequest{System: "system", Messages: history, EntryIDs: entryIDs})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !res.Compacted || res.Compaction == nil {
		t.Fatalf("Prepare result = %+v, want a compaction", res)
	}
	completed, waitErr := waitCompact(t, result, failure)
	if waitErr != nil {
		t.Fatalf("OnError: %v", waitErr)
	}
	if completed.FirstKeptEntryID == "" {
		t.Fatal("prepare-boundary compaction reported no kept anchor")
	}
	host.endTurn()
	select {
	case <-result:
		t.Fatal("compaction completed a second time after endTurn")
	case err := <-failure:
		t.Fatalf("second callback after endTurn: %v", err)
	default:
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range loader.Entries() {
		if _, ok := entry.(*session.CompactionEntry); ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("persisted compaction entries = %d, want exactly 1", count)
	}
}

func TestHostCompactSecondRequestWhilePendingFails(t *testing.T) {
	host, _, adapter, _, _ := newCompactHost(t, 1)
	host.beginTurn()
	defer host.endTurn()
	if !adapter.requestCompact(sdk.CompactOptions{}) {
		t.Fatal("first request must be accepted")
	}
	failure := make(chan error, 1)
	host.requestCompact(context.Background(), sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, errHostCompactPending) {
			t.Fatalf("error = %v, want errHostCompactPending", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending compaction rejection never reported")
	}
}

func TestComposedCompactDuringTurnSchedulesWithoutPhantomRequest(t *testing.T) {
	cwd := t.TempDir()
	var calls int
	done := make(chan sdk.CompactionResult, 1)
	failure := make(chan error, 1)
	extension := &hostHookExtension{
		id: "compact-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			calls++
			if call == 0 {
				ctx.Compact(sdk.CompactOptions{
					OnComplete: func(res sdk.CompactionResult) { done <- res },
					OnError:    func(err error) { failure <- err },
				})
			}
			return nil, nil
		},
	}
	deps, client, _, stderr := runHostPrompt(t, cwd, extension, textStop("done"))
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	select {
	case <-done:
	case <-failure:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled compaction produced no callback")
	}
	if calls != 1 || client.calls != 1 {
		t.Fatalf("context calls = %d client calls = %d, want one turn without model side effects", calls, client.calls)
	}
}

func TestHostCompactKeepsSingleCallbackOnFailure(t *testing.T) {
	host, _, _, _, _ := newCompactHost(t, 1)
	result := make(chan sdk.CompactionResult, 2)
	failure := make(chan error, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { result <- res },
		OnError:    func(err error) { failure <- err },
	})
	if _, err := waitCompact(t, result, failure); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	select {
	case res := <-result:
		t.Fatalf("duplicate completion callback: %+v", res)
	case err := <-failure:
		t.Fatalf("duplicate failure callback: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHostContextUsageReportsTokensWindowAndPercent(t *testing.T) {
	host := newHostRuntime(context.Background(), "/work", nil, extensions.NewToolCatalog())
	host.setWindow(1000)
	host.setMessages([]*agent.Message{
		{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), Usage: agent.Usage{Input: 250}}},
	})
	usage := host.contextUsage()
	if usage == nil || usage.ContextWindow != 1000 {
		t.Fatalf("usage = %+v, want the context window", usage)
	}
	if usage.Tokens == nil || *usage.Tokens != 250 {
		t.Fatalf("tokens = %v, want the last assistant input usage", usage.Tokens)
	}
	if usage.Percent == nil || *usage.Percent != 25 {
		t.Fatalf("percent = %v, want 25", usage.Percent)
	}
}

func TestHostContextWithoutSessionStaysEmptyAndPrintScoped(t *testing.T) {
	host := newHostRuntime(context.Background(), "", nil, extensions.NewToolCatalog())
	ctx := host.context()
	if ctx.Cwd() != "" || ctx.SessionManager() != nil || ctx.Model() != nil || ctx.ModelRegistry() != nil {
		t.Fatalf("empty host exposed state: cwd=%q session=%v model=%v registry=%v", ctx.Cwd(), ctx.SessionManager(), ctx.Model(), ctx.ModelRegistry())
	}
	if ctx.Mode() != sdk.ModePrint || ctx.HasUI() {
		t.Fatalf("mode = %q hasUI = %v, want print without UI", ctx.Mode(), ctx.HasUI())
	}
	if ctx.UI() == nil {
		t.Fatal("print context must expose a no-op UI")
	}
	if _, err := ctx.UI().Confirm("t", "m"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("print dialog error = %v, want ErrModeUnsupported", err)
	}
	if ctx.ThinkingLevel() != sdk.ThinkingOff || ctx.SystemPrompt() != "" {
		t.Fatalf("thinking = %q system = %q, want off and empty", ctx.ThinkingLevel(), ctx.SystemPrompt())
	}
	if ctx.ContextUsage() == nil {
		t.Fatal("context usage must be non-nil for a composed host")
	}
}

func TestComposedContextModelRegistryUsesVerifiedWireModel(t *testing.T) {
	cwd := t.TempDir()
	extension := &hostHookExtension{id: "wire-hooks"}
	var model *sdk.Model
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		model = ctx.Model()
		return nil, nil
	}
	deps, _, _, stderr := runHostPrompt(t, cwd, extension, textStop("done"))
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if model == nil || model.ID != "test/model" || model.Name != "test/model" {
		t.Fatalf("model = %+v, want the verified wire model", model)
	}
}

func TestHostRuntimeHelpersTolerateNilInputs(t *testing.T) {
	host := newHostRuntime(nil, "", nil, nil)
	if host.baseCtx == nil {
		t.Fatal("nil context must fall back to a background context")
	}
	if parent := withHostTurnCancel(nil, nil); parent == nil {
		t.Fatal("nil parent and nil cancel must produce a context")
	}
	_, cancel := context.WithCancel(context.Background())
	cancel()
	wrapped := withHostTurnCancel(nil, cancel)
	if hostTurnCancel(wrapped) == nil {
		t.Fatal("turn cancel must round-trip through the context value")
	}
	host.abort(nil)
	host.shutdown()
	if host.snapshot() != nil {
		t.Fatal("snapshot without a bound session must be nil")
	}
	if host.projectedMessages() != nil {
		t.Fatal("messages without a bound session must be nil")
	}
	if host.modelRegistry() != nil || host.currentModel() != nil {
		t.Fatal("model state without configuration must be empty")
	}
	if result := compactionSDKResult(nil); result != (sdk.CompactionResult{}) {
		t.Fatalf("nil compaction result = %+v", result)
	}
	host.cancelPendingCompact()
}

func TestHostOptionsDirectCallbacks(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	host := newHostRuntime(context.Background(), cwd, nil, nil)
	options := host.hostOptions()
	if err := options.SetActiveTools(nil); !errors.Is(err, extensions.ErrUnavailable) {
		t.Fatalf("SetActiveTools without a catalog = %v, want ErrUnavailable", err)
	}
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	options = host.hostOptions()
	if err := options.AppendEntry("note", map[string]int{"n": 1}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if err := options.SetSessionName("named"); err != nil {
		t.Fatalf("SetSessionName: %v", err)
	}
	if err := options.LabelEntry("entry", "tag"); err != nil {
		t.Fatalf("LabelEntry: %v", err)
	}
	res, err := options.Exec(context.Background(), "/bin/sh", []string{"-c", "printf ok"}, sdk.ExecOptions{})
	if err != nil || res == nil || res.Stdout != "ok" {
		t.Fatalf("Exec = %+v, %v", res, err)
	}
	if err := options.AppendEntry("", nil); !errors.Is(err, errHostCustomType) {
		t.Fatalf("empty custom type = %v", err)
	}
}

func TestHostExecDefaultsAndStartFailure(t *testing.T) {
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	host.setExecLimits(0, 0)
	res, err := host.exec(nil, "/bin/sh", []string{"-c", "printf ok"}, sdk.ExecOptions{})
	if err != nil || res == nil || res.Stdout != "ok" {
		t.Fatalf("default timeout exec = %+v, %v", res, err)
	}
	missing := filepath.Join(t.TempDir(), "missing-binary")
	if _, err := host.exec(nil, missing, nil, sdk.ExecOptions{}); err == nil {
		t.Fatal("start failure must return an error")
	}
}

func TestHostCompactGuardsWithoutPreparerOrSession(t *testing.T) {
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	failure := make(chan error, 1)
	host.requestCompact(context.Background(), sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, extensions.ErrUnavailable) {
			t.Fatalf("error = %v, want ErrUnavailable without a preparer", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing preparer produced no callback")
	}
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	cmCfg := contextmanager.Config{Enabled: true, ContextWindowTokens: 1000, KeepRecentMessages: 1}
	live, err := contextmanager.New(cmCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.attachPreparer(newContextPreparerAdapter(live, cmCfg))
	host.setLifecycle(hostLifecycle{dispatch: func(job func()) bool { job(); return true }})
	failure = make(chan error, 1)
	host.requestCompact(context.Background(), sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, errHostClosed) {
			t.Fatalf("error = %v, want errHostClosed without a session", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing session produced no callback")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	host.runIdleCompact(ctx, host.currentPreparer(), newHostCompactJob(sdk.CompactOptions{OnError: func(err error) {}}))
	host.setLifecycle(hostLifecycle{})
	host.cancelPendingCompact()
}

func TestHostExplicitCompactFailureBranches(t *testing.T) {
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	failure := make(chan error, 1)
	req := agent.ContextRequest{System: "system", Messages: []*agent.Message{{User: &agent.UserMessage{Role: "user", Content: []byte(`"hi"`)}}}}
	result := host.runExplicitCompact(context.Background(), req, sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	if result.Compacted || len(result.Messages) != 1 {
		t.Fatalf("result = %+v, want the original request", result)
	}
	select {
	case err := <-failure:
		if !errors.Is(err, extensions.ErrUnavailable) {
			t.Fatalf("error = %v, want ErrUnavailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing preparer produced no callback")
	}
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	disabled := contextmanager.Config{Enabled: false, ContextWindowTokens: 1000}
	live, err := contextmanager.New(disabled, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.attachPreparer(newContextPreparerAdapter(live, disabled))
	failure = make(chan error, 1)
	host.runExplicitCompact(context.Background(), req, sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, contextmanager.ErrDisabled) {
			t.Fatalf("error = %v, want ErrDisabled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disabled manager produced no callback")
	}
	enabled := contextmanager.Config{Enabled: true, ContextWindowTokens: 1000, KeepRecentMessages: 1}
	live, err = contextmanager.New(enabled, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.attachPreparer(newContextPreparerAdapter(live, enabled))
	failure = make(chan error, 1)
	host.runExplicitCompact(context.Background(), req, sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, errHostNothingToCompact) {
			t.Fatalf("error = %v, want errHostNothingToCompact", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("short history produced no callback")
	}
	host2, _, _, bigHistory, ids := newCompactHost(t, 1)
	host2.mu.Lock()
	handle := *host2.handle
	handle.recorder = nil
	host2.handle = &handle
	host2.mu.Unlock()
	failure = make(chan error, 1)
	host2.runExplicitCompact(context.Background(), agent.ContextRequest{System: "system", Messages: bigHistory, EntryIDs: ids}, sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	select {
	case err := <-failure:
		if !errors.Is(err, errHostClosed) {
			t.Fatalf("error = %v, want errHostClosed for a nil recorder", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nil recorder produced no callback")
	}
}

func TestHostModelRegistryEdges(t *testing.T) {
	reg := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	reg.setModel(models.NewRegistry(), "alpha", "beta", "provider")
	model := reg.currentModel()
	if model == nil || model.ID != "alpha" || model.Name != "beta" {
		t.Fatalf("model = %+v, want the verified wire name", model)
	}
	registry := reg.modelRegistry()
	if _, ok := registry.Find("", ""); ok {
		t.Fatal("Find with an empty id must fail")
	}
	if available := registry.Available(); len(available) == 0 {
		t.Fatal("Available must list registry models")
	}
	if _, ok := registry.Find("provider", "does-not-exist"); ok {
		t.Fatal("Find must not invent models")
	}
}

func TestHostPendingCompactFailsOnceOnShutdown(t *testing.T) {
	host, sess, _, _, _ := newCompactHost(t, 1)
	var queued func()
	host.setLifecycle(hostLifecycle{dispatch: func(job func()) bool {
		queued = job
		return true
	}})
	failure := make(chan error, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{OnError: func(err error) { failure <- err }})
	host.failPendingCompacts(errHostClosed)
	select {
	case err := <-failure:
		if !errors.Is(err, errHostClosed) {
			t.Fatalf("error = %v, want errHostClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not fail the queued compaction")
	}
	if queued == nil {
		t.Fatal("compaction job was not queued")
	}
	queued()
	select {
	case err := <-failure:
		t.Fatalf("late compaction produced a second callback: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loader.Entries() {
		if _, ok := entry.(*session.CompactionEntry); ok {
			t.Fatal("a settled compaction job must not persist a late entry")
		}
	}
	host.setLifecycle(hostLifecycle{})
	host.waitCompacts()
}
