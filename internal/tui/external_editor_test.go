package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveExternalCommand(t *testing.T) {
	if got := ResolveExternalCommand("  vim  ", nil); got != "vim" {
		t.Fatalf("explicit = %q", got)
	}
	getenv := func(key string) string {
		if key == "VISUAL" {
			return "code --wait"
		}
		return ""
	}
	if got := ResolveExternalCommand("", getenv); got != "code --wait" {
		t.Fatalf("visual = %q", got)
	}
	getenv = func(key string) string {
		if key == "VISUAL" {
			return ""
		}
		if key == "EDITOR" {
			return "emacs"
		}
		return ""
	}
	if got := ResolveExternalCommand("", getenv); got != "emacs" {
		t.Fatalf("editor = %q", got)
	}
	if got := ResolveExternalCommand("", func(string) string { return "" }); got != "nano" {
		t.Fatalf("fallback = %q", got)
	}
	if got := ResolveExternalCommand("", nil); got != "nano" && os.Getenv("VISUAL") == "" && os.Getenv("EDITOR") == "" {
		t.Fatalf("nil getenv fallback = %q", got)
	}
}

func TestSplitEditorCommand(t *testing.T) {
	binary, args := splitEditorCommand("vim -p --clean")
	if binary != "vim" || len(args) != 2 {
		t.Fatalf("split = %q %q", binary, args)
	}
	binary, args = splitEditorCommand("")
	if binary != "" || args != nil {
		t.Fatalf("empty split = %q %q", binary, args)
	}
	binary, args = splitEditorCommand("  ")
	if binary != "" {
		t.Fatalf("blank split = %q", binary)
	}
}

func TestStripEditorBOM(t *testing.T) {
	if got := stripEditorBOM("\ufeffhello"); got != "hello" {
		t.Fatalf("bom = %q", got)
	}
	if got := stripEditorBOM("hello"); got != "hello" {
		t.Fatalf("no bom = %q", got)
	}
}

func TestEditInExternalEditorWithRunner(t *testing.T) {
	runner := RunnerFunc(func(command, filePath string) error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if string(data) != "original content" {
			return errors.New("unexpected content: " + string(data))
		}
		return os.WriteFile(filePath, []byte("\ufeffedited content\n"), 0o600)
	})
	result, err := EditInExternalEditor("original content", "fake-editor --wait", runner)
	if err != nil {
		t.Fatalf("edit = %v", err)
	}
	if result != "edited content" {
		t.Fatalf("result = %q", result)
	}
}

func TestEditInExternalEditorCRLFAndEmpty(t *testing.T) {
	runner := RunnerFunc(func(command, filePath string) error {
		return os.WriteFile(filePath, []byte("line one\r\n"), 0o600)
	})
	result, err := EditInExternalEditor("start", "fake", runner)
	if err != nil {
		t.Fatalf("edit = %v", err)
	}
	if result != "line one" {
		t.Fatalf("crlf result = %q", result)
	}
	runner = RunnerFunc(func(command, filePath string) error {
		return os.WriteFile(filePath, []byte("no newline"), 0o600)
	})
	result, err = EditInExternalEditor("start", "fake", runner)
	if err != nil {
		t.Fatalf("edit = %v", err)
	}
	if result != "no newline" {
		t.Fatalf("no newline result = %q", result)
	}
}

func TestEditInExternalEditorRunnerError(t *testing.T) {
	runner := RunnerFunc(func(command, filePath string) error {
		return errors.New("editor failed")
	})
	if _, err := EditInExternalEditor("content", "fake", runner); err == nil {
		t.Fatalf("expected error")
	}
}

func TestEditInExternalEditorDefaultCommand(t *testing.T) {
	called := ""
	runner := RunnerFunc(func(command, filePath string) error {
		called = command
		return os.WriteFile(filePath, []byte("kept"), 0o600)
	})
	result, err := EditInExternalEditor("kept", "", runner)
	if err != nil {
		t.Fatalf("edit = %v", err)
	}
	if result != "kept" {
		t.Fatalf("result = %q", result)
	}
	if strings.TrimSpace(called) == "" {
		t.Fatalf("default command not resolved")
	}
	_ = filepath.Separator
}

func TestEditInExternalEditorExecFailure(t *testing.T) {
	if _, err := EditInExternalEditor("content", "definitely-not-a-real-editor-binary-xyz", nil); err == nil {
		t.Fatalf("expected exec error")
	}
}
