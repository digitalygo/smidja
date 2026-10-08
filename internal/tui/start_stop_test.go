package tui

import (
	"errors"
	"testing"
)

func TestBaseStartPropagatesTerminalError(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	terminal.startErr = errors.New("terminal refused")
	screen := NewMainScreen(terminal, false)
	if err := screen.Start(); err == nil {
		t.Fatal("Start() with failing terminal should return an error")
	} else if err.Error() != "terminal refused" {
		t.Fatalf("Start() error = %v, want the terminal error", err)
	}
	if terminal.started {
		t.Fatal("terminal should not be marked started after a failed Start")
	}
}

func TestBaseStartSucceeds(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	screen := NewMainScreen(terminal, false)
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !terminal.started {
		t.Fatal("terminal should be marked started after Start")
	}
	screen.Stop(StopOptions{})
}

func TestBaseStopIdempotent(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	screen := NewMainScreen(terminal, false)
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	screen.Stop(StopOptions{})
	writesAfterFirstStop := terminal.WriteCount()
	screen.Stop(StopOptions{})
	screen.Stop(StopOptions{})
	if got := terminal.WriteCount(); got != writesAfterFirstStop {
		t.Fatalf("writes after repeated Stop = %d, want %d", got, writesAfterFirstStop)
	}
	if !terminal.stopped {
		t.Fatal("terminal should remain stopped after repeated Stop")
	}
}

func TestBaseStopQuiescesRenders(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	screen := NewMainScreen(terminal, false)
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	screen.RenderNow(true)
	writesAtStop := terminal.WriteCount()
	screen.Stop(StopOptions{})
	settledWrites := terminal.WriteCount()
	screen.RequestRender(false)
	screen.RequestRender(true)
	screen.RenderNow(false)
	screen.RenderNow(true)
	if got := terminal.WriteCount(); got != settledWrites {
		t.Fatalf("writes after Stop grew from %d to %d, want no renders after Stop (had %d at settle)", settledWrites, got, writesAtStop)
	}
}

func TestAltScreenStartErrorBalancesEnterSequence(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	terminal.startErr = errors.New("terminal refused")
	screen := NewAltScreen(terminal, false, AltScreenOptions{})
	if err := screen.Start(); err == nil {
		t.Fatal("AltScreen Start() with failing terminal should return an error")
	}
	output := terminal.Output()
	if !containsSequence(output, AltScreenEnter) {
		t.Fatalf("output should contain the alt-screen enter sequence, got %q", output)
	}
	screen.Stop(StopOptions{})
	balanced := terminal.Output()
	if !containsSequence(balanced, AltScreenExit) {
		t.Fatalf("output should contain the alt-screen exit after Stop, got %q", balanced)
	}
	if indexOfSequence(balanced, AltScreenEnter) > indexOfSequence(balanced, AltScreenExit) {
		t.Fatalf("alt-screen enter must precede exit, got %q", balanced)
	}
}

func containsSequence(output, sequence string) bool {
	return indexOfSequence(output, sequence) >= 0
}

func indexOfSequence(output, sequence string) int {
	for i := 0; i+len(sequence) <= len(output); i++ {
		if output[i:i+len(sequence)] == sequence {
			return i
		}
	}
	return -1
}
