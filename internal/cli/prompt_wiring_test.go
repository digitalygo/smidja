package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func promptBundle(prompts map[string]string) sdk.Bundle {
	files := fstest.MapFS{}
	for name, body := range prompts {
		files["content/prompts/"+name+".md"] = &fstest.MapFile{Data: []byte(body)}
	}
	return sdk.Bundle{ID: "test-bundle", FS: files}
}

func TestPrintModeExpandsPromptTemplate(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("print answer")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = promptBundle(map[string]string{"greet": "hello $1 from $ARGUMENTS"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr

	if err := RunWithDeps([]string{"-p", "/prompt greet Bob"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	want := "hello Bob from Bob"
	if client.lastUserText() != want {
		t.Fatalf("wire prompt = %q, want %q", client.lastUserText(), want)
	}
	transcript := readOnlySession(t, deps.Store.Root())
	if !strings.Contains(transcript, want) {
		t.Fatalf("session transcript missing the expanded prompt:\n%s", transcript)
	}
	if strings.Contains(transcript, "/prompt greet") {
		t.Fatalf("session transcript kept the raw invocation:\n%s", transcript)
	}
}

func TestPrintModeKeepsNonPromptSlashInputLiteral(t *testing.T) {
	for _, prompt := range []string{"/help", "/quit", "/unknown thing"} {
		cwd := t.TempDir()
		client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
		deps := wiringTestDeps(t.TempDir())
		deps.Getwd = func() (string, error) { return cwd, nil }
		deps.Client = client
		deps.Config = testConfig(t, cwd)
		deps.Store = wiringStore(t)
		deps.Bundle = promptBundle(map[string]string{"greet": "hello $1"})
		var stdout, stderr bytes.Buffer
		deps.Stdout = &stdout
		deps.Stderr = &stderr
		if err := RunWithDeps([]string{"-p", prompt}, deps); err != nil {
			t.Fatalf("RunWithDeps %q: %v (stderr %q)", prompt, err, stderr.String())
		}
		if client.lastUserText() != prompt {
			t.Fatalf("wire prompt = %q, want the literal %q", client.lastUserText(), prompt)
		}
	}
}

func TestRunOnceExpandsPromptTemplateBeforeWireAndPersists(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	client := &capturingClient{script: []*agent.AssistantMessage{textStop("answered")}}
	var stdout bytes.Buffer
	d := &runDeps{
		model:         "test/model",
		system:        "be terse",
		sessionPath:   sess.Path(),
		client:        client,
		recorder:      &sessionRecorder{sess},
		stdout:        &stdout,
		prompts:       promptCatalog(map[string]string{"greet": "hi $1"}),
		promptAliases: map[string]string{"greet": "greet"},
	}
	if err := runOnce(context.Background(), d, "/greet Alice"); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if client.lastUserText() != "hi Alice" {
		t.Fatalf("wire prompt = %q", client.lastUserText())
	}
	data, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hi Alice") || strings.Contains(string(data), "/greet") {
		t.Fatalf("session transcript = %s", data)
	}
}

func TestRunOnceContinuedExpandsPromptTemplate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := createLockedSession(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	client := &capturingClient{script: []*agent.AssistantMessage{textStop("continued")}}
	var stdout bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		prompts:     promptCatalog(map[string]string{"greet": "hi $1"}),
	}
	if err := runOnceContinued(context.Background(), d, sess, "/prompt greet Bob"); err != nil {
		t.Fatalf("runOnceContinued: %v", err)
	}
	if client.lastUserText() != "hi Bob" {
		t.Fatalf("wire prompt = %q", client.lastUserText())
	}
	data, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hi Bob") {
		t.Fatalf("session transcript = %s", data)
	}
}

func TestReplPromptCommandExpandsBeforeWire(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("repl answer")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/prompt greet Bob\n/quit\n")
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = promptBundle(map[string]string{"greet": "hello $1"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.lastUserText() != "hello Bob" {
		t.Fatalf("wire prompt = %q", client.lastUserText())
	}
	if !strings.Contains(stdout.String(), "repl answer") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestReplPromptListingPrintsNames(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/prompt\n/quit\n")
	deps.Client = &capturingClient{}
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = promptBundle(map[string]string{"beta": "b", "alpha": "a"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "alpha\nbeta\n") {
		t.Fatalf("stdout = %q, want the sorted prompt names", out)
	}
}

func TestBridgePromptCommandExpandsAndPersists(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("bridge answer"), textStop("shorthand answer")}, nil)
	cat := promptCatalog(map[string]string{"greet": "hi $1"})
	registerPromptCommand(fixture.commands, cat, fixture.bridge.capture, promptShorthandReserved)

	fixture.bridge.handle("/prompt greet Alice")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "hi Alice") {
		t.Fatalf("frame missing the expanded prompt:\n%s", text)
	}
	if fixture.client.lastUserText() != "hi Alice" {
		t.Fatalf("wire prompt = %q", fixture.client.lastUserText())
	}
	data, err := os.ReadFile(fixture.sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hi Alice") || strings.Contains(string(data), "/prompt greet") {
		t.Fatalf("session transcript = %s", data)
	}

	fixture.bridge.handle("/greet Bob")
	if fixture.client.lastUserText() != "hi Bob" {
		t.Fatalf("shorthand wire prompt = %q", fixture.client.lastUserText())
	}
}

func TestBridgePromptListingRoutesToNotice(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	cat := promptCatalog(map[string]string{"greet": "hi"})
	registerPromptCommand(fixture.commands, cat, fixture.bridge.capture, promptShorthandReserved)
	fixture.bridge.handle("/prompt")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "greet") {
		t.Fatalf("frame missing the prompt list:\n%s", text)
	}
	if strings.Contains(fixture.stdout.String(), "greet") {
		t.Fatalf("prompt list leaked to terminal stdout: %q", fixture.stdout.String())
	}
}

func TestBridgePromptInventoryIncludesTemplates(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	cat := promptCatalog(map[string]string{"greet": "hi"})
	aliases := registerPromptCommand(fixture.commands, cat, fixture.bridge.capture, promptShorthandReserved)
	if aliases["greet"] != "greet" {
		t.Fatalf("aliases = %#v", aliases)
	}
	if !fixture.bridge.hasCommand("prompt") || !fixture.bridge.hasCommand("greet") {
		t.Fatalf("inventory names = %#v", fixture.bridge.commandNames())
	}
	items := fixture.bridge.autocompleteInventory()
	found := 0
	for _, item := range items {
		if item.Value == "greet" || item.Value == "prompt" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("autocomplete items = %#v", items)
	}
	entries := fixture.bridge.effectiveHelpEntries()
	found = 0
	for _, entry := range entries {
		if entry.Name == "greet" || entry.Name == "prompt" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("help entries = %#v", entries)
	}
}

func TestRunOnceRejectsInvalidPromptInvocation(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	d := &runDeps{
		model:       "test/model",
		sessionPath: sess.Path(),
		client:      &capturingClient{},
		recorder:    &sessionRecorder{sess},
		stdout:      &bytes.Buffer{},
		prompts:     promptCatalog(map[string]string{"greet": "hi $1"}),
	}
	if err := runOnce(context.Background(), d, "/prompt missing value"); err == nil {
		t.Fatal("runOnce accepted an unknown prompt")
	}
}

func TestRunOnceContinuedRejectsInvalidPromptInvocation(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := createLockedSession(store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	d := &runDeps{
		model:       "test/model",
		sessionPath: sess.Path(),
		client:      &capturingClient{},
		recorder:    &sessionRecorder{sess},
		stdout:      &bytes.Buffer{},
		prompts:     promptCatalog(map[string]string{"greet": "hi $1"}),
	}
	if err := runOnceContinued(context.Background(), d, sess, "/prompt missing value"); err == nil {
		t.Fatal("runOnceContinued accepted an unknown prompt")
	}
}

func TestBuildContentSnapshotHonorsWorkspaceTrust(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	promptDir := filepath.Join(ws, ".smidja", "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "local.md"), []byte("workspace body"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := wiringTestDeps(t.TempDir())
	deps.Home = func() string { return home }

	untrusted, err := buildContentSnapshot(deps, ws, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := untrusted.Prompts["local"]; ok {
		t.Fatal("untrusted workspace prompt must be excluded")
	}
	trusted, err := buildContentSnapshot(deps, ws, true)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := trusted.Prompts["local"]
	if !ok || ref.Content != "workspace body" {
		t.Fatalf("trusted workspace prompt = %+v ok=%v", ref, ok)
	}
}

type promptCollidingExtension struct {
	invocations int
}

func (e *promptCollidingExtension) ID() string { return "prompt-collision" }

func (e *promptCollidingExtension) Setup(api sdk.API) error {
	return api.RegisterCommand("prompt", sdk.Command{
		Description: "extension prompt",
		Handler: func(ctx sdk.CommandContext, args string) error {
			e.invocations++
			injector, ok := ctx.(skillInjector)
			if !ok {
				return errors.New("prompt extension: injection unavailable")
			}
			return injector.runInput("EXTENSION-PROMPT " + args)
		},
	})
}

func promptExtensionRuntime(t *testing.T, ext sdk.Extension) *extensions.Runtime {
	t.Helper()
	registry := extensions.NewRegistry()
	if err := registry.Register(ext); err != nil {
		t.Fatal(err)
	}
	return extensions.NewRuntime(registry)
}

func requestUserText(t *testing.T, req *agent.TurnRequest) string {
	t.Helper()
	if req == nil || len(req.Messages) == 0 {
		return ""
	}
	last := req.Messages[len(req.Messages)-1]
	if last == nil || last.User == nil {
		return ""
	}
	var text string
	if err := json.Unmarshal(last.User.Content, &text); err != nil {
		t.Fatalf("decode request user text: %v", err)
	}
	return text
}

func TestReplExplicitPromptBeatsCollidingExtension(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("host answer"), textStop("extension answer")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/prompt greet Bob\n/prompt2 tail\n/quit\n")
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = promptBundle(map[string]string{"greet": "hello $1"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	ext := &promptCollidingExtension{}
	deps.ExtensionRuntime = promptExtensionRuntime(t, ext)

	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if len(client.reqs) != 2 {
		t.Fatalf("turn requests = %d, want 2", len(client.reqs))
	}
	if got := requestUserText(t, client.reqs[0]); got != "hello Bob" {
		t.Fatalf("first wire prompt = %q, want the host expansion", got)
	}
	if got := requestUserText(t, client.reqs[1]); got != "EXTENSION-PROMPT tail" {
		t.Fatalf("second wire prompt = %q, want the aliased extension", got)
	}
	if ext.invocations != 1 {
		t.Fatalf("extension invocations = %d, want 1", ext.invocations)
	}
	transcript := readOnlySession(t, deps.Store.Root())
	if strings.Contains(transcript, "EXTENSION-PROMPT greet") {
		t.Fatalf("the extension intercepted explicit /prompt:\n%s", transcript)
	}
}

func TestPrintExplicitPromptBeatsCollidingExtension(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("print answer")}}
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = promptBundle(map[string]string{"greet": "hello $1"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	ext := &promptCollidingExtension{}
	deps.ExtensionRuntime = promptExtensionRuntime(t, ext)

	if err := RunWithDeps([]string{"-p", "/prompt greet Bob"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.lastUserText() != "hello Bob" {
		t.Fatalf("wire prompt = %q, want the host expansion", client.lastUserText())
	}
	if ext.invocations != 0 {
		t.Fatalf("extension invocations = %d, want 0", ext.invocations)
	}
}
