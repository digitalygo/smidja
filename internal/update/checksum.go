package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// verifyChecksum opens path, hashes it with SHA-256, and requires that
// checksums matches the resulting digest for assetName.
func verifyChecksum(checksums []byte, assetName, path string) error {
	expected, err := findChecksum(checksums, assetName)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("update: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("update: hash %s: %w", path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("%w: %s: got %s, want %s", ErrChecksumMismatch, assetName, got, expected)
	}
	return nil
}

// findChecksum scans checksums.txt lines for the entry whose filename is
// assetName. Lines use the common "hex  filename" layout, with an
// optional leading "*" before the filename. The matching entry must be
// unique and carry a 64-character hex digest; zero or multiple matches,
// or a malformed digest, are errors.
func findChecksum(checksums []byte, assetName string) (string, error) {
	var digests []string
	for _, line := range strings.Split(string(checksums), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		if !isHexDigest(fields[0]) {
			return "", fmt.Errorf("%w: %s: %q", ErrChecksumEntry, assetName, line)
		}
		digests = append(digests, strings.ToLower(fields[0]))
	}
	if len(digests) == 0 {
		return "", fmt.Errorf("%w: no entry for %s", ErrChecksumEntry, assetName)
	}
	if len(digests) > 1 {
		return "", fmt.Errorf("%w: %d entries for %s", ErrChecksumEntry, len(digests), assetName)
	}
	return digests[0], nil
}

// isHexDigest reports whether s is a 64-character hexadecimal string, the
// SHA-256 digest format used by checksums.txt.
func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
