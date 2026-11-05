package managerpackages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (s *Store) Install(ctx context.Context, source Source) (Revision, error) {
	var release Release
	data, err := s.fetch(ctx, source.URL, source, 1<<20)
	if err != nil {
		return Revision{}, err
	}
	if err = strictJSON(data, &release); err != nil {
		return Revision{}, err
	}
	if !identifier.MatchString(release.ID) || !versionPattern.MatchString(release.Version) || !digestPattern.MatchString(release.SHA256) {
		return Revision{}, errors.New("invalid package release")
	}
	archive, err := s.fetch(ctx, release.ArchiveURL, source, maximumBytes)
	if err != nil {
		return Revision{}, err
	}
	return s.InstallArchive(source, release, archive)
}
func (s *Store) InstallArchive(source Source, release Release, archive []byte) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return Revision{}, errors.New("package store closed")
	}
	if _, err := sourceURL(source.URL); err != nil {
		return Revision{}, err
	}
	if len(archive) > maximumBytes {
		return Revision{}, errors.New("package archive too large")
	}
	hash := sha256.Sum256(archive)
	if hex.EncodeToString(hash[:]) != release.SHA256 {
		return Revision{}, errors.New("package archive digest mismatch")
	}
	files, m, err := unpack(archive)
	if err != nil {
		return Revision{}, err
	}
	if m.ID != release.ID || m.Version != release.Version {
		return Revision{}, errors.New("release and manifest differ")
	}
	if err = validateManifest(m, s.cfg.Version, s.cfg.Target, s.cfg.Scope); err != nil {
		return Revision{}, err
	}
	if err = validateCatalog(m, files); err != nil {
		return Revision{}, err
	}
	if current, ok := s.active[m.ID]; ok {
		if current.Source != source {
			return Revision{}, errors.New("package source is pinned; explicit separate installation required")
		}
		comparison, err := compareVersion(m.Version, current.Version)
		if err != nil {
			return Revision{}, err
		}
		if comparison < 0 {
			return Revision{}, errors.New("automatic downgrade rejected")
		}
		if comparison == 0 {
			if current.Digest != release.SHA256 {
				return Revision{}, errors.New("published version is immutable")
			}
			if err = s.verifyInstalled(current); err != nil {
				return Revision{}, err
			}
			return copyValue(current), nil
		}
	}
	revision := Revision{ID: m.ID, Version: m.Version, Digest: release.SHA256, Source: source, Manifest: m}
	staging, err := os.MkdirTemp(s.cfg.Dir, ".staging-")
	if err != nil {
		return Revision{}, err
	}
	defer func() {
		filepath.Walk(staging, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
		_ = os.RemoveAll(staging)
	}()
	payload := filepath.Join(staging, "payload")
	if err = os.MkdirAll(payload, 0700); err != nil {
		return Revision{}, err
	}
	for _, entry := range m.Files {
		data, ok := files[entry.Path]
		if !ok {
			return Revision{}, errors.New("manifest file missing")
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return Revision{}, fmt.Errorf("file digest mismatch: %s", entry.Path)
		}
		path := filepath.Join(payload, filepath.FromSlash(entry.Path))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return Revision{}, err
		}
		mode := os.FileMode(0444)
		if entry.Executable {
			mode = 0555
		}
		if err = writePayload(path, data, mode); err != nil {
			return Revision{}, err
		}
	}
	if len(files) != len(m.Files)+1 {
		return Revision{}, errors.New("archive contains unlisted files")
	}
	if err = writePayload(filepath.Join(payload, ManifestFilename), files[ManifestFilename], 0444); err != nil {
		return Revision{}, err
	}
	if err = atomicJSON(filepath.Join(staging, "receipt.json"), revision); err != nil {
		return Revision{}, err
	}
	if err = os.Chmod(filepath.Join(staging, "receipt.json"), 0444); err != nil {
		return Revision{}, err
	}
	if err = filepath.Walk(staging, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && path != staging {
			if err := syncDirectory(path); err != nil {
				return err
			}
			return os.Chmod(path, 0555)
		}
		return nil
	}); err != nil {
		return Revision{}, err
	}
	final := s.revisionPath(m.ID, m.Version, release.SHA256)
	if err = os.MkdirAll(filepath.Dir(final), 0700); err != nil {
		return Revision{}, err
	}
	if _, err = os.Stat(final); err == nil {
		if err = s.verifyInstalled(revision); err != nil {
			return Revision{}, err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err = os.Rename(staging, final); err != nil {
			return Revision{}, err
		}
		if err = os.Chmod(final, 0555); err != nil {
			return Revision{}, err
		}
	} else {
		return Revision{}, err
	}
	if err = syncDirectory(filepath.Dir(final)); err != nil {
		return Revision{}, err
	}
	next := copyValue(s.active)
	next[m.ID] = revision
	if err = atomicJSON(filepath.Join(s.cfg.Dir, "active.json"), next); err != nil {
		return Revision{}, err
	}
	s.active = next
	return copyValue(revision), nil
}
func unpack(archive []byte) (map[string][]byte, Manifest, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, Manifest{}, err
	}
	if len(reader.File) > 4096 {
		return nil, Manifest{}, errors.New("too many package files")
	}
	files := map[string][]byte{}
	total := int64(0)
	for _, entry := range reader.File {
		if !validPath(entry.Name) || !entry.Mode().IsRegular() {
			return nil, Manifest{}, errors.New("package entries must be regular relative files")
		}
		if _, ok := files[entry.Name]; ok {
			return nil, Manifest{}, errors.New("duplicate archive path")
		}
		if entry.UncompressedSize64 > maximumBytes {
			return nil, Manifest{}, errors.New("expanded package too large")
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, Manifest{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maximumBytes-total+1))
		stream.Close()
		if readErr != nil {
			return nil, Manifest{}, readErr
		}
		total += int64(len(data))
		if total > maximumBytes {
			return nil, Manifest{}, errors.New("expanded package too large")
		}
		files[entry.Name] = data
	}
	var m Manifest
	if err = strictJSON(files[ManifestFilename], &m); err != nil {
		return nil, Manifest{}, err
	}
	return files, m, nil
}
func (s *Store) Resolve(revision Revision) ([]Revision, error) {
	resolved := []Revision{}
	visiting := map[string]bool{}
	seen := map[string]bool{}
	var visit func(Revision) error
	visit = func(r Revision) error {
		key := r.ID + "@" + r.Version + "#" + r.Digest
		if visiting[key] {
			return errors.New("package dependency cycle")
		}
		if seen[key] {
			return nil
		}
		if err := s.validateRevision(r); err != nil {
			return err
		}
		visiting[key] = true
		for _, dependency := range r.Manifest.Dependencies {
			pinned, err := s.GetPinned(dependency.ID, dependency.Version, dependency.Digest)
			if err != nil {
				return fmt.Errorf("dependency %s@%s unavailable: %w", dependency.ID, dependency.Version, err)
			}
			if err = visit(pinned); err != nil {
				return err
			}
		}
		visiting[key] = false
		seen[key] = true
		resolved = append(resolved, copyValue(r))
		return nil
	}
	if err := visit(revision); err != nil {
		return nil, err
	}
	return resolved, nil
}

func writePayload(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
