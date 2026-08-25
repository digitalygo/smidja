//go:build linux

package sessionimport

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
)

// commitAtomic makes destPath name the temp file's content without ever
// replacing an existing file, and reports whether destPath already held
// identical content (idempotent). The temp file's name is left for the
// caller's deferred cleanup.
//
// The commit is atomic on Linux: os.Link (link(2)) fails with EEXIST if
// destPath already exists, so the existence check and the placement are
// a single operation with no window in which a concurrent import could
// slip in. When the link hits an existing destination, that destination
// is read and hashed: an identical SHA-256 makes the import idempotent,
// anything else is ErrConflict. The comparison always runs on whatever
// file actually won the race, which closes the check-then-replace race
// of a rename-based commit.
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
