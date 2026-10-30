package managerpackages

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) Retained() ([]Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Revision{}
	root := filepath.Join(s.cfg.Dir, "revisions")
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("retained revision root is not a directory")
	}
	packages, err := os.ReadDir(root)
	if err != nil {
		return result, err
	}
	var failures []error
	for _, pkg := range packages {
		if !pkg.IsDir() || !identifier.MatchString(pkg.Name()) {
			failures = append(failures, errors.New("invalid retained package directory"))
			continue
		}
		versions, err := os.ReadDir(filepath.Join(root, pkg.Name()))
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, version := range versions {
			name := version.Name()
			split := strings.LastIndex(name, "-")
			if !version.IsDir() || split < 0 || !versionPattern.MatchString(name[:split]) || !digestPattern.MatchString(name[split+1:]) {
				failures = append(failures, errors.New("invalid retained revision directory"))
				continue
			}
			path := filepath.Join(root, pkg.Name(), name)
			if err = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return errors.New("symlink in retained revision")
				}
				return nil
			}); err != nil {
				failures = append(failures, err)
				continue
			}
			receipt := filepath.Join(path, "receipt.json")
			info, err := os.Lstat(receipt)
			if err != nil || !info.Mode().IsRegular() || info.Size() > maximumBytes {
				failures = append(failures, errors.New("invalid retained revision receipt"))
				continue
			}
			data, err := os.ReadFile(receipt)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			var revision Revision
			if err = strictJSON(data, &revision); err == nil {
				if revision.ID != pkg.Name() || revision.Version != name[:split] || revision.Digest != name[split+1:] {
					err = errors.New("retained revision identity mismatch")
				}
			}
			if err == nil {
				err = s.validateRevision(revision)
			}
			if err == nil {
				err = s.verifyInstalled(revision)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("retained package %s: %w", pkg.Name(), err))
				continue
			}
			result = append(result, copyValue(revision))
		}
	}
	return result, errors.Join(failures...)
}
