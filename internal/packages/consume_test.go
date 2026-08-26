package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installFixture(t *testing.T, root string, m Manifest, files map[string]string) {
	t.Helper()
	tree := filepath.Join(t.TempDir(), "src")
	buildPackageTree(t, tree, m, files)
	archive := makeTarGz(t, tree, "")
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	installer := &Installer{Store: store, Fetch: staticFetch("cafe123", archive)}
	if _, err := installer.Install(t.Context(), Request{Owner: m.Owner, Repo: m.Repo, ID: m.ID, Version: m.Version}, nil, InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
}

func TestReadManifestFromArchive(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := testManifest("consume-pkg", "v1.0.0", "digitalygo", "consume-pkg",
		map[string]string{"skills": "skills"}, files, nil)
	tree := filepath.Join(t.TempDir(), "src")
	buildPackageTree(t, tree, m, files)
	archive := makeTarGz(t, tree, "pkg")

	got, err := ReadManifestFromArchive(archive)
	if err != nil {
		t.Fatalf("ReadManifestFromArchive: %v", err)
	}
	if got.ID != "consume-pkg" || got.Version != "v1.0.0" || got.Owner != "digitalygo" {
		t.Fatalf("manifest = %+v", got)
	}
}

func TestReadManifestFromArchiveMissingManifest(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(tree, 0o755)
	os.WriteFile(filepath.Join(tree, "readme.md"), []byte("x"), 0o644)
	archive := makeTarGz(t, tree, "")
	if _, err := ReadManifestFromArchive(archive); err == nil {
		t.Fatal("ReadManifestFromArchive succeeded without a manifest")
	}
}

func TestReadManifestFromArchiveInvalidJSON(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(tree, 0o755)
	os.WriteFile(filepath.Join(tree, ManifestFilename), []byte("{not json"), 0o644)
	archive := makeTarGz(t, tree, "")
	if _, err := ReadManifestFromArchive(archive); err == nil {
		t.Fatal("ReadManifestFromArchive accepted invalid JSON")
	}
}

func TestStoreConfigDefaults(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"config/defaults.env": "SMIDJA_MODEL=package/model\nSMIDJA_EXEC_TIMEOUT_SECS=7\n"}
	m := testManifest("cfg-pkg", "v1.0.0", "digitalygo", "cfg-pkg",
		map[string]string{"config": "config"}, files, nil)
	installFixture(t, root, m, files)

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := store.ConfigDefaults("cfg-pkg", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if vals["SMIDJA_MODEL"] != "package/model" || vals["SMIDJA_EXEC_TIMEOUT_SECS"] != "7" {
		t.Fatalf("defaults = %v", vals)
	}
}

func TestStoreConfigDefaultsMissingDirEmpty(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"skills/quick.md": "# quick"}
	m := testManifest("nocfg-pkg", "v1.0.0", "digitalygo", "nocfg-pkg",
		map[string]string{"skills": "skills", "config": "config"}, files, nil)
	installFixture(t, root, m, files)

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := store.ConfigDefaults("nocfg-pkg", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 0 {
		t.Fatalf("defaults = %v, want empty for a missing config dir", vals)
	}
}

func TestStoreActiveConfigDefaultsMergesInOrder(t *testing.T) {
	root := t.TempDir()
	firstFiles := map[string]string{"config/env": "SMIDJA_MODEL=first\nSHARED=one\n"}
	first := testManifest("first-pkg", "v1.0.0", "digitalygo", "first-pkg",
		map[string]string{"config": "config"}, firstFiles, nil)
	installFixture(t, root, first, firstFiles)
	secondFiles := map[string]string{"config/env": "SMIDJA_MODEL=second\nONLY_SECOND=2\n"}
	second := testManifest("second-pkg", "v1.0.0", "digitalygo", "second-pkg",
		map[string]string{"config": "config"}, secondFiles, nil)
	installFixture(t, root, second, secondFiles)

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Activate("first-pkg", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate("second-pkg", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	vals, err := store.ActiveConfigDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if vals["SMIDJA_MODEL"] != "second" {
		t.Fatalf("SMIDJA_MODEL = %q, want the later active package to win", vals["SMIDJA_MODEL"])
	}
	if vals["SHARED"] != "one" || vals["ONLY_SECOND"] != "2" {
		t.Fatalf("defaults = %v", vals)
	}
}

func TestStoreActiveConfigDefaultsSkipsInactive(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"config/env": "KEY=value\n"}
	m := testManifest("inactive-pkg", "v1.0.0", "digitalygo", "inactive-pkg",
		map[string]string{"config": "config"}, files, nil)
	installFixture(t, root, m, files)

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := store.ActiveConfigDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 0 {
		t.Fatalf("defaults = %v, want empty without activation", vals)
	}
}

func TestStoreVerifyOKAndTampered(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"skills/quick.md": "# quick"}
	m := testManifest("verify-pkg", "v1.0.0", "digitalygo", "verify-pkg",
		map[string]string{"skills": "skills"}, files, nil)
	installFixture(t, root, m, files)

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify("verify-pkg", "v1.0.0"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	tampered := filepath.Join(root, "verify-pkg", "v1.0.0", "skills", "quick.md")
	if err := os.WriteFile(tampered, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify("verify-pkg", "v1.0.0"); err == nil {
		t.Fatal("Verify accepted a tampered file")
	}
}

func TestStoreVerifyNotInstalled(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify("ghost", "v1.0.0"); err == nil {
		t.Fatal("Verify succeeded for a missing package")
	}
}

func TestParseConfigDefaultsSkipsCommentsAndQuotes(t *testing.T) {
	dir := t.TempDir()
	content := "# comment\nEMPTY=\nKEY=value\nQUOTED=\"quoted value\"\nJUNK LINE\n"
	if err := os.WriteFile(filepath.Join(dir, "env"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	vals, err := parseConfigDefaults(dir)
	if err != nil {
		t.Fatal(err)
	}
	if vals["KEY"] != "value" || vals["QUOTED"] != "quoted value" {
		t.Fatalf("vals = %v", vals)
	}
	if _, ok := vals["JUNK"]; ok {
		t.Fatalf("vals = %v, must not contain the junk line", vals)
	}
	if v, ok := vals["EMPTY"]; !ok || v != "" {
		t.Fatalf("EMPTY = %q, %v", v, ok)
	}
}

func TestParseConfigDefaultsRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := parseConfigDefaults(dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want the symlink rejection", err)
	}
}

func TestStoreConfigDefaultsCorruptManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad-pkg", "v1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigDefaults("bad-pkg", "v1.0.0"); err == nil {
		t.Fatal("ConfigDefaults accepted a corrupt manifest")
	}
}

func TestStoreConfigDefaultsSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"config/env": "A=1\n", "config/other": "B=2\n"}
	m := testManifest("cfg-pkg", "v1.0.0", "digitalygo", "cfg-pkg",
		map[string]string{"config": "config"}, files, nil)
	installFixture(t, root, m, files)
	if err := os.Symlink(filepath.Join(root, "cfg-pkg", "v1.0.0", "config", "env"),
		filepath.Join(root, "cfg-pkg", "v1.0.0", "config", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigDefaults("cfg-pkg", "v1.0.0"); err == nil {
		t.Fatal("ConfigDefaults accepted a symlink")
	}
}

func TestStoreActiveConfigDefaultsErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad-pkg", "v1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"schemaVersion":0,"active":[{"id":"bad-pkg","version":"v1.0.0"}],"installed":[{"id":"bad-pkg","version":"v1.0.0","owner":"o","repo":"r"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveConfigDefaults(); err == nil {
		t.Fatal("ActiveConfigDefaults accepted a corrupt active manifest")
	}
}

func TestStoreActiveConfigDefaultsSkipsNoConfigRoot(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"skills/quick.md": "# quick"}
	m := testManifest("noskills-cfg", "v1.0.0", "digitalygo", "noskills-cfg",
		map[string]string{"skills": "skills"}, files, nil)
	installFixture(t, root, m, files)
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Activate("noskills-cfg", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	vals, err := store.ActiveConfigDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 0 {
		t.Fatalf("defaults = %v, want empty for a package without config content", vals)
	}
}

func TestStoreVerifyReceiptMissing(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"skills/quick.md": "# quick"}
	m := testManifest("verify-pkg", "v1.0.0", "digitalygo", "verify-pkg",
		map[string]string{"skills": "skills"}, files, nil)
	installFixture(t, root, m, files)
	if err := os.Remove(filepath.Join(root, "verify-pkg", "v1.0.0", ReceiptFilename)); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify("verify-pkg", "v1.0.0"); err == nil || !strings.Contains(err.Error(), "receipt") {
		t.Fatalf("error = %v, want the receipt note", err)
	}
}

func TestStoreVerifyInvalidArguments(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify("Bad ID!", "v1.0.0"); err == nil {
		t.Fatal("Verify accepted an invalid id")
	}
	if err := store.Verify("ok-pkg", "nope"); err == nil {
		t.Fatal("Verify accepted an invalid version")
	}
}
