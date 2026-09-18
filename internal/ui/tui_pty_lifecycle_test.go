//go:build linux

package ui

import (
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/digitalygo/smidja/internal/tui"
)

func ptyWrite(t *testing.T, file *os.File, data string) {
	t.Helper()
	if _, err := file.Write([]byte(data)); err != nil {
		t.Fatalf("PTY write %q: %v", data, err)
	}
}

func ptyWaitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func ptyWaitForEditorIdle(t *testing.T, runner *Runner, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for runner.editorActive.Load() || runner.editorRunning.Load() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the external editor action to finish")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func ptyTermiosOrZero(file *os.File) (syscall.Termios, bool) {
	var state syscall.Termios
	if errno := ptyIoctl(file.Fd(), ptyTCGETS, uintptr(unsafe.Pointer(&state))); errno != 0 {
		return state, false
	}
	return state, true
}

func TestRealPTYCtrlDExitsRunner(t *testing.T) {
	pair, ok := openPTY(t)
	if !ok {
		return
	}
	before := ptyTermios(t, pair.slave)
	terminal := tui.NewProcessTerminal(pair.slave, pair.slave)
	runner := NewRunner(RunnerOptions{
		Stdin:  pair.slave,
		Stdout: pair.slave,
		Mode:   TUIModeRegular,
		Home:   t.TempDir(),
		NewTerminal: func(io.Reader, io.Writer) tui.Terminal {
			return terminal
		},
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() on a real PTY: %v", err)
	}
	ptyReadUntil(t, pair.master, tui.OSCTitle("smidja"), 5*time.Second)
	ptyWrite(t, pair.master, "\x04")
	select {
	case <-runner.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ctrl+d did not exit the runner without deadlocking")
	}
	runner.Stop()
	after := ptyTermios(t, pair.slave)
	if after != before {
		t.Fatal("terminal attributes were not restored after ctrl+d exit")
	}
}

func TestRealPTYCtrlGEditorSuspendsAndResumes(t *testing.T) {
	pair, ok := openPTY(t)
	if !ok {
		return
	}
	before := ptyTermios(t, pair.slave)
	marker := filepath.Join(t.TempDir(), "editor-ran")
	var (
		runs     int32
		restored atomic.Bool
	)
	terminal := tui.NewProcessTerminal(pair.slave, pair.slave)
	runner := NewRunner(RunnerOptions{
		Stdin:  pair.slave,
		Stdout: pair.slave,
		Mode:   TUIModeRegular,
		Home:   t.TempDir(),
		NewTerminal: func(io.Reader, io.Writer) tui.Terminal {
			return terminal
		},
		ExternalRunner: tui.RunnerFunc(func(command, filePath string) error {
			if state, ok := ptyTermiosOrZero(pair.slave); ok && state == before {
				restored.Store(true)
			}
			atomic.AddInt32(&runs, 1)
			return os.WriteFile(marker, []byte("done"), 0o600)
		}),
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() on a real PTY: %v", err)
	}
	ptyReadUntil(t, pair.master, tui.OSCTitle("smidja"), 5*time.Second)
	ptyWrite(t, pair.master, "\x07")
	ptyWaitForFile(t, marker, 5*time.Second)
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("external editor runs = %d, want 1", got)
	}
	if !restored.Load() {
		t.Fatal("terminal was not restored to canonical mode during the external editor")
	}
	ptyWaitForEditorIdle(t, runner, 5*time.Second)
	ptyWrite(t, pair.master, "\x04")
	select {
	case <-runner.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ctrl+d after the external editor did not exit, the resumed reader is not delivering input")
	}
	runner.Stop()
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("external editor runs after stop = %d, want 1", got)
	}
	after := ptyTermios(t, pair.slave)
	if after != before {
		t.Fatal("terminal attributes were not restored after the external editor round trip")
	}
}
