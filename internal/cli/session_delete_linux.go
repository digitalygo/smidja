//go:build linux

package cli

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func deleteTransactionSupported() error {
	return nil
}

type deleteSidecarLock struct {
	file *os.File
}

func acquireDeleteSidecarLock(c *deleteCandidate) (*deleteSidecarLock, error) {
	if c == nil || c.parentFile == nil || c.name == "" || c.lockPath == "" {
		return nil, errors.New("session: delete candidate lost its transaction handles")
	}
	fd, err := syscall.Openat(int(c.parentFile.Fd()), c.name+".lock", syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, errDeleteSymlink
		}
		return nil, fmt.Errorf("session: open session lock %q: %w", c.lockPath, err)
	}
	file := os.NewFile(uintptr(fd), c.lockPath)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("session: inspect session lock %q: %w", c.lockPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errDeleteSymlink
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("session: session lock %q is held by another writer: %w", c.lockPath, err)
	}
	return &deleteSidecarLock{file: file}, nil
}

func (l *deleteSidecarLock) identity() (os.FileInfo, error) {
	if l == nil || l.file == nil {
		return nil, errors.New("session: delete lock is not held")
	}
	return l.file.Stat()
}

func (l *deleteSidecarLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func anchoredUnlink(c *deleteCandidate) error {
	if c == nil || c.parentFile == nil || c.name == "" {
		return errors.New("session: delete candidate lost its transaction handles")
	}
	return syscall.Unlinkat(int(c.parentFile.Fd()), c.name)
}
