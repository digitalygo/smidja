package tui

import (
	"errors"
	"os"
	"testing"
)

func suspendTestTerminal(t *testing.T) (*ProcessTerminal, *fakeOps) {
	t.Helper()
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() stdin: %v", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		stdinRead.Close()
		stdinWrite.Close()
		t.Fatalf("os.Pipe() stdout: %v", err)
	}
	t.Cleanup(func() {
		stdinRead.Close()
		stdinWrite.Close()
		stdoutRead.Close()
		stdoutWrite.Close()
	})
	ops := newFakeOps()
	previous := currentTerminalOps()
	setDefaultTerminalOps(ops)
	t.Cleanup(func() { setDefaultTerminalOps(previous) })
	terminal := NewProcessTerminal(stdinRead, stdoutWrite)
	state, err := ops.makeRaw(stdinRead)
	if err != nil {
		t.Fatalf("makeRaw: %v", err)
	}
	terminal.wasRaw = state
	return terminal, ops
}

func TestSuspendRawRestoresAndResumeReapplies(t *testing.T) {
	terminal, ops := suspendTestTerminal(t)
	rawCalls := ops.rawCalls
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls = %d, want 1", ops.restoreCalls)
	}
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("second SuspendRaw() error = %v", err)
	}
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls after second suspend = %d, want still 1", ops.restoreCalls)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	if ops.rawCalls != rawCalls+1 {
		t.Fatalf("rawCalls = %d, want %d", ops.rawCalls, rawCalls+1)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("second ResumeRaw() error = %v", err)
	}
	if ops.rawCalls != rawCalls+1 {
		t.Fatalf("rawCalls after second resume = %d, want still %d", ops.rawCalls, rawCalls+1)
	}
}

func TestSuspendRawWithoutRawStateIsNoop(t *testing.T) {
	terminal, ops := suspendTestTerminal(t)
	terminal.wasRaw = nil
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	if ops.restoreCalls != 0 {
		t.Fatalf("restoreCalls = %d, want 0", ops.restoreCalls)
	}
	if err := terminal.ResumeRaw(); err != nil {
		t.Fatalf("ResumeRaw() error = %v", err)
	}
	if ops.rawCalls != 1 {
		t.Fatalf("rawCalls = %d, want 1", ops.rawCalls)
	}
}

func TestSuspendRawPropagatesRestoreError(t *testing.T) {
	terminal, ops := suspendTestTerminal(t)
	ops.restoreErr = errors.New("restore refused")
	if err := terminal.SuspendRaw(); err == nil {
		t.Fatal("SuspendRaw() with restore error should fail")
	}
	if terminal.suspended {
		t.Fatal("terminal should not be marked suspended after a failed SuspendRaw")
	}
}

func TestResumeRawPropagatesMakeRawError(t *testing.T) {
	terminal, ops := suspendTestTerminal(t)
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	ops.makeRawErr = errors.New("raw refused")
	if err := terminal.ResumeRaw(); err == nil {
		t.Fatal("ResumeRaw() with makeRaw error should fail")
	}
	if !terminal.suspended {
		t.Fatal("terminal should remain suspended after a failed ResumeRaw")
	}
}

func TestStopAfterSuspendLeavesCookedTerminal(t *testing.T) {
	terminal, _ := suspendTestTerminal(t)
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := terminal.SuspendRaw(); err != nil {
		t.Fatalf("SuspendRaw() error = %v", err)
	}
	terminal.Stop()
	if terminal.suspended {
		t.Fatal("suspended flag should clear on Stop")
	}
	if terminal.wasRaw != nil {
		t.Fatal("raw state should clear on Stop")
	}
}
