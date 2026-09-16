//go:build linux

package cli

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type panicPTYClient struct {
	entered chan struct{}
	once    sync.Once
}

func (c *panicPTYClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.once.Do(func() { close(c.entered) })
	panic("pty turn exploded")
}

func TestTUIRealPTYPanicRestoresTerminal(t *testing.T) {
	master, slave := smokePTYOpen(t)
	before := smokePTYTermios(t, slave)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	home := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	client := &panicPTYClient{entered: make(chan struct{})}
	hookRuntime := extensions.NewRuntime(extensions.NewRegistry())
	var rdStdout bytes.Buffer
	var rdStderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return home },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &rdStderr,
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
		catalog:     extensions.NewToolCatalog(),
		commands:    extensions.NewCommandCatalog(),
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
		done <- runTUI(context.Background(), deps, rd, lineUI, ui.TUIModeFullscreen, workspace, workspace, nil, factory)
	}()
	capture.waitFor(t, tui.AltScreenEnter, 5*time.Second)
	capture.waitFor(t, tui.OSCTitle("smidja"), 5*time.Second)
	smokePTYWrite(t, master, "explode now\r")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("panicking turn did not start")
	}
	capture.waitFor(t, workerPanicNotice, 5*time.Second)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not exit after the worker panic")
	}
	capture.drain(t, 500*time.Millisecond)
	full := capture.snapshot()
	exit := smokePTYIndex(full, tui.AltScreenExit)
	if exit < 0 {
		t.Fatalf("output missing alt-screen exit:\n%q", full)
	}
	final := tui.StripTerminalSequences(full[exit:])
	if !smokePTYContains(final, workerPanicNotice) {
		t.Fatalf("final document after exit missing the panic notice:\n%q", final)
	}
	after := smokePTYTermios(t, slave)
	if after != before {
		t.Fatal("terminal attributes were not restored after the worker panic")
	}
	if smokePTYContains(rdStdout.String(), workerPanicNotice) {
		t.Fatalf("panic notice leaked to direct stdout: %q", rdStdout.String())
	}
}
