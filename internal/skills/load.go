package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitalygo/smidja/internal/packages"
	"github.com/digitalygo/smidja/sdk"
)

func FromBundle(b sdk.Bundle) (*Catalog, error) {
	c := New()
	if b.FS == nil {
		return c, nil
	}
	if _, err := fs.Stat(b.FS, "content/skills"); err != nil {
		return c, nil
	}
	sub, err := fs.Sub(b.FS, "content/skills")
	if err != nil {
		return c, nil
	}
	pkg := b.ID
	if pkg == "" {
		pkg = "bundle"
	}
	if err := addFromFS(c, sub, pkg); err != nil {
		return nil, err
	}
	return c, nil
}

func FromPackages(store *packages.Store) (*Catalog, error) {
	c := New()
	if store == nil {
		return c, nil
	}
	active, err := store.Active()
	if err != nil {
		return nil, err
	}
	for _, a := range active {
		m, err := store.Manifest(a.ID, a.Version)
		if err != nil {
			return nil, err
		}
		root := m.Contents["skills"]
		if root == "" {
			continue
		}
		dir := filepath.Join(store.Root(), a.ID, a.Version, filepath.FromSlash(root))
		if _, err := os.Stat(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if err := addFromFS(c, os.DirFS(dir), a.ID); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func addFromFS(c *Catalog, root fs.FS, pkg string) error {
	return fs.WalkDir(root, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("skills: %s: symlink %s rejected", pkg, p)
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		return c.Add(pkg, strings.TrimSuffix(p, ".md"), string(data))
	})
}
