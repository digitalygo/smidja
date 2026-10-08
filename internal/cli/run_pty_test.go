//go:build linux

package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/tui"
)

func TestRunSubcommandNeverStartsTUIOnRealTTY(t *testing.T) {
	master, slave := smokePTYOpen(t)
	before := smokePTYTermios(t, slave)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	deps, client, _, stderr := runSubcommandDeps(t, workspace, textStop("pty run answer"))
	deps.Stdin = slave
	deps.Stdout = slave
	if err := RunWithDeps([]string{"run", "-model", "test/model", "hello pty"}, deps); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, stderr.String())
	}
	capture.drain(t, 500*time.Millisecond)
	output := capture.snapshot()
	if !smokePTYContains(output, "pty run answer") {
		t.Fatalf("output missing the response:\n%q", output)
	}
	if smokePTYContains(output, tui.AltScreenEnter) {
		t.Fatalf("run entered the alternate screen:\n%q", output)
	}
	if smokePTYContains(output, tui.AltScreenExit) {
		t.Fatalf("run exited the alternate screen:\n%q", output)
	}
	if client.lastUserText() != "hello pty" {
		t.Fatalf("wire prompt = %q", client.lastUserText())
	}
	after := smokePTYTermios(t, slave)
	if after != before {
		t.Fatal("run changed the terminal attributes")
	}
}

func TestTUIExplicitPromptBeatsCollidingExtension(t *testing.T) {
	master, slave := openCLIPTY(t)
	var stderr bytes.Buffer
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("tui host answer"), textStop("tui extension answer")}}
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = slave
	deps.Stdout = slave
	deps.Stderr = &stderr
	deps.Home = func() string { return t.TempDir() }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = promptBundle(map[string]string{"greet": "hello $1"})
	ext := &promptCollidingExtension{}
	deps.ExtensionRuntime = promptExtensionRuntime(t, ext)

	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps([]string{"--tui-mode", "regular"}, deps)
	}()
	readMasterUntil(t, master, "]0;smidja", 10*time.Second)
	if _, err := master.Write([]byte("/prompt greet Bob\r")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	readMasterUntil(t, master, "tui host answer", 10*time.Second)
	if _, err := master.Write([]byte("/prompt2 tail\r")); err != nil {
		t.Fatalf("write alias prompt: %v", err)
	}
	readMasterUntil(t, master, "tui extension answer", 10*time.Second)
	if _, err := master.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("write quit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunWithDeps did not exit after /quit")
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
}
