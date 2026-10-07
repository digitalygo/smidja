//go:build linux

package cli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui"
)

type agentPTYAbortClient struct {
	mu      sync.Mutex
	calls   int
	cancels int
	once    sync.Once
	started chan struct{}
}

func (c *agentPTYAbortClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	c.mu.Lock()
	c.cancels++
	c.mu.Unlock()
	return nil, ctx.Err()
}

func (c *agentPTYAbortClient) snapshot() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.cancels
}

func TestTUIRealPTYAgentAbortMidChild(t *testing.T) {
	master, slave := smokePTYOpen(t)
	before := smokePTYTermios(t, slave)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	client := &agentPTYAbortClient{started: make(chan struct{})}
	var depsStderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return workspace, nil }
	deps.Home = func() string { return t.TempDir() }
	deps.Stdin = slave
	deps.Stdout = slave
	deps.Stderr = &depsStderr
	deps.Client = client
	deps.Config = testConfig(t, workspace)
	deps.Config.TUIMode = "fullscreen"
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "---\ndescription: reader\n---\nchild body"})
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps(nil, deps)
	}()
	capture.waitFor(t, tui.AltScreenEnter, 5*time.Second)
	smokePTYWrite(t, master, "/agent reader hello\r")
	select {
	case <-client.started:
	case <-time.After(10 * time.Second):
		t.Fatalf("child stream never started; output=%q stderr=%q", capture.snapshot(), depsStderr.String())
	}
	smokePTYWrite(t, master, "\x1b")
	waitUntil(t, 10*time.Second, "child stream was not canceled by the interrupt", func() bool {
		_, cancels := client.snapshot()
		return cancels >= 1
	})
	smokePTYWrite(t, master, "/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithDeps: %v (stderr %q)", err, depsStderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("/quit did not stop the TUI after the aborted agent command")
	}
	if after := smokePTYTermios(t, slave); after != before {
		t.Fatal("terminal attributes were not restored after the aborted child")
	}
	parent := parentSessionPath(t, deps.Store.Root())
	loader, err := session.LoadWithOptions(parent, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomEntry); ok && custom.CustomType == subagentResultCustomType {
			t.Fatalf("aborted child persisted a subagent result: %s", custom.Data)
		}
	}
	if paths := childSessionPaths(t, deps.Store.Root()); len(paths) != 1 {
		t.Fatalf("aborted child sessions = %v, want the single canceled child session", paths)
	}
	if calls, cancels := client.snapshot(); calls != 1 || cancels != 1 {
		t.Fatalf("child calls/cancels = %d/%d, want 1/1", calls, cancels)
	}
	if !strings.Contains(capture.snapshot(), tui.AltScreenEnter) {
		t.Fatal("alt screen lifecycle was not preserved")
	}
}
