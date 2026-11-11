package managerpackages

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"syscall"
)

type Config struct {
	Dir        string
	Version    string
	Target     string
	Scope      string
	HTTPClient *http.Client
	LookupEnv  func(string) string
}
type Source struct {
	URL      string `json:"url"`
	TokenEnv string `json:"token_env,omitempty"`
}
type Release struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	ArchiveURL string `json:"archive_url"`
	SHA256     string `json:"sha256"`
}
type Revision struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Digest   string   `json:"digest"`
	Source   Source   `json:"source"`
	Manifest Manifest `json:"manifest"`
}
type Store struct {
	mu     sync.Mutex
	cfg    Config
	active map[string]Revision
	lock   *os.File
}

func Open(cfg Config) (*Store, error) {
	if !filepath.IsAbs(cfg.Dir) || !supportedTargets[cfg.Target] || (cfg.Scope != "manager" && cfg.Scope != "client") {
		return nil, errors.New("absolute directory, supported target and execution scope required")
	}
	if _, err := compareVersion(cfg.Version, cfg.Version); err != nil {
		return nil, err
	}
	if cfg.LookupEnv == nil {
		cfg.LookupEnv = os.Getenv
	}
	if err := os.MkdirAll(cfg.Dir, 0700); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(cfg.Dir, ".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), "package lock")
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("package store already open")
	}
	s := &Store{cfg: cfg, lock: lock, active: map[string]Revision{}}
	data, err := os.ReadFile(filepath.Join(cfg.Dir, "active.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Close()
		return nil, err
	}
	if err == nil {
		if err = strictJSON(data, &s.active); err != nil {
			s.Close()
			return nil, err
		}
		if s.active == nil {
			s.Close()
			return nil, errors.New("invalid package index")
		}
	}
	for id, revision := range s.active {
		if id != revision.ID || s.validateRevision(revision) != nil || s.verifyInstalled(revision) != nil {
			s.Close()
			return nil, errors.New("invalid active package revision")
		}
	}
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}
func copyValue[T any](value T) T {
	data, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}
func (s *Store) List() []Revision {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Revision, 0, len(s.active))
	for _, revision := range s.active {
		out = append(out, copyValue(revision))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) Active(id string) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revision, ok := s.active[id]
	if !ok {
		return Revision{}, os.ErrNotExist
	}
	return copyValue(revision), nil
}
func (s *Store) revisionPath(id, version, digest string) string {
	return filepath.Join(s.cfg.Dir, "revisions", id, version+"-"+digest)
}
func (s *Store) validateRevision(r Revision) error {
	if r.ID != r.Manifest.ID || r.Version != r.Manifest.Version || !digestPattern.MatchString(r.Digest) {
		return errors.New("invalid revision")
	}
	if _, err := sourceURL(r.Source.URL); err != nil {
		return err
	}
	return validateManifest(r.Manifest, s.cfg.Version, s.cfg.Target, s.cfg.Scope)
}
func (s *Store) GetPinned(id, version, digest string) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !identifier.MatchString(id) || !versionPattern.MatchString(version) || !digestPattern.MatchString(digest) {
		return Revision{}, errors.New("invalid revision reference")
	}
	data, err := os.ReadFile(filepath.Join(s.revisionPath(id, version, digest), "receipt.json"))
	if err != nil {
		return Revision{}, err
	}
	var r Revision
	if err = strictJSON(data, &r); err != nil {
		return Revision{}, err
	}
	if r.ID != id || r.Version != version || r.Digest != digest {
		return Revision{}, errors.New("revision receipt mismatch")
	}
	return r, s.validateRevision(r)
}
func (s *Store) ReadFile(r Revision, name string) ([]byte, error) {
	if err := s.validateRevision(r); err != nil {
		return nil, err
	}
	var digest string
	for _, file := range r.Manifest.Files {
		if file.Path == name {
			digest = file.SHA256
			break
		}
	}
	if digest == "" || !validPath(name) {
		return nil, errors.New("file is not part of the pinned revision")
	}
	path := filepath.Join(s.revisionPath(r.ID, r.Version, r.Digest), "payload", filepath.FromSlash(name))
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("package file is not regular")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != digest {
		return nil, errors.New("package file integrity mismatch")
	}
	return data, nil
}
func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return fmt.Errorf("package directory sync: %w", err)
	}
	return nil
}

func (s *Store) verifyInstalled(revision Revision) error {
	data, err := os.ReadFile(filepath.Join(s.revisionPath(revision.ID, revision.Version, revision.Digest), "receipt.json"))
	if err != nil {
		return err
	}
	var receipt Revision
	if err = strictJSON(data, &receipt); err != nil {
		return err
	}
	if !reflect.DeepEqual(receipt, revision) {
		return errors.New("installed revision receipt mismatch")
	}
	for _, file := range revision.Manifest.Files {
		if _, err = s.ReadFile(revision, file.Path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) FilePath(revision Revision, name string) (string, error) {
	if _, err := s.ReadFile(revision, name); err != nil {
		return "", err
	}
	return filepath.Join(s.revisionPath(revision.ID, revision.Version, revision.Digest), "payload", filepath.FromSlash(name)), nil
}
