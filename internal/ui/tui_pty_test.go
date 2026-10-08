//go:build linux

package ui

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/digitalygo/smidja/internal/tui"
)

const (
	ptyIOCGPTN   = 0x80045430
	ptyIOCSPTLCK = 0x40045431
	ptyIOCSWINSZ = 0x5414
	ptyTCGETS    = 0x5401
)

type ptyWinsize struct {
	rows uint16
	cols uint16
	xpix uint16
	ypix uint16
}

func ptyIoctl(fd uintptr, request uintptr, arg uintptr) syscall.Errno {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, request, arg, 0, 0, 0)
	return errno
}

type ptyPair struct {
	master *os.File
	slave  *os.File
}

func openPTY(t *testing.T) (ptyPair, bool) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("real PTY smoke test requires linux devpts")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("real PTY smoke test needs /dev/ptmx: %v", err)
	}
	unlock := 0
	if errno := ptyIoctl(master.Fd(), ptyIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		t.Skipf("real PTY smoke test needs unlockpt ioctl: %v", errno)
	}
	var number uint32
	if errno := ptyIoctl(master.Fd(), ptyIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
		master.Close()
		t.Skipf("real PTY smoke test needs TIOCGPTN ioctl: %v", errno)
	}
	slaveName := "/dev/pts/" + itoaPTY(int(number))
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Skipf("real PTY smoke test needs %s: %v", slaveName, err)
	}
	size := ptyWinsize{rows: 24, cols: 80}
	if errno := ptyIoctl(slave.Fd(), ptyIOCSWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		slave.Close()
		master.Close()
		t.Skipf("real PTY smoke test needs TIOCSWINSZ ioctl: %v", errno)
	}
	t.Cleanup(func() {
		slave.Close()
		master.Close()
	})
	return ptyPair{master: master, slave: slave}, true
}

func itoaPTY(value int) string {
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

func ptyTermios(t *testing.T, file *os.File) syscall.Termios {
	t.Helper()
	var state syscall.Termios
	if errno := ptyIoctl(file.Fd(), ptyTCGETS, uintptr(unsafe.Pointer(&state))); errno != 0 {
		t.Fatalf("TCGETS: %v", errno)
	}
	return state
}

const ptyPollInterval = 50 * time.Millisecond

func ptyReadable(fd int, timeout time.Duration) bool {
	for {
		var set syscall.FdSet
		wordBits := 32 << (^uint(0) >> 63)
		set.Bits[fd/wordBits] |= 1 << (uint(fd) % uint(wordBits))
		wait := syscall.NsecToTimeval(timeout.Nanoseconds())
		count, err := syscall.Select(fd+1, &set, nil, nil, &wait)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false
		}
		return count > 0 && set.Bits[fd/wordBits]&(1<<(uint(fd)%uint(wordBits))) != 0
	}
}

func ptyReadUntil(t *testing.T, file *os.File, fragment string, timeout time.Duration) string {
	t.Helper()
	fd := int(file.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		t.Fatalf("SetNonblock: %v", err)
	}
	var output []byte
	buffer := make([]byte, 4096)
	deadline := time.Now().Add(timeout)
	for {
		if containsFragment(string(output), fragment) {
			return string(output)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		wait := remaining
		if wait > ptyPollInterval {
			wait = ptyPollInterval
		}
		if !ptyReadable(fd, wait) {
			continue
		}
		n, err := syscall.Read(fd, buffer)
		if n > 0 {
			output = append(output, buffer[:n]...)
		}
		if err != nil && err != syscall.EAGAIN {
			break
		}
	}
	t.Fatalf("timed out waiting for %q, got %q", fragment, string(output))
	return ""
}

func containsFragment(output, fragment string) bool {
	for i := 0; i+len(fragment) <= len(output); i++ {
		if output[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

func fragmentIndex(output, fragment string) int {
	for i := 0; i+len(fragment) <= len(output); i++ {
		if output[i:i+len(fragment)] == fragment {
			return i
		}
	}
	return -1
}

func TestRealPTYFullscreenEnterOutputExitOrder(t *testing.T) {
	pair, ok := openPTY(t)
	if !ok {
		return
	}
	if !isTerminalFile(pair.slave) {
		t.Fatal("PTY slave should be detected as a terminal through the ioctl path")
	}
	before := ptyTermios(t, pair.slave)
	terminal := tui.NewProcessTerminal(pair.slave, pair.slave)
	screen := tui.NewAltScreen(terminal, false, tui.AltScreenOptions{})
	screen.AddChild(tui.NewText("pty smoke marker", 0, 0, nil))
	if err := screen.Start(); err != nil {
		t.Fatalf("Start() on a real PTY: %v", err)
	}
	stream := ptyReadUntil(t, pair.master, tui.AltScreenEnter, 5*time.Second)
	stream += ptyReadUntil(t, pair.master, "pty smoke marker", 5*time.Second)
	enter := fragmentIndex(stream, tui.AltScreenEnter)
	marker := fragmentIndex(stream, "pty smoke marker")
	if enter < 0 || marker < 0 || enter > marker {
		t.Fatalf("alt-screen enter must precede output, got %q", stream)
	}
	screen.Stop(tui.StopOptions{})
	rest := ptyReadUntil(t, pair.master, tui.AltScreenExit, 5*time.Second)
	full := stream + rest
	exit := fragmentIndex(full, tui.AltScreenExit)
	lastMarker := fragmentIndex(full, "pty smoke marker")
	if exit < 0 || exit < lastMarker {
		t.Fatalf("alt-screen exit must follow output, got %q", full)
	}
	after := ptyTermios(t, pair.slave)
	if after != before {
		t.Fatal("terminal attributes were not restored after Stop")
	}
}
