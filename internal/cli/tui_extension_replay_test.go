package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type replayRendererComponent struct{ text string }

func (c replayRendererComponent) Render(width int) []string { return []string{c.text} }

func (c replayRendererComponent) Invalidate() {}

type replayRegistrationExtension struct {
	setupOK  bool
	registry *extensionui.Registry
}

func (e *replayRegistrationExtension) ID() string { return "replay-renderers" }

func (e *replayRegistrationExtension) Setup(api sdk.API) error {
	registration, ok := api.(sdk.UIRegistrationAPI)
	if !ok {
		return nil
	}
	e.setupOK = true
	if err := registration.RegisterMessageRenderer("notice", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return replayRendererComponent{text: "MESSAGE-RENDER " + message.Content}
	}); err != nil {
		return err
	}
	if err := registration.RegisterEntryRenderer("note", func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component {
		return replayRendererComponent{text: "ENTRY-RENDER " + entry.CustomType}
	}); err != nil {
		return err
	}
	e.registry = extensionsUIRegistry(api)
	return nil
}

func extensionsUIRegistry(api sdk.API) *extensionui.Registry {
	return extensions.UIRegistryOf(api)
}

func TestRunTUIReplayDispatchesCustomMessageAndEntryRenderers(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: "note", Data: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "notice", Content: json.RawMessage(`"visible body"`), Display: true, Details: json.RawMessage(`{"a":1}`)}); err != nil {
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

	terminal := newFakeBridgeTerminal()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("unused")}}
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	extension := &replayRegistrationExtension{}
	extensionRegistry := extensions.NewRegistry()
	if err := extensionRegistry.Register(extension); err != nil {
		t.Fatal(err)
	}
	uiRegistry := extensionui.NewRegistry()
	api := extensions.NewAPI(extensions.APIOptions{UI: uiRegistry})
	runtime := extensions.NewRuntime(extensionRegistry)
	runtime.SetAPI(func() sdk.API { return api })
	runtime.SetUIRegistry(uiRegistry)
	if err := runtime.Start(); err != nil {
		t.Fatal(err)
	}
	if !extension.setupOK {
		t.Fatal("extension Setup did not see UIRegistrationAPI")
	}
	if extension.registry != uiRegistry {
		t.Fatal("extension registered against a different UI registry")
	}

	cfg := testConfig(t, cwd)
	env := &sessionBuildEnv{
		cfg:         cfg,
		providerID:  "openrouter",
		system:      "be terse",
		catalog:     extensions.NewToolCatalog(),
		modelReg:    models.NewRegistry(),
		fingerprint: func() string { return "fp" },
	}
	controller := newSessionController(store, cwd)
	defer controller.Close()
	rd := &runDeps{
		model:          "test/model",
		wireModel:      "test/model",
		sessionPath:    path,
		client:         client,
		recorder:       &sessionRecorder{reopened},
		stdout:         &stdout,
		stderr:         &stderr,
		hooks:          runtime.Dispatcher(),
		commands:       extensions.NewCommandCatalog(),
		store:          store,
		sess:           reopened,
		cwd:            cwd,
		controller:     controller,
		env:            env,
		resumedSession: true,
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return runtime.HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal), runtime, nil)
	}()
	output := waitForOutputSettled(t, terminal, "ENTRY-RENDER", 5*time.Second)
	if !strings.Contains(output, "MESSAGE-RENDER visible body") {
		t.Fatalf("custom message renderer was not invoked:\n%s", output)
	}
	if !strings.Contains(output, "ENTRY-RENDER note") {
		t.Fatalf("custom entry renderer was not invoked:\n%s", output)
	}
	if strings.Contains(strings.ReplaceAll(output, "\x1b", ""), "[skill]") {
		t.Fatalf("fallback custom block rendered despite renderers:\n%s", output)
	}
	terminal.FireEOF()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after EOF")
	}
	if terminal.Started() {
		t.Fatal("terminal must be stopped after runTUI returns")
	}
	_ = tui.StopOptions{}
}

func TestProjectTranscriptCarriesCustomDiscriminator(t *testing.T) {
	store := wiringStore(t)
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: "note", Data: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "notice", Content: json.RawMessage(`"visible body"`), Display: true, Details: json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := projectTranscript(loader)
	var entry, message bool
	for _, item := range entries {
		if item.Kind != "custom" {
			continue
		}
		switch item.CustomKind {
		case "entry":
			entry = true
			if item.CustomType != "note" || string(item.CustomData.(json.RawMessage)) != `{"x":1}` {
				t.Fatalf("entry projection = %+v", item)
			}
		case "message":
			message = true
			if item.CustomType != "notice" || string(item.CustomData.(json.RawMessage)) != `{"a":1}` {
				t.Fatalf("message projection = %+v", item)
			}
		}
	}
	if !entry || !message {
		t.Fatalf("custom discriminator missing: entry=%v message=%v", entry, message)
	}
}
