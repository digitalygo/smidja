package packages

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const StaleLockAge = 10 * time.Minute

type Index struct {
	SchemaVersion int               `json:"schemaVersion"`
	Generation    int               `json:"generation"`
	Active        []ActiveEntry     `json:"active"`
	Installed     []InstalledRecord `json:"installed"`
}

type ActiveEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type InstalledRecord struct {
	ID              string               `json:"id"`
	Version         string               `json:"version"`
	Owner           string               `json:"owner"`
	Repo            string               `json:"repo"`
	Commit          string               `json:"commit"`
	ManifestSHA256  string               `json:"manifestSha256"`
	InstalledAt     string               `json:"installedAt"`
	Integrity       string               `json:"integrity"`
	Authenticity    string               `json:"authenticity"`
	ResolvedDepends []ResolvedDependency `json:"resolvedDepends"`
}

type ResolvedDependency struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

const (
	IntegrityOK            = "ok"
	AuthenticityUnverified = "unverified"
)

type Store struct {
	root string
	mu   sync.Mutex
}

func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".smidja", "packages")
	}
	return filepath.Join(home, ".smidja", "packages")
}

func Open(root string) (*Store, error) {
	if root == "" {
		root = DefaultRoot()
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("packages: open store %s: %w", root, err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".staging"), 0o755); err != nil {
		return nil, fmt.Errorf("packages: open store: %w", err)
	}
	return &Store{root: root}, nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Index() (Index, error) {
	return s.readIndex()
}

func (s *Store) Installed() ([]InstalledRecord, error) {
	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	return idx.Installed, nil
}

func (s *Store) Active() ([]ActiveEntry, error) {
	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	return idx.Active, nil
}

func (s *Store) Manifest(id, version string) (Manifest, error) {
	return s.loadManifest(id, version)
}

func (s *Store) InstalledIndex() (map[string]InstalledInfo, error) {
	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	out := map[string]InstalledInfo{}
	for _, rec := range idx.Installed {
		cur, ok := out[rec.ID]
		if ok && compareVersions(rec.Version, cur.Version) <= 0 {
			continue
		}
		m, err := s.loadManifest(rec.ID, rec.Version)
		if err != nil {
			return nil, err
		}
		out[rec.ID] = InstalledInfo{ID: rec.ID, Version: rec.Version, Owner: rec.Owner, Repo: rec.Repo, Manifest: m}
	}
	return out, nil
}

func (s *Store) indexPath() string {
	return filepath.Join(s.root, "index.json")
}

func (s *Store) readIndex() (Index, error) {
	data, err := os.ReadFile(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return Index{SchemaVersion: 0}, nil
	}
	if err != nil {
		return Index{}, fmt.Errorf("packages: read index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, fmt.Errorf("packages: read index: %w", err)
	}
	if idx.SchemaVersion != 0 {
		return Index{}, fmt.Errorf("packages: read index: schemaVersion %d, want 0", idx.SchemaVersion)
	}
	return idx, nil
}

func (s *Store) writeIndex(idx Index) error {
	idx.Generation++
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("packages: write index: %w", err)
	}
	tmp, err := os.CreateTemp(s.root, ".index-*")
	if err != nil {
		return fmt.Errorf("packages: write index: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("packages: write index: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("packages: write index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("packages: write index: %w", err)
	}
	if err := os.Rename(name, s.indexPath()); err != nil {
		return fmt.Errorf("packages: write index: %w", err)
	}
	return syncDir(s.root)
}

func (s *Store) withLock(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := acquireLock(filepath.Join(s.root, ".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func acquireLock(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		writeLockInfo(f)
		return lockRelease(path), nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("packages: acquire lock: %w", err)
	}
	st, statErr := os.Stat(path)
	if statErr == nil && time.Since(st.ModTime()) > StaleLockAge {
		if rmErr := os.Remove(path); rmErr == nil {
			f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err == nil {
				writeLockInfo(f)
				return lockRelease(path), nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrLocked, path)
}

func writeLockInfo(f *os.File) {
	fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	f.Close()
}

func lockRelease(path string) func() {
	return func() { os.Remove(path) }
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("packages: sync %s: %w", dir, err)
	}
	return nil
}

func entryKey(id, version string) string {
	return id + "@" + version
}

func installedRecord(idx Index, id, version string) (InstalledRecord, bool) {
	for _, rec := range idx.Installed {
		if rec.ID == id && rec.Version == version {
			return rec, true
		}
	}
	return InstalledRecord{}, false
}

func activeContains(active []ActiveEntry, id, version string) bool {
	for _, a := range active {
		if a.ID == id && a.Version == version {
			return true
		}
	}
	return false
}

func removeInstalled(installed []InstalledRecord, id, version string) []InstalledRecord {
	out := installed[:0]
	for _, rec := range installed {
		if rec.ID == id && rec.Version == version {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func (s *Store) loadManifest(id, version string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(s.root, id, version, ManifestFilename))
	if err != nil {
		return Manifest{}, fmt.Errorf("packages: load manifest %s@%s: %w", id, version, err)
	}
	return Parse(data)
}

func writeReceipt(dir string, rec InstalledRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("packages: receipt: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".receipt-*")
	if err != nil {
		return fmt.Errorf("packages: receipt: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("packages: receipt: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("packages: receipt: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("packages: receipt: %w", err)
	}
	if err := os.Rename(name, filepath.Join(dir, ReceiptFilename)); err != nil {
		return fmt.Errorf("packages: receipt: %w", err)
	}
	return syncDir(dir)
}
