package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

type Workspace struct {
	root string
}

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

func (w *Workspace) Root() string {
	return w.root
}

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

func IsForbidden(p string) bool {
	for _, part := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
		if part == ".git" {
			return true
		}
	}
	return false
}

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

func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
