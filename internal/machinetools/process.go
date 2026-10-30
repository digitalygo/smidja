package machinetools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ProcessResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}
type GitResult struct {
	Root   string `json:"root"`
	Output string `json:"output"`
	Staged bool   `json:"staged,omitempty"`
}
type boundedOutput struct {
	mu        sync.Mutex
	limit     int
	data      []byte
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (s *Service) run(ctx context.Context, path string, timeout time.Duration, exe string, args ...string) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = path
	if s.cfg.Environment != nil {
		cmd.Env = s.cfg.Environment
	}
	if exe == "git" {
		environment := cmd.Env
		if environment == nil {
			environment = os.Environ()
		}
		filtered := make([]string, 0, len(environment)+1)
		for _, entry := range environment {
			if !strings.HasPrefix(entry, "GIT_OPTIONAL_LOCKS=") {
				filtered = append(filtered, entry)
			}
		}
		cmd.Env = append(filtered, "GIT_OPTIONAL_LOCKS=0")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = time.Second
	output := &boundedOutput{limit: s.cfg.MaxBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
	}
	data := ProcessResult{Output: strings.ToValidUTF8(string(output.data), "�"), ExitCode: exitCode, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	result := Result{OK: err == nil, Data: data, Truncated: output.truncated}
	if err != nil {
		result.Code = "process_failed"
		result.Error = err.Error()
		if ctx.Err() != nil {
			result.Code = "cancelled"
			result.Error = ctx.Err().Error()
		}
	}
	return result
}
func (s *Service) bash(ctx context.Context, a Arguments) Result {
	if strings.TrimSpace(a.Command) == "" {
		return failure("invalid_arguments", errors.New("command is required"))
	}
	timeout := s.cfg.DefaultTimeout
	if a.TimeoutSeconds != 0 {
		if a.TimeoutSeconds < 0 || int64(a.TimeoutSeconds) > int64(s.cfg.MaxTimeout/time.Second) {
			return failure("invalid_arguments", errors.New("timeout_seconds exceeds allowed range"))
		}
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	return s.run(ctx, a.Path, timeout, "bash", "-c", a.Command)
}
func (s *Service) git(ctx context.Context, a Arguments, diff bool) Result {
	path, err := absolute(a.Path)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	rootResult := s.run(ctx, path, s.cfg.DefaultTimeout, "git", "-c", "core.fsmonitor=false", "rev-parse", "--show-toplevel")
	if !rootResult.OK {
		return rootResult
	}
	rootProcess := rootResult.Data.(ProcessResult)
	if rootResult.Truncated {
		return failure("too_large", errors.New("repository path exceeds output limit"))
	}
	root := strings.TrimSuffix(rootProcess.Output, "\n")
	args := []string{"-c", "core.fsmonitor=false", "-c", "core.pager=cat"}
	filters := s.run(ctx, root, s.cfg.DefaultTimeout, "git", "config", "--name-only", "--null", "--get-regexp", `^filter\..*\.(clean|smudge|process|required)$`)
	filterProcess := filters.Data.(ProcessResult)
	if filters.Truncated {
		return failure("too_large", errors.New("repository filter configuration exceeds limit"))
	}
	if !filters.OK && filterProcess.ExitCode != 1 {
		return filters
	}
	for _, key := range strings.Split(filterProcess.Output, "\x00") {
		if key == "" {
			continue
		}
		value := ""
		if strings.HasSuffix(key, ".required") {
			value = "false"
		}
		args = append(args, "-c", key+"="+value)
	}
	if diff {
		args = append(args, "diff", "--no-ext-diff", "--no-textconv", "--no-color")
		if a.Staged {
			args = append(args, "--cached")
		}
		args = append(args, "--")
	} else {
		args = append(args, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	}
	result := s.run(ctx, root, s.cfg.DefaultTimeout, "git", args...)
	if result.OK {
		result.Data = GitResult{Root: root, Output: result.Data.(ProcessResult).Output, Staged: a.Staged}
	}
	return result
}
