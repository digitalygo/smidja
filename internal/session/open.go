package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type OpenOptions struct {
	Strict bool
}

func (s *Store) Open(pathOrID string, opts OpenOptions) (*Session, error) {
	if pathOrID == "" {
		return nil, errors.New("session: empty path or session id")
	}
	path, err := s.resolveSessionPath(pathOrID)
	if err != nil {
		return nil, err
	}
	l, err := LoadWithOptions(path, LoadOptions{Strict: opts.Strict})
	if err != nil {
		return nil, fmt.Errorf("session: open %q: %w", path, err)
	}
	if err := validateOpenHeader(l.Header()); err != nil {
		return nil, fmt.Errorf("session: open %q: %w", path, err)
	}
	lock, err := acquireFileLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		lock.release()
		return nil, fmt.Errorf("session: open %q for append: %w", path, err)
	}
	if !l.fileEndsWithNewline {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			f.Close()
			lock.release()
			return nil, fmt.Errorf("session: terminate trailing line of %q: %w", path, err)
		}
		if err := f.Sync(); err != nil {
			f.Close()
			lock.release()
			return nil, fmt.Errorf("session: sync %q: %w", path, err)
		}
	}
	sess := &Session{
		id:              l.Header().ID,
		cwd:             l.Header().Cwd,
		path:            path,
		headerTimestamp: l.Header().Timestamp,
		file:            f,
		lock:            lock,
		used:            make(map[string]struct{}, len(l.Entries())+1),
		profile:         findRuntimeProfile(l.Entries()),
	}
	for _, e := range l.Entries() {
		if id, _, _ := envelopeOf(e); id != "" {
			sess.used[id] = struct{}{}
		}
	}
	if l.Leaf() != nil {
		sess.leaf, _, _ = envelopeOf(l.Leaf())
	}
	return sess, nil
}

func (s *Store) resolveSessionPath(pathOrID string) (string, error) {
	if fi, err := os.Stat(pathOrID); err == nil {
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("session: %q is not a regular file", pathOrID)
		}
		abs, err := filepath.Abs(pathOrID)
		if err != nil {
			return "", fmt.Errorf("session: resolve %q: %w", pathOrID, err)
		}
		return abs, nil
	}
	if len(pathOrID) <= maxSessionIDLen && sessionIDRe.MatchString(pathOrID) {
		return s.findSessionFileByID(pathOrID)
	}
	return "", fmt.Errorf("session: no session file or id %q", pathOrID)
}

func (s *Store) findSessionFileByID(id string) (string, error) {
	suffix := "_" + id + ".jsonl"
	var found []string
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), suffix) {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("session: search for session id %q: %w", id, err)
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("session: no session with id %q", id)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("session: session id %q is ambiguous across %d files", id, len(found))
	}
}

func validateOpenHeader(h *Header) error {
	if h == nil {
		return ErrNotASession
	}
	if h.Type != EntryTypeSession {
		return ErrNotASession
	}
	if h.Version != sessionFormatVersion {
		return fmt.Errorf("session: header version %d, want %d", h.Version, sessionFormatVersion)
	}
	if h.ID == "" {
		return errors.New("session: header has empty id")
	}
	if _, err := time.Parse(time.RFC3339Nano, h.Timestamp); err != nil {
		return fmt.Errorf("session: header timestamp %q is not RFC3339: %w", h.Timestamp, err)
	}
	return nil
}
