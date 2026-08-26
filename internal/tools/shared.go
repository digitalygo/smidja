package tools

import (
	"encoding/json"
	"fmt"
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Result = agent.Result

const (
	defaultMaxLines = 2000
	defaultMaxBytes = 50 * 1024
)

const (
	defaultExecTimeoutSec = 30
	maxExecTimeoutSec     = 120
)

type Deps struct {
	Workspace      *workspace.Workspace
	ExecTimeoutSec int
	MaxOutputBytes int64
	MaxReadLines   int
	MaxReadBytes   int64
	MaxExecLines   int
}

func All(d Deps) []agent.Tool {
	timeout := d.ExecTimeoutSec
	if timeout <= 0 {
		timeout = defaultExecTimeoutSec
	}
	readLines := d.MaxReadLines
	if readLines <= 0 {
		readLines = defaultMaxLines
	}
	readBytes := d.MaxReadBytes
	if readBytes <= 0 {
		readBytes = defaultMaxBytes
	}
	execLines := d.MaxExecLines
	if execLines <= 0 {
		execLines = defaultMaxLines
	}
	execBytes := d.MaxOutputBytes
	if execBytes <= 0 {
		execBytes = defaultMaxBytes
	}
	return []agent.Tool{
		&readTool{ws: d.Workspace, maxLines: readLines, maxBytes: readBytes},
		&writeTool{ws: d.Workspace},
		&editTool{ws: d.Workspace},
		&execTool{ws: d.Workspace, defaultTimeout: time.Duration(timeout) * time.Second, maxLines: execLines, maxBytes: execBytes},
	}
}

func decodeArgs(tool string, raw json.RawMessage, dst any) Result {
	if err := json.Unmarshal(raw, dst); err != nil {
		return agent.ErrorResult(fmt.Sprintf("%s: invalid arguments: %v", tool, err))
	}
	return Result{}
}

func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("tools: marshal schema: %v", err))
	}
	return b
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func contain(ws *workspace.Workspace, p string) (string, Result) {
	if ws == nil {
		return "", agent.ErrorResult("tools: no workspace configured")
	}
	full, err := ws.Contain(p)
	if err != nil {
		return "", agent.ErrorResult(fmt.Sprintf("path %q rejected: %v", p, err))
	}
	if workspace.IsForbidden(full) {
		return "", agent.ErrorResult(fmt.Sprintf("path %q rejected: .git internals are off limits", p))
	}
	return full, Result{}
}

func formatSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".smidja-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file over target: %w", err)
	}
	return nil
}
