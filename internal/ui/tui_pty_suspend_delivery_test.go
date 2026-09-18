//go:build linux

package ui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestRealPTYSuspendDropsSameReadTrailingInput(t *testing.T) {
	pair, ok := openPTY(t)
	if !ok {
		return
	}
	terminal := tui.NewProcessTerminal(pair.slave, pair.slave)
	var (
		mu     sync.Mutex
		inputs []string
	)
	suspended := make(chan error, 1)
	var once sync.Once
	if err := terminal.Start(func(data string) {
		mu.Lock()
		inputs = append(inputs, data)
		mu.Unlock()
		once.Do(func() { suspended <- terminal.SuspendRaw() })
	}, func() {}); err != nil {
		t.Fatalf("Start() on a real PTY: %v", err)
	}
	defer terminal.Stop()

	ptyWrite(t, pair.master, "\x07tail")
	select {
	case err := <-suspended:
		if err != nil {
			t.Fatalf("SuspendRaw() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("input handler did not suspend")
	}
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	got := strings.Join(inputs, "")
	mu.Unlock()
	if got != "\x07" {
		t.Fatalf("inputs = %q, want only the suspending control byte", got)
	}
}
