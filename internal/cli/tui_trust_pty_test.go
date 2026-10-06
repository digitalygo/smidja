//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/sdk"
)

const trustInstructionMarker = "PROJECT-INSTRUCTION-MARKER"

func writeTrustWorkspace(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte(trustInstructionMarker), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(workspace, ".smidja", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "demo.md"), []byte("# demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpConfig := `{"schemaVersion":1,"servers":{"ws":{"enabled":true,"command":"smidja-mcp-does-not-exist"}}}`
	if err := os.WriteFile(filepath.Join(workspace, ".smidja", "mcp.json"), []byte(mcpConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	return workspace
}

type trustScenario struct {
	requests      []*agent.TurnRequest
	stderr        string
	sessionStarts []string
	sessionStops  []string
	termiosBefore syscall.Termios
	termiosAfter  syscall.Termios
	err           error
}

func runTrustScenario(t *testing.T, decision string, allowWorkspaceMCP bool) trustScenario {
	t.Helper()
	master, slave := openCLIPTY(t)
	before := smokePTYTermios(t, slave)
	workspace := writeTrustWorkspace(t)
	home := t.TempDir()
	store := wiringStore(t)
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("trust answer")}}
	recorder := &sessionStartRecorder{}
	runtime := extensions.NewRuntime(mustRegister(t, &sessionStartExtension{rec: recorder}))
	var stderr bytes.Buffer
	deps := &Deps{
		Env:              envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": t.TempDir()}),
		Getwd:            func() (string, error) { return workspace, nil },
		Home:             func() string { return home },
		Stdin:            slave,
		Stdout:           slave,
		Stderr:           &stderr,
		Client:           client,
		Store:            store,
		Config:           testConfig(t, workspace),
		ExtensionRuntime: runtime,
	}
	done := make(chan error, 1)
	args := []string{"--tui-mode", "regular"}
	if allowWorkspaceMCP {
		args = append(args, "--allow-workspace-mcp")
	}
	go func() {
		done <- RunWithDeps(args, deps)
	}()
	readMasterUntil(t, master, "Project trust", 10*time.Second)
	if _, err := master.Write([]byte(decision)); err != nil {
		t.Fatalf("write trust decision: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := master.Write([]byte("hello trust\r")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	readMasterUntil(t, master, "trust answer", 10*time.Second)
	if _, err := master.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("write quit: %v", err)
	}
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunWithDeps did not exit after /quit")
	}
	time.Sleep(50 * time.Millisecond)
	return trustScenario{
		requests:      append([]*agent.TurnRequest(nil), client.reqs...),
		stderr:        stderr.String(),
		sessionStarts: append([]string(nil), recorder.starts...),
		sessionStops:  append([]string(nil), recorder.shutdowns...),
		termiosBefore: before,
		termiosAfter:  smokePTYTermios(t, slave),
		err:           runErr,
	}
}

func TestRunChatAcceptsWorkspaceTrust(t *testing.T) {
	result := runTrustScenario(t, "y", true)
	if result.err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", result.err, result.stderr)
	}
	if len(result.requests) != 1 {
		t.Fatalf("turn requests = %d, want 1", len(result.requests))
	}
	if !strings.Contains(result.requests[0].System, trustInstructionMarker) {
		t.Fatalf("system prompt missing project instructions:\n%s", result.requests[0].System)
	}
	if !strings.Contains(result.stderr, "unavailable, skipping") {
		t.Fatalf("stderr = %q, want the workspace MCP spawn attempt", result.stderr)
	}
	if result.termiosBefore != result.termiosAfter {
		t.Fatalf("terminal state not restored: before %+v after %+v", result.termiosBefore, result.termiosAfter)
	}
	if len(result.sessionStarts) != 1 || result.sessionStarts[0] != string(sdk.SessionStartStartup) {
		t.Fatalf("session starts = %v, want exactly one startup", result.sessionStarts)
	}
	if len(result.sessionStops) != 1 || result.sessionStops[0] != string(sdk.SessionShutdownQuit) {
		t.Fatalf("session stops = %v, want exactly one quit", result.sessionStops)
	}
}

func TestRunChatRefusesWorkspaceTrust(t *testing.T) {
	result := runTrustScenario(t, "n", true)
	if result.err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", result.err, result.stderr)
	}
	if len(result.requests) != 1 {
		t.Fatalf("turn requests = %d, want 1", len(result.requests))
	}
	if strings.Contains(result.requests[0].System, trustInstructionMarker) {
		t.Fatalf("system prompt must not contain project instructions after refusal:\n%s", result.requests[0].System)
	}
	if !strings.Contains(result.stderr, "workspace-defined server") {
		t.Fatalf("stderr = %q, want the workspace MCP skip", result.stderr)
	}
	if strings.Contains(result.stderr, "unavailable, skipping") {
		t.Fatalf("stderr = %q, a refused workspace must not spawn workspace MCP", result.stderr)
	}
	if result.termiosBefore != result.termiosAfter {
		t.Fatalf("terminal state not restored: before %+v after %+v", result.termiosBefore, result.termiosAfter)
	}
	if len(result.sessionStarts) != 1 || result.sessionStarts[0] != string(sdk.SessionStartStartup) {
		t.Fatalf("session starts = %v, want exactly one after refusal", result.sessionStarts)
	}
}

func TestRunChatTrustDoesNotEnableWorkspaceMCP(t *testing.T) {
	result := runTrustScenario(t, "y", false)
	if result.err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", result.err, result.stderr)
	}
	if !strings.Contains(result.stderr, "workspace-defined server") {
		t.Fatalf("stderr = %q, want the workspace MCP skip without the flag", result.stderr)
	}
	if strings.Contains(result.stderr, "unavailable, skipping") {
		t.Fatalf("stderr = %q, trust must not opt into workspace MCP", result.stderr)
	}
}

func TestRunChatStartupCanceledDuringTrust(t *testing.T) {
	master, slave := openCLIPTY(t)
	before := smokePTYTermios(t, slave)
	workspace := writeTrustWorkspace(t)
	home := t.TempDir()
	store := wiringStore(t)
	client := &capturingClient{}
	var stderr bytes.Buffer
	deps := &Deps{
		Env:     envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": t.TempDir()}),
		Getwd:   func() (string, error) { return workspace, nil },
		Home:    func() string { return home },
		Stdin:   slave,
		Stdout:  slave,
		Stderr:  &stderr,
		Client:  client,
		Store:   store,
		Config:  testConfig(t, workspace),
		Context: nil,
	}
	ctx, cancel := context.WithCancel(context.Background())
	deps.Context = ctx
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps([]string{"--tui-mode", "fullscreen", "--allow-workspace-mcp"}, deps)
	}()
	readMasterUntil(t, master, "Project trust", 10*time.Second)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("RunWithDeps = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunWithDeps did not exit after the context was canceled")
	}
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want none before startup completed", client.calls)
	}
	time.Sleep(50 * time.Millisecond)
	after := smokePTYTermios(t, slave)
	if before != after {
		t.Fatalf("terminal state not restored: before %+v after %+v", before, after)
	}
}

func TestRunChatStartupInvalidMCPConfig(t *testing.T) {
	master, slave := openCLIPTY(t)
	before := smokePTYTermios(t, slave)
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".smidja"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".smidja", "mcp.json"), []byte(`{nope`), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &capturingClient{}
	var stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": t.TempDir()}),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &stderr,
		Client: client,
		Store:  wiringStore(t),
		Config: testConfig(t, workspace),
	}
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps([]string{"--tui-mode", "regular"}, deps)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "mcp") {
			t.Fatalf("RunWithDeps = %v, want the mcp config error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunWithDeps did not exit after the mcp config error")
	}
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want none before startup completed", client.calls)
	}
	time.Sleep(50 * time.Millisecond)
	after := smokePTYTermios(t, slave)
	if before != after {
		t.Fatalf("terminal state not restored: before %+v after %+v", before, after)
	}
	_ = master
}

func TestRunChatStartupSignalDuringTrust(t *testing.T) {
	master, slave := openCLIPTY(t)
	before := smokePTYTermios(t, slave)
	workspace := writeTrustWorkspace(t)
	client := &capturingClient{}
	var stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": t.TempDir()}),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &stderr,
		Client: client,
		Store:  wiringStore(t),
		Config: testConfig(t, workspace),
	}
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps([]string{"--tui-mode", "regular", "--allow-workspace-mcp"}, deps)
	}()
	readMasterUntil(t, master, "Project trust", 10*time.Second)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithDeps = %v, want a clean exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunWithDeps did not exit after the signal")
	}
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want none before startup completed", client.calls)
	}
	time.Sleep(50 * time.Millisecond)
	after := smokePTYTermios(t, slave)
	if before != after {
		t.Fatalf("terminal state not restored: before %+v after %+v", before, after)
	}
}
