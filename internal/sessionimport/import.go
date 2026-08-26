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

var ErrInvalidSource = errors.New("sessionimport: source is not a valid Pi session file")

var ErrConflict = errors.New("sessionimport: destination exists with different content")

var ErrUnsupportedPlatform = errors.New("sessionimport: only linux is supported")

type ImportStats struct {
	Entries    int
	PerType    map[string]int
	Opaque     int
	Idempotent bool
}

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

	headerChunk, hdr, err := readHeader(br)
	if err != nil {
		return "", stats, err
	}
	if hdr.ID == "" || hdr.Timestamp == "" || hdr.Cwd == "" {
		return "", stats, fmt.Errorf("%w: header is missing id, timestamp, or cwd", ErrInvalidSource)
	}

	dir, err := store.DirForCwd(hdr.Cwd)
	if err != nil {
		return "", stats, fmt.Errorf("sessionimport: resolve destination for cwd %q: %w", hdr.Cwd, err)
	}
	destPath, err = session.SessionFilePath(dir, hdr.Timestamp, hdr.ID)
	if err != nil {
		return "", stats, fmt.Errorf("%w: invalid session identity: %v", ErrInvalidSource, err)
	}

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
			return nil
		}
		line := bytes.TrimRight(chunk, "\r\n")
		e, derr := session.DecodeEntry(line)
		if derr != nil {
			return nil
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

	idempotent, err := commitAtomic(tmpName, destPath, wantHash)
	if err != nil {
		return "", stats, err
	}
	stats.Idempotent = idempotent
	return destPath, stats, nil
}

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
