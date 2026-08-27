//go:build !linux

package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// On platforms without flock the lock file's exclusive existence is the
// cross-process lock; it is removed on release.
type fileLock struct {
	path string
	ok   bool
}

func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("session: lock file %q is held by another writer", path)
		}
		return nil, fmt.Errorf("session: create lock file %q: %w", path, err)
	}
	f.Close()
	return &fileLock{path: path, ok: true}, nil
}

func (l *fileLock) release() error {
	if l == nil || !l.ok {
		return nil
	}
	l.ok = false
	return os.Remove(l.path)
}
