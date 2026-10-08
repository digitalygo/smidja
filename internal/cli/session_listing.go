package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/digitalygo/smidja/internal/session"
)

const maxSessionTargetIDLen = 36

var sessionTargetIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]+$`)

type sessionListingCandidate struct {
	path      string
	id        string
	timestamp string
	name      string
}

func validatedSessionCandidates(store *session.Store, cwd string) ([]sessionListingCandidate, error) {
	if store == nil {
		return nil, errors.New("session: no store configured")
	}
	if strings.TrimSpace(cwd) == "" {
		return nil, errors.New("session: empty cwd")
	}
	dir, err := store.DirForCwd(cwd)
	if err != nil {
		return nil, err
	}
	if err := validateSessionDirectory(dir); err != nil {
		return nil, err
	}
	canonicalDir, err := canonicalPath(dir)
	if err != nil {
		return nil, err
	}
	canonicalRoot, err := canonicalPath(store.Root())
	if err != nil {
		return nil, err
	}
	if !canonicallyContains(canonicalRoot, canonicalDir) {
		return nil, fmt.Errorf("session: session directory %q escapes the store root", dir)
	}
	canonicalCwd, err := canonicalPath(cwd)
	if err != nil {
		return nil, err
	}
	names, err := sessionFileNames(dir)
	if err != nil {
		return nil, err
	}
	accepted := make([]sessionListingCandidate, 0, len(names))
	for _, name := range names {
		if candidate := validateSessionCandidate(dir, canonicalDir, canonicalCwd, name); candidate != nil {
			accepted = append(accepted, *candidate)
		}
	}
	candidates := dropDuplicateSessionIDs(accepted)
	sortSessionCandidates(candidates)
	return candidates, nil
}

func validateSessionDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session: inspect session directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("session: session directory %q is not a plain directory", dir)
	}
	return nil
}

func sessionFileNames(dir string) ([]string, error) {
	dirEntries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: list sessions under %q: %w", dir, err)
	}
	names := make([]string, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		name := dirEntry.Name()
		if !strings.HasSuffix(name, ".jsonl") || !filepath.IsLocal(name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

func validateSessionCandidate(dir, canonicalDir, canonicalCwd, name string) *sessionListingCandidate {
	if name == "" || name == "." || name == string(filepath.Separator) || !filepath.IsLocal(name) {
		return nil
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}
	parentInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return nil
	}
	lockInfo, err := os.Lstat(path + ".lock")
	if err == nil {
		if lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.Mode().IsRegular() {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		return nil
	}
	header := loader.Header()
	if header == nil || strings.TrimSpace(header.ID) == "" || strings.TrimSpace(header.Cwd) == "" {
		return nil
	}
	headerCwd, err := canonicalPath(header.Cwd)
	if err != nil || headerCwd != canonicalCwd {
		return nil
	}
	canonicalFile, err := canonicalPath(path)
	if err != nil || canonicalFile == canonicalDir || !canonicallyContains(canonicalDir, canonicalFile) {
		return nil
	}
	return &sessionListingCandidate{
		path:      path,
		id:        header.ID,
		timestamp: header.Timestamp,
		name:      sessionDisplayName(loader),
	}
}

func canonicallyContains(parent, child string) bool {
	if parent == child {
		return true
	}
	if !strings.HasSuffix(parent, string(filepath.Separator)) {
		parent += string(filepath.Separator)
	}
	return strings.HasPrefix(child, parent)
}

func dropDuplicateSessionIDs(candidates []sessionListingCandidate) []sessionListingCandidate {
	counts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		counts[strings.ToLower(candidate.id)]++
	}
	unique := make([]sessionListingCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if counts[strings.ToLower(candidate.id)] == 1 {
			unique = append(unique, candidate)
		}
	}
	return unique
}

func sortSessionCandidates(candidates []sessionListingCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		si, ei := os.Stat(candidates[i].path)
		sj, ej := os.Stat(candidates[j].path)
		if ei == nil && ej == nil && !si.ModTime().Equal(sj.ModTime()) {
			return si.ModTime().After(sj.ModTime())
		}
		return candidates[i].path > candidates[j].path
	})
}

func resolveValidatedSessionTarget(store *session.Store, cwd, target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", errors.New("session: empty session target")
	}
	candidates, err := validatedSessionCandidates(store, cwd)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if sameFilePath(trimmed, candidate.path) {
			return candidate.path, nil
		}
	}
	if isSessionTargetID(trimmed) {
		for _, candidate := range candidates {
			if strings.EqualFold(candidate.id, trimmed) {
				return candidate.path, nil
			}
		}
	}
	return "", fmt.Errorf("session: %q is not a session of the current project", trimmed)
}

func isSessionTargetID(value string) bool {
	return len(value) <= maxSessionTargetIDLen && sessionTargetIDPattern.MatchString(value)
}

func (b *tuiBridge) resolveSessionTarget(target string) (string, error) {
	if b.rd == nil || b.rd.store == nil {
		return "", errors.New("session: no store configured")
	}
	return resolveValidatedSessionTarget(b.rd.store, b.rd.cwd, target)
}
