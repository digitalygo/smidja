package machinetools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}
type Entry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Symlink   bool   `json:"symlink"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
}
type Listing struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func digest(b []byte) string  { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func textBytes(b []byte) bool { return utf8.Valid(b) && !bytes.ContainsRune(b, 0) }

func (s *Service) read(ctx context.Context, path string) Result {
	p, err := absolute(path)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	b, err := s.readBytesContext(ctx, p)
	if err != nil {
		return failure("read_failed", err)
	}
	if !textBytes(b) {
		return failure("unsupported_file", errors.New("file is not UTF-8 text"))
	}
	return success(File{p, string(b), digest(b)})
}
func (s *Service) list(path string) Result {
	p, err := absolute(path)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	dir, err := os.Open(p)
	if err != nil {
		return failure("read_failed", err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(s.cfg.MaxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) && len(entries) == 0 {
		return failure("read_failed", err)
	}
	result := Listing{Path: p, Entries: []Entry{}}
	truncated := len(entries) > s.cfg.MaxEntries
	if truncated {
		entries = entries[:s.cfg.MaxEntries]
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return failure("read_failed", err)
		}
		entryPath := filepath.Join(p, entry.Name())
		isDirectory := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			if target, statErr := os.Stat(entryPath); statErr == nil {
				isDirectory = target.IsDir()
			}
		}
		result.Entries = append(result.Entries, Entry{entry.Name(), entryPath, isDirectory, entry.Type()&os.ModeSymlink != 0, info.Size(), info.Mode().String()})
	}
	return Result{OK: true, Data: result, Truncated: truncated}
}
func (s *Service) write(a Arguments) Result {
	s.writes.Lock()
	defer s.writes.Unlock()
	p, err := absolute(a.Path)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	return s.replace(p, []byte(a.Content), a.ExpectedSHA256)
}
func (s *Service) edit(a Arguments) Result {
	s.writes.Lock()
	defer s.writes.Unlock()
	p, err := absolute(a.Path)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	b, err := s.readBytes(p)
	if err != nil {
		return failure("read_failed", err)
	}
	if !textBytes(b) {
		return failure("unsupported_file", errors.New("file is not UTF-8 text"))
	}
	if a.ExpectedSHA256 == "" || digest(b) != a.ExpectedSHA256 {
		return failure("conflict", errors.New("file changed or expected_sha256 is missing"))
	}
	if a.OldText == "" || strings.Count(string(b), a.OldText) != 1 {
		return failure("ambiguous_edit", errors.New("old_text must identify exactly one occurrence"))
	}
	return s.replace(p, []byte(strings.Replace(string(b), a.OldText, a.NewText, 1)), a.ExpectedSHA256)
}
func (s *Service) replace(path string, b []byte, expected string) Result {
	if len(b) > s.cfg.MaxBytes {
		return failure("too_large", errors.New("content exceeds text editor size limit"))
	}
	if !textBytes(b) {
		return failure("unsupported_file", errors.New("content is not UTF-8 text"))
	}
	resolved, err := filepath.EvalSymlinks(path)
	exists := true
	if errors.Is(err, fs.ErrNotExist) {
		exists = false
		resolved = path
	} else if err != nil {
		return failure("write_failed", err)
	}
	mode := os.FileMode(0o600)
	if exists {
		old, err := s.readBytes(resolved)
		if err != nil {
			return failure("read_failed", err)
		}
		if !textBytes(old) {
			return failure("unsupported_file", errors.New("existing file is not UTF-8 text"))
		}
		if expected == "" || digest(old) != expected {
			return failure("conflict", errors.New("file changed or expected_sha256 is missing"))
		}
		writable, err := os.OpenFile(resolved, os.O_WRONLY, 0)
		if err != nil {
			return failure("write_failed", err)
		}
		info, err := writable.Stat()
		writable.Close()
		if err != nil {
			return failure("write_failed", err)
		}
		mode = info.Mode().Perm()
	} else if expected != "" {
		return failure("conflict", errors.New("file no longer exists"))
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), ".smidja-edit-*")
	if err != nil {
		return failure("write_failed", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return failure("write_failed", err)
	}
	if exists {
		current, readErr := s.readBytes(resolved)
		if readErr != nil || digest(current) != expected {
			return failure("conflict", errors.New("file changed before save"))
		}
		err = os.Rename(tmpName, resolved)
	} else {
		err = os.Link(tmpName, resolved)
	}
	if err != nil {
		return failure("write_failed", err)
	}
	return success(File{path, string(b), digest(b)})
}
func (s *Service) grep(ctx context.Context, a Arguments) Result {
	p, err := absolute(a.Path)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	matches := []Match{}
	truncated := false
	visited := 0
	outputBytes := 0
	stop := errors.New("limit")
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > s.cfg.MaxEntries*10 {
			truncated = true
			return stop
		}
		if d.IsDir() {
			if d.Name() == ".git" && path != p {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		b, err := s.readBytesContext(ctx, path)
		if err != nil {
			truncated = true
			return nil
		}
		if !textBytes(b) {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if re.MatchString(line) {
				if len(matches) >= s.cfg.MaxEntries || outputBytes+len(line) > s.cfg.MaxBytes {
					truncated = true
					return stop
				}
				matches = append(matches, Match{path, i + 1, line})
				outputBytes += len(line)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return failure("search_failed", err)
	}
	return Result{OK: true, Data: matches, Truncated: truncated}
}
