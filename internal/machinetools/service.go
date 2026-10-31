package machinetools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Config struct {
	MaxBytes       int
	MaxEntries     int
	DefaultTimeout time.Duration
	MaxTimeout     time.Duration
	Environment    []string
}

type Service struct {
	cfg    Config
	writes sync.Mutex
}
type Definition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type Result struct {
	OK        bool   `json:"ok"`
	Data      any    `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
	Code      string `json:"code,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}
type Arguments struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSHA256 string `json:"expected_sha256"`
	OldText        string `json:"old_text"`
	NewText        string `json:"new_text"`
	Command        string `json:"command"`
	Pattern        string `json:"pattern"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Staged         bool   `json:"staged"`
}

func New(cfg Config) *Service {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 1 << 20
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 1000
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 30 * time.Second
	}
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = 10 * time.Minute
	}
	if cfg.DefaultTimeout > cfg.MaxTimeout {
		cfg.DefaultTimeout = cfg.MaxTimeout
	}
	if cfg.Environment != nil {
		cfg.Environment = append([]string{}, cfg.Environment...)
	}
	return &Service{cfg: cfg}
}

func Definitions() []Definition {
	definitions := []struct {
		name, description string
		required          []string
		properties        map[string]any
	}{
		{"read", "Read a UTF-8 text file using the current OS user's permissions.", []string{"path"}, map[string]any{"path": map[string]string{"type": "string"}}},
		{"list", "List a filesystem directory, without a project-root restriction.", []string{"path"}, map[string]any{"path": map[string]string{"type": "string"}}},
		{"write", "Create a text file or replace it only if expected_sha256 matches its current contents. Empty hash permits create only.", []string{"path", "content"}, map[string]any{"path": map[string]string{"type": "string"}, "content": map[string]string{"type": "string"}, "expected_sha256": map[string]string{"type": "string"}}},
		{"edit", "Replace one unique text occurrence after verifying expected_sha256.", []string{"path", "old_text", "new_text", "expected_sha256"}, map[string]any{"path": map[string]string{"type": "string"}, "old_text": map[string]string{"type": "string"}, "new_text": map[string]string{"type": "string"}, "expected_sha256": map[string]string{"type": "string"}}},
		{"grep", "Search UTF-8 files for a regular expression; returns bounded file/line matches without following directory symlinks.", []string{"path", "pattern"}, map[string]any{"path": map[string]string{"type": "string"}, "pattern": map[string]string{"type": "string"}}},
		{"bash", "Run Bash as the current OS user, with explicit timeout and bounded output. This is not a sandbox.", []string{"command"}, map[string]any{"path": map[string]string{"type": "string"}, "command": map[string]string{"type": "string"}, "timeout_seconds": map[string]any{"type": "integer", "minimum": 1}}},
		{"git_status", "Detect the enclosing Git repository and list changed and untracked files.", []string{"path"}, map[string]any{"path": map[string]string{"type": "string"}}},
		{"git_diff", "Show a repository diff with external diff and text conversion disabled.", []string{"path"}, map[string]any{"path": map[string]string{"type": "string"}, "staged": map[string]string{"type": "boolean"}}},
	}
	out := make([]Definition, 0, len(definitions))
	for _, d := range definitions {
		b, _ := json.Marshal(map[string]any{"type": "object", "properties": d.properties, "required": d.required, "additionalProperties": false})
		out = append(out, Definition{d.name, d.description, b})
	}
	return out
}

func (s *Service) Execute(ctx context.Context, name string, raw json.RawMessage) Result {
	if err := ctx.Err(); err != nil {
		return failure("cancelled", err)
	}
	var a Arguments
	if err := json.Unmarshal(raw, &a); err != nil {
		return failure("invalid_arguments", err)
	}
	switch name {
	case "read":
		return s.read(ctx, a.Path)
	case "list":
		return s.list(a.Path)
	case "write":
		return s.write(a)
	case "edit":
		return s.edit(a)
	case "grep":
		return s.grep(ctx, a)
	case "bash":
		return s.bash(ctx, a)
	case "git_status":
		return s.git(ctx, a, false)
	case "git_diff":
		return s.git(ctx, a, true)
	default:
		return failure("unknown_tool", fmt.Errorf("unknown tool %q", name))
	}
}

func failure(code string, err error) Result { return Result{Error: err.Error(), Code: code} }
func success(data any) Result               { return Result{OK: true, Data: data} }
func absolute(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	return filepath.Abs(path)
}
func (s *Service) readBytes(path string) ([]byte, error) {
	return s.readBytesContext(context.Background(), path)
}

func (s *Service) readBytesContext(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, int64(s.cfg.MaxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > s.cfg.MaxBytes {
		return nil, errors.New("file exceeds text editor size limit")
	}
	return b, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if contextErr := r.ctx.Err(); contextErr != nil {
		return n, contextErr
	}
	return n, err
}
