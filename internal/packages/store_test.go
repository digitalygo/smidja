package packages

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "packages"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func installPackage(t *testing.T, s *Store, req Request, nodes []Node, archive, commit string, opts InstallOptions) InstalledRecord {
	t.Helper()
	inst := &Installer{Store: s, Fetch: staticFetch(commit, archive)}
	rec, err := inst.Install(context.Background(), req, nodes, opts)
	if err != nil {
		t.Fatalf("Install %s@%s: %v", req.ID, req.Version, err)
	}
	return rec
}

func TestOpenCreatesLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != root {
		t.Errorf("Root() = %s, want %s", s.Root(), root)
	}
	if _, err := os.Stat(filepath.Join(root, ".staging")); err != nil {
		t.Errorf(".staging missing: %v", err)
	}
	idx, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if idx.SchemaVersion != 0 || idx.Generation != 0 || len(idx.Active) != 0 || len(idx.Installed) != 0 {
		t.Errorf("empty index = %+v", idx)
	}
}

func TestIndexSchemaRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"schemaVersion":9,"generation":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Index(); err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Errorf("Index error = %v, want schemaVersion rejection", err)
	}
}

func TestInstallLifecycle(t *testing.T) {
	s := openStore(t)
	files := map[string]string{
		"skills/read.md":       "hello world",
		"agents/supervisor.md": "supervise",
	}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills", "agents": "agents"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "tools-abc123")
	rec := installPackage(t, s, Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}, []Node{nodeFromManifest(m)}, archive, "abc123", InstallOptions{})

	if rec.ID != "tools" || rec.Version != "v1.0.0" || rec.Commit != "abc123" {
		t.Errorf("record identity wrong: %+v", rec)
	}
	if rec.Integrity != IntegrityOK || rec.Authenticity != AuthenticityUnverified {
		t.Errorf("record trust fields wrong: %+v", rec)
	}
	if rec.InstalledAt == "" || rec.ManifestSHA256 == "" {
		t.Errorf("record timestamps or hash missing: %+v", rec)
	}
	if len(rec.ResolvedDepends) != 0 {
		t.Errorf("resolvedDepends = %+v, want none", rec.ResolvedDepends)
	}

	dest := filepath.Join(s.Root(), "tools", "v1.0.0")
	for path := range files {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(path))); err != nil {
			t.Errorf("installed file %s missing: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, ManifestFilename)); err != nil {
		t.Errorf("manifest not installed: %v", err)
	}
	receipt := readFile(t, filepath.Join(dest, ReceiptFilename))
	var got InstalledRecord
	if err := json.Unmarshal(receipt, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ResolvedDepends) != len(rec.ResolvedDepends) {
		t.Errorf("receipt resolvedDepends = %+v, want %+v", got.ResolvedDepends, rec.ResolvedDepends)
	}
	if got.ID != rec.ID || got.Version != rec.Version || got.Owner != rec.Owner || got.Repo != rec.Repo || got.Commit != rec.Commit || got.ManifestSHA256 != rec.ManifestSHA256 || got.InstalledAt != rec.InstalledAt || got.Integrity != rec.Integrity || got.Authenticity != rec.Authenticity {
		t.Errorf("receipt = %+v, want %+v", got, rec)
	}

	idx, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if idx.Generation != 1 {
		t.Errorf("generation = %d, want 1", idx.Generation)
	}
	if len(idx.Installed) != 1 || idx.Installed[0].ID != "tools" {
		t.Errorf("index installed = %+v", idx.Installed)
	}
	if len(idx.Active) != 0 {
		t.Errorf("index active = %+v, want none", idx.Active)
	}
	manifestWant := hashBytes(readFile(t, filepath.Join(dest, ManifestFilename)))
	if rec.ManifestSHA256 != manifestWant {
		t.Errorf("manifestSha256 = %s, want %s", rec.ManifestSHA256, manifestWant)
	}
}

func TestInstallFlatArchiveWithoutTopDir(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "")
	rec := installPackage(t, s, Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}, []Node{nodeFromManifest(m)}, archive, "abc", InstallOptions{})
	if rec.ID != "tools" {
		t.Fatalf("install failed: %+v", rec)
	}
}

func TestInstallZip(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeZip(t, src, "tools-abc")
	rec := installPackage(t, s, Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}, []Node{nodeFromManifest(m)}, archive, "abc", InstallOptions{})
	if rec.ID != "tools" {
		t.Fatalf("install failed: %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "tools", "v1.0.0", "skills", "read.md")); err != nil {
		t.Errorf("zip content missing: %v", err)
	}
}

func TestInstallWithResolvedClosure(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	base := testManifest("base", "v1.0.0", "acme", "base", map[string]string{"skills": "skills"}, files, nil)
	app := testManifest("app", "v1.0.0", "acme", "app", map[string]string{"skills": "skills"}, files,
		[]Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v1.0.0"}})
	srcBase := t.TempDir()
	buildPackageTree(t, srcBase, base, files)
	srcApp := t.TempDir()
	buildPackageTree(t, srcApp, app, files)
	archiveBase := makeTarGz(t, srcBase, "base-1")
	archiveApp := makeTarGz(t, srcApp, "app-1")

	baseReq := Request{Owner: "acme", Repo: "base", ID: "base", Version: "v1.0.0"}
	appReq := Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}
	inst := &Installer{Store: s, Fetch: staticFetch("c1", archiveBase)}
	recBase, err := inst.Install(context.Background(), baseReq, []Node{nodeFromManifest(base)}, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = recBase
	inst2 := &Installer{Store: s, Fetch: staticFetch("c2", archiveApp)}
	recApp, err := inst2.Install(context.Background(), appReq, []Node{nodeFromManifest(base), nodeFromManifest(app)}, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvedDependency{{ID: "base", Version: "v1.0.0"}}
	if len(recApp.ResolvedDepends) != 1 || recApp.ResolvedDepends[0] != want[0] {
		t.Errorf("app resolvedDepends = %+v, want %+v", recApp.ResolvedDepends, want)
	}
}

func TestInstallCommitPin(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "tools-1")

	req := Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}
	inst := &Installer{Store: s, Fetch: staticFetch("abc123", archive)}
	if _, err := inst.Install(context.Background(), req, []Node{nodeFromManifest(m)}, InstallOptions{PinCommit: "abc123"}); err != nil {
		t.Fatalf("install with matching pin: %v", err)
	}
	inst2 := &Installer{Store: s, Fetch: staticFetch("abc123", archive)}
	req2 := Request{Owner: "acme", Repo: "tools2", ID: "tools2", Version: "v1.0.0"}
	_, err := inst2.Install(context.Background(), req2, []Node{}, InstallOptions{PinCommit: "zzzz99"})
	if err == nil || !strings.Contains(err.Error(), "does not match pin") {
		t.Errorf("Install pin mismatch error = %v, want pin mismatch", err)
	}
}

func TestInstallDuplicate(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "tools-1")
	req := Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}
	installPackage(t, s, req, []Node{nodeFromManifest(m)}, archive, "abc", InstallOptions{})
	inst := &Installer{Store: s, Fetch: staticFetch("abc", archive)}
	_, err := inst.Install(context.Background(), req, []Node{nodeFromManifest(m)}, InstallOptions{})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Errorf("Install duplicate error = %v, want ErrAlreadyInstalled", err)
	}
}

func TestInstallRejects(t *testing.T) {
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	req := Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}

	t.Run("nil store", func(t *testing.T) {
		inst := &Installer{Fetch: staticFetch("a", "x")}
		if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil {
			t.Error("nil store accepted")
		}
	})
	t.Run("nil fetch", func(t *testing.T) {
		s := openStore(t)
		inst := &Installer{Store: s}
		if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil {
			t.Error("nil fetch accepted")
		}
	})
	t.Run("invalid id", func(t *testing.T) {
		s := openStore(t)
		inst := &Installer{Store: s, Fetch: staticFetch("a", "x")}
		bad := req
		bad.ID = "Bad ID"
		if _, err := inst.Install(context.Background(), bad, nil, InstallOptions{}); err == nil {
			t.Error("invalid id accepted")
		}
	})
	t.Run("invalid version", func(t *testing.T) {
		s := openStore(t)
		inst := &Installer{Store: s, Fetch: staticFetch("a", "x")}
		bad := req
		bad.Version = "1.0.0"
		if _, err := inst.Install(context.Background(), bad, nil, InstallOptions{}); err == nil {
			t.Error("invalid version accepted")
		}
	})
	t.Run("unrecognized archive", func(t *testing.T) {
		s := openStore(t)
		junk := filepath.Join(t.TempDir(), "junk.bin")
		if err := os.WriteFile(junk, []byte("not an archive"), 0o644); err != nil {
			t.Fatal(err)
		}
		inst := &Installer{Store: s, Fetch: staticFetch("a", junk)}
		if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "unrecognized") {
			t.Errorf("Install error = %v, want unrecognized archive", err)
		}
	})
	t.Run("missing manifest", func(t *testing.T) {
		s := openStore(t)
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "skills", "a.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		archive := makeTarGz(t, src, "tools-1")
		inst := &Installer{Store: s, Fetch: staticFetch("a", archive)}
		if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil {
			t.Error("missing manifest accepted")
		}
	})
	t.Run("invalid manifest", func(t *testing.T) {
		s := openStore(t)
		bad := m
		bad.Version = "1.0.0"
		src := t.TempDir()
		buildPackageTree(t, src, bad, files)
		archive := makeTarGz(t, src, "tools-1")
		inst := &Installer{Store: s, Fetch: staticFetch("a", archive)}
		if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("Install error = %v, want manifest version error", err)
		}
	})
	t.Run("identity mismatch", func(t *testing.T) {
		s := openStore(t)
		other := testManifest("other", "v1.0.0", "acme", "other", map[string]string{"skills": "skills"}, files, nil)
		src := t.TempDir()
		buildPackageTree(t, src, other, files)
		archive := makeTarGz(t, src, "other-1")
		inst := &Installer{Store: s, Fetch: staticFetch("a", archive)}
		if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "does not match request") {
			t.Errorf("Install error = %v, want identity mismatch", err)
		}
	})
	t.Run("tree validation failure cleans staging", func(t *testing.T) {
		s := openStore(t)
		src := t.TempDir()
		buildPackageTree(t, src, m, files)
		if err := os.WriteFile(filepath.Join(src, "skills", "extra.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		archive := makeTarGz(t, src, "tools-1")
		inst := &Installer{Store: s, Fetch: staticFetch("a", archive)}
		if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "unexpected file") {
			t.Errorf("Install error = %v, want unexpected file", err)
		}
		entries, err := os.ReadDir(filepath.Join(s.Root(), ".staging"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("staging not cleaned: %v", entries)
		}
		if _, err := os.Stat(filepath.Join(s.Root(), "tools", "v1.0.0")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("install target must not exist after failure: %v", err)
		}
	})
}

func TestInstallTraversalArchive(t *testing.T) {
	s := openStore(t)
	req := Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "smidja.json"), []byte(`{}`), 0o644)
	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("evil"))
	tw.Close()
	gz.Close()
	f.Close()
	inst := &Installer{Store: s, Fetch: staticFetch("a", archive)}
	if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "unsafe entry") {
		t.Errorf("Install error = %v, want unsafe entry rejection", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.Root()), "evil")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("traversal wrote outside staging: %v", err)
	}
}

func TestInstallSymlinkArchive(t *testing.T) {
	s := openStore(t)
	req := Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}
	archive := filepath.Join(t.TempDir(), "link.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "smidja.json", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("{}"))
	tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Mode: 0o777, Typeflag: tar.TypeSymlink})
	tw.Close()
	gz.Close()
	f.Close()
	inst := &Installer{Store: s, Fetch: staticFetch("a", archive)}
	if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "link entry") {
		t.Errorf("Install error = %v, want link entry rejection", err)
	}
}

func TestInstallPlainTar(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makePlainTar(t, src, "tools-1")
	rec := installPackage(t, s, Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}, []Node{nodeFromManifest(m)}, archive, "abc", InstallOptions{})
	if rec.ID != "tools" {
		t.Fatalf("install failed: %+v", rec)
	}
}

func TestInstallZipSymlinkEntry(t *testing.T) {
	s := openStore(t)
	archive := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	reg, err := zw.Create("smidja.json")
	if err != nil {
		t.Fatal(err)
	}
	reg.Write([]byte(`{}`))
	hdr := &zip.FileHeader{Name: "link", Method: zip.Store}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("/etc/passwd"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	inst := &Installer{Store: s, Fetch: staticFetch("a", archive)}
	req := Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}
	if _, err := inst.Install(context.Background(), req, nil, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "symlink entry") {
		t.Errorf("Install error = %v, want symlink entry rejection", err)
	}
}

func TestDefaultRootAndAccessors(t *testing.T) {
	if root := DefaultRoot(); root == "" || !strings.HasSuffix(root, string(filepath.Separator)+"packages") {
		t.Errorf("DefaultRoot() = %q, want .../.smidja/packages", root)
	}
	s := openStore(t)
	if recs, err := s.Installed(); err != nil || len(recs) != 0 {
		t.Errorf("Installed() = %v, %v", recs, err)
	}
	if active, err := s.Active(); err != nil || len(active) != 0 {
		t.Errorf("Active() = %v, %v", active, err)
	}
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "tools-1")
	installPackage(t, s, Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}, []Node{nodeFromManifest(m)}, archive, "abc", InstallOptions{})
	recs, err := s.Installed()
	if err != nil || len(recs) != 1 || recs[0].ID != "tools" {
		t.Errorf("Installed() = %v, %v", recs, err)
	}
}

func TestInstalledIndexLoadError(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "tools-1")
	installPackage(t, s, Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}, []Node{nodeFromManifest(m)}, archive, "abc", InstallOptions{})
	if err := os.Remove(filepath.Join(s.Root(), "tools", "v1.0.0", ManifestFilename)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstalledIndex(); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Errorf("InstalledIndex error = %v, want load manifest failure", err)
	}
}

func TestActivateCorruptIndex(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{root: root}

	t.Run("active references missing record", func(t *testing.T) {
		idx := Index{SchemaVersion: 0, Installed: []InstalledRecord{{ID: "a", Version: "v1.0.0", Owner: "o", Repo: "a"}}}
		writeTestIndex(t, root, idx)
		corrupt := Index{SchemaVersion: 0, Active: []ActiveEntry{{ID: "ghost", Version: "v1.0.0"}}, Installed: idx.Installed}
		writeTestIndex(t, root, corrupt)
		if err := s.Activate("a", "v1.0.0"); err == nil || !strings.Contains(err.Error(), "is not installed") {
			t.Errorf("Activate error = %v, want missing active record error", err)
		}
	})

	t.Run("closure references missing record", func(t *testing.T) {
		idx := Index{SchemaVersion: 0, Installed: []InstalledRecord{
			{ID: "a", Version: "v1.0.0", Owner: "o", Repo: "a", ResolvedDepends: []ResolvedDependency{{ID: "ghost", Version: "v1.0.0"}}},
		}}
		writeTestIndex(t, root, idx)
		if err := s.Activate("a", "v1.0.0"); err == nil || !strings.Contains(err.Error(), "missing installed dependency") {
			t.Errorf("Activate error = %v, want missing dependency error", err)
		}
	})
}

func writeTestIndex(t *testing.T, root string, idx Index) {
	t.Helper()
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestActivateCycleRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{root: root}
	idx := Index{SchemaVersion: 0, Installed: []InstalledRecord{
		{ID: "a", Version: "v1.0.0", Owner: "o", Repo: "a", ResolvedDepends: []ResolvedDependency{{ID: "b", Version: "v1.0.0"}}},
		{ID: "b", Version: "v1.0.0", Owner: "o", Repo: "b", ResolvedDepends: []ResolvedDependency{{ID: "a", Version: "v1.0.0"}}},
	}}
	writeTestIndex(t, root, idx)
	if err := s.Activate("a", "v1.0.0"); !errors.Is(err, ErrCycle) {
		t.Errorf("Activate error = %v, want ErrCycle", err)
	}
}

func TestActivateDepsFirst(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	base := testManifest("base", "v1.0.0", "acme", "base", map[string]string{"skills": "skills"}, files, nil)
	app := testManifest("app", "v1.0.0", "acme", "app", map[string]string{"skills": "skills"}, files,
		[]Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v1.0.0"}})
	srcBase := t.TempDir()
	buildPackageTree(t, srcBase, base, files)
	srcApp := t.TempDir()
	buildPackageTree(t, srcApp, app, files)
	archiveBase := makeTarGz(t, srcBase, "base-1")
	archiveApp := makeTarGz(t, srcApp, "app-1")

	installPackage(t, s, Request{Owner: "acme", Repo: "base", ID: "base", Version: "v1.0.0"}, []Node{nodeFromManifest(base)}, archiveBase, "c1", InstallOptions{})
	installPackage(t, s, Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, []Node{nodeFromManifest(base), nodeFromManifest(app)}, archiveApp, "c2", InstallOptions{})

	if err := s.Activate("app", "v1.0.0"); err != nil {
		t.Fatalf("Activate app: %v", err)
	}
	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID != "base" || active[1].ID != "app" {
		t.Errorf("active = %+v, want base before app (deps-first)", active)
	}

	if err := s.Deactivate("app", "v1.0.0"); err != nil {
		t.Fatalf("Deactivate app: %v", err)
	}
	if err := s.Activate("base", "v1.0.0"); err != nil {
		t.Fatalf("Activate base: %v", err)
	}
	if err := s.Activate("app", "v1.0.0"); err != nil {
		t.Fatalf("Activate app again: %v", err)
	}
	active, err = s.Active()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID != "base" || active[1].ID != "app" {
		t.Errorf("active = %+v, want [base app]", active)
	}
}

func TestActivateIdempotentAndErrors(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "tools-1")
	installPackage(t, s, Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}, []Node{nodeFromManifest(m)}, archive, "c1", InstallOptions{})

	if err := s.Activate("tools", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	gen1, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Activate("tools", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	gen2, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if gen1.Generation != gen2.Generation {
		t.Errorf("idempotent activate bumped generation: %d -> %d", gen1.Generation, gen2.Generation)
	}
	if err := s.Activate("missing", "v1.0.0"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Activate missing error = %v, want ErrNotInstalled", err)
	}
	if err := s.Activate("tools", "v2.0.0"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Activate wrong version error = %v, want ErrNotInstalled", err)
	}
	if err := s.Activate("Tools!", "v1.0.0"); err == nil {
		t.Error("Activate invalid id accepted")
	}
	if err := s.Deactivate("tools", "v2.0.0"); err != nil {
		t.Errorf("Deactivate wrong version: %v", err)
	}
	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Errorf("active = %+v, want tools still active", active)
	}
	if err := s.Deactivate("tools", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveGuards(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	base := testManifest("base", "v1.0.0", "acme", "base", map[string]string{"skills": "skills"}, files, nil)
	app := testManifest("app", "v1.0.0", "acme", "app", map[string]string{"skills": "skills"}, files,
		[]Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v1.0.0"}})
	srcBase := t.TempDir()
	buildPackageTree(t, srcBase, base, files)
	srcApp := t.TempDir()
	buildPackageTree(t, srcApp, app, files)
	archiveBase := makeTarGz(t, srcBase, "base-1")
	archiveApp := makeTarGz(t, srcApp, "app-1")

	installPackage(t, s, Request{Owner: "acme", Repo: "base", ID: "base", Version: "v1.0.0"}, []Node{nodeFromManifest(base)}, archiveBase, "c1", InstallOptions{})
	installPackage(t, s, Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, []Node{nodeFromManifest(base), nodeFromManifest(app)}, archiveApp, "c2", InstallOptions{})

	if err := s.Activate("app", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("app", "v1.0.0"); !errors.Is(err, ErrActive) {
		t.Errorf("Remove active error = %v, want ErrActive", err)
	}
	if err := s.Deactivate("app", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deactivate("base", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("base", "v1.0.0"); !errors.Is(err, ErrHasDependents) {
		t.Errorf("Remove depended-on error = %v, want ErrHasDependents", err)
	}
	if err := s.Remove("missing", "v1.0.0"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Remove missing error = %v, want ErrNotInstalled", err)
	}
	if err := s.Remove("app", "v1.0.0"); err != nil {
		t.Fatalf("Remove app: %v", err)
	}
	if err := s.Remove("base", "v1.0.0"); err != nil {
		t.Fatalf("Remove base: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "base", "v1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("base dir not removed: %v", err)
	}
	idx, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Installed) != 0 {
		t.Errorf("index installed = %+v, want empty", idx.Installed)
	}
}

func TestRemoveOnlyExactVersionDependent(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	base1 := testManifest("base", "v1.0.0", "acme", "base", map[string]string{"skills": "skills"}, files, nil)
	base2 := testManifest("base", "v2.0.0", "acme", "base", map[string]string{"skills": "skills"}, files, nil)
	app := testManifest("app", "v1.0.0", "acme", "app", map[string]string{"skills": "skills"}, files,
		[]Dependency{{ID: "base", Owner: "acme", Repo: "base", ExactVersion: "v1.0.0"}})
	src := t.TempDir()
	buildPackageTree(t, src, base1, files)
	archive1 := makeTarGz(t, src, "base-1")
	src2 := t.TempDir()
	buildPackageTree(t, src2, base2, files)
	archive2 := makeTarGz(t, src2, "base-2")
	src3 := t.TempDir()
	buildPackageTree(t, src3, app, files)
	archive3 := makeTarGz(t, src3, "app-1")

	installPackage(t, s, Request{Owner: "acme", Repo: "base", ID: "base", Version: "v1.0.0"}, []Node{nodeFromManifest(base1)}, archive1, "c1", InstallOptions{})
	installPackage(t, s, Request{Owner: "acme", Repo: "base", ID: "base", Version: "v2.0.0"}, []Node{nodeFromManifest(base2)}, archive2, "c2", InstallOptions{})
	installPackage(t, s, Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, []Node{nodeFromManifest(base1), nodeFromManifest(app)}, archive3, "c3", InstallOptions{})

	if err := s.Remove("base", "v1.0.0"); !errors.Is(err, ErrHasDependents) {
		t.Errorf("Remove base v1 error = %v, want ErrHasDependents", err)
	}
	if err := s.Remove("base", "v2.0.0"); err != nil {
		t.Fatalf("Remove base v2: %v", err)
	}
}

func TestInstalledIndex(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	base1 := testManifest("base", "v1.0.0", "acme", "base", map[string]string{"skills": "skills"}, files, nil)
	base2 := testManifest("base", "v2.0.0", "acme", "base", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, base1, files)
	archive1 := makeTarGz(t, src, "base-1")
	src2 := t.TempDir()
	buildPackageTree(t, src2, base2, files)
	archive2 := makeTarGz(t, src2, "base-2")

	installPackage(t, s, Request{Owner: "acme", Repo: "base", ID: "base", Version: "v1.0.0"}, []Node{nodeFromManifest(base1)}, archive1, "c1", InstallOptions{})
	installPackage(t, s, Request{Owner: "acme", Repo: "base", ID: "base", Version: "v2.0.0"}, []Node{nodeFromManifest(base2)}, archive2, "c2", InstallOptions{})

	index, err := s.InstalledIndex()
	if err != nil {
		t.Fatal(err)
	}
	info, ok := index["base"]
	if !ok {
		t.Fatal("base missing from InstalledIndex")
	}
	if info.Version != "v2.0.0" || info.Owner != "acme" || info.Repo != "base" {
		t.Errorf("InstalledIndex base = %+v, want v2.0.0", info)
	}
	if info.Manifest.ID != "base" || info.Manifest.Depends != nil {
		t.Errorf("InstalledIndex manifest = %+v", info.Manifest)
	}
}

func TestLocking(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{root: root}

	t.Run("fresh lock blocks", func(t *testing.T) {
		lock := filepath.Join(root, ".lock")
		if err := os.WriteFile(lock, []byte("pid=1 time=now\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := s.Activate("x", "v1.0.0")
		if !errors.Is(err, ErrLocked) {
			t.Errorf("Activate error = %v, want ErrLocked", err)
		}
		os.Remove(lock)
	})

	t.Run("stale lock reclaimed", func(t *testing.T) {
		lock := filepath.Join(root, ".lock")
		if err := os.WriteFile(lock, []byte("pid=1 time=old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-(StaleLockAge + time.Minute))
		if err := os.Chtimes(lock, old, old); err != nil {
			t.Fatal(err)
		}
		err := s.Activate("x", "v1.0.0")
		if !errors.Is(err, ErrNotInstalled) {
			t.Errorf("Activate error = %v, want ErrNotInstalled after stale reclaim", err)
		}
		if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale lock not reclaimed: %v", err)
		}
	})
}

func TestConcurrentStoreOps(t *testing.T) {
	s := openStore(t)
	const n = 8
	archives := make([]string, n)
	manifests := make([]Manifest, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("pkg%d", i)
		files := map[string]string{"skills/read.md": fmt.Sprintf("content %d", i)}
		m := testManifest(id, "v1.0.0", "acme", fmt.Sprintf("repo%d", i), map[string]string{"skills": "skills"}, files, nil)
		manifests[i] = m
		src := t.TempDir()
		buildPackageTree(t, src, m, files)
		archives[i] = makeTarGz(t, src, fmt.Sprintf("%s-1", id))
	}

	var wg sync.WaitGroup
	errs := make(chan error, n*4)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := manifests[i]
			req := Request{Owner: m.Owner, Repo: m.Repo, ID: m.ID, Version: m.Version}
			inst := &Installer{Store: s, Fetch: staticFetch("abc", archives[i])}
			if _, err := inst.Install(context.Background(), req, []Node{nodeFromManifest(m)}, InstallOptions{}); err != nil {
				errs <- fmt.Errorf("install %s: %w", m.ID, err)
			}
			if err := s.Activate(m.ID, m.Version); err != nil {
				errs <- fmt.Errorf("activate %s: %w", m.ID, err)
			}
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Index(); err != nil {
				errs <- fmt.Errorf("index: %w", err)
			}
			if _, err := s.InstalledIndex(); err != nil {
				errs <- fmt.Errorf("installed index: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	idx, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Installed) != n || len(idx.Active) != n {
		t.Errorf("installed = %d, active = %d, want %d each", len(idx.Installed), len(idx.Active), n)
	}
}

func TestConcurrentSamePackageInstall(t *testing.T) {
	s := openStore(t)
	files := map[string]string{"skills/read.md": "hello"}
	m := testManifest("tools", "v1.0.0", "acme", "tools", map[string]string{"skills": "skills"}, files, nil)
	src := t.TempDir()
	buildPackageTree(t, src, m, files)
	archive := makeTarGz(t, src, "tools-1")

	req := Request{Owner: "acme", Repo: "tools", ID: "tools", Version: "v1.0.0"}
	const n = 6
	var wg sync.WaitGroup
	success := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst := &Installer{Store: s, Fetch: staticFetch("abc", archive)}
			_, err := inst.Install(context.Background(), req, []Node{nodeFromManifest(m)}, InstallOptions{})
			success <- err
		}()
	}
	wg.Wait()
	close(success)
	ok := 0
	already := 0
	for err := range success {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyInstalled):
			already++
		default:
			t.Errorf("unexpected install error: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("successful installs = %d, want exactly 1", ok)
	}
	if already != n-1 {
		t.Errorf("already-installed = %d, want %d", already, n-1)
	}
}
