//go:build linux

package tui

import (
	"syscall"
	"time"
)

const fdSetWordBits = 32 << (^uint(0) >> 63)

func fdSetSet(set *syscall.FdSet, fd int) {
	set.Bits[fd/fdSetWordBits] |= 1 << (uint(fd) % fdSetWordBits)
}

func fdSetIsSet(set *syscall.FdSet, fd int) bool {
	return set.Bits[fd/fdSetWordBits]&(1<<(uint(fd)%fdSetWordBits)) != 0
}

func stdinReadableSelect(fd uintptr, timeout time.Duration) (bool, error) {
	for {
		var set syscall.FdSet
		fdSetSet(&set, int(fd))
		remaining := syscall.NsecToTimeval(timeout.Nanoseconds())
		if _, err := syscall.Select(int(fd)+1, &set, nil, nil, &remaining); err != nil {
			if err == syscall.EINTR {
				continue
			}
			return false, err
		}
		return fdSetIsSet(&set, int(fd)), nil
	}
}
