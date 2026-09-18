package ui

import (
	"io"
	"os"
)

func ShouldUseTUI(stdin io.Reader, stdout io.Writer, prompt string) bool {
	return shouldUseTUI(stdin, stdout, prompt, isTerminalFile)
}

func shouldUseTUI(stdin io.Reader, stdout io.Writer, prompt string, check func(*os.File) bool) bool {
	if prompt != "" {
		return false
	}
	if check == nil {
		return false
	}
	stdinFile, ok := stdin.(*os.File)
	if !ok || stdinFile == nil {
		return false
	}
	stdoutFile, ok := stdout.(*os.File)
	if !ok || stdoutFile == nil {
		return false
	}
	return check(stdinFile) && check(stdoutFile)
}
