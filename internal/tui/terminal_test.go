package tui

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func newTestTerminal(t *testing.T) (*ProcessTerminal, *os.File, *fakeOps, func() string) {
	t.Helper()
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() stdin: %v", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() stdout: %v", err)
	}
	ops := newFakeOps()
	setDefaultTerminalOps(ops)
	t.Cleanup(func() {
		setDefaultTerminalOps(linuxTerminalOps{})
		terminalCleanupMu.Lock()
		openPipes := terminalOpenPipes
		terminalOpenPipes = nil
		terminalCleanupMu.Unlock()
		for _, file := range openPipes {
			file.Close()
		}
	})
	terminal := NewProcessTerminal(stdinRead, stdoutWrite)
	terminalOpenPipes = append(terminalOpenPipes, stdinRead, stdinWrite, stdoutRead, stdoutWrite)
	return terminal, stdinWrite, ops, func() string { return drainPipeFile(stdoutRead) }
}

var (
	terminalCleanupMu sync.Mutex
	terminalOpenPipes []*os.File
)

func drainPipeFile(file *os.File) string {
	buffer := make([]byte, 64*1024)
	var output []byte
	for {
		if err := file.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
			break
		}
		n, err := file.Read(buffer)
		output = append(output, buffer[:n]...)
		if err != nil {
			break
		}
	}
	return string(output)
}

func drainPipe(t *testing.T, file *os.File) string {
	t.Helper()
	buffer := make([]byte, 64*1024)
	var output []byte
	for {
		if err := file.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			break
		}
		n, err := file.Read(buffer)
		output = append(output, buffer[:n]...)
		if err != nil {
			break
		}
	}
	return string(output)
}

func TestProcessTerminalRequiresTerminalFiles(t *testing.T) {
	stdinRead, _, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdinRead.Close()
	ops := newFakeOps()
	ops.isTerminalOK = false
	setDefaultTerminalOps(ops)
	t.Cleanup(func() { setDefaultTerminalOps(linuxTerminalOps{}) })
	terminal := NewProcessTerminal(stdinRead, stdinRead)
	if err := terminal.Start(func(string) {}, func() {}); !isErrNotTerminal(err) {
		t.Fatalf("Start() error = %v, want ErrNotATerminal", err)
	}
}

func isErrNotTerminal(err error) bool {
	return err == ErrNotATerminal
}

func TestProcessTerminalStartFailureRestoresNothing(t *testing.T) {
	terminal, stdinWrite, ops, drain := newTestTerminal(t)
	defer stdinWrite.Close()
	ops.makeRawErr = errors.New("make raw refused")
	var inputs []string
	if err := terminal.Start(func(data string) { inputs = append(inputs, data) }, func() {}); err == nil {
		t.Fatal("Start() with makeRaw error should fail")
	}
	if ops.rawCalls != 0 {
		t.Fatalf("rawCalls = %d, want 0", ops.rawCalls)
	}
	if ops.restoreCalls != 0 {
		t.Fatalf("restoreCalls = %d, want 0", ops.restoreCalls)
	}
	if got := drain(); containsString(got, BracketedPasteOn) {
		t.Fatalf("bracketed paste written on failed start: %q", got)
	}
	_ = inputs
}

func containsString(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestProcessTerminalRestoresRawStateOnStop(t *testing.T) {
	terminal, stdinWrite, ops, drain := newTestTerminal(t)
	defer stdinWrite.Close()

	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if ops.rawCalls != 1 {
		t.Fatalf("rawCalls = %d, want 1", ops.rawCalls)
	}
	output := drain()
	if !containsString(output, BracketedPasteOn) {
		t.Fatalf("expected bracketed paste enable, got %q", output)
	}
	if !containsString(output, kittyQuery) {
		t.Fatalf("expected kitty query, got %q", output)
	}

	terminal.Stop()
	if ops.restoreCalls != 1 {
		t.Fatalf("restoreCalls = %d, want 1", ops.restoreCalls)
	}
	if ops.capturedRestore != ops.capturedRaw {
		t.Fatalf("restored state %v does not match captured raw state %v", ops.capturedRestore, ops.capturedRaw)
	}
	afterStop := drain()
	if !containsString(afterStop, BracketedPasteOff) {
		t.Fatalf("expected bracketed paste disable after stop, got %q", afterStop)
	}
	if !containsString(afterStop, KittyProtocolPop) {
		t.Fatalf("expected kitty protocol pop after stop, got %q", afterStop)
	}

	terminal.mu.Lock()
	alreadyStopped := terminal.stopped
	terminal.mu.Unlock()
	if !alreadyStopped {
		t.Fatal("terminal should be marked stopped")
	}
}

func TestProcessTerminalDeliversSequencesAndEOF(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	var mu sync.Mutex
	var inputs []string
	eofCount := 0
	inputDone := make(chan struct{}, 8)
	if err := terminal.Start(func(data string) {
		mu.Lock()
		inputs = append(inputs, data)
		mu.Unlock()
		inputDone <- struct{}{}
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	terminal.OnEOF(func() {
		mu.Lock()
		eofCount++
		mu.Unlock()
	})

	stdinWrite.WriteString("ab\x1b[18~c")
	waitForInputCount(t, &mu, &inputs, 4, inputDone)

	mu.Lock()
	want := []string{"a", "b", "\x1b[18~", "c"}
	failed := len(inputs) != len(want)
	if !failed {
		for i := range want {
			if inputs[i] != want[i] {
				t.Fatalf("inputs[%d] = %q, want %q", i, inputs[i], want[i])
			}
		}
	}
	mu.Unlock()
	if failed {
		t.Fatalf("inputs = %q, want %q", inputs, want)
	}

	stdinWrite.Close()
	waitForCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return eofCount == 1
	}, "EOF callback")
}

func TestProcessTerminalBuffersPartialEscapeSequences(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	var mu sync.Mutex
	var inputs []string
	inputDone := make(chan struct{}, 8)
	if err := terminal.Start(func(data string) {
		mu.Lock()
		inputs = append(inputs, data)
		mu.Unlock()
		inputDone <- struct{}{}
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stdinWrite.WriteString("\x1b[<0;12;5")
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	count := len(inputs)
	mu.Unlock()
	if count != 0 {
		t.Fatalf("partial mouse sequence delivered as %d inputs", count)
	}
	stdinWrite.WriteString("M")
	waitForInputCount(t, &mu, &inputs, 1, inputDone)
	mu.Lock()
	defer mu.Unlock()
	if inputs[0] != "\x1b[<0;12;5M" {
		t.Fatalf("input[0] = %q, want complete mouse sequence", inputs[0])
	}
}

func TestProcessTerminalBracketsPaste(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	var mu sync.Mutex
	var inputs []string
	inputDone := make(chan struct{}, 8)
	if err := terminal.Start(func(data string) {
		mu.Lock()
		inputs = append(inputs, data)
		mu.Unlock()
		inputDone <- struct{}{}
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stdinWrite.WriteString("\x1b[200~pasted\x1b[201~")
	waitForInputCount(t, &mu, &inputs, 1, inputDone)
	mu.Lock()
	defer mu.Unlock()
	if inputs[0] != BracketedPaste("pasted") {
		t.Fatalf("input[0] = %q, want re-wrapped paste", inputs[0])
	}
}

func TestProcessTerminalCapabilityQueries(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	go func() {
		time.Sleep(5 * time.Millisecond)
		stdinWrite.WriteString("\x1b[?1;2c")
	}()
	supported := terminal.QueryDeviceAttributes(500 * time.Millisecond)
	if !supported {
		t.Fatal("QueryDeviceAttributes() = false, want true after DA response")
	}

	go func() {
		time.Sleep(5 * time.Millisecond)
		stdinWrite.WriteString("\x1b[12;34R")
	}()
	row, col, ok := terminal.QueryCursorPosition(500 * time.Millisecond)
	if !ok {
		t.Fatal("QueryCursorPosition() failed despite reply")
	}
	if row != 12 || col != 34 {
		t.Fatalf("cursor position = (%d, %d), want (12, 34)", row, col)
	}
}

func TestProcessTerminalQueriesTimeoutWithSafeDefaults(t *testing.T) {
	terminal, stdinWrite, _, drain := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if terminal.QueryDeviceAttributes(5 * time.Millisecond) {
		t.Fatal("QueryDeviceAttributes() = true without response")
	}
	if _, _, ok := terminal.QueryCursorPosition(5 * time.Millisecond); ok {
		t.Fatal("QueryCursorPosition() ok without response")
	}
	if _, ok := terminal.QueryBackgroundColor(5 * time.Millisecond); ok {
		t.Fatal("QueryBackgroundColor() ok without response")
	}
	if got := drain(); !containsString(got, CursorDeviceAttrs) {
		t.Fatalf("expected DA query written, got %q", got)
	}
}

func TestProcessTerminalBackgroundColorReply(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	go func() {
		time.Sleep(5 * time.Millisecond)
		stdinWrite.WriteString("\x1b]11;rgb:1e1e/2e2e/3e3e\x07")
	}()
	color, ok := terminal.QueryBackgroundColor(500 * time.Millisecond)
	if !ok || color == nil {
		t.Fatal("QueryBackgroundColor() failed with rgb reply")
	}
	if color.R != 0x1e || color.G != 0x2e || color.B != 0x3e {
		t.Fatalf("color = %+v, want {30, 46, 62}", color)
	}
}

func TestParseOSC11BackgroundVariants(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     *RGBColor
	}{
		{name: "hash short", response: "\x1b]11;#1e2e3e\x07", want: &RGBColor{R: 0x1e, G: 0x2e, B: 0x3e}},
		{name: "rgb slash", response: "\x1b]11;rgb:1e1e/2e2e/3e3e\x1b\\", want: &RGBColor{R: 0x1e, G: 0x2e, B: 0x3e}},
		{name: "rgba prefix", response: "\x1b]11;rgba:ffff/0000/8080/ffff\x07", want: &RGBColor{R: 255, G: 0, B: 128}},
		{name: "invalid", response: "\x1b]11;rgb:zz/yy/xx\x07", want: nil},
		{name: "not osc11", response: "\x1b]10;rgb:00/00/00\x07", want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseOSC11Background(test.response)
			if test.want == nil {
				if ok && got != nil {
					t.Fatalf("parseOSC11Background(%q) = %+v, want failure", test.response, got)
				}
				return
			}
			if !ok || got == nil {
				t.Fatalf("parseOSC11Background(%q) failed, want %+v", test.response, test.want)
			}
			if *got != *test.want {
				t.Fatalf("parseOSC11Background(%q) = %+v, want %+v", test.response, got, test.want)
			}
		})
	}
}

func TestProcessTerminalKittyNegotiation(t *testing.T) {
	terminal, stdinWrite, _, drain := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stdinWrite.WriteString("\x1b[?1u")
	waitForCondition(t, func() bool { return terminal.KittyProtocolActive() }, "kitty protocol active")

	stdinWrite.WriteString("\x1b[?0u")
	waitForCondition(t, func() bool {
		return terminal.ModifyOtherKeysActive() && !terminal.KittyProtocolActive()
	}, "modifyOtherKeys fallback")

	terminal.Stop()
	after := drain()
	if !containsString(after, ModifyOtherKeysDisable) {
		t.Fatalf("expected modifyOtherKeys disable after stop, got %q", after)
	}
}

func TestProcessTerminalModifyOtherKeysOnDA(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stdinWrite.WriteString("\x1b[?1;2c")
	waitForCondition(t, func() bool { return terminal.ModifyOtherKeysActive() }, "modifyOtherKeys enabled via DA")
}

func TestProcessTerminalResizeSignal(t *testing.T) {
	terminal, stdinWrite, ops, _ := newTestTerminal(t)
	defer stdinWrite.Close()

	resized := make(chan struct{}, 4)
	if err := terminal.Start(func(string) {}, func() { resized <- struct{}{} }); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ops.size = [2]int{120, 40}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("SIGWINCH failed: %v", err)
	}
	select {
	case <-resized:
	case <-time.After(2 * time.Second):
		t.Fatal("resize handler not called after SIGWINCH")
	}
	if columns, rows := terminal.Columns(), terminal.Rows(); columns != 120 || rows != 40 {
		t.Fatalf("size = (%d, %d), want (120, 40)", columns, rows)
	}
	terminal.Stop()
}

func TestProcessTerminalDrainInput(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	start := time.Now()
	terminal.DrainInput(20, 5)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("DrainInput took %v", elapsed)
	}
}

func TestProcessTerminalWriteHelpers(t *testing.T) {
	terminal, stdinWrite, _, drain := newTestTerminal(t)
	defer stdinWrite.Close()
	if err := terminal.Start(func(string) {}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	drain()

	terminal.Write("hello")
	terminal.MoveBy(3)
	terminal.MoveBy(-2)
	terminal.MoveBy(0)
	terminal.HideCursor()
	terminal.ShowCursor()
	terminal.ClearLine()
	terminal.ClearFromCursor()
	terminal.ClearScreen()
	terminal.SetTitle("smidja")
	output := drain()
	for _, want := range []string{"hello", "\x1b[3B", "\x1b[2A", CursorHide, CursorShow, CursorEraseLine, CursorEraseBelow, CursorEraseScreen + CursorHome, "\x1b]0;smidja\x07"} {
		if !containsString(output, want) {
			t.Fatalf("output %q missing %q", output, want)
		}
	}
}

func TestIncompleteUTF8Suffix(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"abc", 0},
		{"ab\xe2", 1},
		{"ab\xe2\x82", 2},
		{"ab\xe2\x82\xac", 0},
		{"\xf0\x9f\x98", 3},
		{"\xf0\x9f\x98\x80", 0},
	}
	for _, test := range tests {
		if got := incompleteUTF8Suffix([]byte(test.input)); got != test.want {
			t.Errorf("incompleteUTF8Suffix(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}

func waitForInputCount(t *testing.T, mu *sync.Mutex, inputs *[]string, count int, done chan struct{}) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		current := len(*inputs)
		mu.Unlock()
		if current >= count {
			return
		}
		select {
		case <-done:
		case <-deadline:
			t.Fatalf("timed out waiting for %d inputs, have %d", count, current)
		}
	}
}

func waitForCondition(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", description)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func TestRealPTYRawModeRoundTrip(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx available")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open /dev/ptmx: %v", err)
	}
	defer master.Close()

	var unlock int32
	if errno := ioctl(master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Skipf("cannot unlock pty: %v", errno)
	}
	var index int32
	if errno := ioctl(master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&index))); errno != 0 {
		t.Skipf("cannot query pty index: %v", errno)
	}
	slavePath := "/dev/pts/" + itoa(int(index))
	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open slave pty: %v", err)
	}
	defer slave.Close()

	ops := linuxTerminalOps{}
	if !ops.isTerminal(slave) {
		t.Fatal("isTerminal(slave) = false, want true")
	}
	state, err := ops.makeRaw(slave)
	if err != nil {
		t.Fatalf("makeRaw error = %v", err)
	}
	cols, rows, err := ops.windowSize(slave)
	if err != nil {
		t.Fatalf("windowSize error = %v", err)
	}
	if cols < 0 || rows < 0 {
		t.Fatalf("windowSize = (%d, %d)", cols, rows)
	}
	if err := ops.restoreState(slave, state); err != nil {
		t.Fatalf("restoreState error = %v", err)
	}
	if !ops.isTerminal(slave) {
		t.Fatal("isTerminal after restore = false")
	}
}
