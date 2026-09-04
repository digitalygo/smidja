//go:build !linux

package authstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

const (
	lockPollInterval    = 10 * time.Millisecond
	lockAcquireDeadline = 30 * time.Second
)

type fileLock struct {
	path string
	ok   bool
}

func acquireFileLock(path string) (*fileLock, error) {
	deadline := time.Now().Add(lockAcquireDeadline)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return &fileLock{path: path, ok: true}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("authstore: create lock file %q: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("authstore: lock file %q is held by another writer", path)
		}
		time.Sleep(lockPollInterval)
	}
}

func (l *fileLock) release() error {
	if l == nil || !l.ok {
		return nil
	}
	l.ok = false
	return os.Remove(l.path)
}
