//go:build !linux && !darwin

package cli

import (
	"os"
)

func deleteTransactionSupported() error {
	return errDeleteUnsupported
}

type deleteSidecarLock struct{}

func acquireDeleteSidecarLock(c *deleteCandidate) (*deleteSidecarLock, error) {
	return nil, errDeleteUnsupported
}

func (l *deleteSidecarLock) identity() (os.FileInfo, error) {
	return nil, errDeleteUnsupported
}

func (l *deleteSidecarLock) release() error {
	return errDeleteUnsupported
}

func anchoredUnlink(c *deleteCandidate) error {
	return errDeleteUnsupported
}
