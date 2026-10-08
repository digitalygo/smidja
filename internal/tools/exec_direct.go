package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type DirectExecOptions struct {
	Timeout  time.Duration
	MaxLines int
	MaxBytes int64
}

const execWaitDelay = 2 * time.Second

type DirectExecResult struct {
	Stdout string
	Stderr string
	Code   int
	Killed bool
}

func ExecDirect(ctx context.Context, command string, args []string, dir string, opts DirectExecOptions) (DirectExecResult, error) {
	var zero DirectExecResult
	if strings.TrimSpace(command) == "" {
		return zero, errors.New("exec: command must not be empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultExecTimeoutSec * time.Second
	}
	if limit := maxExecTimeoutSec * time.Second; timeout > limit {
		timeout = limit
	}
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}

	stdout := newExecOutput(maxLines, maxBytes)
	stderr := newExecOutput(maxLines, maxBytes)
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = sanitizeEnv(os.Environ())
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = execWaitDelay
	if err := cmd.Start(); err != nil {
		return zero, fmt.Errorf("exec: start: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	killed := false
	select {
	case <-ctx.Done():
		killGroup(cmd)
		killed = true
		<-waitCh
	case <-timer.C:
		killGroup(cmd)
		killed = true
		<-waitCh
	case <-waitCh:
	}
	stdout.finish()
	stderr.finish()

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return DirectExecResult{
		Stdout: stdout.display(),
		Stderr: stderr.display(),
		Code:   code,
		Killed: killed,
	}, nil
}
