//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui"
)

type agentPTYClient struct {
	mu    sync.Mutex
	calls int
}

func (c *agentPTYClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	text := "pty child answer 3a1f"
	if onText != nil {
		onText(text)
	}
	return textStop(text), nil
}

func (c *agentPTYClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestTUIRealPTYAgentCommandCleanup(t *testing.T) {
	master, slave := smokePTYOpen(t)
	before := smokePTYTermios(t, slave)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	client := &agentPTYClient{}
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
	deps.Bundle = agentBundle(map[string]string{"reader": "---\ndescription: reader\n---\nYou are the child agent."})
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps(nil, deps)
	}()
	capture.waitFor(t, tui.AltScreenEnter, 5*time.Second)
	smokePTYWrite(t, master, "/agent reader hello\r")
	capture.waitFor(t, "pty child answer 3a1f", 10*time.Second)
	smokePTYWrite(t, master, "/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithDeps: %v (stderr %q)", err, depsStderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("/quit did not stop the TUI")
	}
	after := smokePTYTermios(t, slave)
	if after != before {
		t.Fatal("terminal attributes were not restored after the TUI exit")
	}
	if client.callCount() != 1 {
		t.Fatalf("child calls = %d, want 1", client.callCount())
	}
	parentLoader, err := session.LoadWithOptions(parentSessionPath(t, deps.Store.Root()), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range parentLoader.Entries() {
		custom, ok := entry.(*session.CustomEntry)
		if !ok || custom.CustomType != subagentResultCustomType {
			continue
		}
		var payload subagentResultEntry
		if err := json.Unmarshal(custom.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Agent == "reader" && strings.Contains(payload.Result, "pty child answer 3a1f") {
			found = true
		}
	}
	if !found {
		t.Fatal("TUI /agent did not persist the subagent result")
	}
	if len(childSessionPaths(t, deps.Store.Root())) != 1 {
		t.Fatalf("child sessions = %v", childSessionPaths(t, deps.Store.Root()))
	}
}
