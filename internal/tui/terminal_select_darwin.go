//go:build darwin

package tui

import (
	"syscall"
	"time"
)

func fdSetSet(set *syscall.FdSet, fd int) {
	set.Bits[fd/32] |= 1 << (uint(fd) % 32)
}

func fdSetIsSet(set *syscall.FdSet, fd int) bool {
	return set.Bits[fd/32]&(1<<(uint(fd)%32)) != 0
}

func stdinReadableSelect(fd uintptr, timeout time.Duration) (bool, error) {
	for {
		var set syscall.FdSet
		fdSetSet(&set, int(fd))
		remaining := syscall.NsecToTimeval(timeout.Nanoseconds())
		if err := syscall.Select(int(fd)+1, &set, nil, nil, &remaining); err != nil {
			if err == syscall.EINTR {
				continue
			}
			return false, err
		}
		return fdSetIsSet(&set, int(fd)), nil
	}
}
