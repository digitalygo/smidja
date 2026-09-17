//go:build linux

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

const (
	smokePTYIOCGPTN   = 0x80045430
	smokePTYIOCSPTLCK = 0x40045431
	smokePTYIOCSWINSZ = 0x5414
	smokePTYTCGETS    = 0x5401
)

type smokePTYWinsize struct {
	rows uint16
	cols uint16
	xpix uint16
	ypix uint16
}

type smokePTYClient struct {
	mu      sync.Mutex
	calls   int
	cancels int
	entered chan struct{}
	once    sync.Once
}

func (c *smokePTYClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == 1 {
		if onText != nil {
			onText("pty first answer 7f3a")
		}
		return textStop("pty first answer 7f3a"), nil
	}
	if call == 2 {
		c.once.Do(func() { close(c.entered) })
		<-ctx.Done()
		c.mu.Lock()
		c.cancels++
		c.mu.Unlock()
		return nil, ctx.Err()
	}
	if call == 3 {
		if onText != nil {
			onText("pty second answer 9c2e")
		}
		return textStop("pty second answer 9c2e"), nil
	}
	return nil, errors.New("smokePTYClient: unexpected call")
}

func (c *smokePTYClient) snapshot() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.cancels
}

type smokePTYCapture struct {
	master *os.File
	mu     sync.Mutex
	data   []byte
}

func (c *smokePTYCapture) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(append([]byte(nil), c.data...))
}

func smokePTYReadable(file *os.File, timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	fd := int(file.Fd())
	for {
		var set syscall.FdSet
		wordBits := 32 << (^uint(0) >> 63)
		set.Bits[fd/wordBits] |= 1 << (uint(fd) % uint(wordBits))
		wait := syscall.NsecToTimeval(timeout.Nanoseconds())
		n, err := syscall.Select(fd+1, &set, nil, nil, &wait)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false
		}
		return n > 0
	}
}

func (c *smokePTYCapture) waitFor(t *testing.T, fragment string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	buffer := make([]byte, 4096)
	for {
		c.mu.Lock()
		accumulated := string(c.data)
		c.mu.Unlock()
		if smokePTYContains(accumulated, fragment) {
			return accumulated
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("timed out waiting for %q, got %q", fragment, accumulated)
		}
		if !smokePTYReadable(c.master, 50*time.Millisecond) {
			continue
		}
		n, err := c.master.Read(buffer)
		if n > 0 {
			c.mu.Lock()
			c.data = append(c.data, buffer[:n]...)
			c.mu.Unlock()
		}
		if err != nil && n == 0 {
			continue
		}
	}
}

func (c *smokePTYCapture) drain(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	buffer := make([]byte, 4096)
	for time.Now().Before(deadline) {
		if !smokePTYReadable(c.master, 50*time.Millisecond) {
			continue
		}
		n, err := c.master.Read(buffer)
		if n > 0 {
			c.mu.Lock()
			c.data = append(c.data, buffer[:n]...)
			c.mu.Unlock()
		}
		if err != nil && n == 0 {
			continue
		}
	}
}

func smokePTYIoctl(fd uintptr, request uintptr, arg uintptr) syscall.Errno {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, request, arg, 0, 0, 0)
	return errno
}

func smokePTYItoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func smokePTYContains(haystack string, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func smokePTYIndex(haystack string, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func smokePTYOpen(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("real PTY smoke requires linux devpts")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("real PTY smoke needs /dev/ptmx: %v", err)
	}
	unlock := 0
	if errno := smokePTYIoctl(master.Fd(), smokePTYIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		t.Skipf("real PTY smoke needs unlockpt ioctl: %v", errno)
	}
	var number uint32
	if errno := smokePTYIoctl(master.Fd(), smokePTYIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
		master.Close()
		t.Skipf("real PTY smoke needs TIOCGPTN ioctl: %v", errno)
	}
	slaveName := "/dev/pts/" + smokePTYItoa(int(number))
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Skipf("real PTY smoke needs %s: %v", slaveName, err)
	}
	size := smokePTYWinsize{rows: 24, cols: 80}
	if errno := smokePTYIoctl(slave.Fd(), smokePTYIOCSWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		slave.Close()
		master.Close()
		t.Skipf("real PTY smoke needs TIOCSWINSZ ioctl: %v", errno)
	}
	t.Cleanup(func() {
		slave.Close()
		master.Close()
	})
	return master, slave
}

func smokePTYTermios(t *testing.T, file *os.File) syscall.Termios {
	t.Helper()
	var state syscall.Termios
	if errno := smokePTYIoctl(file.Fd(), smokePTYTCGETS, uintptr(unsafe.Pointer(&state))); errno != 0 {
		t.Fatalf("TCGETS: %v", errno)
	}
	return state
}

func smokePTYWrite(t *testing.T, file *os.File, data string) {
	t.Helper()
	bytesOut := []byte(data)
	for len(bytesOut) > 0 {
		n, err := file.Write(bytesOut)
		if err != nil {
			t.Fatalf("PTY write: %v", err)
		}
		if n <= 0 {
			t.Fatalf("PTY write returned 0")
		}
		bytesOut = bytesOut[n:]
	}
}

func smokePTYWaitCalls(t *testing.T, client *smokePTYClient, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		calls, _ := client.snapshot()
		if calls >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls, _ := client.snapshot()
	t.Fatalf("client calls = %d, want at least %d", calls, want)
}

func smokePTYWaitCancels(t *testing.T, client *smokePTYClient, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, cancels := client.snapshot()
		if cancels >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, cancels := client.snapshot()
	t.Fatalf("client cancels = %d, want at least %d", cancels, want)
}

func TestTUIRealPTYWiredSmoke(t *testing.T) {
	master, slave := smokePTYOpen(t)
	before := smokePTYTermios(t, slave)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	home := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sessionPath := sess.Path()
	client := &smokePTYClient{entered: make(chan struct{})}
	var probeCalls int
	probe := &probeTool{calls: &probeCalls}
	hookRuntime := extensions.NewRuntime(extensions.NewRegistry())
	var depsStdout bytes.Buffer
	var depsStderr bytes.Buffer
	var rdStdout bytes.Buffer
	var rdStderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return home },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &depsStderr,
	}
	_ = depsStdout
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sessionPath,
		client:      client,
		tools:       []agent.Tool{probe},
		recorder:    &sessionRecorder{sess},
		stdout:      &rdStdout,
		stderr:      &rdStderr,
		hooks:       hookRuntime.Dispatcher(),
		retry:       retryAdapter,
		retryPolicy: agent.RetryPolicy{Enabled: false},
		catalog:     commandsCatalogFor([]agent.Tool{probe}),
		commands:    extensions.NewCommandCatalog(),
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return hookRuntime.HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	factory := func(io.Reader, io.Writer) tui.Terminal {
		return tui.NewProcessTerminal(slave, slave)
	}
	done := make(chan error, 1)
	go func() {
		done <- runTUI(context.Background(), deps, rd, lineUI, ui.TUIModeFullscreen, workspace, workspace, nil, factory, nil)
	}()
	capture.waitFor(t, tui.AltScreenEnter, 5*time.Second)
	capture.waitFor(t, tui.OSCTitle("smidja"), 5*time.Second)
	smokePTYWrite(t, master, "pty first prompt\r")
	capture.waitFor(t, "pty first answer 7f3a", 10*time.Second)
	smokePTYWaitCalls(t, client, 1, 5*time.Second)
	smokePTYWrite(t, master, "pty slow prompt\r")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow turn did not start")
	}
	time.Sleep(150 * time.Millisecond)
	smokePTYWrite(t, master, "\x1b")
	capture.waitFor(t, "interrupted", 5*time.Second)
	smokePTYWaitCancels(t, client, 1, 5*time.Second)
	time.Sleep(200 * time.Millisecond)
	_, cancels := client.snapshot()
	if cancels != 1 {
		t.Fatalf("cancels = %d, want exactly 1", cancels)
	}
	smokePTYWrite(t, master, "pty second prompt\r")
	capture.waitFor(t, "pty second answer 9c2e", 10*time.Second)
	smokePTYWaitCalls(t, client, 3, 5*time.Second)
	smokePTYWrite(t, master, "/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not exit after /quit")
	}
	capture.drain(t, 500*time.Millisecond)
	full := capture.snapshot()
	enter := smokePTYIndex(full, tui.AltScreenEnter)
	first := smokePTYIndex(full, "pty first answer 7f3a")
	aborted := smokePTYIndex(full, "interrupted")
	second := smokePTYIndex(full, "pty second answer 9c2e")
	exit := smokePTYIndex(full, tui.AltScreenExit)
	if enter < 0 {
		t.Fatalf("output missing alt-screen enter:\n%q", full)
	}
	if first < 0 {
		t.Fatalf("output missing first answer:\n%q", full)
	}
	if aborted < 0 {
		t.Fatalf("output missing interrupt notice:\n%q", full)
	}
	if second < 0 {
		t.Fatalf("output missing second answer:\n%q", full)
	}
	if exit < 0 {
		t.Fatalf("output missing alt-screen exit:\n%q", full)
	}
	if !(enter < first) {
		t.Fatalf("alt-screen enter must precede content: enter=%d first=%d\n%q", enter, first, full)
	}
	if !(first < aborted) {
		t.Fatalf("first answer must precede interrupt: first=%d aborted=%d\n%q", first, aborted, full)
	}
	if !(aborted < second) {
		t.Fatalf("interrupt must precede second answer: aborted=%d second=%d\n%q", aborted, second, full)
	}
	if !(second < exit) {
		t.Fatalf("second answer must precede exit: second=%d exit=%d\n%q", second, exit, full)
	}
	after := smokePTYTermios(t, slave)
	if after != before {
		t.Fatal("terminal attributes were not restored after Stop")
	}
	select {
	case err := <-done:
		t.Fatalf("runTUI returned twice: %v", err)
	default:
	}
	calls, finalCancels := client.snapshot()
	if calls != 3 {
		t.Fatalf("client calls = %d, want 3", calls)
	}
	if finalCancels != 1 {
		t.Fatalf("client cancels = %d, want exactly 1", finalCancels)
	}
	if probeCalls != 0 {
		t.Fatalf("probe calls = %d, want 0", probeCalls)
	}
	if smokePTYContains(rdStdout.String(), "pty first answer 7f3a") {
		t.Fatalf("agent text leaked to direct stdout: %q", rdStdout.String())
	}
	if smokePTYContains(rdStdout.String(), "pty second answer 9c2e") {
		t.Fatalf("agent text leaked to direct stdout: %q", rdStdout.String())
	}
	transcript, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	for _, want := range []string{"pty first prompt", "pty first answer 7f3a", "pty slow prompt", "pty second prompt", "pty second answer 9c2e"} {
		if !smokePTYContains(string(transcript), want) {
			t.Fatalf("session missing %q:\n%s", want, transcript)
		}
	}
	stripped := tui.StripTerminalSequences(full[exit:])
	if !smokePTYContains(stripped, "pty second answer 9c2e") {
		t.Fatalf("final document after exit missing transcript:\n%q", stripped)
	}
}

func TestTUIRealPTYDialogAcceptCancelExit(t *testing.T) {
	master, slave := smokePTYOpen(t)
	capture := &smokePTYCapture{master: master}
	workspace := t.TempDir()
	home := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	var depsStderr bytes.Buffer
	var rdStdout bytes.Buffer
	var rdStderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return home },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &depsStderr,
	}
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      &capturingClient{},
		recorder:    &sessionRecorder{sess},
		stdout:      &rdStdout,
		stderr:      &rdStderr,
		hooks:       extensions.NewRuntime(extensions.NewRegistry()).Dispatcher(),
		retry:       retryAdapter,
		retryPolicy: agent.RetryPolicy{Enabled: false},
		commands:    extensions.NewCommandCatalog(),
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return extensions.NewRuntime(extensions.NewRegistry()).HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	factory := func(io.Reader, io.Writer) tui.Terminal {
		return tui.NewProcessTerminal(slave, slave)
	}
	done := make(chan error, 1)
	go func() {
		done <- runTUI(context.Background(), deps, rd, lineUI, ui.TUIModeFullscreen, workspace, workspace, nil, factory, nil)
	}()
	capture.waitFor(t, tui.AltScreenEnter, 5*time.Second)

	smokePTYWrite(t, master, "/help\r")
	capture.waitFor(t, "Command help", 5*time.Second)
	smokePTYWrite(t, master, "\r")
	time.Sleep(150 * time.Millisecond)

	smokePTYWrite(t, master, "/settings\r")
	capture.waitFor(t, "Auto retry", 5*time.Second)
	smokePTYWrite(t, master, "\x03")
	time.Sleep(150 * time.Millisecond)

	smokePTYWrite(t, master, "/help\r")
	capture.waitFor(t, "Command help", 5*time.Second)
	smokePTYWrite(t, master, "\x1b")
	time.Sleep(150 * time.Millisecond)

	smokePTYWrite(t, master, "/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not exit after /quit")
	}
	capture.drain(t, 300*time.Millisecond)
	full := capture.snapshot()
	if smokePTYIndex(full, "Command help") < 0 {
		t.Fatalf("output missing the help dialog:\n%q", full)
	}
	if smokePTYIndex(full, "Auto retry") < 0 {
		t.Fatalf("output missing the settings dialog:\n%q", full)
	}
	if smokePTYIndex(full, tui.AltScreenExit) < 0 {
		t.Fatalf("output missing alt-screen exit:\n%q", full)
	}
}
