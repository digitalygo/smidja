// Package sessionimport imports existing Pi (or Pi-format) session files
// into a smidja session store. It validates the source like Pi's
// loadEntriesFromFile, preserves the raw bytes of every kept line, and
// writes the result to the canonical Store location with the same munged
// directory and file naming the Store uses, so imported sessions are
// indistinguishable from sessions smidja created itself.
package sessionimport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/digitalygo/smidja/internal/session"
)

// ErrInvalidSource is returned when the source file is not a valid Pi
// session: its first parsed line is not a session header with a string
// id, or the header is missing the id, timestamp, or cwd needed to place
// the file in the store.
var ErrInvalidSource = errors.New("sessionimport: source is not a valid Pi session file")

// ErrConflict is returned when the canonical destination already exists
// with different content. The existing file is never overwritten.
var ErrConflict = errors.New("sessionimport: destination exists with different content")

// ErrUnsupportedPlatform is returned when the atomic no-replace commit
// (link(2)) is unavailable, which is the case on every platform but
// Linux. The destination is left untouched.
var ErrUnsupportedPlatform = errors.New("sessionimport: only linux is supported")

// ImportStats describes one import: how many entries were copied, how
// they break down by type, how many were opaque (unknown future types),
// and whether the destination already held identical content.
type ImportStats struct {
	// Entries is the number of entries copied, excluding the session
	// header.
	Entries int
	// PerType counts copied entries by their JSON "type" field,
	// excluding the header. The map is never nil.
	PerType map[string]int
	// Opaque is the number of copied entries whose type the codec does
	// not understand; their bytes are preserved verbatim.
	Opaque int
	// Idempotent reports that the destination already existed with
	// identical content, so nothing was written.
	Idempotent bool
}

// Import copies the Pi session file at srcPath into store and returns the
// path of the imported file plus per-type statistics.
//
// The source is streamed line by line: blank and malformed lines are
// skipped exactly like Pi's parseSessionEntries, and the first parsed
// line must be a session header with a string id, timestamp, and cwd. The
// destination directory is the Store's munged directory for the header
// cwd, and the file name is the canonical "<timestamp>_<id>.jsonl" form,
// so an import lands exactly where the Store would have written the
// session. Every kept line is copied byte-for-byte, never re-marshaled,
// so known and unknown entry types survive unchanged (including line
// endings).
//
// The imported bytes are written to a 0600 temp file in the destination
// directory, synced, and committed into place without ever replacing an
// existing file. The commit uses os.Link (link(2)), which fails with
// EEXIST when the destination already exists, so the existence check and
// the placement are one atomic operation with no race window. When the
// link hits an existing destination, that file is hashed: an identical
// SHA-256 makes the import idempotent (ImportStats.Idempotent is true
// and the file is left untouched), different content fails with
// ErrConflict and nothing is overwritten. The comparison always runs on
// the file that won the race, so a concurrent importer can never be
// silently replaced.
//
// The atomic no-replace commit depends on Linux link(2) semantics; on
// any other platform Import fails with ErrUnsupportedPlatform rather
// than falling back to a racy rename.
func Import(srcPath string, store *session.Store) (destPath string, stats ImportStats, err error) {
	stats.PerType = make(map[string]int)
	if store == nil {
		return "", stats, errors.New("sessionimport: nil store")
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", stats, fmt.Errorf("sessionimport: open %q: %w", srcPath, err)
	}
	defer src.Close()
	br := bufio.NewReader(src)

	// Phase 1: locate the session header. Blank and malformed lines
	// before it are skipped like Pi; the first parsed line must be a
	// session header with a string id.
	headerChunk, hdr, err := readHeader(br)
	if err != nil {
		return "", stats, err
	}
	if hdr.ID == "" || hdr.Timestamp == "" || hdr.Cwd == "" {
		return "", stats, fmt.Errorf("%w: header is missing id, timestamp, or cwd", ErrInvalidSource)
	}

	// Phase 2: resolve the destination from the header, exactly like the
	// Store would. SessionFilePath validates the id and timestamp and
	// rejects an identity that would escape the store root.
	dir, err := store.DirForCwd(hdr.Cwd)
	if err != nil {
		return "", stats, fmt.Errorf("sessionimport: resolve destination for cwd %q: %w", hdr.Cwd, err)
	}
	destPath, err = session.SessionFilePath(dir, hdr.Timestamp, hdr.ID)
	if err != nil {
		return "", stats, fmt.Errorf("%w: invalid session identity: %v", ErrInvalidSource, err)
	}

	// Phase 3: stream the remaining lines into a temp file in the
	// destination directory, preserving raw bytes and hashing as we go.
	tmp, err := os.CreateTemp(dir, ".smidja-import-*")
	if err != nil {
		return "", stats, fmt.Errorf("sessionimport: create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil {
			tmp.Close()
		}
		os.Remove(tmpName)
	}()

	hasher := sha256.New()
	write := func(chunk []byte) error {
		if _, err := tmp.Write(chunk); err != nil {
			return err
		}
		_, err := hasher.Write(chunk)
		return err
	}
	if err := write(headerChunk); err != nil {
		return "", stats, fmt.Errorf("sessionimport: write header: %w", err)
	}

	if err := forEachLine(br, func(chunk []byte) error {
		if len(bytes.TrimSpace(chunk)) == 0 {
			return nil // blank line, skipped like Pi
		}
		line := bytes.TrimRight(chunk, "\r\n")
		e, derr := session.DecodeEntry(line)
		if derr != nil {
			return nil // malformed line, skipped like Pi
		}
		stats.Entries++
		stats.PerType[e.EntryType()]++
		if _, ok := e.(*session.OpaqueEntry); ok {
			stats.Opaque++
		}
		return write(chunk)
	}); err != nil {
		return "", stats, fmt.Errorf("sessionimport: read %q: %w", srcPath, err)
	}

	if err := tmp.Sync(); err != nil {
		return "", stats, fmt.Errorf("sessionimport: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", stats, fmt.Errorf("sessionimport: close temp file: %w", err)
	}
	tmp = nil
	wantHash := hasher.Sum(nil)

	// Phase 4: commit atomically without ever replacing an existing
	// destination. commitAtomic is platform-specific: on Linux it is
	// link(2)-based and race-free, elsewhere it fails with
	// ErrUnsupportedPlatform.
	idempotent, err := commitAtomic(tmpName, destPath, wantHash)
	if err != nil {
		return "", stats, err
	}
	stats.Idempotent = idempotent
	return destPath, stats, nil
}

// readHeader scans the reader for the session header: the first parsed
// line must be a session header with a string id. Blank and malformed
// lines before it are skipped like Pi's loadEntriesFromFile; a parsed
// non-header line or an unreadable header makes the whole file invalid.
// It returns the header line's raw bytes (with its line ending) so the
// import can copy them verbatim.
func readHeader(br *bufio.Reader) ([]byte, session.Header, error) {
	var hdr session.Header
	for {
		chunk, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(chunk)) > 0 {
			line := bytes.TrimRight(chunk, "\r\n")
			var probe struct {
				Type json.RawMessage `json:"type"`
			}
			if err := json.Unmarshal(line, &probe); err != nil {
				// Malformed line before the header: skip, like Pi.
			} else if !bytes.Equal(probe.Type, []byte(`"session"`)) {
				return nil, hdr, fmt.Errorf("%w: first parsed entry is not a session header", ErrInvalidSource)
			} else if err := json.Unmarshal(line, &hdr); err != nil {
				return nil, hdr, fmt.Errorf("%w: header does not parse: %v", ErrInvalidSource, err)
			} else {
				return chunk, hdr, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, hdr, fmt.Errorf("%w: no session header found", ErrInvalidSource)
			}
			return nil, hdr, err
		}
	}
}

// forEachLine calls fn for every raw line chunk read from r, including
// its newline terminator (absent only on a final unterminated line).
func forEachLine(r *bufio.Reader, fn func(chunk []byte) error) error {
	for {
		chunk, err := r.ReadBytes('\n')
		if len(chunk) > 0 {
			if err := fn(chunk); err != nil {
				return err
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
