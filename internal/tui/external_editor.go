package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ExternalRunner interface {
	Run(command string, filePath string) error
}

type RunnerFunc func(command string, filePath string) error

func (f RunnerFunc) Run(command string, filePath string) error {
	return f(command, filePath)
}

func ResolveExternalCommand(explicit string, getenv func(string) string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if visual := strings.TrimSpace(getenv("VISUAL")); visual != "" {
		return visual
	}
	if editor := strings.TrimSpace(getenv("EDITOR")); editor != "" {
		return editor
	}
	return "nano"
}

func stripEditorBOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}

func splitEditorCommand(command string) (string, []string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

func EditInExternalEditor(content, command string, runner ExternalRunner) (string, error) {
	name := strings.TrimSpace(command)
	if name == "" {
		name = ResolveExternalCommand("", nil)
	}
	binary, args := splitEditorCommand(name)
	if binary == "" {
		binary = "nano"
	}
	directory, err := os.MkdirTemp("", "smidja-editor-")
	if err != nil {
		return "", err
	}
	cleanup := func() {
		_ = os.RemoveAll(directory)
	}
	filePath := filepath.Join(directory, "prompt.md")
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		cleanup()
		return "", err
	}
	var runErr error
	if runner != nil {
		runErr = runner.Run(name, filePath)
	} else {
		fullArgs := append(append([]string(nil), args...), filePath)
		cmd := exec.Command(binary, fullArgs...)
		runErr = cmd.Run()
	}
	if runErr != nil {
		cleanup()
		return "", runErr
	}
	data, err := os.ReadFile(filePath)
	cleanup()
	if err != nil {
		return "", err
	}
	result := stripEditorBOM(string(data))
	if strings.HasSuffix(result, "\r\n") {
		result = result[:len(result)-2]
	} else if strings.HasSuffix(result, "\n") {
		result = result[:len(result)-1]
	}
	return result, nil
}
