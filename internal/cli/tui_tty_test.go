//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/digitalygo/smidja/internal/agent"
)

const (
	cliPTYIOCGPTN   = 0x80045430
	cliPTYIOCSPTLCK = 0x40045431
	cliPTYIOCSWINSZ = 0x5414
)

type cliPTYWinsize struct {
	rows uint16
	cols uint16
	xpix uint16
	ypix uint16
}

func cliPTYIoctl(fd uintptr, request uintptr, arg uintptr) syscall.Errno {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, request, arg, 0, 0, 0)
	return errno
}

func openCLIPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("TTY selection test requires linux devpts")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("TTY selection test needs /dev/ptmx: %v", err)
	}
	unlock := 0
	if errno := cliPTYIoctl(master.Fd(), cliPTYIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		t.Skipf("TTY selection test needs unlockpt ioctl: %v", errno)
	}
	var number uint32
	if errno := cliPTYIoctl(master.Fd(), cliPTYIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
		master.Close()
		t.Skipf("TTY selection test needs TIOCGPTN ioctl: %v", errno)
	}
	slave, err := os.OpenFile("/dev/pts/"+itoaCLIPTY(int(number)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Skipf("TTY selection test needs the slave device: %v", err)
	}
	size := cliPTYWinsize{rows: 24, cols: 80}
	if errno := cliPTYIoctl(slave.Fd(), cliPTYIOCSWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		slave.Close()
		master.Close()
		t.Skipf("TTY selection test needs TIOCSWINSZ ioctl: %v", errno)
	}
	t.Cleanup(func() {
		slave.Close()
		master.Close()
	})
	return master, slave
}

func itoaCLIPTY(value int) string {
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

func cliPTYReadable(file *os.File, timeout time.Duration) bool {
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

func readMasterUntil(t *testing.T, master *os.File, fragment string, timeout time.Duration) string {
	t.Helper()
	var output []byte
	buffer := make([]byte, 4096)
	deadline := time.Now().Add(timeout)
	for {
		if fragmentIndexCLIPTY(string(output), fragment) >= 0 {
			return string(output)
		}
		if !time.Now().Before(deadline) {
			break
		}
		remaining := time.Until(deadline)
		slice := 50 * time.Millisecond
		if remaining < slice {
			slice = remaining
		}
		if !cliPTYReadable(master, slice) {
			continue
		}
		n, err := master.Read(buffer)
		if n > 0 {
			output = append(output, buffer[:n]...)
		}
		if n == 0 && err != nil {
			continue
		}
	}
	t.Fatalf("timed out waiting for %q, got %q", fragment, string(output))
	return ""
}

func fragmentIndexCLIPTY(output, fragment string) int {
	for i := 0; i+len(fragment) <= len(output); i++ {
		if output[i:i+len(fragment)] == fragment {
			return i
		}
	}
	return -1
}

func TestRunChatSelectsTUIOnRealTerminal(t *testing.T) {
	master, slave := openCLIPTY(t)
	var stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdin = slave
	deps.Stdout = slave
	deps.Stderr = &stderr
	deps.Home = func() string { return t.TempDir() }
	deps.Client = &capturingClient{script: []*agent.AssistantMessage{textStop("tty answer")}}
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps([]string{"--tui-mode", "regular"}, deps)
	}()
	readMasterUntil(t, master, "]0;smidja", 10*time.Second)
	if _, err := master.Write([]byte("hi\r")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	readMasterUntil(t, master, "tty answer", 10*time.Second)
	if _, err := master.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("write quit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithDeps on a real terminal: %v (stderr %q)", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runChat on a real terminal did not exit after /quit")
	}
	if strings.Contains(stderr.String(), "tui unavailable") {
		t.Errorf("stderr = %q, want the TUI to own the real terminal", stderr.String())
	}
}

func TestRunChatTUIReturnsContextError(t *testing.T) {
	master, slave := openCLIPTY(t)
	var stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Stdin = slave
	deps.Stdout = slave
	deps.Stderr = &stderr
	deps.Home = func() string { return t.TempDir() }
	deps.Client = &capturingClient{}
	deps.Config = testConfig(t, t.TempDir())
	deps.Store = wiringStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	deps.Context = ctx
	done := make(chan error, 1)
	go func() {
		done <- RunWithDeps([]string{"--tui-mode", "fullscreen"}, deps)
	}()
	readMasterUntil(t, master, "]0;smidja", 10*time.Second)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("RunWithDeps = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runChat did not return after cancel")
	}
}
