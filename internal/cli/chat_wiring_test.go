package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/digitalygo/smidja/internal/config"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/skills"
	"github.com/digitalygo/smidja/sdk"
)

type capturingClient struct {
	script []*agent.AssistantMessage
	calls  int
	reqs   []*agent.TurnRequest
}

func (c *capturingClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if c.calls >= len(c.script) {
		return nil, errors.New("capturingClient: script exhausted")
	}
	c.reqs = append(c.reqs, req)
	m := c.script[c.calls]
	c.calls++
	for _, b := range m.Content {
		switch b.Type {
		case agent.BlockTypeText:
			if onText != nil {
				onText(b.Text)
			}
		case agent.BlockTypeThinking:
			if onThinking != nil {
				onThinking(b.Thinking)
			}
		}
	}
	return m, nil
}

func (c *capturingClient) lastUserText() string {
	if len(c.reqs) == 0 {
		return ""
	}
	req := c.reqs[len(c.reqs)-1]
	if len(req.Messages) == 0 {
		return ""
	}
	last := req.Messages[len(req.Messages)-1]
	if last == nil || last.User == nil {
		return ""
	}
	var text string
	if err := json.Unmarshal(last.User.Content, &text); err != nil {
		return string(last.User.Content)
	}
	return text
}

func wiringTestDeps(storeDir string) *Deps {
	return &Deps{
		Env:   envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": storeDir}),
		Getwd: func() (string, error) { return "/work/dir", nil },
		Home:  func() string { return "/home/tester" },
		Stdin: strings.NewReader(""),
	}
}

func TestExtensionRegisteredToolReachesLoop(t *testing.T) {
	probe := &sdkToolProbe{name: "ext-probe"}
	reg := extensions.NewRegistry()
	if err := reg.Register(&sdkSetupExtension{apiTool: probe}); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(reg)
	client := &capturingClient{script: []*agent.AssistantMessage{
		toolUse("call_1", "ext-probe", `{"x":1}`),
		textStop("answer"),
	}}
	var stdout, stderr bytes.Buffer
	cwd := t.TempDir()
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.ExtensionRuntime = runtime

	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if probe.calls != 1 {
		t.Fatalf("extension tool executed %d times, want 1", probe.calls)
	}
	if len(client.reqs) == 0 {
		t.Fatal("client received no requests")
	}
	found := false
	for _, req := range client.reqs {
		for _, tool := range req.Tools {
			if tool != nil && tool.Name() == "ext-probe" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the extension tool never reached the loop's tool list")
	}
	if !strings.Contains(stdout.String(), "answer") {
		t.Fatalf("stdout = %q, want the response", stdout.String())
	}
}

func TestSkillCommandInjectsContent(t *testing.T) {
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = client
	deps.Stdin = strings.NewReader("/skill quick\n/quit\n")
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	deps.Bundle = sdk.Bundle{
		ID: "digitalygo",
		FS: fstest.MapFS{"content/skills/quick.md": {Data: []byte("# quick\nusage instructions")}},
	}

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	injected := client.lastUserText()
	if !strings.Contains(injected, "[skill digitalygo/quick]") {
		t.Errorf("injected user message = %q, want the provenance header", injected)
	}
	if !strings.Contains(injected, "usage instructions") {
		t.Errorf("injected user message = %q, want the skill content", injected)
	}
	if strings.Contains(stdout.String(), "unknown command") {
		t.Errorf("stdout = %q, want no unknown-command error", stdout.String())
	}
}

func TestSkillCommandListsNames(t *testing.T) {
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = client
	deps.Stdin = strings.NewReader("/skill\n/quit\n")
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	deps.Bundle = sdk.Bundle{
		ID: "digitalygo",
		FS: fstest.MapFS{
			"content/skills/quick.md":        {Data: []byte("a")},
			"content/skills/orchestrator.md": {Data: []byte("b")},
		},
	}
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	for _, want := range []string{"digitalygo/quick", "digitalygo/orchestrator"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("skill list missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestSkillCommandUnknownName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{}
	deps.Stdin = strings.NewReader("/skill nope\n/quit\n")
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if !strings.Contains(stderr.String(), "no skill named") {
		t.Errorf("stderr = %q, want the unknown-skill error", stderr.String())
	}
}

func TestUnknownReplCommandReported(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{}
	deps.Stdin = strings.NewReader("/bogus\n/quit\n")
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if !strings.Contains(stderr.String(), "unknown command /bogus") {
		t.Errorf("stderr = %q, want the unknown-command error", stderr.String())
	}
}

func TestCommandContextMethodsUnsupported(t *testing.T) {
	ctx := &commandContext{HandlerContext: nil}
	if err := ctx.WaitForIdle(); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("WaitForIdle = %v", err)
	}
	if _, err := ctx.NewSession(sdk.NewSessionOptions{}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("NewSession = %v", err)
	}
	if _, err := ctx.Fork("e", sdk.ForkOptions{}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Fork = %v", err)
	}
	if _, err := ctx.NavigateTree("t", sdk.TreeOptions{}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("NavigateTree = %v", err)
	}
	if _, err := ctx.SwitchSession("p", sdk.SwitchOptions{}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("SwitchSession = %v", err)
	}
	if err := ctx.Reload(); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Reload = %v", err)
	}
}

func TestSessionStartHookDispatched(t *testing.T) {
	reg := extensions.NewRegistry()
	recorder := &sessionStartRecorder{}
	if err := reg.Register(&sessionStartExtension{rec: recorder}); err != nil {
		t.Fatal(err)
	}
	runtime := extensions.NewRuntime(reg)
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("hi")}}
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	deps.ExtensionRuntime = runtime

	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if len(recorder.starts) != 1 || recorder.starts[0] != "startup" {
		t.Fatalf("session starts = %v, want [startup]", recorder.starts)
	}
	if len(recorder.shutdowns) != 1 || recorder.shutdowns[0] != "quit" {
		t.Fatalf("session shutdowns = %v, want [quit]", recorder.shutdowns)
	}
}

type sdkToolProbe struct {
	name  string
	calls int
	args  json.RawMessage
}

func (p *sdkToolProbe) Name() string        { return p.name }
func (p *sdkToolProbe) Description() string { return "sdk tool probe" }
func (p *sdkToolProbe) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (p *sdkToolProbe) Exec(ctx context.Context, args json.RawMessage) sdk.Result {
	p.calls++
	p.args = args
	return sdk.Result{Content: []sdk.Block{{Type: agent.BlockTypeText, Text: "probe done"}}}
}

type sdkSetupExtension struct {
	apiTool sdk.Tool
}

func (e *sdkSetupExtension) ID() string { return "sdk-setup" }

func (e *sdkSetupExtension) Setup(api sdk.API) error {
	return api.RegisterTool(e.apiTool)
}

type sessionStartRecorder struct {
	starts    []string
	shutdowns []string
}

type sessionStartExtension struct {
	rec *sessionStartRecorder
}

func (e *sessionStartExtension) ID() string { return "session-hook" }

func (e *sessionStartExtension) RegisterSessionHooks(r sdk.SessionHookRegistry) {
	r.OnSessionStart(func(ctx sdk.HandlerContext, ev sdk.SessionStartEvent) error {
		e.rec.starts = append(e.rec.starts, string(ev.Reason))
		return nil
	})
	r.OnSessionShutdown(func(ctx sdk.HandlerContext, ev sdk.SessionShutdownEvent) error {
		e.rec.shutdowns = append(e.rec.shutdowns, string(ev.Reason))
		return nil
	})
}

func testConfig(t *testing.T, cwd string) *config.Config {
	t.Helper()
	cfg, err := config.Load(
		envFrom(map[string]string{"OPENROUTER_API_KEY": "sk-test"}),
		func() (string, error) { return cwd, nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func wiringStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestReplHelpListsCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = &capturingClient{}
	deps.Stdin = strings.NewReader("/help\n/quit\n")
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "/skill - inject a skill into the conversation") {
		t.Errorf("help missing the skill command:\n%s", out)
	}
	if !strings.Contains(out, "/quit, /exit") {
		t.Errorf("help missing the quit note:\n%s", out)
	}
}

func TestSkillCommandRejectsNonInjectorContext(t *testing.T) {
	cat := skills.New()
	cat.Add("bundle", "quick", "content")

	var listOut bytes.Buffer
	listCommands := extensions.NewCommandCatalog()
	registerSkillCommand(listCommands, cat, &listOut)
	listCmd, ok := listCommands.Get("skill")
	if !ok {
		t.Fatal("skill command missing")
	}
	if err := listCmd.Handler(&fakeCommandContext{}, ""); err != nil {
		t.Fatalf("skill list: %v", err)
	}
	if !strings.Contains(listOut.String(), "bundle/quick") {
		t.Fatalf("skill list = %q", listOut.String())
	}

	commands := extensions.NewCommandCatalog()
	registerSkillCommand(commands, cat, &bytes.Buffer{})
	cmd, _ := commands.Get("skill")
	if err := cmd.Handler(&fakeCommandContext{}, "quick"); err == nil {
		t.Fatal("skill handler accepted a context without injection support")
	}
	if err := cmd.Handler(&fakeCommandContext{}, "missing"); err == nil {
		t.Fatal("skill handler accepted an unknown name")
	}
}

type fakeCommandContext struct {
	sdk.HandlerContext
}

func (c *fakeCommandContext) WaitForIdle() error { return nil }
func (c *fakeCommandContext) NewSession(sdk.NewSessionOptions) (*sdk.SessionSwitchResult, error) {
	return nil, nil
}
func (c *fakeCommandContext) Fork(string, sdk.ForkOptions) (*sdk.SessionSwitchResult, error) {
	return nil, nil
}
func (c *fakeCommandContext) NavigateTree(string, sdk.TreeOptions) (*sdk.SessionSwitchResult, error) {
	return nil, nil
}
func (c *fakeCommandContext) SwitchSession(string, sdk.SwitchOptions) (*sdk.SessionSwitchResult, error) {
	return nil, nil
}
func (c *fakeCommandContext) Reload() error { return nil }
