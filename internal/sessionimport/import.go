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
	"path/filepath"

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
// directory, synced, and atomically renamed into place. If the canonical
// destination already exists with an identical SHA-256 of the imported
// content the import is idempotent (ImportStats.Idempotent is true and
// the file is left untouched). If it exists with different content the
// import fails with ErrConflict and nothing is overwritten.
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
	// Store would.
	dir, err := store.DirForCwd(hdr.Cwd)
	if err != nil {
		return "", stats, fmt.Errorf("sessionimport: resolve destination for cwd %q: %w", hdr.Cwd, err)
	}
	destPath = filepath.Join(dir, session.SessionFileName(hdr.Timestamp, hdr.ID))

	// Phase 3: stream the remaining lines into a temp file in the
	// destination directory, preserving raw bytes and hashing as we go.
	tmp, err := os.CreateTemp(dir, ".smidja-import-*")
	if err != nil {
		return "", stats, fmt.Errorf("sessionimport: create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if tmp != nil {
			tmp.Close()
		}
		if !committed {
			os.Remove(tmpName)
		}
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

	// Phase 4: idempotence or conflict check, then atomic rename.
	if existing, statErr := os.ReadFile(destPath); statErr == nil {
		existingHash := sha256.Sum256(existing)
		if bytes.Equal(existingHash[:], wantHash) {
			stats.Idempotent = true
			return destPath, stats, nil
		}
		return "", stats, fmt.Errorf("%w: %q already exists with different content", ErrConflict, destPath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", stats, fmt.Errorf("sessionimport: read destination %q: %w", destPath, statErr)
	}

	if err := os.Rename(tmpName, destPath); err != nil {
		return "", stats, fmt.Errorf("sessionimport: rename to %q: %w", destPath, err)
	}
	committed = true
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
