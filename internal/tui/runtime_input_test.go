package tui

import (
	"sync"
	"testing"
)

func TestHandleTerminalInputListenerReentrancy(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	target := newRecordingComponent("target")
	base.AddChild(target)
	base.SetFocus(target)
	reentered := false
	base.AddInputListener(func(string) InputListenerResult {
		base.AddInputListener(func(string) InputListenerResult { return InputListenerResult{} })
		base.RequestRender(false)
		reentered = true
		return InputListenerResult{}
	})
	base.handleTerminalInput("x")
	if !reentered {
		t.Fatal("listener did not run")
	}
	if len(target.inputs) != 1 || target.inputs[0] != "x" {
		t.Fatalf("focused component inputs = %q, want [x]", target.inputs)
	}
}

func TestHandleTerminalInputListenerTransformAndConsume(t *testing.T) {
	base := NewBase(newFakeTerminal(20, 5), false, "regular")
	target := newRecordingComponent("target")
	base.AddChild(target)
	base.SetFocus(target)
	var mu sync.Mutex
	var seen []string
	base.AddInputListener(func(data string) InputListenerResult {
		mu.Lock()
		seen = append(seen, data)
		mu.Unlock()
		if data == "drop" {
			return InputListenerResult{Consume: true}
		}
		return InputListenerResult{Data: data + "!", HasData: true}
	})
	base.handleTerminalInput("a")
	base.handleTerminalInput("drop")
	if len(target.inputs) != 1 || target.inputs[0] != "a!" {
		t.Fatalf("focused component inputs = %q, want [a!]", target.inputs)
	}
	if len(seen) != 2 {
		t.Fatalf("listener calls = %d, want 2", len(seen))
	}
}
