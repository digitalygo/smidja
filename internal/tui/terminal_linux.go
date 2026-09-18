//go:build linux

package tui

import (
	"os"
	"syscall"
	"unsafe"
)

type linuxTerminalOps struct{}

func (linuxTerminalOps) name() string { return "linux" }

func ioctl(fd uintptr, request uintptr, arg uintptr) syscall.Errno {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, request, arg, 0, 0, 0)
	return errno
}

func (linuxTerminalOps) isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	var termios syscall.Termios
	return ioctl(file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&termios))) == 0
}

func (linuxTerminalOps) makeRaw(file *os.File) (*rawState, error) {
	var termios syscall.Termios
	if errno := ioctl(file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&termios))); errno != 0 {
		return nil, errno
	}
	saved := &rawState{value: termios}

	raw := termios
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if errno := ioctl(file.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return saved, nil
}

func (linuxTerminalOps) restoreState(file *os.File, state *rawState) error {
	if file == nil || state == nil {
		return os.ErrInvalid
	}
	restored := state.value.(syscall.Termios)
	if errno := ioctl(file.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&restored))); errno != 0 {
		return errno
	}
	return nil
}

type winsize struct {
	rows uint16
	cols uint16
	xpix uint16
	ypix uint16
}

func (linuxTerminalOps) windowSize(file *os.File) (int, int, error) {
	if file == nil {
		return 0, 0, os.ErrInvalid
	}
	var size winsize
	if errno := ioctl(file.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		return 0, 0, errno
	}
	return int(size.cols), int(size.rows), nil
}

func init() {
	setDefaultTerminalOps(linuxTerminalOps{})
}

func resizeSignalChannel() (chan os.Signal, func(), bool) {
	notify := make(chan os.Signal, 1)
	signalNotifyResize(notify)
	stop := func() { signalStopResize(notify) }
	return notify, stop, true
}

func refreshTerminalDimensions() {
	signalSelf(resizeSignal)
}
