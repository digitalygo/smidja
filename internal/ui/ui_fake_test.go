package ui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
)

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type fakeUITerminal struct {
	mu        sync.Mutex
	writes    []string
	events    []string
	columns   int
	rows      int
	started   bool
	stopped   bool
	startErr  error
	stopCalls int

	onInput  func(string)
	onResize func()
	onEOF    func()

	onStart func()
	onStop  func()

	suspendCalls int
	resumeCalls  int
	suspendErr   error
	resumeErr    error
	suspended    bool
}

func newFakeUITerminal(columns, rows int) *fakeUITerminal {
	return &fakeUITerminal{columns: columns, rows: rows}
}

func (f *fakeUITerminal) Start(onInput func(string), onResize func()) error {
	f.mu.Lock()
	hook := f.onStart
	if f.startErr != nil {
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
		return f.startErr
	}
	f.started = true
	f.stopped = false
	f.onInput = onInput
	f.onResize = onResize
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (f *fakeUITerminal) Stop() {
	f.mu.Lock()
	f.started = false
	f.stopped = true
	f.stopCalls++
	hook := f.onStop
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (f *fakeUITerminal) StopCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

func (f *fakeUITerminal) Write(data string) {
	f.mu.Lock()
	f.writes = append(f.writes, data)
	switch {
	case strings.Contains(data, tui.AltScreenEnter):
		f.events = append(f.events, "altEnter")
	case strings.Contains(data, tui.AltScreenExit):
		f.events = append(f.events, "altExit")
	}
	f.mu.Unlock()
}

func (f *fakeUITerminal) record(event string) {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
}

func (f *fakeUITerminal) Events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeUITerminal) Columns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.columns
}

func (f *fakeUITerminal) Rows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows
}

func (f *fakeUITerminal) SetSize(columns, rows int) {
	f.mu.Lock()
	f.columns = columns
	f.rows = rows
	resize := f.onResize
	f.mu.Unlock()
	if resize != nil {
		resize()
	}
}

func (f *fakeUITerminal) KittyProtocolActive() bool   { return false }
func (f *fakeUITerminal) ModifyOtherKeysActive() bool { return false }

func (f *fakeUITerminal) MoveBy(lines int)             { f.Write(tui.CursorMoveLines(lines)) }
func (f *fakeUITerminal) HideCursor()                  { f.Write(tui.CursorHide) }
func (f *fakeUITerminal) ShowCursor()                  { f.Write(tui.CursorShow) }
func (f *fakeUITerminal) ClearLine()                   { f.Write(tui.CursorEraseLine) }
func (f *fakeUITerminal) ClearFromCursor()             { f.Write(tui.CursorEraseBelow) }
func (f *fakeUITerminal) ClearScreen()                 { f.Write(tui.CursorEraseScreen + tui.CursorHome) }
func (f *fakeUITerminal) SetTitle(title string)        { f.Write(tui.OSCTitle(title)) }
func (f *fakeUITerminal) DrainInput(maxMs, idleMs int) {}

func (f *fakeUITerminal) OnEOF(callback func()) {
	f.mu.Lock()
	f.onEOF = callback
	f.mu.Unlock()
}

func (f *fakeUITerminal) SuspendRaw() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspendCalls++
	f.events = append(f.events, "suspend")
	if f.suspendErr != nil {
		return f.suspendErr
	}
	f.suspended = true
	return nil
}

func (f *fakeUITerminal) ResumeRaw() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeCalls++
	f.events = append(f.events, "resume")
	if f.resumeErr != nil {
		return f.resumeErr
	}
	f.suspended = false
	return nil
}

func (f *fakeUITerminal) SuspendCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suspendCalls
}

func (f *fakeUITerminal) ResumeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeCalls
}

func (f *fakeUITerminal) SendInput(data string) {
	f.mu.Lock()
	handler := f.onInput
	f.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

func (f *fakeUITerminal) FireEOF() {
	f.mu.Lock()
	handler := f.onEOF
	f.mu.Unlock()
	if handler != nil {
		handler()
	}
}

func (f *fakeUITerminal) Output() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.writes, "")
}

func (f *fakeUITerminal) OutputSince(mark int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if mark < 0 {
		mark = 0
	}
	if mark > len(f.writes) {
		mark = len(f.writes)
	}
	return strings.Join(f.writes[mark:], "")
}

func (f *fakeUITerminal) WriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func fakeUIRunnerOptions(terminal *fakeUITerminal) RunnerOptions {
	return RunnerOptions{
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Home:   os.TempDir(),
		NewTerminal: func(stdin io.Reader, stdout io.Writer) tui.Terminal {
			return terminal
		},
	}
}
