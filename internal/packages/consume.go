package packages

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func ReadManifestFromArchive(archivePath string) (Manifest, error) {
	dir, err := os.MkdirTemp("", "smidja-manifest-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("packages: manifest temp: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := extractArchive(archivePath, dir, ManifestFilename); err != nil {
		return Manifest{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		return Manifest{}, fmt.Errorf("packages: read manifest from %s: %w", archivePath, err)
	}
	return Parse(data)
}

func (s *Store) ConfigDefaults(id, version string) (map[string]string, error) {
	m, err := s.loadManifest(id, version)
	if err != nil {
		return nil, err
	}
	root := m.Contents["config"]
	if root == "" {
		return map[string]string{}, nil
	}
	dir := filepath.Join(s.root, id, version, filepath.FromSlash(root))
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return parseConfigDefaults(dir)
}

func (s *Store) ActiveConfigDefaults() (map[string]string, error) {
	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, a := range idx.Active {
		m, err := s.loadManifest(a.ID, a.Version)
		if err != nil {
			return nil, err
		}
		root := m.Contents["config"]
		if root == "" {
			continue
		}
		vals, err := parseConfigDefaults(filepath.Join(s.root, a.ID, a.Version, filepath.FromSlash(root)))
		if err != nil {
			return nil, err
		}
		for k, v := range vals {
			out[k] = v
		}
	}
	return out, nil
}

func (s *Store) Verify(id, version string) error {
	if !packageIDPattern.MatchString(id) {
		return fmt.Errorf("packages: verify: invalid id %q", id)
	}
	if !isCanonicalVersion(version) {
		return fmt.Errorf("packages: verify: invalid version %q", version)
	}
	idx, err := s.readIndex()
	if err != nil {
		return err
	}
	if _, ok := installedRecord(idx, id, version); !ok {
		return fmt.Errorf("%w: %s@%s", ErrNotInstalled, id, version)
	}
	m, err := s.loadManifest(id, version)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.root, id, version)
	if err := ValidateTree(dir, m); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ReceiptFilename)); err != nil {
		return fmt.Errorf("packages: verify: %s@%s: receipt: %w", id, version, err)
	}
	return nil
}

func parseConfigDefaults(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("packages: config defaults: symlink %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			out[key] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
