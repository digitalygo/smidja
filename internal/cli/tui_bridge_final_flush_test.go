package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type flushSequenceClient struct {
	mu        sync.Mutex
	calls     int
	entered   chan struct{}
	enterOnce sync.Once
}

func (c *flushSequenceClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == 1 {
		return nil, errors.New("flush boom failure")
	}
	c.enterOnce.Do(func() { close(c.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func waitForOutputSettled(t *testing.T, terminal *fakeBridgeTerminal, fragment string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	stableSince := time.Time{}
	var snapshot string
	for {
		output := terminal.Output()
		if strings.Contains(output, fragment) {
			if output == snapshot {
				if !stableSince.IsZero() && time.Since(stableSince) >= 60*time.Millisecond {
					return output
				}
			} else {
				snapshot = output
				stableSince = time.Now()
			}
		} else {
			stableSince = time.Time{}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q to settle, output:\n%s", fragment, terminal.Output())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRunTUIRegularFlushesFinalMarkersBeforeStop(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	terminal := newFakeBridgeTerminal()
	client := &flushSequenceClient{entered: make(chan struct{})}
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
	rd := &runDeps{
		model:       "test/model",
		sessionPath: sess.Path(),
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeRegular, cwd, cwd, nil, bridgeTerminalFactory(terminal))
	}()
	select {
	case <-terminal.startedC:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	terminal.SendInput("first failing prompt")
	terminal.SendInput("\r")
	waitForOutputSettled(t, terminal, "flush boom failure", 5*time.Second)
	terminal.SendInput("second slow prompt")
	terminal.SendInput("\r")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("second turn did not start after the failed first turn")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("runTUI = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the canceled turn")
	}
	output := terminal.Output()
	if !strings.Contains(output, "flush boom failure") {
		t.Fatalf("final error notice was not flushed before terminal restore:\n%s", output)
	}
	if !strings.Contains(output, "interrupted") {
		t.Fatalf("interruption notice was not flushed before terminal restore:\n%s", output)
	}
	if got := terminal.StopCount(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	frozen := terminal.Output()
	time.Sleep(60 * time.Millisecond)
	if got := terminal.Output(); got != frozen {
		t.Fatalf("terminal received writes after runTUI returned:\n%s", got)
	}
}
