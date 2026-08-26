package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/workspace"
)

func newTestDeps(t *testing.T) Deps {
	t.Helper()
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}
	return Deps{Workspace: ws}
}

func toolByName(t *testing.T, deps Deps, name string) agent.Tool {
	t.Helper()
	for _, tl := range All(deps) {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("tool %q not registered by All", name)
	return nil
}

func run(t *testing.T, tl agent.Tool, args string) agent.Result {
	t.Helper()
	return tl.Exec(context.Background(), json.RawMessage(args))
}

func text(t *testing.T, r agent.Result) string {
	t.Helper()
	if len(r.Content) == 0 {
		return ""
	}
	if len(r.Content) > 1 {
		t.Fatalf("result has %d content blocks, want 1", len(r.Content))
	}
	return r.Content[0].Text
}

func mustWriteTo(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", rel, err)
	}
	return string(data)
}

func TestAllRegistersToolsInOrder(t *testing.T) {
	tools := All(newTestDeps(t))
	want := []string{"read", "write", "edit", "exec"}
	if len(tools) != len(want) {
		t.Fatalf("All returned %d tools, want %d", len(tools), len(want))
	}
	for i, w := range want {
		if tools[i].Name() != w {
			t.Errorf("tool[%d].Name() = %q, want %q", i, tools[i].Name(), w)
		}
		var s map[string]any
		if err := json.Unmarshal(tools[i].Schema(), &s); err != nil {
			t.Errorf("%s Schema is not valid JSON: %v", w, err)
		}
		if s["type"] != "object" {
			t.Errorf("%s Schema type = %v, want object", w, s["type"])
		}
		if tools[i].Description() == "" {
			t.Errorf("%s Description is empty", w)
		}
	}
}

func TestReadNormal(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "notes.txt", "hello\nworld\n")

	res := run(t, toolByName(t, deps, "read"), `{"path": "notes.txt"}`)
	if res.IsError {
		t.Fatalf("read failed: %s", text(t, res))
	}
	if want := "     1\thello\n     2\tworld\n"; text(t, res) != want {
		t.Errorf("read = %q, want %q", text(t, res), want)
	}
}

func TestReadTrailingLineWithoutNewline(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "a\nb")

	res := run(t, toolByName(t, deps, "read"), `{"path": "f.txt"}`)
	if res.IsError {
		t.Fatalf("read failed: %s", text(t, res))
	}
	if want := "     1\ta\n     2\tb\n"; text(t, res) != want {
		t.Errorf("read = %q, want %q", text(t, res), want)
	}
}

func TestReadOffsetLimitWindow(t *testing.T) {
	deps := newTestDeps(t)
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line%d\n", i)
	}
	mustWriteTo(t, deps.Workspace.Root(), "ten.txt", sb.String())

	res := run(t, toolByName(t, deps, "read"), `{"path": "ten.txt", "offset": 3, "limit": 4}`)
	if res.IsError {
		t.Fatalf("read failed: %s", text(t, res))
	}
	got := text(t, res)
	if want := "     3\tline3\n     4\tline4\n     5\tline5\n     6\tline6\n"; got != want {
		t.Errorf("read window = %q, want %q", got, want)
	}
	if strings.Contains(got, "line7") {
		t.Errorf("read window leaks line7: %q", got)
	}
	if strings.Contains(got, "Output truncated") {
		t.Errorf("read window has truncation marker without hitting caps: %q", got)
	}
}

func TestReadOffsetBeyondEOF(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "a\nb\n")

	res := run(t, toolByName(t, deps, "read"), `{"path": "f.txt", "offset": 5}`)
	if !res.IsError {
		t.Errorf("read with offset beyond EOF: want error, got %q", text(t, res))
	}
}

func TestReadTruncatesAtLineCap(t *testing.T) {
	deps := newTestDeps(t)
	var sb strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&sb, "line%d\n", i)
	}
	full := sb.String()
	mustWriteTo(t, deps.Workspace.Root(), "big.txt", full)

	res := run(t, toolByName(t, deps, "read"), `{"path": "big.txt"}`)
	if res.IsError {
		t.Fatalf("read failed: %s", text(t, res))
	}
	got := text(t, res)
	if n := strings.Count(got, "\tline"); n != defaultMaxLines {
		t.Errorf("read returned %d numbered lines, want %d", n, defaultMaxLines)
	}
	if !strings.Contains(got, "  2000\tline2000") {
		t.Errorf("read does not show line 2000: %q", got)
	}
	if !strings.Contains(got, "[Showing lines 1-2000 of 5000. Use offset=2001 to continue. Full output: ") {
		t.Errorf("read missing Pi-style truncation marker: %q", got)
	}
	path := fullOutputPath(t, got)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if string(data) != full {
		t.Errorf("temp file content = %d bytes, want the full %d-byte file", len(data), len(full))
	}
	if got := text(t, res); strings.Contains(got, "line4999") {
		t.Errorf("numbered output leaks beyond the line cap: %q", got)
	}
}

func TestReadTruncatesAtByteCap(t *testing.T) {
	deps := newTestDeps(t)
	deps.MaxReadBytes = 4096
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		sb.WriteString(strings.Repeat("a", 100))
		sb.WriteByte('\n')
	}
	full := sb.String()
	mustWriteTo(t, deps.Workspace.Root(), "wide.txt", full)

	res := run(t, toolByName(t, deps, "read"), `{"path": "wide.txt"}`)
	if res.IsError {
		t.Fatalf("read failed: %s", text(t, res))
	}
	got := text(t, res)
	if !strings.HasPrefix(got, "     1\t") {
		t.Errorf("read does not start with line 1: %q", got[:min(20, len(got))])
	}
	if !strings.Contains(got, "(4.0KB limit)") {
		t.Errorf("read missing byte-cap marker with the limit named: %q", got)
	}
	if !strings.Contains(got, "[Showing lines 1-") {
		t.Errorf("read missing Showing-lines marker: %q", got)
	}
	contentPart := strings.SplitN(got, "\n[Showing", 2)[0]
	if len(contentPart) > int(deps.MaxReadBytes)+512 {
		t.Errorf("read returned %d bytes, want capped near %d", len(contentPart), deps.MaxReadBytes)
	}
	path := fullOutputPath(t, got)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if string(data) != full {
		t.Errorf("temp file content = %d bytes, want the full %d-byte window", len(data), len(full))
	}
}

func TestReadFirstLineExceedsByteCap(t *testing.T) {
	deps := newTestDeps(t)
	deps.MaxReadBytes = 1024
	mustWriteTo(t, deps.Workspace.Root(), "huge.txt", strings.Repeat("b", 3000)+"\n")

	res := run(t, toolByName(t, deps, "read"), `{"path": "huge.txt"}`)
	if res.IsError {
		t.Fatalf("read failed: %s", text(t, res))
	}
	got := text(t, res)
	if !strings.Contains(got, "[Line 1 is 2.9KB, exceeds 1.0KB limit") {
		t.Errorf("read missing first-line-exceeds marker: %q", got)
	}
	if !strings.Contains(got, "Full output: ") {
		t.Errorf("read marker missing the temp file path: %q", got)
	}
	path := fullOutputPath(t, got)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if string(data) != strings.Repeat("b", 3000)+"\n" {
		t.Errorf("temp file content = %d bytes, want the full window", len(data))
	}
}

func fullOutputPath(t *testing.T, res string) string {
	t.Helper()
	const prefix = "Full output: "
	i := strings.LastIndex(res, prefix)
	if i < 0 {
		t.Fatalf("result missing 'Full output:' marker: %q", res)
	}
	rest := res[i+len(prefix):]
	rest = strings.TrimSuffix(rest, "]")
	return strings.TrimSpace(rest)
}

func TestReadMissingFile(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "read"), `{"path": "nope.txt"}`)
	if !res.IsError {
		t.Errorf("read of missing file: want error, got %q", text(t, res))
	}
}

func TestReadBinaryFile(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "blob.bin", "abc\x00def")

	res := run(t, toolByName(t, deps, "read"), `{"path": "blob.bin"}`)
	if !res.IsError {
		t.Errorf("read of binary file: want error, got %q", text(t, res))
	}
	if got := text(t, res); got != "[binary file]" {
		t.Errorf("read binary error text = %q, want %q", got, "[binary file]")
	}
}

func TestReadRejectsGitPath(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "read"), `{"path": ".git/config"}`)
	if !res.IsError {
		t.Errorf("read of .git path: want error, got %q", text(t, res))
	}
}

func TestReadIgnoresExtraArgs(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "hi\n")

	res := run(t, toolByName(t, deps, "read"), `{"path": "f.txt", "bogus": 42, "extra": [1, 2]}`)
	if res.IsError {
		t.Fatalf("read with extra args failed: %s", text(t, res))
	}
	if want := "     1\thi\n"; text(t, res) != want {
		t.Errorf("read = %q, want %q", text(t, res), want)
	}
}

func TestWriteCreateNested(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "write"), `{"path": "a/b/c.txt", "content": "nested\n"}`)
	if res.IsError {
		t.Fatalf("write failed: %s", text(t, res))
	}
	if got := readFile(t, deps.Workspace.Root(), "a/b/c.txt"); got != "nested\n" {
		t.Errorf("written content = %q, want %q", got, "nested\n")
	}
	if !strings.Contains(text(t, res), "a/b/c.txt") {
		t.Errorf("write result does not name the file: %q", text(t, res))
	}
}

func TestWriteOverwritesExisting(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "old")

	run(t, toolByName(t, deps, "write"), `{"path": "f.txt", "content": "new"}`)
	if got := readFile(t, deps.Workspace.Root(), "f.txt"); got != "new" {
		t.Errorf("overwritten content = %q, want %q", got, "new")
	}
}

func TestWriteRejectsGitPath(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "write"), `{"path": ".git/config", "content": "x"}`)
	if !res.IsError {
		t.Errorf("write to .git: want error, got %q", text(t, res))
	}
	if _, err := os.Stat(filepath.Join(deps.Workspace.Root(), ".git")); !os.IsNotExist(err) {
		t.Errorf(".git directory was created despite rejection")
	}
}

func TestWriteRejectsTraversal(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "write"), `{"path": "../outside", "content": "x"}`)
	if !res.IsError {
		t.Errorf("write outside workspace: want error, got %q", text(t, res))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(deps.Workspace.Root()), "outside")); !os.IsNotExist(err) {
		t.Errorf("file was created outside the workspace")
	}
}

func TestWriteAcceptsLargeContent(t *testing.T) {
	deps := newTestDeps(t)
	big := strings.Repeat("x", 3<<20)
	res := run(t, toolByName(t, deps, "write"), `{"path": "big.txt", "content": "`+big+`"}`)
	if res.IsError {
		t.Fatalf("write of large content failed: %s", text(t, res))
	}
	if got := readFile(t, deps.Workspace.Root(), "big.txt"); got != big {
		t.Errorf("large write content mismatch: got %d bytes, want %d", len(got), len(big))
	}
}

func TestEditUniqueReplace(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "foo bar baz")

	res := run(t, toolByName(t, deps, "edit"), `{"path": "f.txt", "oldText": "bar", "newText": "qux"}`)
	if res.IsError {
		t.Fatalf("edit failed: %s", text(t, res))
	}
	if got := readFile(t, deps.Workspace.Root(), "f.txt"); got != "foo qux baz" {
		t.Errorf("edited content = %q, want %q", got, "foo qux baz")
	}
	if !strings.Contains(text(t, res), "1 occurrence") {
		t.Errorf("edit result = %q, want mention of 1 occurrence", text(t, res))
	}
}

func TestEditAmbiguousMatchErrors(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "x y x")

	res := run(t, toolByName(t, deps, "edit"), `{"path": "f.txt", "oldText": "x", "newText": "q"}`)
	if !res.IsError {
		t.Errorf("ambiguous edit: want error, got %q", text(t, res))
	}
	if !strings.Contains(text(t, res), "2 times") {
		t.Errorf("ambiguous edit error should state the match count: %q", text(t, res))
	}
	if got := readFile(t, deps.Workspace.Root(), "f.txt"); got != "x y x" {
		t.Errorf("file changed despite ambiguous error: %q", got)
	}
}

func TestEditReplaceAll(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "x y x")

	res := run(t, toolByName(t, deps, "edit"), `{"path": "f.txt", "oldText": "x", "newText": "q", "replaceAll": true}`)
	if res.IsError {
		t.Fatalf("replaceAll edit failed: %s", text(t, res))
	}
	if got := readFile(t, deps.Workspace.Root(), "f.txt"); got != "q y q" {
		t.Errorf("edited content = %q, want %q", got, "q y q")
	}
	if !strings.Contains(text(t, res), "2 occurrence") {
		t.Errorf("edit result = %q, want mention of 2 occurrences", text(t, res))
	}
}

func TestEditNoMatchErrors(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "abc")

	res := run(t, toolByName(t, deps, "edit"), `{"path": "f.txt", "oldText": "zzz", "newText": "q"}`)
	if !res.IsError {
		t.Errorf("no-match edit: want error, got %q", text(t, res))
	}
}

func TestEditEmptyOldTextErrors(t *testing.T) {
	deps := newTestDeps(t)
	mustWriteTo(t, deps.Workspace.Root(), "f.txt", "abc")

	res := run(t, toolByName(t, deps, "edit"), `{"path": "f.txt", "oldText": "", "newText": "q"}`)
	if !res.IsError {
		t.Errorf("empty oldText: want error, got %q", text(t, res))
	}
}

func TestEditMissingFileErrors(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "edit"), `{"path": "nope.txt", "oldText": "a", "newText": "b"}`)
	if !res.IsError {
		t.Errorf("edit of missing file: want error, got %q", text(t, res))
	}
}

func TestEditRejectsGitPath(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "edit"), `{"path": ".git/config", "oldText": "a", "newText": "b"}`)
	if !res.IsError {
		t.Errorf("edit of .git path: want error, got %q", text(t, res))
	}
}

func TestExecEcho(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "exec"), `{"command": "echo hello"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	if want := "exit code 0\nhello\n"; text(t, res) != want {
		t.Errorf("exec = %q, want %q", text(t, res), want)
	}
}

func TestExecExitCodePropagation(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "exec"), `{"command": "exit 3"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	if want := "exit code 3\n"; text(t, res) != want {
		t.Errorf("exec = %q, want %q", text(t, res), want)
	}
}

func TestExecTimeoutKillsProcessGroup(t *testing.T) {
	deps := newTestDeps(t)
	start := time.Now()
	res := run(t, toolByName(t, deps, "exec"), `{"command": "sleep 10", "timeout_secs": 1}`)
	elapsed := time.Since(start)

	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	if !strings.Contains(text(t, res), "[timed out after 1s]") {
		t.Errorf("exec result missing timeout marker: %q", text(t, res))
	}
	if elapsed > 5*time.Second {
		t.Errorf("exec took %v after 1s timeout, process group was not killed", elapsed)
	}
}

func TestExecOutputTruncatesAtByteCap(t *testing.T) {
	deps := newTestDeps(t)
	deps.MaxOutputBytes = 4096
	res := run(t, toolByName(t, deps, "exec"), `{"command": "seq 1 2000"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	got := text(t, res)
	if !strings.HasPrefix(got, "exit code 0\n") {
		t.Errorf("exec result missing exit code prefix: %q", got)
	}
	if !strings.Contains(got, "\n2000\n") {
		t.Errorf("exec tail lost the last line: %q", got)
	}
	if strings.Contains(got, "\n1\n") {
		t.Errorf("exec result shows the head instead of the tail: %q", got)
	}
	if !strings.Contains(got, "Truncated: ") || !strings.Contains(got, "lines shown (4.0KB limit)") {
		t.Errorf("exec missing warning line 'Truncated: N lines shown (4.0KB limit)': %q", got)
	}
	if !strings.Contains(got, "[Showing lines ") || !strings.Contains(got, "(4.0KB limit). Full output: ") {
		t.Errorf("exec missing Pi-style marker with temp file path: %q", got)
	}
	path := fullOutputPath(t, got)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("exec temp file: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2000 || lines[0] != "1" || lines[1999] != "2000" {
		t.Errorf("temp file = %d lines (first %q last %q), want lines 1..2000", len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestExecOutputTruncatesAtLineCap(t *testing.T) {
	deps := newTestDeps(t)
	deps.MaxExecLines = 100
	res := run(t, toolByName(t, deps, "exec"), `{"command": "seq 1 500"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	got := text(t, res)
	if !strings.Contains(got, "\n500\n") {
		t.Errorf("exec tail lost the last line: %q", got)
	}
	if strings.Contains(got, "\n1\n") {
		t.Errorf("exec result shows the head instead of the tail: %q", got)
	}
	if !strings.Contains(got, "Truncated: showing 100 of 500 lines") {
		t.Errorf("exec missing line-cap warning: %q", got)
	}
	if !strings.Contains(got, "[Showing lines 401-500 of 500. Full output: ") {
		t.Errorf("exec missing line-cap marker: %q", got)
	}
	if strings.Contains(got, "(50.0KB limit)") {
		t.Errorf("line-cap marker must not name a byte limit: %q", got)
	}
	path := fullOutputPath(t, got)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("exec temp file: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 500 || lines[0] != "1" || lines[499] != "500" {
		t.Errorf("temp file = %d lines (first %q last %q), want lines 1..500", len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestExecOutputSingleHugeLine(t *testing.T) {
	deps := newTestDeps(t)
	deps.MaxOutputBytes = 4096
	res := run(t, toolByName(t, deps, "exec"), `{"command": "head -c 20000 /dev/zero | tr '\\0' a"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	got := text(t, res)
	if !strings.Contains(got, "Truncated: 0 lines shown (4.0KB limit)") {
		t.Errorf("exec missing zero-lines warning: %q", got)
	}
	if !strings.Contains(got, "[Last line is 19.5KB, exceeds 4.0KB limit. Full output: ") {
		t.Errorf("exec missing last-line-exceeds marker: %q", got)
	}
	path := fullOutputPath(t, got)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("exec temp file: %v", err)
	}
	if len(data) != 20000 {
		t.Errorf("temp file = %d bytes, want 20000", len(data))
	}
}

func TestExecSmallOutputNoTruncation(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "exec"), `{"command": "printf 'a\\nb\\n'"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	got := text(t, res)
	if want := "exit code 0\na\nb\n"; got != want {
		t.Errorf("exec = %q, want %q (no truncation, no marker)", got, want)
	}
	if strings.Contains(got, "Full output: ") {
		t.Errorf("small exec output must not create a temp file: %q", got)
	}
}

func TestExecStripsSensitiveEnv(t *testing.T) {
	deps := newTestDeps(t)
	t.Setenv("OPENROUTER_API_KEY", "sk-super-secret")
	t.Setenv("PI_CODING_AGENT_DIR", "/secret/pi/dir")
	t.Setenv("SMIDJA_TEST_VAR", "smidja-secret")
	t.Setenv("TOOLS_KEEP_VAR", "kept-value")

	res := run(t, toolByName(t, deps, "exec"), `{"command": "printenv OPENROUTER_API_KEY; echo rc=$?"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	got := text(t, res)
	if strings.Contains(got, "sk-super-secret") {
		t.Errorf("OPENROUTER_API_KEY leaked into child env: %q", got)
	}
	if !strings.Contains(got, "rc=1") {
		t.Errorf("OPENROUTER_API_KEY was not stripped (printenv should exit 1): %q", got)
	}

	res = run(t, toolByName(t, deps, "exec"), `{"command": "printenv SMIDJA_TEST_VAR"}`)
	if got := text(t, res); !strings.HasPrefix(got, "exit code 1") {
		t.Errorf("SMIDJA_TEST_VAR was not stripped: %q", got)
	}

	res = run(t, toolByName(t, deps, "exec"), `{"command": "printenv TOOLS_KEEP_VAR"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	if want := "exit code 0\nkept-value\n"; text(t, res) != want {
		t.Errorf("non-sensitive env var lost: %q, want %q", text(t, res), want)
	}
}

func TestExecCwdIsWorkspaceRoot(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "exec"), `{"command": "pwd -P"}`)
	if res.IsError {
		t.Fatalf("exec failed: %s", text(t, res))
	}
	if want := "exit code 0\n" + deps.Workspace.Root() + "\n"; text(t, res) != want {
		t.Errorf("exec cwd = %q, want %q", text(t, res), want)
	}
}

func TestExecTimeoutClampedToCap(t *testing.T) {
	cases := []struct {
		name    string
		def     time.Duration
		secs    *int
		want    time.Duration
		wantErr bool
	}{
		{"default", 30 * time.Second, nil, 30 * time.Second, false},
		{"per-call override", 30 * time.Second, intPtr(5), 5 * time.Second, false},
		{"clamped", 30 * time.Second, intPtr(9999), 120 * time.Second, false},
		{"at cap", 30 * time.Second, intPtr(120), 120 * time.Second, false},
		{"zero rejected", 30 * time.Second, intPtr(0), 0, true},
		{"negative rejected", 30 * time.Second, intPtr(-3), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := execTimeout(tc.def, tc.secs)
			if tc.wantErr {
				if err == nil {
					t.Errorf("execTimeout(%v, %v) = %v, want error", tc.def, tc.secs, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("execTimeout(%v, %v): %v", tc.def, tc.secs, err)
			}
			if got != tc.want {
				t.Errorf("execTimeout(%v, %v) = %v, want %v", tc.def, tc.secs, got, tc.want)
			}
		})
	}
}

func TestExecAcceptsLargeTimeoutSecs(t *testing.T) {
	deps := newTestDeps(t)
	res := run(t, toolByName(t, deps, "exec"), `{"command": "true", "timeout_secs": 9999}`)
	if res.IsError {
		t.Errorf("exec with oversized timeout_secs: want success, got %q", text(t, res))
	}
	if want := "exit code 0\n"; text(t, res) != want {
		t.Errorf("exec = %q, want %q", text(t, res), want)
	}
}

func intPtr(v int) *int { return &v }

func TestBadArgsReturnErrors(t *testing.T) {
	deps := newTestDeps(t)
	tests := []struct {
		name string
		tool string
		args string
	}{
		{"read-invalid-json", "read", `{"path": }`},
		{"read-missing-path", "read", `{"offset": 1}`},
		{"read-wrong-type-path", "read", `{"path": 42}`},
		{"read-wrong-type-offset", "read", `{"path": "f", "offset": "1"}`},
		{"read-nil-args", "read", ``},
		{"write-invalid-json", "write", `not json`},
		{"write-missing-path", "write", `{"content": "x"}`},
		{"write-missing-content", "write", `{"path": "f"}`},
		{"write-wrong-type", "write", `{"path": "f", "content": 5}`},
		{"edit-invalid-json", "edit", `{`},
		{"edit-missing-oldText", "edit", `{"path": "f", "newText": "y"}`},
		{"edit-missing-newText", "edit", `{"path": "f", "oldText": "x"}`},
		{"edit-wrong-type", "edit", `{"path": "f", "oldText": 1, "newText": "y"}`},
		{"exec-invalid-json", "exec", `{"command":`},
		{"exec-missing-command", "exec", `{}`},
		{"exec-empty-command", "exec", `{"command": "   "}`},
		{"exec-wrong-type", "exec", `{"command": 5}`},
		{"exec-bad-timeout", "exec", `{"command": "true", "timeout_secs": 0}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage
			if tc.args != "" {
				raw = json.RawMessage(tc.args)
			}
			res := toolByName(t, deps, tc.tool).Exec(context.Background(), raw)
			if !res.IsError {
				t.Errorf("%s with args %q: want error result, got %q", tc.tool, tc.args, text(t, res))
			}
		})
	}
}
