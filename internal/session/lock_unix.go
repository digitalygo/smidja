//go:build linux

package session

import (
	"fmt"
	"os"
	"syscall"
)

type fileLock struct {
	f *os.File
}

func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: open lock file %q: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("session: lock file %q is held by another writer: %w", path, err)
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
