package packages

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const MaxPackageBytes int64 = 32 << 20

const MaxPackageFiles = 500

func ValidateTree(root string, m Manifest) error {
	if len(m.Files) > MaxPackageFiles {
		return fmt.Errorf("packages: validate: %d files exceeds cap %d", len(m.Files), MaxPackageFiles)
	}
	var total int64
	for _, f := range m.Files {
		total += f.Size
	}
	if total > MaxPackageBytes {
		return fmt.Errorf("packages: validate: total size %d exceeds cap %d", total, MaxPackageBytes)
	}
	expected := make(map[string]FileEntry, len(m.Files))
	for _, f := range m.Files {
		expected[f.Path] = f
	}
	actual := make(map[string]int64, len(m.Files))
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			return fmt.Errorf("packages: validate: symlink %s", rel)
		case mode&fs.ModeDevice != 0:
			return fmt.Errorf("packages: validate: device %s", rel)
		case mode&fs.ModeNamedPipe != 0:
			return fmt.Errorf("packages: validate: fifo %s", rel)
		case mode&fs.ModeSocket != 0:
			return fmt.Errorf("packages: validate: socket %s", rel)
		case !mode.IsRegular():
			return fmt.Errorf("packages: validate: irregular file %s", rel)
		}
		if err := checkNoHardlinks(path, info); err != nil {
			return err
		}
		if rel == ManifestFilename || rel == ReceiptFilename {
			return nil
		}
		if _, ok := expected[rel]; !ok {
			return fmt.Errorf("packages: validate: unexpected file %s", rel)
		}
		actual[rel] = info.Size()
		return nil
	})
	if err != nil {
		return err
	}
	for _, f := range m.Files {
		if _, ok := actual[f.Path]; !ok {
			return fmt.Errorf("packages: validate: missing file %s", f.Path)
		}
	}
	for _, f := range m.Files {
		if got := actual[f.Path]; got != f.Size {
			return fmt.Errorf("packages: validate: size mismatch %s: got %d want %d", f.Path, got, f.Size)
		}
	}
	for _, f := range m.Files {
		if err := verifyFileHash(filepath.Join(root, filepath.FromSlash(f.Path)), f.Path, f.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func verifyFileHash(path, name, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("packages: validate: hash %s: %w", name, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("packages: validate: hash mismatch %s: got %s want %s", name, got, want)
	}
	return nil
}
