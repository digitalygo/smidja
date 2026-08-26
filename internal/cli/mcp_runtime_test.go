package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/mcp"
)

func helperServerConfig() mcp.ServerConfig {
	return mcp.ServerConfig{
		Enabled: true,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess"},
		Env:     map[string]string{mcpHelperEnv: "1"},
		Framing: "ndjson",
	}
}

func TestLoadMCPConfigMergeAndWorkspaceIDs(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeMCPFixture(t, filepath.Join(home, ".smidja", "mcp.json"), `{
		"schemaVersion": 1,
		"servers": {
			"user-only": {"enabled": true, "command": "/bin/user"},
			"shared": {"enabled": true, "command": "/bin/user-shared", "args": ["old"]},
			"tombstoned": {"enabled": true, "command": "/bin/tomb"}
		}
	}`)
	writeMCPFixture(t, filepath.Join(cwd, ".smidja", "mcp.json"), `{
		"schemaVersion": 1,
		"servers": {
			"shared": {"enabled": true, "command": "/bin/ws-shared", "args": ["new"], "toolPrefix": "ws"},
			"tombstoned": {"enabled": false},
			"ws-only": {"enabled": true, "command": "/bin/ws"}
		}
	}`)

	cfg, workspaceIDs, err := loadMCPConfig(home, cwd)
	if err != nil {
		t.Fatalf("loadMCPConfig: %v", err)
	}
	if _, ok := cfg.Servers["tombstoned"]; ok {
		t.Fatal("tombstoned server still present after merge")
	}
	if _, ok := cfg.Servers["user-only"]; !ok {
		t.Fatal("user-only server missing")
	}
	shared, ok := cfg.Servers["shared"]
	if !ok {
		t.Fatal("shared server missing")
	}
	if shared.Command != "/bin/ws-shared" || len(shared.Args) != 1 || shared.Args[0] != "new" {
		t.Fatalf("shared server not replaced by the workspace entry: %+v", shared)
	}
	if _, ok := cfg.Servers["ws-only"]; !ok {
		t.Fatal("ws-only server missing")
	}
	if !workspaceIDs["shared"] || !workspaceIDs["ws-only"] {
		t.Fatalf("workspaceIDs = %v, want shared and ws-only", workspaceIDs)
	}
	if workspaceIDs["user-only"] || workspaceIDs["tombstoned"] {
		t.Fatalf("workspaceIDs = %v, must not contain user-only or tombstoned", workspaceIDs)
	}
}

func TestLoadMCPConfigMissingFiles(t *testing.T) {
	cfg, workspaceIDs, err := loadMCPConfig(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("loadMCPConfig: %v", err)
	}
	if cfg == nil || len(cfg.Servers) != 0 {
		t.Fatalf("cfg = %+v, want empty", cfg)
	}
	if len(workspaceIDs) != 0 {
		t.Fatalf("workspaceIDs = %v, want empty", workspaceIDs)
	}
}

func TestLoadMCPConfigInvalidUserConfigFails(t *testing.T) {
	home := t.TempDir()
	writeMCPFixture(t, filepath.Join(home, ".smidja", "mcp.json"), `{not json`)
	if _, _, err := loadMCPConfig(home, t.TempDir()); err == nil {
		t.Fatal("loadMCPConfig accepted an invalid user config")
	}
}

func TestStartMCPRegistersToolsFromHelper(t *testing.T) {
	catalog := extensions.NewToolCatalog()
	var stderr bytes.Buffer
	cfg := &mcp.FileConfig{SchemaVersion: 1, Servers: map[string]mcp.ServerConfig{
		"helper": helperServerConfig(),
	}}
	rt, err := startMCP(context.Background(), cfg, nil, false, catalog, nil, &stderr)
	if err != nil {
		t.Fatalf("startMCP: %v", err)
	}
	defer rt.Close()
	tool, ok := catalog.Get("helper_helper_echo")
	if !ok {
		t.Fatalf("helper tool missing from the catalog; got %v", catalog.Names())
	}
	if tool.Description() != "echo back the arguments" {
		t.Fatalf("helper tool description = %q", tool.Description())
	}
	res := tool.Exec(context.Background(), json.RawMessage(`{"value":"hi"}`))
	if res.IsError || len(res.Content) != 1 || res.Content[0].Text != "helper-result" {
		t.Fatalf("helper call result = %+v", res)
	}
	infos := catalog.AllInfo()
	if len(infos) != 1 || infos[0].Source != "mcp:helper" {
		t.Fatalf("AllInfo() = %+v, want source mcp:helper", infos)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestStartMCPRequiredUnreachableFails(t *testing.T) {
	catalog := extensions.NewToolCatalog()
	cfg := &mcp.FileConfig{SchemaVersion: 1, Servers: map[string]mcp.ServerConfig{
		"broken": {Enabled: true, Required: true, Command: "/nonexistent-mcp-binary", Framing: "ndjson"},
	}}
	_, err := startMCP(context.Background(), cfg, nil, false, catalog, nil, io.Discard)
	if err == nil {
		t.Fatal("startMCP succeeded with an unreachable required server")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error = %v, want the server id", err)
	}
	if len(catalog.Names()) != 0 {
		t.Fatalf("catalog = %v, want empty after the failure", catalog.Names())
	}
}

func TestStartMCPOptionalUnreachableSkips(t *testing.T) {
	catalog := extensions.NewToolCatalog()
	var stderr bytes.Buffer
	cfg := &mcp.FileConfig{SchemaVersion: 1, Servers: map[string]mcp.ServerConfig{
		"flaky": {Enabled: true, Command: "/nonexistent-mcp-binary", Framing: "ndjson"},
	}}
	rt, err := startMCP(context.Background(), cfg, nil, false, catalog, nil, &stderr)
	if err != nil {
		t.Fatalf("startMCP: %v", err)
	}
	rt.Close()
	if len(catalog.Names()) != 0 {
		t.Fatalf("catalog = %v, want empty", catalog.Names())
	}
	if !strings.Contains(stderr.String(), "flaky") {
		t.Fatalf("stderr = %q, want the skip notice with the server id", stderr.String())
	}
}

func TestStartMCPWorkspaceFailClosed(t *testing.T) {
	catalog := extensions.NewToolCatalog()
	var stderr bytes.Buffer
	cfg := &mcp.FileConfig{SchemaVersion: 1, Servers: map[string]mcp.ServerConfig{
		"helper": helperServerConfig(),
	}}
	workspaceIDs := map[string]bool{"helper": true}
	rt, err := startMCP(context.Background(), cfg, workspaceIDs, false, catalog, nil, &stderr)
	if err != nil {
		t.Fatalf("startMCP: %v", err)
	}
	rt.Close()
	if len(catalog.Names()) != 0 {
		t.Fatalf("catalog = %v, want no tools without --allow-workspace-mcp", catalog.Names())
	}
	if !strings.Contains(stderr.String(), "--allow-workspace-mcp") {
		t.Fatalf("stderr = %q, want the flag hint", stderr.String())
	}
}

func TestStartMCPWorkspaceAllowed(t *testing.T) {
	catalog := extensions.NewToolCatalog()
	var stderr bytes.Buffer
	cfg := &mcp.FileConfig{SchemaVersion: 1, Servers: map[string]mcp.ServerConfig{
		"helper": helperServerConfig(),
	}}
	workspaceIDs := map[string]bool{"helper": true}
	rt, err := startMCP(context.Background(), cfg, workspaceIDs, true, catalog, nil, &stderr)
	if err != nil {
		t.Fatalf("startMCP: %v", err)
	}
	defer rt.Close()
	if _, ok := catalog.Get("helper_helper_echo"); !ok {
		t.Fatalf("helper tool missing with --allow-workspace-mcp; got %v", catalog.Names())
	}
}

func TestStartMCPNilConfig(t *testing.T) {
	rt, err := startMCP(context.Background(), nil, nil, false, extensions.NewToolCatalog(), nil, io.Discard)
	if err != nil {
		t.Fatalf("startMCP(nil): %v", err)
	}
	rt.Close()
}

func TestMCPRuntimeCloseIsIdempotent(t *testing.T) {
	rt := &mcpRuntime{}
	rt.Close()
	rt.Close()
}

func writeMCPFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
