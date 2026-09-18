package ui

import (
	"io"
	"os"
	"strings"
	"testing"
)

type fdImpostor struct {
	io.Reader
}

func (fdImpostor) Fd() uintptr { return 0 }

func TestShouldUseTUIRejectsPrompt(t *testing.T) {
	always := func(*os.File) bool { return true }
	stdin, _, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdin.Close()
	stdout, _, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdout.Close()
	if shouldUseTUI(stdin, stdout, "hello", always) {
		t.Error("prompt mode must never select the TUI")
	}
}

func TestShouldUseTUIRejectsNonFiles(t *testing.T) {
	always := func(*os.File) bool { return true }
	if shouldUseTUI(strings.NewReader(""), io.Discard, "", always) {
		t.Error("buffers must never select the TUI")
	}
	if shouldUseTUI(fdImpostor{strings.NewReader("")}, io.Discard, "", always) {
		t.Error("Fd impostors must never select the TUI")
	}
	var nilFile *os.File
	pipe, _, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pipe.Close()
	if shouldUseTUI(nilFile, pipe, "", always) {
		t.Error("nil stdin must never select the TUI")
	}
	if shouldUseTUI(pipe, nilFile, "", always) {
		t.Error("nil stdout must never select the TUI")
	}
	if shouldUseTUI(pipe, pipe, "", nil) {
		t.Error("nil check must fail closed")
	}
}

func TestShouldUseTUIRejectsPipes(t *testing.T) {
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdinRead.Close()
	defer stdinWrite.Close()
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdoutRead.Close()
	defer stdoutWrite.Close()
	if ShouldUseTUI(stdinRead, stdoutWrite, "") {
		t.Error("pipe pairs must fail closed to the line interface")
	}
	if ShouldUseTUI(stdinRead, stdoutRead, "") {
		t.Error("pipe pairs must fail closed to the line interface")
	}
}

func TestShouldUseTUIRejectsMixedPairs(t *testing.T) {
	firstRead, firstWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer firstRead.Close()
	defer firstWrite.Close()
	secondRead, secondWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer secondRead.Close()
	defer secondWrite.Close()
	first, second := firstRead, secondRead
	onlyFirst := func(file *os.File) bool { return file == first }
	if shouldUseTUI(first, second, "", onlyFirst) {
		t.Error("mixed terminal and pipe pairs must fail closed")
	}
	if shouldUseTUI(second, first, "", onlyFirst) {
		t.Error("mixed pipe and terminal pairs must fail closed in either order")
	}
	neither := func(*os.File) bool { return false }
	if shouldUseTUI(first, first, "", neither) {
		t.Error("unsupported platforms must fail closed")
	}
}

func TestShouldUseTUIAcceptsTerminals(t *testing.T) {
	first, _, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer first.Close()
	second, _, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer second.Close()
	both := func(*os.File) bool { return true }
	if !shouldUseTUI(first, second, "", both) {
		t.Error("two terminals must select the TUI")
	}
}
