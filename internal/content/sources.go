package content

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

type source struct {
	tier   Tier
	pkg    string
	origin string
	root   fs.FS
}

func sourcesFor(opts Options, kind string, tier Tier) []source {
	switch tier {
	case TierBundle:
		if opts.BundleFS == nil {
			return nil
		}
		root, ok := subDir(opts.BundleFS, "content/"+kind)
		if !ok {
			return nil
		}
		pkg := opts.BundleID
		if pkg == "" {
			pkg = "bundle"
		}
		return []source{{tier: tier, pkg: pkg, origin: "bundle:" + kind, root: root}}
	case TierWorkspace:
		if !opts.TrustWorkspace || opts.WorkspaceDir == "" {
			return nil
		}
		return dirSource(tier, "workspace", filepath.Join(opts.WorkspaceDir, ".smidja", kind))
	case TierUser:
		if opts.UserHome == "" {
			return nil
		}
		return dirSource(tier, "user", filepath.Join(opts.UserHome, ".smidja", kind))
	case TierPackages:
		var out []source
		for _, pkgDir := range opts.PackagesDirs {
			if pkgDir == "" {
				continue
			}
			pkg, contents := packageMeta(pkgDir)
			root := kind
			if v := contents[kind]; v != "" {
				root = v
			}
			out = append(out, dirSource(tier, pkg, filepath.Join(pkgDir, root))...)
		}
		return out
	case TierCore:
		return nil
	}
	return nil
}

func subDir(fsys fs.FS, path string) (fs.FS, bool) {
	info, err := fs.Stat(fsys, path)
	if err != nil {
		return nil, false
	}
	if !info.IsDir() {
		return nil, false
	}
	sub, err := fs.Sub(fsys, path)
	if err != nil {
		return nil, false
	}
	return sub, true
}

func dirSource(tier Tier, pkg, dir string) []source {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return nil
	}
	return []source{{tier: tier, pkg: pkg, origin: dir, root: os.DirFS(dir)}}
}

func packageMeta(dir string) (string, map[string]string) {
	data, err := os.ReadFile(filepath.Join(dir, "smidja.json"))
	if err != nil {
		return filepath.Base(dir), nil
	}
	var meta struct {
		ID       string            `json:"id"`
		Contents map[string]string `json:"contents"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return filepath.Base(dir), nil
	}
	if meta.ID == "" {
		meta.ID = filepath.Base(dir)
	}
	return meta.ID, meta.Contents
}
