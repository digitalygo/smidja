package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecDirectSeparatesStreamsAndExitCode(t *testing.T) {
	res, err := ExecDirect(context.Background(), "/bin/sh", []string{"-c", "printf out; printf err >&2; exit 3"}, t.TempDir(), DirectExecOptions{})
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	if res.Stdout != "out" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "out")
	}
	if res.Stderr != "err" {
		t.Fatalf("stderr = %q, want %q", res.Stderr, "err")
	}
	if res.Code != 3 {
		t.Fatalf("code = %d, want 3", res.Code)
	}
	if res.Killed {
		t.Fatal("killed = true for a normal nonzero exit")
	}
}

func TestExecDirectRunsInRequestedCwd(t *testing.T) {
	dir := t.TempDir()
	res, err := ExecDirect(context.Background(), "/bin/sh", []string{"-c", "pwd"}, dir, DirectExecOptions{})
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
}

func TestExecDirectSanitizesEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret-key")
	t.Setenv("SMIDJA_TEST_SECRET", "hidden")
	t.Setenv("HOST_TEST_VISIBLE", "visible")
	res, err := ExecDirect(context.Background(), "/bin/sh", []string{"-c", `printf "%s|%s|%s" "$OPENROUTER_API_KEY" "$SMIDJA_TEST_SECRET" "$HOST_TEST_VISIBLE"`}, t.TempDir(), DirectExecOptions{})
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	if res.Stdout != "||visible" {
		t.Fatalf("stdout = %q, want redacted provider and smidja variables", res.Stdout)
	}
}

func TestExecDirectStartFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-binary")
	res, err := ExecDirect(context.Background(), missing, nil, t.TempDir(), DirectExecOptions{})
	if err == nil {
		t.Fatal("ExecDirect must report a start failure")
	}
	if res.Stdout != "" || res.Stderr != "" || res.Code != 0 || res.Killed {
		t.Fatalf("result = %+v, want the zero result on start failure", res)
	}
}

func TestExecDirectEmptyCommand(t *testing.T) {
	if _, err := ExecDirect(context.Background(), "   ", nil, t.TempDir(), DirectExecOptions{}); err == nil {
		t.Fatal("ExecDirect must reject an empty command")
	}
}

func TestExecDirectTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	res, err := ExecDirect(context.Background(), "/bin/sh", []string{"-c", "sleep 30 & sleep 30 & wait"}, t.TempDir(), DirectExecOptions{Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	if !res.Killed {
		t.Fatal("timeout must report killed")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took %v; the process group was not killed", elapsed)
	}
}

func TestExecDirectCancelKillsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	res, err := ExecDirect(ctx, "/bin/sh", []string{"-c", "sleep 30"}, t.TempDir(), DirectExecOptions{})
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	if !res.Killed {
		t.Fatal("cancellation must report killed")
	}
}

func TestExecDirectDoesNotEvaluateArguments(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "created")
	arg := "$(touch " + marker + ")"
	res, err := ExecDirect(context.Background(), "printf", []string{"%s", arg}, t.TempDir(), DirectExecOptions{})
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	if res.Stdout != arg {
		t.Fatalf("stdout = %q, want the literal argument", res.Stdout)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("argument was evaluated: stat(%s) = %v", marker, err)
	}
}

func TestExecDirectCanceledContextDoesNotStartProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := ExecDirect(ctx, "/bin/sh", []string{"-c", "touch " + marker}, t.TempDir(), DirectExecOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if res.Stdout != "" || res.Stderr != "" || res.Code != 0 || res.Killed {
		t.Fatalf("result = %+v, want the zero result", res)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("a canceled context still started the command: stat(%s) = %v", marker, statErr)
	}
}

func TestExecDirectBoundedWhenDescendantHoldsPipes(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	script := fmt.Sprintf("sleep 60 & echo $! > %s; exit 0", pidFile)
	start := time.Now()
	res, err := ExecDirect(context.Background(), "/bin/sh", []string{"-c", script}, dir, DirectExecOptions{Timeout: 10 * time.Second})
	elapsed := time.Since(start)
	if pidBytes, readErr := os.ReadFile(pidFile); readErr == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(pidBytes))); convErr == nil && pid > 0 {
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		}
	}
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	if res.Code != 0 || res.Killed {
		t.Fatalf("result = %+v, want a clean zero exit", res)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("ExecDirect waited %v for a descendant holding the pipes", elapsed)
	}
}

func TestExecDirectOutputCapsWriteFullArtifact(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	script := `i=0; while [ $i -lt 200 ]; do printf 'line-%04d-padding-padding-padding\n' $i; i=$((i+1)); done`
	res, err := ExecDirect(context.Background(), "/bin/sh", []string{"-c", script}, t.TempDir(), DirectExecOptions{MaxLines: 20, MaxBytes: 256})
	if err != nil {
		t.Fatalf("ExecDirect: %v", err)
	}
	if !strings.Contains(res.Stdout, "Truncated:") {
		t.Fatalf("stdout was not truncated:\n%s", res.Stdout)
	}
	const marker = "Full output: "
	index := strings.LastIndex(res.Stdout, marker)
	if index < 0 {
		t.Fatalf("truncated stdout has no full-output artifact path:\n%s", res.Stdout)
	}
	path := strings.TrimSpace(res.Stdout[index+len(marker):])
	path = strings.TrimSuffix(path, "]")
	path = strings.TrimSpace(path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("full output artifact %q: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("full output artifact %q is empty", path)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(tmp, "smidja-exec-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover exec artifacts: %v", matches)
	}
}
