package packages

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func testManifest(id, version, owner, repo string, contents map[string]string, files map[string]string, deps []Dependency) Manifest {
	entries := make([]FileEntry, 0, len(files))
	for path, content := range files {
		sum := sha256.Sum256([]byte(content))
		entries = append(entries, FileEntry{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return Manifest{
		SchemaVersion:  0,
		ID:             id,
		Version:        version,
		Owner:          owner,
		Repo:           repo,
		Description:    "test package",
		Contents:       contents,
		Depends:        deps,
		MinimumHarness: "v0.1.0",
		Files:          entries,
	}
}

func graphManifest(id, version, owner, repo string, deps []Dependency) Manifest {
	return Manifest{
		SchemaVersion:  0,
		ID:             id,
		Version:        version,
		Owner:          owner,
		Repo:           repo,
		Depends:        deps,
		MinimumHarness: "v0.1.0",
	}
}

func buildPackageTree(t *testing.T, root string, m Manifest, files map[string]string) {
	t.Helper()
	for _, f := range m.Files {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(files[f.Path]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestFilename), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeTarGz(t *testing.T, src string, topDir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pkg.tar.gz")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if topDir != "" {
			name = topDir + "/" + name
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{Name: name + "/", Mode: 0o755, Typeflag: tar.TypeDir})
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func makeZip(t *testing.T, src string, topDir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pkg.zip")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if topDir != "" {
			name = topDir + "/" + name
		}
		if d.IsDir() {
			_, err := zw.Create(name + "/")
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func makePlainTar(t *testing.T, src string, topDir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pkg.tar")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if topDir != "" {
			name = topDir + "/" + name
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{Name: name + "/", Mode: 0o755, Typeflag: tar.TypeDir})
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func staticFetch(commit, archive string) FetchArchive {
	return func(owner, repo, version string) (string, string, error) {
		return commit, archive, nil
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func nodeFromManifest(m Manifest) Node {
	return Node{ID: m.ID, Owner: m.Owner, Repo: m.Repo, Version: m.Version, Manifest: m}
}

func nodeKey(m Manifest) string {
	return fmt.Sprintf("%s/%s/%s@%s", m.Owner, m.Repo, m.ID, m.Version)
}

func universeFetch(t *testing.T, manifests ...Manifest) (FetchFunc, *int) {
	t.Helper()
	byKey := map[string]Node{}
	for _, m := range manifests {
		byKey[nodeKey(m)] = nodeFromManifest(m)
	}
	calls := 0
	return func(req Request) (Node, error) {
		calls++
		n, ok := byKey[fmt.Sprintf("%s/%s/%s@%s", req.Owner, req.Repo, req.ID, req.Version)]
		if !ok {
			return Node{}, fmt.Errorf("universe: %s@%s from %s/%s not found", req.ID, req.Version, req.Owner, req.Repo)
		}
		return n, nil
	}, &calls
}

func archiveReader(t *testing.T, path string) io.Reader {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
