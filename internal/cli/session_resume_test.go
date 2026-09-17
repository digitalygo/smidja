package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func TestRunTUIResumeReplaysAndSeedsHistoryBeforeFirstTurn(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: []byte(`"prior question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{Role: "assistant", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "prior answer"}}, StopReason: "stop", Timestamp: 2}); err != nil {
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
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("fresh answer")}}
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	runtime := extensions.NewRuntime(extensions.NewRegistry())
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
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal), nil)
	}()
	output := waitForOutputSettled(t, terminal, "prior answer", 5*time.Second)
	if !strings.Contains(output, "prior question") {
		t.Fatalf("replayed transcript is missing the prior user message:\n%s", output)
	}
	if client.calls != 0 {
		t.Fatalf("resume replay must not execute a turn, client calls = %d", client.calls)
	}

	terminal.SendInput("follow up")
	terminal.SendInput("\r")
	waitForOutputSettled(t, terminal, "fresh answer", 5*time.Second)
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want exactly one turn", client.calls)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(client.reqs))
	}
	history := client.reqs[0].Messages
	foundPrior := false
	for _, message := range history {
		if message.User != nil && strings.Contains(string(message.User.Content), "prior question") {
			foundPrior = true
		}
	}
	if !foundPrior {
		t.Fatalf("the first turn after resume did not include the reconstructed prior history: %+v", history)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after cancel")
	}
}
