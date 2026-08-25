// Package session persists smidja conversations as JSONL files aligned
// with the Pi coding-agent v3 session format observed on the installed Pi
// 0.84.2 client, so a future import command can read Pi session trees.
//
// Layout under the sessions root: one munged directory per working
// directory ("--" + absolute cwd with '/' and ':' replaced by '-' + "--"),
// containing one append-only *.jsonl file per session, named
// "<ISO timestamp with ':' and '.' replaced by '-'>_<uuidv7>.jsonl".
//
// Every file starts with a session header line, then one message entry per
// line. Entries chain through id/parentId: each entry's parentId names the
// id of the entry before it (null for the first). The header is written
// lazily, on the first successful Append, mirroring Pi's deferred file
// creation: no file exists until the first entry is persisted.
//
// Writes are synchronous: each line is written in one Write call and the
// file is fsynced before the session leaf advances, so a failed append
// (marshal, write, or sync error) leaves the session unchanged and
// retryable. Files are created 0600 and directories 0700.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

// sessionFormatVersion is the Pi session JSONL format version written into
// every session header.
const sessionFormatVersion = 3

// headerTimestampLayout renders times exactly like JavaScript's
// Date.prototype.toISOString: UTC with three millisecond digits and a Z
// suffix (for example 2026-08-25T00:02:45.655Z).
const headerTimestampLayout = "2006-01-02T15:04:05.000Z"

// maxIDCollisions is how many 8-hex entry id draws are attempted before
// falling back to a full UUID, mirroring Pi's generateId loop.
const maxIDCollisions = 100

// Store is the sessions root: the directory that holds one munged
// subdirectory per working directory.
type Store struct {
	root string
}

// NewStore validates rootDir, canonicalizes it to an absolute path, and
// creates it with 0700 permissions. All paths returned by the store are
// absolute.
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

// Create starts a new session for cwd: it generates a UUIDv7 session id
// and the header timestamp, computes the session file path under the
// munged directory for cwd, and creates that directory with 0700
// permissions. The session file itself is not created until the first
// successful Append (lazy creation), matching Pi's deferral of file
// creation until the first persisted entry.
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
	dir := filepath.Join(s.root, dirNameForCwd(abs))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("session: create session dir %q: %w", dir, err)
	}
	now := time.Now().UTC()
	headerTimestamp := now.Format(headerTimestampLayout)
	fileStamp := strings.ReplaceAll(strings.ReplaceAll(headerTimestamp, ":", "-"), ".", "-")
	return &Session{
		id:              id,
		cwd:             abs,
		path:            filepath.Join(dir, fileStamp+"_"+id+".jsonl"),
		headerTimestamp: headerTimestamp,
		used:            make(map[string]struct{}),
	}, nil
}

// List returns the absolute paths of the *.jsonl session files under the
// munged directory for cwd, newest first (by modification time, then by
// file name), as a convenience for a future resume flow. It returns an
// empty slice when no sessions exist for cwd yet.
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

// dirNameForCwd encodes cwd into the munged session directory name used
// under the sessions root: the absolute, cleaned path with its leading
// slash stripped and every '/' and ':' replaced by '-', wrapped in
// "--" ... "--" (for example /var/home/foo becomes --var-home-foo--).
// This mirrors Pi's session directory scheme.
func dirNameForCwd(cwd string) string {
	munged := strings.TrimPrefix(filepath.Clean(cwd), "/")
	munged = strings.ReplaceAll(munged, "/", "-")
	munged = strings.ReplaceAll(munged, ":", "-")
	return "--" + munged + "--"
}

// Session is one append-only JSONL session file. Every entry chains to the
// previous one via parentId, and the leaf advances only after a successful
// write and sync, so a failed append leaves the session unchanged.
type Session struct {
	id              string // UUIDv7 session id
	cwd             string // absolute working directory, stored in the header
	path            string // absolute session file path
	headerTimestamp string // ISO timestamp shared by the header and file name

	mu   sync.Mutex
	file *os.File // nil until the first successful append
	leaf string   // id of the last persisted entry; "" before any append
	used map[string]struct{}
	// used holds every entry id already persisted in this session, for
	// collision-checking new ids.
	closed bool
}

// Path returns the absolute path of the session file. The path is fixed at
// creation; the file itself only appears on the first successful append.
func (sess *Session) Path() string {
	return sess.path
}

// AppendUser persists one user message entry, chaining it to the previous
// entry. On success the session leaf advances to the new entry; on error
// (marshal, write, or sync failure) nothing is persisted and the leaf
// stays unchanged.
func (sess *Session) AppendUser(m *agent.UserMessage) error {
	if m == nil {
		return errors.New("session: nil user message")
	}
	return sess.append(m)
}

// AppendAssistant persists one assistant message entry, with the same
// success and failure semantics as AppendUser.
func (sess *Session) AppendAssistant(m *agent.AssistantMessage) error {
	if m == nil {
		return errors.New("session: nil assistant message")
	}
	return sess.append(m)
}

// AppendToolResult persists one tool result message entry, with the same
// success and failure semantics as AppendUser.
func (sess *Session) AppendToolResult(m *agent.ToolResultMessage) error {
	if m == nil {
		return errors.New("session: nil tool result message")
	}
	return sess.append(m)
}

// append marshals and persists one message entry. The parentId is the
// current leaf (null for the first entry), the entry id is an 8-hex
// crypto/rand draw collision-checked against ids already used in this
// session, and the entry timestamp is RFC3339 UTC ISO. The entry is
// marshaled before any file I/O, so a marshal failure leaves the session
// untouched, including the leaf. The leaf advances only after the line is
// written and the file synced.
func (sess *Session) append(m any) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return errors.New("session: append on closed session")
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
	entry := messageEntry{
		Type:      "message",
		ID:        id,
		ParentID:  parentID,
		Timestamp: time.Now().UTC().Format(headerTimestampLayout),
		Message:   m,
	}
	line, err := json.Marshal(entry)
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

// newEntryIDLocked draws an 8-hex entry id from crypto/rand, rejecting ids
// already used in this session. After maxIDCollisions consecutive
// collisions it falls back to a full UUID, mirroring Pi's generateId.
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

// writeLocked appends one full line to the session file, creating the file
// and writing the header first when this is the first persisted entry.
// Callers hold mu.
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

// firstWriteLocked creates the session file (O_APPEND, 0600), writes the
// header line followed by the first entry line, and syncs. On any failure
// the partial file is removed so the session stays lazily uncreated and a
// retry starts clean. Callers hold mu.
func (sess *Session) firstWriteLocked(line []byte) error {
	header, err := json.Marshal(sessionHeader{
		Type:      "session",
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

// Close closes the session file. It is idempotent and a no-op for a
// session whose file was never created. Every append is synced on write,
// so Close performs no additional flush. Appending after Close returns an
// error.
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

// sessionHeader is the first line of every session file, aligned with Pi's
// v3 header: type, version, id, timestamp, cwd.
type sessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

// messageEntry wraps one agent message in the Pi session entry envelope:
// type, id, parentId, timestamp, message.
type messageEntry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
	Message   any     `json:"message"`
}
