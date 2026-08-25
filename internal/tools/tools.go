// Package tools implements the four built-in tools of the Smidja Phase 0
// spike: read, write, edit, and exec. They are the only filesystem and
// command surface the model gets, and they all enforce the workspace
// containment rules: every path is routed through
// (*workspace.Workspace).Contain, and paths touching .git internals are
// rejected before any I/O happens.
//
// The exec tool is deliberately NOT a sandbox: it runs commands as the
// smidja process itself, with the full privileges of the invoking user,
// and the workspace boundary is the only confinement. The tool
// description says so explicitly, and it must stay that way until a real
// sandbox lands.
//
// Argument handling is lenient by design: unknown extra fields are
// ignored, missing required fields and type mismatches produce error
// results (never panics), and every file mutation is atomic (write to a
// temp file in the same directory, then rename).
package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/workspace"
)

// Result is the tool execution outcome, aliased from the agent contract so
// method signatures stay short.
type Result = agent.Result

// Truncation defaults, matching Pi (dist/core/tools/truncate.js): read
// and exec outputs are bounded by two independent limits, and whichever
// is hit first wins. Lines are never split mid-line.
const (
	defaultMaxLines = 2000
	defaultMaxBytes = 50 * 1024 // 50 KB
)

// Exec defaults and limits. The per-call timeout_secs argument defaults
// to the constructor value (defaultExecTimeoutSec when unset) and is
// clamped to maxExecTimeoutSec.
const (
	defaultExecTimeoutSec = 30
	maxExecTimeoutSec     = 120
)

// Deps carries the constructor dependencies for the built-in tools.
type Deps struct {
	// Workspace is the root every tool path must stay inside. All four
	// tools require it; exec additionally runs commands with it as the
	// working directory.
	Workspace *workspace.Workspace
	// ExecTimeoutSec is the default timeout for exec when the call does
	// not pass timeout_secs. Zero means defaultExecTimeoutSec.
	ExecTimeoutSec int
	// MaxOutputBytes is the exec byte cap: a truncated result keeps the
	// last whole lines that fit in this many bytes. Zero means
	// defaultMaxBytes (50 KB, matching Pi).
	MaxOutputBytes int64
	// MaxReadLines caps the numbered lines a read call returns. Zero
	// means defaultMaxLines (2000, matching Pi).
	MaxReadLines int
	// MaxReadBytes caps the raw content a read call returns, in bytes.
	// Zero means defaultMaxBytes (50 KB, matching Pi).
	MaxReadBytes int64
	// MaxExecLines caps the lines an exec result shows. Zero means
	// defaultMaxLines (2000, matching Pi).
	MaxExecLines int
}

// All returns the four built-in tools in registration order: read, write,
// edit, exec.
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

// decodeArgs unmarshals the raw tool arguments into dst, wrapping JSON
// syntax and type errors into an error result. Unknown extra fields are
// ignored, which keeps argument handling lenient.
func decodeArgs(tool string, raw json.RawMessage, dst any) Result {
	if err := json.Unmarshal(raw, dst); err != nil {
		return agent.ErrorResult(fmt.Sprintf("%s: invalid arguments: %v", tool, err))
	}
	return Result{}
}

// schema builds a JSON schema object with the given properties and
// required field names.
func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("tools: marshal schema: %v", err)) // schema literals are static
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

// contain resolves p through the workspace and applies the .git
// prohibition. It returns the cleaned absolute path and a result; the
// path is only meaningful when the result is not an error.
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

// formatSize renders a byte count the way Pi's truncate.js does: plain
// bytes under 1 KiB, one-decimal KB up to 1 MiB, one-decimal MB beyond.
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

// splitLines splits s into lines the way Pi's truncate.js does: on "\n",
// with a trailing empty segment dropped when s ends with a newline. An
// empty input yields no lines.
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

// readArgs is the argument shape of the read tool. Pointer fields
// distinguish "absent" from a zero value so missing required arguments
// are reported precisely.
type readArgs struct {
	Path   *string `json:"path"`
	Offset *int    `json:"offset"`
	Limit  *int    `json:"limit"`
}

// readTool implements agent.Tool for the "read" tool.
type readTool struct {
	ws       *workspace.Workspace
	maxLines int
	maxBytes int64
}

// Name returns "read".
func (t *readTool) Name() string { return "read" }

// Description returns one sentence describing the read tool.
func (t *readTool) Description() string {
	return "Reads a text file inside the workspace and returns its lines numbered like cat -n, with an optional 1-based offset and limit for paging; binary files are rejected and output is truncated to 2000 lines or 50 KB (whichever is hit first), with the full content saved to a temp file when truncated."
}

// Schema returns the JSON schema of the read arguments.
func (t *readTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"path":   strProp("Path to the file to read, relative to the workspace root."),
			"offset": intProp("1-based line number of the first line to return. Defaults to 1."),
			"limit":  intProp("Maximum number of lines to return. Defaults to returning the rest of the file."),
		},
		"path",
	)
}

// Exec runs the read tool. It returns at most maxLines lines and maxBytes
// bytes of content, whichever is hit first, with lines never split. The
// numbered output stops at the cap; hitting one appends a marker naming
// the lines shown and the temp file holding the full requested window.
// Binary files (a NUL byte in the first 512 bytes) and missing files are
// error results.
func (t *readTool) Exec(ctx context.Context, args json.RawMessage) Result {
	var a readArgs
	if res := decodeArgs("read", args, &a); res.IsError {
		return res
	}
	if a.Path == nil {
		return agent.ErrorResult("read: missing required argument 'path'")
	}
	full, res := contain(t.ws, *a.Path)
	if res.IsError {
		return res
	}
	offset := 1
	if a.Offset != nil {
		offset = *a.Offset
	}
	if offset < 1 {
		return agent.ErrorResult(fmt.Sprintf("read: offset must be a 1-based line number, got %d", offset))
	}
	limit := 0
	if a.Limit != nil {
		limit = *a.Limit
		if limit < 0 {
			return agent.ErrorResult(fmt.Sprintf("read: limit must be >= 0, got %d", limit))
		}
	}

	f, err := os.Open(full)
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}
	if st.IsDir() {
		return agent.ErrorResult(fmt.Sprintf("read %q: is a directory", *a.Path))
	}

	// Binary detection: a NUL byte anywhere in the first 512 bytes marks
	// the file as binary. A short read (file smaller than 512 bytes) is
	// fine; only the bytes actually read matter.
	probe := make([]byte, 512)
	n, _ := io.ReadFull(f, probe)
	if bytes.IndexByte(probe[:n], 0) >= 0 {
		return agent.ErrorResult("[binary file]")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}

	total, err := countLines(f)
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}
	if offset > total {
		return agent.ErrorResult(fmt.Sprintf("read %q: offset %d is beyond the end of the file (%d lines)", *a.Path, offset, total))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}

	br := bufio.NewReaderSize(f, 64<<10)
	end := total
	if limit > 0 && offset+limit-1 < end {
		end = offset + limit - 1
	}
	for ln := 1; ln < offset; ln++ {
		if err := skipLine(br); err != nil {
			return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
		}
	}

	// Stream the window: numbered lines accumulate in sb until a cap hits,
	// and the full window is mirrored to a temp file the moment that
	// happens so the model can read everything.
	var sb strings.Builder
	var rawBuf bytes.Buffer
	var tmp *os.File
	var tmpPath string
	keptBytes := int64(0) // raw bytes of the numbered lines kept so far
	lastShown := 0        // 0 until a line is emitted
	truncatedBy := ""     // "lines" or "bytes" once a cap stops the output
	firstLineExceeds := false
	lineExceedsSize := int64(0)

	defer func() {
		if tmp != nil {
			_ = tmp.Close() // left in place on purpose; the OS cleans the temp dir
		}
	}()

	ensureTemp := func() error {
		if tmp != nil {
			return nil
		}
		f, err := os.CreateTemp("", "smidja-read-*.log")
		if err != nil {
			return fmt.Errorf("read %q: create temp output file: %v", *a.Path, err)
		}
		tmp = f
		tmpPath = f.Name()
		if _, err := f.Write(rawBuf.Bytes()); err != nil {
			return fmt.Errorf("read %q: write temp output file: %v", *a.Path, err)
		}
		rawBuf.Reset()
		return nil
	}

	for ln := offset; ln <= end; ln++ {
		line, err := readLineRaw(br)
		if err != nil && !errors.Is(err, io.EOF) {
			return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
		}
		// Mirror every window line into the temp file (or the bounded
		// buffer before the temp file is needed).
		if tmp != nil {
			if _, err := tmp.Write(line); err != nil {
				return agent.ErrorResult(fmt.Sprintf("read %q: write temp output file: %v", *a.Path, err))
			}
		} else {
			rawBuf.Write(line)
		}

		body := line
		if len(body) > 0 && body[len(body)-1] == '\n' {
			body = body[:len(body)-1]
		}
		if truncatedBy == "" && !firstLineExceeds {
			switch {
			case ln == offset && int64(len(body)) > t.maxBytes:
				// The first line of the window alone exceeds the byte cap;
				// nothing can be shown.
				firstLineExceeds = true
				lineExceedsSize = int64(len(body))
			case lastShown >= offset+t.maxLines-1:
				truncatedBy = "lines"
			default:
				// Byte budget, Pi truncateHead accounting: each line costs
				// its own bytes plus one for the newline before it (except
				// the first kept line), and no line is ever split.
				lineBytes := int64(len(body))
				if keptBytes > 0 {
					lineBytes++
				}
				if keptBytes+lineBytes > t.maxBytes {
					truncatedBy = "bytes"
					break
				}
				fmt.Fprintf(&sb, "%6d\t", ln)
				sb.Write(body)
				sb.WriteByte('\n')
				lastShown = ln
				keptBytes += lineBytes
			}
		}
		if truncatedBy != "" || firstLineExceeds {
			if err := ensureTemp(); err != nil {
				return agent.ErrorResult(err.Error())
			}
		}
	}

	if total == 0 {
		return agent.TextResult("")
	}

	out := sb.String()
	switch {
	case firstLineExceeds:
		return agent.TextResult(fmt.Sprintf(
			"[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d. Full output: %s]",
			offset, formatSize(lineExceedsSize), formatSize(t.maxBytes), offset, *a.Path, t.maxBytes, tmpPath))
	case truncatedBy == "lines":
		return agent.TextResult(out + fmt.Sprintf(
			"\n[Showing lines %d-%d of %d. Use offset=%d to continue. Full output: %s]",
			offset, lastShown, total, lastShown+1, tmpPath))
	case truncatedBy == "bytes":
		return agent.TextResult(out + fmt.Sprintf(
			"\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue. Full output: %s]",
			offset, lastShown, total, formatSize(t.maxBytes), lastShown+1, tmpPath))
	default:
		return agent.TextResult(out)
	}
}

// readLineRaw reads the next line from br including its trailing newline;
// the last line of a file without a final newline is returned without one.
// err is io.EOF when the line is the last and lacks a final newline.
func readLineRaw(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		out = append(out, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return out, err // nil or io.EOF
	}
}

// skipLine consumes the next line of br without returning its content.
func skipLine(br *bufio.Reader) error {
	for {
		_, err := br.ReadSlice('\n')
		if err == nil {
			return nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

// countLines returns the number of lines in r: one per newline, plus one
// for a final line that does not end with a newline. An empty input has
// zero lines.
func countLines(r io.Reader) (int, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	total := 0
	readAny := false
	lastByteNewline := true
	for {
		b, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
		readAny = true
		if b == '\n' {
			total++
			lastByteNewline = true
		} else {
			lastByteNewline = false
		}
	}
	if readAny && !lastByteNewline {
		total++
	}
	return total, nil
}

// writeArgs is the argument shape of the write tool.
type writeArgs struct {
	Path    *string `json:"path"`
	Content *string `json:"content"`
}

// writeTool implements agent.Tool for the "write" tool.
type writeTool struct {
	ws *workspace.Workspace
}

// Name returns "write".
func (t *writeTool) Name() string { return "write" }

// Description returns one sentence describing the write tool.
func (t *writeTool) Description() string {
	return "Writes a file inside the workspace, creating parent directories as needed and replacing any existing file atomically."
}

// Schema returns the JSON schema of the write arguments.
func (t *writeTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"path":    strProp("Path to the file to write, relative to the workspace root."),
			"content": strProp("Full text content to write."),
		},
		"path", "content",
	)
}

// Exec runs the write tool. Content has no size limit (matching Pi); the
// file is written to a temp file in the target directory and renamed over
// the destination, so a crash never leaves a half-written file behind.
func (t *writeTool) Exec(ctx context.Context, args json.RawMessage) Result {
	var a writeArgs
	if res := decodeArgs("write", args, &a); res.IsError {
		return res
	}
	if a.Path == nil {
		return agent.ErrorResult("write: missing required argument 'path'")
	}
	if a.Content == nil {
		return agent.ErrorResult("write: missing required argument 'content'")
	}
	full, res := contain(t.ws, *a.Path)
	if res.IsError {
		return res
	}
	if err := atomicWrite(full, []byte(*a.Content)); err != nil {
		return agent.ErrorResult(fmt.Sprintf("write %q: %v", *a.Path, err))
	}
	return agent.TextResult(fmt.Sprintf("Wrote %d bytes to %s", len(*a.Content), *a.Path))
}

// editArgs is the argument shape of the edit tool.
type editArgs struct {
	Path       *string `json:"path"`
	OldText    *string `json:"oldText"`
	NewText    *string `json:"newText"`
	ReplaceAll *bool   `json:"replaceAll"`
}

// editTool implements agent.Tool for the "edit" tool.
type editTool struct {
	ws *workspace.Workspace
}

// Name returns "edit".
func (t *editTool) Name() string { return "edit" }

// Description returns one sentence describing the edit tool.
func (t *editTool) Description() string {
	return "Replaces literal text in a file inside the workspace, atomically; it errors when the search text matches more than once unless replaceAll is true."
}

// Schema returns the JSON schema of the edit arguments.
func (t *editTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"path":       strProp("Path to the file to edit, relative to the workspace root."),
			"oldText":    strProp("Literal text to find. Must match at least once."),
			"newText":    strProp("Replacement text."),
			"replaceAll": boolProp("Replace every occurrence instead of exactly one. Defaults to false."),
		},
		"path", "oldText", "newText",
	)
}

// Exec runs the edit tool. Matching is literal (no regex), and the
// replacement is applied atomically via the same temp-file-plus-rename
// strategy as write.
func (t *editTool) Exec(ctx context.Context, args json.RawMessage) Result {
	var a editArgs
	if res := decodeArgs("edit", args, &a); res.IsError {
		return res
	}
	if a.Path == nil {
		return agent.ErrorResult("edit: missing required argument 'path'")
	}
	if a.OldText == nil {
		return agent.ErrorResult("edit: missing required argument 'oldText'")
	}
	if a.NewText == nil {
		return agent.ErrorResult("edit: missing required argument 'newText'")
	}
	if *a.OldText == "" {
		return agent.ErrorResult("edit: oldText must not be empty")
	}
	full, res := contain(t.ws, *a.Path)
	if res.IsError {
		return res
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("edit %q: %v", *a.Path, err))
	}
	old, new := []byte(*a.OldText), []byte(*a.NewText)
	count := bytes.Count(data, old)
	if count == 0 {
		return agent.ErrorResult(fmt.Sprintf("edit %q: oldText not found in file", *a.Path))
	}
	replaceAll := a.ReplaceAll != nil && *a.ReplaceAll
	if count > 1 && !replaceAll {
		return agent.ErrorResult(fmt.Sprintf("edit %q: oldText matches %d times; pass replaceAll=true or use a more specific match", *a.Path, count))
	}
	if replaceAll {
		data = bytes.ReplaceAll(data, old, new)
	} else {
		data = bytes.Replace(data, old, new, 1)
	}
	if err := atomicWrite(full, data); err != nil {
		return agent.ErrorResult(fmt.Sprintf("edit %q: %v", *a.Path, err))
	}
	return agent.TextResult(fmt.Sprintf("Replaced %d occurrence(s) of the given text in %s", count, *a.Path))
}

// execArgs is the argument shape of the exec tool.
type execArgs struct {
	Command     *string `json:"command"`
	TimeoutSecs *int    `json:"timeout_secs"`
}

// execTool implements agent.Tool for the "exec" tool.
type execTool struct {
	ws             *workspace.Workspace
	defaultTimeout time.Duration
	maxLines       int
	maxBytes       int64
}

// Name returns "exec".
func (t *execTool) Name() string { return "exec" }

// Description returns one sentence describing the exec tool. It must stay
// explicit about the lack of a sandbox: exec is a real shell with the
// user's full privileges.
func (t *execTool) Description() string {
	return "Runs a shell command via /bin/sh -c with the workspace root as the working directory and a timeout, capturing combined output; output is truncated to the last 2000 lines or 50 KB (whichever is hit first), with the full output saved to a temp file when truncated; NOT a sandbox, commands run with the full privileges of the user."
}

// Schema returns the JSON schema of the exec arguments.
func (t *execTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"command":      strProp("Shell command to run with /bin/sh -c. Combined stdout and stderr are truncated to the last 2000 lines or 50 KB, whichever is hit first; a timeout kills the whole process group."),
			"timeout_secs": intProp("Timeout in seconds. Defaults to the harness value (30s), hard-capped at 120s."),
		},
		"command",
	)
}

// Exec runs the exec tool. The child shell gets its own process group so
// a timeout or context cancellation kills every descendant, not just the
// shell itself. Output is combined stdout+stderr, captured in full and
// mirrored to a temp file the moment either display cap is exceeded; the
// result keeps the last whole lines that fit within the caps.
func (t *execTool) Exec(ctx context.Context, args json.RawMessage) Result {
	var a execArgs
	if res := decodeArgs("exec", args, &a); res.IsError {
		return res
	}
	if a.Command == nil {
		return agent.ErrorResult("exec: missing required argument 'command'")
	}
	if strings.TrimSpace(*a.Command) == "" {
		return agent.ErrorResult("exec: command must not be empty")
	}
	if t.ws == nil {
		return agent.ErrorResult("tools: no workspace configured")
	}
	timeout, err := execTimeout(t.defaultTimeout, a.TimeoutSecs)
	if err != nil {
		return agent.ErrorResult(err.Error())
	}

	out := newExecOutput(t.maxLines, t.maxBytes)
	cmd := exec.Command("/bin/sh", "-c", *a.Command)
	cmd.Dir = t.ws.Root()
	cmd.Env = sanitizeEnv(os.Environ())
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return agent.ErrorResult(fmt.Sprintf("exec: start: %v", err))
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		killGroup(cmd)
		<-waitCh
		out.finish()
		return t.execResult(cmd, out, "[cancelled]")
	case <-timer.C:
		killGroup(cmd)
		<-waitCh
		out.finish()
		return t.execResult(cmd, out, fmt.Sprintf("[timed out after %ds]", int(timeout/time.Second)))
	case err := <-waitCh:
		_ = err // the exit code is read from the process state
	}
	out.finish()
	return t.execResult(cmd, out, "")
}

// execTimeout resolves the effective exec timeout: the per-call
// timeout_secs when given, else the constructor default. Values below 1
// are rejected; values above maxExecTimeoutSec are clamped to it.
func execTimeout(def time.Duration, secs *int) (time.Duration, error) {
	if secs == nil {
		return def, nil
	}
	if *secs < 1 {
		return 0, fmt.Errorf("exec: timeout_secs must be >= 1, got %d", *secs)
	}
	s := *secs
	if s > maxExecTimeoutSec {
		s = maxExecTimeoutSec
	}
	return time.Duration(s) * time.Second, nil
}

// execResult assembles the "exit code N\n<output>" text, appending the
// timeout or cancellation note when applicable. The output itself carries
// the truncation warning and marker when lines were dropped.
func (t *execTool) execResult(cmd *exec.Cmd, out *execOutput, note string) Result {
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	body := fmt.Sprintf("exit code %d\n%s", code, out.display())
	if note != "" {
		body += "\n" + note
	}
	return agent.TextResult(body)
}

// killGroup kills the whole process group of cmd. Setpgid makes the child
// the group leader, so a negative PID targets it and every descendant. It
// is safe to call concurrently with Wait.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// sanitizeEnv filters the child environment: the OpenRouter API key, every
// SMIDJA_* variable, and the Pi harness directory are not passed to
// commands. Everything else from os.Environ() passes through unchanged.
func sanitizeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name == "OPENROUTER_API_KEY" {
			continue
		}
		if strings.HasPrefix(name, "SMIDJA_") {
			continue
		}
		if name == "PI_CODING_AGENT_DIR" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// execOutput captures the combined stdout+stderr of one command with
// bounded memory. While the output fits the display caps it is kept
// entirely in memory; the moment either cap is exceeded the full output
// is mirrored to a temp file and only a bounded rolling tail is kept for
// the final truncation. The temp file is intentionally left behind: the
// model may read it, and the OS temp directory is cleaned up by the
// system.
type execOutput struct {
	maxLines   int
	maxBytes   int64
	tailBudget int

	buf       bytes.Buffer // full output while it fits the caps
	tmpPath   string
	tmpFile   *os.File
	tail      []byte // rolling tail once the temp file is open
	tailBound bool   // whether the rolling tail starts at a line boundary

	totalBytes  int64
	newlines    int64
	openLineLen int64 // bytes in the current unterminated line
	hasOpenLine bool
}

// newExecOutput returns an accumulator bounded by the given display caps.
func newExecOutput(maxLines int, maxBytes int64) *execOutput {
	tailBudget := int(maxBytes)
	if tailBudget < 1 {
		tailBudget = 1
	}
	return &execOutput{maxLines: maxLines, maxBytes: maxBytes, tailBudget: 2 * tailBudget}
}

// totalLines returns the number of lines seen so far: one per newline,
// plus one for a final unterminated line.
func (w *execOutput) totalLines() int64 {
	if w.hasOpenLine {
		return w.newlines + 1
	}
	return w.newlines
}

// Write appends p to the captured output.
func (w *execOutput) Write(p []byte) (int, error) {
	n := len(p)
	w.totalBytes += int64(n)
	w.noteWritten(p)
	if w.tmpFile != nil {
		if _, err := w.tmpFile.Write(p); err != nil {
			return 0, err
		}
		w.appendTail(p)
		return n, nil
	}
	w.buf.Write(p)
	if w.totalBytes > w.maxBytes || w.totalLines() > int64(w.maxLines) {
		if err := w.openTempFile(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// noteWritten folds p into the line counters.
func (w *execOutput) noteWritten(p []byte) {
	last := bytes.LastIndexByte(p, '\n')
	if last < 0 {
		if len(p) > 0 {
			w.openLineLen += int64(len(p))
			w.hasOpenLine = true
		}
		return
	}
	for _, b := range p {
		if b == '\n' {
			w.newlines++
		}
	}
	rest := p[last+1:]
	w.openLineLen = int64(len(rest))
	w.hasOpenLine = len(rest) > 0
}

// openTempFile starts mirroring the full output to a temp file, keeping
// the last tailBudget bytes as the rolling tail.
func (w *execOutput) openTempFile() error {
	if w.tmpFile != nil {
		return nil
	}
	f, err := os.CreateTemp("", "smidja-exec-*.log")
	if err != nil {
		return fmt.Errorf("exec: create temp output file: %w", err)
	}
	w.tmpPath = f.Name()
	w.tmpFile = f
	buf := w.buf.Bytes()
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("exec: write temp output file: %w", err)
	}
	w.tail = append([]byte(nil), buf...)
	w.buf.Reset()
	w.tailBound = true
	if len(w.tail) > w.tailBudget {
		cut := len(w.tail) - w.tailBudget
		w.tailBound = w.tail[cut-1] == '\n'
		w.tail = append([]byte(nil), w.tail[cut:]...)
	}
	return nil
}

// appendTail rolls p into the bounded tail.
func (w *execOutput) appendTail(p []byte) {
	w.tail = append(w.tail, p...)
	if len(w.tail) > 2*w.tailBudget {
		cut := len(w.tail) - w.tailBudget
		w.tailBound = w.tail[cut-1] == '\n'
		w.tail = append([]byte(nil), w.tail[cut:]...)
	}
}

// finish closes the temp file, leaving it in place.
func (w *execOutput) finish() {
	if w.tmpFile != nil {
		_ = w.tmpFile.Close()
		w.tmpFile = nil
	}
}

// snapshotTail returns the rolling tail with any leading partial line
// dropped, so it always begins at a line boundary.
func (w *execOutput) snapshotTail() []byte {
	tail := w.tail
	if !w.tailBound {
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		} else {
			tail = nil
		}
	}
	return tail
}

// lastLineLen returns the byte size of the final line of the output.
func (w *execOutput) lastLineLen() int64 {
	if w.hasOpenLine {
		return w.openLineLen
	}
	tail := w.tail
	i := bytes.LastIndexByte(tail, '\n')
	if i < 0 {
		return int64(len(tail))
	}
	if i == len(tail)-1 {
		j := bytes.LastIndexByte(tail[:i], '\n')
		return int64(i - j - 1)
	}
	return int64(len(tail) - i - 1)
}

// display renders the kept output: the full content when it fits the
// caps, otherwise the truncated tail plus the warning line and the marker
// pointing at the temp file holding the full output.
func (w *execOutput) display() string {
	truncated := w.totalBytes > w.maxBytes || w.totalLines() > int64(w.maxLines)
	if !truncated {
		return w.buf.String()
	}
	totalLines := w.totalLines()
	lastLine := w.lastLineLen()
	lastLineExceeds := lastLine > w.maxBytes

	content := ""
	outputLines := 0
	truncatedBy := "bytes"
	if !lastLineExceeds {
		res := truncateTail(string(w.snapshotTail()), w.maxLines, w.maxBytes)
		content = res.content
		outputLines = res.outputLines
		truncatedBy = res.truncatedBy
		if !res.truncated {
			// The tail slice alone fits the caps, but the true output did
			// not: show the whole slice and blame the true counters.
			if w.totalBytes <= w.maxBytes {
				truncatedBy = "lines"
			}
			outputLines = len(splitLines(content))
		}
	}

	limit := formatSize(w.maxBytes)
	var sb strings.Builder
	sb.WriteString(content)
	switch {
	case lastLineExceeds:
		fmt.Fprintf(&sb, "\n\nTruncated: 0 lines shown (%s limit)\n[Last line is %s, exceeds %s limit. Full output: %s]",
			limit, formatSize(lastLine), limit, w.tmpPath)
	case truncatedBy == "lines":
		fmt.Fprintf(&sb, "\n\nTruncated: showing %d of %d lines\n[Showing lines %d-%d of %d. Full output: %s]",
			outputLines, totalLines, totalLines-int64(outputLines)+1, totalLines, totalLines, w.tmpPath)
	default:
		fmt.Fprintf(&sb, "\n\nTruncated: %d lines shown (%s limit)\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
			outputLines, limit, totalLines-int64(outputLines)+1, totalLines, totalLines, limit, w.tmpPath)
	}
	return sb.String()
}

// truncateTail keeps the last whole lines of content that fit within
// maxLines lines and maxBytes bytes, whichever is hit first, mirroring
// Pi's truncate.js truncateTail. Lines are never split; when the final
// line alone exceeds the byte cap nothing is shown and lastLineExceeds
// is set.
type truncateResult struct {
	content         string
	truncated       bool
	truncatedBy     string // "lines" or "bytes"; "" when not truncated
	outputLines     int
	outputBytes     int64
	lastLineExceeds bool
}

func truncateTail(content string, maxLines int, maxBytes int64) truncateResult {
	lines := splitLines(content)
	totalBytes := int64(len(content))
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return truncateResult{content: content, truncated: false, outputLines: totalLines, outputBytes: totalBytes}
	}
	kept := make([]string, 0, maxLines)
	var bytes int64
	truncatedBy := "lines"
	for i := totalLines - 1; i >= 0 && len(kept) < maxLines; i-- {
		lineBytes := int64(len(lines[i]))
		if len(kept) > 0 {
			lineBytes++ // newline between kept lines
		}
		if bytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(kept) == 0 {
				return truncateResult{
					content: "", truncated: true, truncatedBy: "bytes",
					outputLines: 0, outputBytes: 0, lastLineExceeds: true,
				}
			}
			break
		}
		kept = append(kept, lines[i])
		bytes += lineBytes
	}
	if len(kept) >= maxLines && bytes <= maxBytes {
		truncatedBy = "lines"
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	content = strings.Join(kept, "\n")
	return truncateResult{
		content: content, truncated: true, truncatedBy: truncatedBy,
		outputLines: len(kept), outputBytes: int64(len(content)),
	}
}

// atomicWrite writes data to path atomically: a temp file is created in
// the same directory, written, synced, and renamed over path. On any
// failure the temp file is removed and path is left untouched.
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
	defer os.Remove(tmpName) // no-op after a successful rename
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
