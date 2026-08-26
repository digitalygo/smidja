package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/workspace"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type execArgs struct {
	Command     *string `json:"command"`
	TimeoutSecs *int    `json:"timeout_secs"`
}

type execTool struct {
	ws             *workspace.Workspace
	defaultTimeout time.Duration
	maxLines       int
	maxBytes       int64
}

func (t *execTool) Name() string { return "exec" }

func (t *execTool) Description() string {
	return "Runs a shell command via /bin/sh -c with the workspace root as the working directory and a timeout, capturing combined output; output is truncated to the last 2000 lines or 50 KB (whichever is hit first), with the full output saved to a temp file when truncated; NOT a sandbox, commands run with the full privileges of the user."
}

func (t *execTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"command":      strProp("Shell command to run with /bin/sh -c. Combined stdout and stderr are truncated to the last 2000 lines or 50 KB, whichever is hit first; a timeout kills the whole process group."),
			"timeout_secs": intProp("Timeout in seconds. Defaults to the harness value (30s), hard-capped at 120s."),
		},
		"command",
	)
}

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
		_ = err
	}
	out.finish()
	return t.execResult(cmd, out, "")
}

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

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

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

type execOutput struct {
	maxLines   int
	maxBytes   int64
	tailBudget int

	buf       bytes.Buffer
	tmpPath   string
	tmpFile   *os.File
	tail      []byte
	tailBound bool

	totalBytes  int64
	newlines    int64
	openLineLen int64
	hasOpenLine bool
}

func newExecOutput(maxLines int, maxBytes int64) *execOutput {
	tailBudget := int(maxBytes)
	if tailBudget < 1 {
		tailBudget = 1
	}
	return &execOutput{maxLines: maxLines, maxBytes: maxBytes, tailBudget: 2 * tailBudget}
}

func (w *execOutput) totalLines() int64 {
	if w.hasOpenLine {
		return w.newlines + 1
	}
	return w.newlines
}

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

func (w *execOutput) appendTail(p []byte) {
	w.tail = append(w.tail, p...)
	if len(w.tail) > 2*w.tailBudget {
		cut := len(w.tail) - w.tailBudget
		w.tailBound = w.tail[cut-1] == '\n'
		w.tail = append([]byte(nil), w.tail[cut:]...)
	}
}

func (w *execOutput) finish() {
	if w.tmpFile != nil {
		_ = w.tmpFile.Close()
		w.tmpFile = nil
	}
}

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

type truncateResult struct {
	content         string
	truncated       bool
	truncatedBy     string
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
			lineBytes++
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
