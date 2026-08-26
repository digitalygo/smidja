//go:build linux

package sessionimport

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
)

func commitAtomic(tmpName, destPath string, wantHash []byte) (bool, error) {
	if err := os.Link(tmpName, destPath); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrExist) {
		return false, fmt.Errorf("sessionimport: link to %q: %w", destPath, err)
	}

	existing, err := os.ReadFile(destPath)
	if err != nil {
		return false, fmt.Errorf("sessionimport: read destination %q: %w", destPath, err)
	}
	existingHash := sha256.Sum256(existing)
	if bytes.Equal(existingHash[:], wantHash) {
		return true, nil
	}
	return false, fmt.Errorf("%w: %q already exists with different content", ErrConflict, destPath)
}
