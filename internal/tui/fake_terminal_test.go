package tui

import (
	"os"
	"strings"
	"sync"
)

type fakeTerminal struct {
	mu        sync.Mutex
	writes    []string
	columns   int
	rows      int
	started   bool
	stopped   bool
	onInput   func(string)
	onResize  func()
	startErr  error
	eofSeen   bool
	eofSignal chan struct{}
}

func newFakeTerminal(columns, rows int) *fakeTerminal {
	return &fakeTerminal{
		columns:   columns,
		rows:      rows,
		eofSignal: make(chan struct{}),
	}
}

func (f *fakeTerminal) Start(onInput func(string), onResize func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	f.stopped = false
	f.onInput = onInput
	f.onResize = onResize
	return nil
}

func (f *fakeTerminal) Stop() {
	f.mu.Lock()
	f.started = false
	f.stopped = true
	f.mu.Unlock()
}

func (f *fakeTerminal) SuspendRaw() error { return nil }

func (f *fakeTerminal) ResumeRaw() error { return nil }

func (f *fakeTerminal) Write(data string) {
	f.mu.Lock()
	f.writes = append(f.writes, data)
	f.mu.Unlock()
}

func (f *fakeTerminal) Columns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.columns
}

func (f *fakeTerminal) Rows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows
}

func (f *fakeTerminal) SetSize(columns, rows int) {
	f.mu.Lock()
	f.columns = columns
	f.rows = rows
	resize := f.onResize
	f.mu.Unlock()
	if resize != nil {
		resize()
	}
}

func (f *fakeTerminal) KittyProtocolActive() bool    { return false }
func (f *fakeTerminal) ModifyOtherKeysActive() bool  { return false }
func (f *fakeTerminal) MoveBy(lines int)             { f.Write(CursorMoveLines(lines)) }
func (f *fakeTerminal) HideCursor()                  { f.Write(CursorHide) }
func (f *fakeTerminal) ShowCursor()                  { f.Write(CursorShow) }
func (f *fakeTerminal) ClearLine()                   { f.Write(CursorEraseLine) }
func (f *fakeTerminal) ClearFromCursor()             { f.Write(CursorEraseBelow) }
func (f *fakeTerminal) ClearScreen()                 { f.Write(CursorEraseScreen + CursorHome) }
func (f *fakeTerminal) SetTitle(title string)        { f.Write(OSCTitle(title)) }
func (f *fakeTerminal) DrainInput(maxMs, idleMs int) {}
func (f *fakeTerminal) OnEOF(callback func())        {}

func (f *fakeTerminal) SendInput(data string) {
	f.mu.Lock()
	handler := f.onInput
	f.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

func (f *fakeTerminal) Output() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.writes, "")
}

func (f *fakeTerminal) WriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakeTerminal) ResetWrites() {
	f.mu.Lock()
	f.writes = nil
	f.mu.Unlock()
}

type fakeOps struct {
	mu           sync.Mutex
	isTerminalOK bool
	makeRawErr   error
	restoreErr   error

	rawCalls     int
	restoreCalls int

	capturedRaw     any
	capturedRestore any

	savedStates []any
	sizeErr     error
	size        [2]int
}

func newFakeOps() *fakeOps {
	return &fakeOps{isTerminalOK: true, size: [2]int{80, 24}}
}

func (f *fakeOps) name() string { return "fake" }

func (f *fakeOps) isTerminal(*os.File) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.isTerminalOK
}

func (f *fakeOps) makeRaw(*os.File) (*rawState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.makeRawErr != nil {
		return nil, f.makeRawErr
	}
	f.rawCalls++
	f.capturedRaw = f.rawCalls
	state := &rawState{value: f.rawCalls}
	f.savedStates = append(f.savedStates, state.value)
	return state, nil
}

func (f *fakeOps) restoreState(_ *os.File, state *rawState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreCalls++
	f.capturedRestore = state.value
	if f.restoreErr != nil {
		return f.restoreErr
	}
	return nil
}

func (f *fakeOps) windowSize(*os.File) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sizeErr != nil {
		return 0, 0, f.sizeErr
	}
	return f.size[0], f.size[1], nil
}
