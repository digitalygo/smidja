package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func TestTUIModeFlagRejectsUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--tui-mode", "bogus"}, testDeps("", &stdout, &stderr))
	if err == nil {
		t.Fatal("unknown tui mode: want error")
	}
	if !strings.Contains(stderr.String(), "regular or fullscreen") {
		t.Errorf("stderr = %q, want the valid values", stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: smidja") {
		t.Errorf("stderr = %q, want usage", stderr.String())
	}
}

func TestTUIModeFlagValidation(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen", "Regular", "FULLSCREEN"} {
		var stdout, stderr bytes.Buffer
		deps := wiringTestDeps(t.TempDir())
		deps.Stdout = &stdout
		deps.Stderr = &stderr
		deps.Stdin = strings.NewReader("")
		deps.Client = &capturingClient{}
		deps.Config = testConfig(t, t.TempDir())
		deps.Store = wiringStore(t)
		if err := RunWithDeps([]string{"--tui-mode", mode}, deps); err != nil {
			t.Errorf("run --tui-mode %s: %v (stderr %q)", mode, err, stderr.String())
		}
		if strings.Contains(stderr.String(), "tui unavailable") {
			t.Errorf("--tui-mode %s with pipes should stay on line mode, stderr = %q", mode, stderr.String())
		}
	}
}

func TestUsageDocumentsTUIMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-h"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("run -h: %v", err)
	}
	usage := stderr.String()
	if !strings.Contains(usage, "-tui-mode mode") {
		t.Errorf("usage missing the tui-mode flag:\n%s", usage)
	}
	if !strings.Contains(usage, "regular|fullscreen") {
		t.Errorf("usage missing the renderer values:\n%s", usage)
	}
}

func TestSingleShotIgnoresTUIMode(t *testing.T) {
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("one shot answer")}}
	var stdout, stderr bytes.Buffer
	cwd := t.TempDir()
	deps := wiringTestDeps(t.TempDir())
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	if err := RunWithDeps([]string{"-p", "hello", "-model", "test/model", "--tui-mode", "fullscreen"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "one shot answer") {
		t.Fatalf("stdout = %q, want the response", stdout.String())
	}
}

type signalClient struct {
	inner    *capturingClient
	finished chan struct{}
	once     chan struct{}
}

func (c *signalClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	message, err := c.inner.StreamTurn(ctx, req, onText, onThinking)
	select {
	case <-c.once:
	default:
		close(c.once)
		close(c.finished)
	}
	return message, err
}

func TestRunTUIEndToEnd(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sessionPath := sess.Path()
	terminal := newFakeBridgeTerminal()
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
	client := &signalClient{
		inner:    &capturingClient{script: []*agent.AssistantMessage{textStop("end to end answer")}},
		finished: make(chan struct{}),
		once:     make(chan struct{}),
	}
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sessionPath,
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		hooks:       runtime.Dispatcher(),
		commands:    extensions.NewCommandCatalog(),
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return runtime.HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	done := make(chan error, 1)
	go func() {
		done <- runTUI(context.Background(), deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal), nil)
	}()
	select {
	case <-terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	for _, key := range []string{"h", "i", "\r"} {
		terminal.SendInput(key)
	}
	select {
	case <-client.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("async turn did not reach the client")
	}
	for _, key := range []string{"/", "q", "u", "i", "t", "\r"} {
		terminal.SendInput(key)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not exit after /quit")
	}
	if strings.Contains(stdout.String(), "end to end answer") {
		t.Errorf("agent text leaked to terminal stdout: %q", stdout.String())
	}
	transcript, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !strings.Contains(string(transcript), "end to end answer") {
		t.Errorf("session missing the assistant response:\n%s", transcript)
	}
	if !strings.Contains(string(transcript), "hi") {
		t.Errorf("session missing the user prompt:\n%s", transcript)
	}
}
