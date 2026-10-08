//go:build linux

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type ptyHostClient struct {
	mu      sync.Mutex
	calls   int
	cancels int
}

func (c *ptyHostClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	switch call {
	case 1, 3:
		<-ctx.Done()
		c.mu.Lock()
		c.cancels++
		c.mu.Unlock()
		return nil, ctx.Err()
	case 2:
		if onText != nil {
			onText("after abort answer")
		}
		return textStop("after abort answer"), nil
	default:
		return nil, errors.New("ptyHostClient: unexpected call")
	}
}

func (c *ptyHostClient) snapshot() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.cancels
}

func waitPTYClient(t *testing.T, client *ptyHostClient, calls, cancels int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		gotCalls, gotCancels := client.snapshot()
		if gotCalls >= calls && gotCancels >= cancels {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	gotCalls, gotCancels := client.snapshot()
	t.Fatalf("client calls = %d cancels = %d, want >= %d and >= %d", gotCalls, gotCancels, calls, cancels)
}

func TestTUIRealPTYHostAbortAndShutdown(t *testing.T) {
	master, slave := smokePTYOpen(t)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	home := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	client := &ptyHostClient{}
	extension := &hostHookExtension{
		id:       "pty-host-hooks",
		contexts: []sdk.HandlerContext{},
	}
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		switch call {
		case 0:
			ctx.Abort()
		case 2:
			ctx.Shutdown()
		}
		return nil, nil
	}
	registry := extensions.NewRegistry()
	if err := registry.Register(extension); err != nil {
		t.Fatal(err)
	}
	hookRuntime := extensions.NewRuntime(registry)
	catalog := extensions.NewToolCatalog()
	host := newHostRuntime(context.Background(), workspace, nil, catalog)
	api := extensions.NewAPI(extensions.APIOptions{Catalog: catalog, Host: host.hostOptions()})
	host.bindAPI(api)
	host.setModel(models.NewRegistry(), "test/model", "test/model", "openrouter")
	host.setSystem("be terse")
	host.setWindow(128000)
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), workspace, "")
	hookRuntime.SetAPI(func() sdk.API { return api })
	hookRuntime.SetContext(func() sdk.HandlerContext { return host.context() })
	if err := hookRuntime.Start(); err != nil {
		t.Fatal(err)
	}
	var depsStderr bytes.Buffer
	var rdStdout bytes.Buffer
	var rdStderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return home },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &depsStderr,
	}
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &rdStdout,
		stderr:      &rdStderr,
		hooks:       hookRuntime.Dispatcher(),
		retry:       retryAdapter,
		retryPolicy: agent.RetryPolicy{Enabled: false},
		catalog:     catalog,
		commands:    extensions.NewCommandCatalog(),
		host:        host,
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return hookRuntime.HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	factory := func(io.Reader, io.Writer) tui.Terminal {
		return tui.NewProcessTerminal(slave, slave)
	}
	done := make(chan error, 1)
	go func() {
		done <- runTUI(context.Background(), deps, rd, lineUI, ui.TUIModeFullscreen, workspace, workspace, nil, factory, hookRuntime, nil)
	}()
	capture.waitFor(t, tui.AltScreenEnter, 5*time.Second)
	smokePTYWrite(t, master, "abort me\r")
	capture.waitFor(t, "interrupted", 10*time.Second)
	waitPTYClient(t, client, 1, 1, 5*time.Second)

	smokePTYWrite(t, master, "normal\r")
	capture.waitFor(t, "after abort answer", 10*time.Second)
	waitPTYClient(t, client, 2, 1, 5*time.Second)

	smokePTYWrite(t, master, "shutdown\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not stop the real PTY run")
	}
	calls, cancels := client.snapshot()
	if calls != 3 || cancels != 2 {
		t.Fatalf("calls = %d cancels = %d, want 3 and 2", calls, cancels)
	}
	capture.drain(t, 500*time.Millisecond)
	full := capture.snapshot()
	if !strings.Contains(full, tui.AltScreenEnter) || !strings.Contains(full, tui.AltScreenExit) {
		t.Fatalf("alt screen lifecycle was not clean:\n%q", full)
	}
}

func TestTUIRealPTYInitialThinkingLabelMatchesProviderDefault(t *testing.T) {
	master, slave := smokePTYOpen(t)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	home := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	registry := models.NewRegistry()
	registry.Register("test/model", models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	hookRuntime := extensions.NewRuntime(extensions.NewRegistry())
	catalog := extensions.NewToolCatalog()
	host := newHostRuntime(context.Background(), workspace, nil, catalog)
	api := extensions.NewAPI(extensions.APIOptions{Catalog: catalog, Host: host.hostOptions()})
	host.bindAPI(api)
	host.setModelRegistry(registry)
	host.setModel(registry, "test/model", "test/model", "openrouter")
	host.setReasoningSeam(true)
	host.setSystem("be terse")
	host.setWindow(128000)
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), workspace, "")
	hookRuntime.SetAPI(func() sdk.API { return api })
	hookRuntime.SetContext(func() sdk.HandlerContext { return host.context() })
	if err := hookRuntime.Start(); err != nil {
		t.Fatal(err)
	}
	var depsStderr bytes.Buffer
	var rdStdout bytes.Buffer
	var rdStderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return home },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &depsStderr,
	}
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      &capturingClient{},
		recorder:    &sessionRecorder{sess},
		stdout:      &rdStdout,
		stderr:      &rdStderr,
		hooks:       hookRuntime.Dispatcher(),
		catalog:     catalog,
		commands:    extensions.NewCommandCatalog(),
		host:        host,
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return hookRuntime.HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	factory := func(io.Reader, io.Writer) tui.Terminal {
		return tui.NewProcessTerminal(slave, slave)
	}
	done := make(chan error, 1)
	go func() {
		done <- runTUI(context.Background(), deps, rd, lineUI, ui.TUIModeFullscreen, workspace, workspace, nil, factory, hookRuntime, nil)
	}()
	capture.waitFor(t, tui.AltScreenEnter, 5*time.Second)
	capture.waitFor(t, "test/model • default", 5*time.Second)
	if strings.Contains(capture.snapshot(), "test/model • off") {
		t.Fatalf("initial footer claimed reasoning was off:\n%q", capture.snapshot())
	}
	smokePTYWrite(t, master, "/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not exit after /quit")
	}
}
