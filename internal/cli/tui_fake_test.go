package cli

import (
	"io"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
)

type fakeBridgeTerminal struct {
	mu        sync.Mutex
	writes    []string
	columns   int
	rows      int
	startErr  error
	started   bool
	stopCount int

	suspendCalls int
	resumeCalls  int
	suspendErr   error
	resumeErr    error

	onInput  func(string)
	onEOF    func()
	startedC chan struct{}
}

func newFakeBridgeTerminal() *fakeBridgeTerminal {
	return &fakeBridgeTerminal{columns: 80, rows: 24, startedC: make(chan struct{})}
}

func (f *fakeBridgeTerminal) Start(onInput func(string), onResize func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	wasStarted := f.started
	f.started = true
	f.onInput = onInput
	if !wasStarted {
		close(f.startedC)
	}
	return nil
}

func (f *fakeBridgeTerminal) Stop() {
	f.mu.Lock()
	f.started = false
	f.stopCount++
	f.mu.Unlock()
}

func (f *fakeBridgeTerminal) StopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCount
}

func (f *fakeBridgeTerminal) SuspendRaw() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspendCalls++
	return f.suspendErr
}

func (f *fakeBridgeTerminal) ResumeRaw() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeCalls++
	return f.resumeErr
}

func (f *fakeBridgeTerminal) SuspendCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suspendCalls
}

func (f *fakeBridgeTerminal) ResumeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeCalls
}

func (f *fakeBridgeTerminal) Started() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func (f *fakeBridgeTerminal) Write(data string) {
	f.mu.Lock()
	f.writes = append(f.writes, data)
	f.mu.Unlock()
}

func (f *fakeBridgeTerminal) Columns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.columns
}

func (f *fakeBridgeTerminal) Rows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows
}

func (f *fakeBridgeTerminal) KittyProtocolActive() bool   { return false }
func (f *fakeBridgeTerminal) ModifyOtherKeysActive() bool { return false }

func (f *fakeBridgeTerminal) MoveBy(lines int)             { f.Write(tui.CursorMoveLines(lines)) }
func (f *fakeBridgeTerminal) HideCursor()                  { f.Write(tui.CursorHide) }
func (f *fakeBridgeTerminal) ShowCursor()                  { f.Write(tui.CursorShow) }
func (f *fakeBridgeTerminal) ClearLine()                   { f.Write(tui.CursorEraseLine) }
func (f *fakeBridgeTerminal) ClearFromCursor()             { f.Write(tui.CursorEraseBelow) }
func (f *fakeBridgeTerminal) ClearScreen()                 { f.Write(tui.CursorEraseScreen + tui.CursorHome) }
func (f *fakeBridgeTerminal) SetTitle(title string)        { f.Write(tui.OSCTitle(title)) }
func (f *fakeBridgeTerminal) DrainInput(maxMs, idleMs int) {}

func (f *fakeBridgeTerminal) OnEOF(callback func()) {
	f.mu.Lock()
	f.onEOF = callback
	f.mu.Unlock()
}

func (f *fakeBridgeTerminal) SendInput(data string) {
	f.mu.Lock()
	handler := f.onInput
	f.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

func (f *fakeBridgeTerminal) FireEOF() {
	f.mu.Lock()
	handler := f.onEOF
	f.mu.Unlock()
	if handler != nil {
		handler()
	}
}

func (f *fakeBridgeTerminal) Output() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.writes, "")
}

func bridgeTerminalFactory(terminal *fakeBridgeTerminal) func(io.Reader, io.Writer) tui.Terminal {
	return func(io.Reader, io.Writer) tui.Terminal { return terminal }
}
