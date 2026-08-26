package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

const sessionFormatVersion = 3

const headerTimestampLayout = "2006-01-02T15:04:05.000Z"

const maxIDCollisions = 100

const maxSessionIDLen = 36

var sessionIDRe = regexp.MustCompile(`^[0-9a-fA-F-]+$`)

type Store struct {
	root string
}

func NewStore(rootDir string) (*Store, error) {
	if rootDir == "" {
		return nil, errors.New("session: empty root dir")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("session: make root dir %q absolute: %w", rootDir, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("session: create root dir %q: %w", abs, err)
	}
	return &Store{root: abs}, nil
}

func (s *Store) Create(cwd string) (*Session, error) {
	if cwd == "" {
		return nil, errors.New("session: empty cwd")
	}
	id, err := newUUIDv7()
	if err != nil {
		return nil, fmt.Errorf("session: generate session id: %w", err)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("session: make cwd %q absolute: %w", cwd, err)
	}
	dir, err := s.DirForCwd(abs)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	headerTimestamp := now.Format(headerTimestampLayout)
	path, err := SessionFilePath(dir, headerTimestamp, id)
	if err != nil {
		return nil, err
	}
	return &Session{
		id:              id,
		cwd:             abs,
		path:            path,
		headerTimestamp: headerTimestamp,
		used:            make(map[string]struct{}),
	}, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) DirForCwd(cwd string) (string, error) {
	if cwd == "" {
		return "", errors.New("session: empty cwd")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("session: make cwd %q absolute: %w", cwd, err)
	}
	dir := filepath.Join(s.root, dirNameForCwd(abs))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("session: create session dir %q: %w", dir, err)
	}
	return dir, nil
}

func SessionFileName(headerTimestamp, id string) (string, error) {
	if id == "" {
		return "", errors.New("session: empty session id")
	}
	if len(id) > maxSessionIDLen {
		return "", fmt.Errorf("session: session id %q is %d characters, want at most %d", id, len(id), maxSessionIDLen)
	}
	if !sessionIDRe.MatchString(id) {
		return "", fmt.Errorf("session: session id %q contains characters outside [0-9a-fA-F-]", id)
	}
	if headerTimestamp == "" {
		return "", errors.New("session: empty header timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, headerTimestamp)
	if err != nil {
		return "", fmt.Errorf("session: header timestamp %q is not RFC3339: %v", headerTimestamp, err)
	}
	if _, offset := t.Zone(); offset != 0 {
		return "", fmt.Errorf("session: header timestamp %q is not UTC", headerTimestamp)
	}
	fileStamp := strings.ReplaceAll(strings.ReplaceAll(headerTimestamp, ":", "-"), ".", "-")
	name := fileStamp + "_" + id + ".jsonl"
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("session: file name %q is not a local path", name)
	}
	return name, nil
}

func SessionFilePath(dir, headerTimestamp, id string) (string, error) {
	name, err := SessionFileName(headerTimestamp, id)
	if err != nil {
		return "", err
	}
	return filePathUnder(dir, name)
}

func filePathUnder(dir, name string) (string, error) {
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("session: file name %q is not a local path", name)
	}
	cleanDir := filepath.Clean(dir)
	full := filepath.Clean(filepath.Join(cleanDir, name))
	prefix := cleanDir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if full != cleanDir && !strings.HasPrefix(full, prefix) {
		return "", fmt.Errorf("session: file name %q escapes session dir %q", name, dir)
	}
	return full, nil
}

func (s *Store) List(cwd string) ([]string, error) {
	if cwd == "" {
		return nil, errors.New("session: empty cwd")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("session: make cwd %q absolute: %w", cwd, err)
	}
	dir := filepath.Join(s.root, dirNameForCwd(abs))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("session: list sessions under %q: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Slice(paths, func(i, j int) bool {
		si, ei := os.Stat(paths[i])
		sj, ej := os.Stat(paths[j])
		if ei == nil && ej == nil && !si.ModTime().Equal(sj.ModTime()) {
			return si.ModTime().After(sj.ModTime())
		}
		return paths[i] > paths[j]
	})
	return paths, nil
}

func dirNameForCwd(cwd string) string {
	munged := strings.TrimPrefix(filepath.Clean(cwd), "/")
	munged = strings.ReplaceAll(munged, "/", "-")
	munged = strings.ReplaceAll(munged, ":", "-")
	return "--" + munged + "--"
}

type Session struct {
	id              string
	cwd             string
	path            string
	headerTimestamp string

	mu     sync.Mutex
	file   *os.File
	leaf   string
	used   map[string]struct{}
	closed bool
}

func (sess *Session) Path() string {
	return sess.path
}

func (sess *Session) AppendUser(m *agent.UserMessage) error {
	if m == nil {
		return errors.New("session: nil user message")
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("session: marshal user message: %w", err)
	}
	return sess.append(&MessageEntry{Message: payload})
}

func (sess *Session) AppendAssistant(m *agent.AssistantMessage) error {
	if m == nil {
		return errors.New("session: nil assistant message")
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("session: marshal assistant message: %w", err)
	}
	return sess.append(&MessageEntry{Message: payload})
}

func (sess *Session) AppendToolResult(m *agent.ToolResultMessage) error {
	if m == nil {
		return errors.New("session: nil tool result message")
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("session: marshal tool result message: %w", err)
	}
	return sess.append(&MessageEntry{Message: payload})
}

func (sess *Session) AppendEntry(e Entry) error {
	if e == nil {
		return errors.New("session: nil entry")
	}
	return sess.append(e)
}

func (sess *Session) append(e Entry) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return errors.New("session: append on closed session")
	}
	base, err := entryBaseOf(e)
	if err != nil {
		return err
	}
	id, err := sess.newEntryIDLocked()
	if err != nil {
		return err
	}
	var parentID *string
	if sess.leaf != "" {
		pid := sess.leaf
		parentID = &pid
	}
	base.Type = e.EntryType()
	base.ID = id
	base.ParentID = parentID
	base.Timestamp = time.Now().UTC().Format(headerTimestampLayout)
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("session: marshal entry: %w", err)
	}
	if err := sess.writeLocked(append(line, '\n')); err != nil {
		return err
	}
	sess.used[id] = struct{}{}
	sess.leaf = id
	return nil
}

func (sess *Session) newEntryIDLocked() (string, error) {
	for i := 0; i < maxIDCollisions; i++ {
		id, err := shortID()
		if err != nil {
			return "", fmt.Errorf("session: generate entry id: %w", err)
		}
		if _, used := sess.used[id]; !used {
			return id, nil
		}
	}
	id, err := fullUUID()
	if err != nil {
		return "", fmt.Errorf("session: generate fallback entry id: %w", err)
	}
	return id, nil
}

func (sess *Session) writeLocked(line []byte) error {
	if sess.file == nil {
		if err := sess.firstWriteLocked(line); err != nil {
			return err
		}
		return nil
	}
	if _, err := sess.file.Write(line); err != nil {
		return fmt.Errorf("session: append to %q: %w", sess.path, err)
	}
	if err := sess.file.Sync(); err != nil {
		return fmt.Errorf("session: sync %q: %w", sess.path, err)
	}
	return nil
}

func (sess *Session) firstWriteLocked(line []byte) error {
	header, err := json.Marshal(Header{
		Type:      EntryTypeSession,
		Version:   sessionFormatVersion,
		ID:        sess.id,
		Timestamp: sess.headerTimestamp,
		Cwd:       sess.cwd,
	})
	if err != nil {
		return fmt.Errorf("session: marshal header: %w", err)
	}
	f, err := os.OpenFile(sess.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("session: create %q: %w", sess.path, err)
	}
	cleanup := func(wrapErr error) error {
		f.Close()
		os.Remove(sess.path)
		return wrapErr
	}
	if _, err := f.Write(append(header, '\n')); err != nil {
		return cleanup(fmt.Errorf("session: write header to %q: %w", sess.path, err))
	}
	if _, err := f.Write(line); err != nil {
		return cleanup(fmt.Errorf("session: append to %q: %w", sess.path, err))
	}
	if err := f.Sync(); err != nil {
		return cleanup(fmt.Errorf("session: sync %q: %w", sess.path, err))
	}
	sess.file = f
	return nil
}

func (sess *Session) Close() error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.closed = true
	if sess.file == nil {
		return nil
	}
	err := sess.file.Close()
	sess.file = nil
	if err != nil {
		return fmt.Errorf("session: close %q: %w", sess.path, err)
	}
	return nil
}
