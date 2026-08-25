// Package workspace models the directory smidja operates in and enforces
// that every path the agent touches stays inside it.
//
// A Workspace root is canonicalized at construction: absolute, symlink
// resolved, and tolerant of a nonexistent leaf (for example a repository
// that will be checked out later). Contain validates every path the agent
// reads or writes against that canonical root, rejecting lexical escapes,
// absolute paths outside the root, and symlink escapes present at check
// time. IsForbidden blocks paths that touch .git internals.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// Workspace is a canonical, symlink-resolved directory root. All paths the
// agent may touch must be contained by it (see Contain).
type Workspace struct {
	root string
}

// New canonicalizes root: it makes the path absolute, resolves symlinks via
// filepath.EvalSymlinks, and tolerates a nonexistent leaf by resolving the
// deepest existing ancestor and re-appending the missing suffix. The
// returned workspace root is always absolute and cleaned.
func New(root string) (*Workspace, error) {
	if root == "" {
		return nil, fmt.Errorf("workspace: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace: make %q absolute: %w", root, err)
	}
	resolved, err := resolveToleratingMissing(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve %q: %w", root, err)
	}
	return &Workspace{root: filepath.Clean(resolved)}, nil
}

// Root returns the canonical workspace root.
func (w *Workspace) Root() string {
	return w.root
}

// Contain resolves p inside the workspace and returns the cleaned absolute
// path when p stays within the root.
//
// A relative p is resolved against the root; an absolute p is checked for
// containment as-is. Both are rejected when the cleaned path falls outside
// the root lexically (".." traversal beyond the root, absolute paths
// outside it) or when symlinks present at check time resolve outside the
// root. A nonexistent leaf is tolerated: the deepest existing ancestor is
// resolved and checked, so paths into not-yet-created files and
// directories work as long as every existing component stays inside the
// root.
//
// Contain is a best-effort escape guard against the paths as they exist at
// check time, not a security boundary against concurrent filesystem
// mutation.
func (w *Workspace) Contain(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("workspace: empty path")
	}
	var full string
	if filepath.IsAbs(p) {
		full = p
	} else {
		full = filepath.Join(w.root, p)
	}
	cleaned := filepath.Clean(full)
	if !within(cleaned, w.root) {
		return "", fmt.Errorf("workspace: path %q escapes root %q", p, w.root)
	}
	eval, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		if !within(eval, w.root) {
			return "", fmt.Errorf("workspace: path %q escapes root %q through a symlink", p, w.root)
		}
		return cleaned, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("workspace: resolve %q: %w", p, err)
	}
	// The leaf does not exist yet (for example a file about to be
	// written). Resolve the deepest existing ancestor and check it: every
	// nonexistent component between that ancestor and the leaf cannot be a
	// symlink, so the ancestor's resolution decides.
	anc := cleaned
	for {
		parent := filepath.Dir(anc)
		if parent == anc {
			break
		}
		anc = parent
		eval, err := filepath.EvalSymlinks(anc)
		if err == nil {
			if within(eval, w.root) {
				return cleaned, nil
			}
			// The existing ancestor resolves outside the root. Accept it
			// only when it sits lexically above the root itself, which
			// implies the root (and everything under it, including the
			// requested path) is nonexistent and therefore cannot carry a
			// symlink escape.
			if within(w.root, anc) {
				return cleaned, nil
			}
			return "", fmt.Errorf("workspace: path %q escapes root %q through a symlink", p, w.root)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("workspace: resolve %q: %w", p, err)
		}
	}
	return "", fmt.Errorf("workspace: no existing ancestor for %q", p)
}

// IsForbidden reports whether any path component of p is exactly ".git".
// It is meant to be called on paths already returned by Contain (absolute,
// inside the workspace): any component equal to ".git" anywhere in the
// path forbids access, so ".git", ".git/config", and nested
// "sub/.git/objects" are all rejected while ".github", ".gitignore", and
// plain sources are not. The check is deliberately conservative: it
// inspects every component of the cleaned path.
func IsForbidden(p string) bool {
	for _, part := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
		if part == ".git" {
			return true
		}
	}
	return false
}

// resolveToleratingMissing resolves p with filepath.EvalSymlinks, and when
// p does not exist, resolves the deepest existing ancestor and re-appends
// the missing suffix.
func resolveToleratingMissing(p string) (string, error) {
	eval, err := filepath.EvalSymlinks(p)
	if err == nil {
		return eval, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	var suffix []string
	cur := p
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing ancestor for %q", p)
		}
		suffix = append([]string{filepath.Base(cur)}, suffix...)
		cur = parent
		eval, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{eval}, suffix...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
}

// within reports whether p equals root or lies lexically inside it, using
// filepath.Rel so that a sibling whose name merely shares a prefix with
// root is not confused with a descendant.
func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
