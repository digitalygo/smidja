package extensiontools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

const maximumOutput = 128 << 10
const maximumInput = 1 << 20

type Runner struct {
	base        *machinetools.Service
	environment []string
	timeout     time.Duration
	outputLimit int
}
type ExecutionResult struct {
	Content     string `json:"content"`
	Diagnostics string `json:"diagnostics,omitempty"`
	ExitCode    int    `json:"exit_code"`
	TimedOut    bool   `json:"timed_out"`
}

func New(base *machinetools.Service, environment []string) *Runner {
	copied := append([]string{}, environment...)
	if base == nil {
		base = machinetools.New(machinetools.Config{Environment: copied})
	}
	return &Runner{base: base, environment: copied, timeout: 30 * time.Second, outputLimit: maximumOutput}
}
func (r *Runner) WithEnvironment(values map[string]string) *Runner {
	environment := make([]string, 0, len(r.environment)+len(values))
	for _, entry := range r.environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[name]; !replaced {
			environment = append(environment, entry)
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environment = append(environment, name+"="+values[name])
	}
	return &Runner{base: r.base, environment: environment, timeout: r.timeout, outputLimit: r.outputLimit}
}

func (r *Runner) Execute(ctx context.Context, name string, args json.RawMessage) machinetools.Result {
	return r.base.Execute(ctx, name, args)
}
func invalid(code string, err error) machinetools.Result {
	return machinetools.Result{OK: false, Code: code, Error: err.Error()}
}
func executable(name string, environment []string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	path := ""
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			path = strings.TrimPrefix(entry, "PATH=")
		}
	}
	for _, directory := range filepath.SplitList(path) {
		if directory == "" {
			continue
		}
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
			return filepath.Abs(candidate)
		}
	}
	return "", fmt.Errorf("extension prerequisite %q is not installed or absent from configured PATH", name)
}
func (r *Runner) ExecuteDefinition(ctx context.Context, definition privateconfig.Extension, args json.RawMessage) machinetools.Result {
	if err := ctx.Err(); err != nil {
		return invalid("cancelled", err)
	}
	if len(definition.Command) == 0 || strings.TrimSpace(definition.Command[0]) == "" {
		return invalid("invalid_definition", errors.New("extension command is required"))
	}
	if definition.ClientID != "" && definition.ClientID != "manager" {
		return invalid("wrong_target", errors.New("extension is bound to another machine; route it explicitly before local execution"))
	}
	if len(args) > maximumInput {
		return invalid("invalid_arguments", errors.New("extension arguments exceed size limit"))
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(args, &object); err != nil || object == nil {
		return invalid("invalid_arguments", errors.New("extension arguments must be one JSON object"))
	}
	path, err := executable(definition.Command[0], r.environment)
	if err != nil {
		return invalid("missing_dependency", err)
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, definition.Command[1:]...)
	command.Env = append([]string{}, r.environment...)
	command.Stdin = bytes.NewReader(args)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	command.WaitDelay = time.Second
	stdout, stderr := &boundedBuffer{limit: r.outputLimit}, &boundedBuffer{limit: r.outputLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	output := ExecutionResult{Content: strings.ToValidUTF8(string(stdout.data), "�"), Diagnostics: strings.ToValidUTF8(string(stderr.data), "�"), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	if command.ProcessState != nil {
		output.ExitCode = command.ProcessState.ExitCode()
	} else if err != nil {
		output.ExitCode = -1
	}
	result := machinetools.Result{OK: err == nil, Data: output, Truncated: stdout.truncated || stderr.truncated}
	if err != nil {
		result.Code = "process_failed"
		result.Error = err.Error()
		if errors.Is(err, os.ErrNotExist) {
			result.Code = "missing_dependency"
			result.Error = "extension executable is not installed: " + definition.Command[0]
		}
		if ctx.Err() != nil {
			result.Code = "cancelled"
			result.Error = ctx.Err().Error()
		}
	}
	return result
}

type boundedBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	size := len(data)
	remaining := b.limit - len(b.data)
	if remaining < 0 {
		return 0, io.ErrShortWrite
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, data...)
	return size, nil
}
