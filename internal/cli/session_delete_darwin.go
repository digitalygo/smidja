//go:build darwin

package cli

import (
	"errors"
	"fmt"
	"os"
)

func deleteTransactionSupported() error {
	return nil
}

type deleteSidecarLock struct {
	file *os.File
	root *os.Root
	name string
}

func acquireDeleteSidecarLock(c *deleteCandidate) (*deleteSidecarLock, error) {
	if c == nil || c.parentRoot == nil || c.name == "" || c.lockPath == "" {
		return nil, errors.New("session: delete candidate lost its transaction handles")
	}
	file, err := c.parentRoot.OpenFile(c.name+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("session: session lock is held by another writer")
		}
		return nil, fmt.Errorf("session: create session lock %q: %w", c.lockPath, err)
	}
	return &deleteSidecarLock{file: file, root: c.parentRoot, name: c.name + ".lock"}, nil
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
	info, statErr := file.Stat()
	closeErr := file.Close()
	return errors.Join(removeCreatedDeleteLock(l.root, l.name, info), statErr, closeErr)
}

func removeCreatedDeleteLock(root *os.Root, name string, identity os.FileInfo) error {
	if root == nil || name == "" || identity == nil {
		return errors.New("session: cannot verify the created session lock identity")
	}
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session: inspect session lock: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return errors.New("session: refusing to remove a replaced session lock")
	}
	if !os.SameFile(identity, current) {
		return errors.New("session: refusing to remove a replaced session lock")
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("session: remove session lock: %w", err)
	}
	return nil
}

func anchoredUnlink(c *deleteCandidate) error {
	if c == nil || c.parentRoot == nil || c.name == "" {
		return errors.New("session: delete candidate lost its transaction handles")
	}
	return c.parentRoot.Remove(c.name)
}
