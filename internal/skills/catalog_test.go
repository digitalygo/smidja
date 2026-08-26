package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/digitalygo/smidja/internal/packages"
	"github.com/digitalygo/smidja/sdk"
)

func TestCatalogAddValidatesName(t *testing.T) {
	c := New()
	valid := []string{"quick", "role/orchestrator", "unit-converter"}
	for _, name := range valid {
		if err := c.Add("bundle", name, "content"); err != nil {
			t.Errorf("Add(%q): %v", name, err)
		}
	}
	invalid := []string{"", "..", "a/../b", "a/b/../../c", "/abs", ".hidden", "a\\b", "a//b"}
	for _, name := range invalid {
		if err := c.Add("bundle", name, "content"); err == nil {
			t.Errorf("Add(%q) succeeded, want rejection", name)
		}
	}
	if err := c.Add("", "quick", "content"); err == nil {
		t.Error("Add with empty package must fail")
	}
}

func TestCatalogAddRejectsNonUTF8(t *testing.T) {
	c := New()
	bad := []byte{0xff, 0xfe, 0x00, 0x41}
	if err := c.Add("bundle", "bad", string(bad)); err == nil {
		t.Fatal("Add with invalid utf-8 succeeded")
	} else if !strings.Contains(err.Error(), "utf-8") {
		t.Fatalf("error = %v, want the utf-8 note", err)
	}
}

func TestCatalogAddRejectsOversize(t *testing.T) {
	c := New()
	big := strings.Repeat("x", MaxSkillBytes+1)
	if err := c.Add("bundle", "big", big); err == nil {
		t.Fatal("Add over the size cap succeeded")
	} else if !strings.Contains(err.Error(), "cap") {
		t.Fatalf("error = %v, want the size-cap note", err)
	}
	ok := strings.Repeat("x", MaxSkillBytes)
	if err := c.Add("bundle", "at-cap", ok); err != nil {
		t.Fatalf("Add at the cap: %v", err)
	}
}

func TestCatalogGetLookupAndNames(t *testing.T) {
	c := New()
	c.Add("bundle", "quick", "bundle quick content")
	c.Add("pkg-a", "quick", "package quick content")
	c.Add("pkg-a", "deep/nested", "nested content")

	if got, ok := c.Get("bundle/quick"); !ok || got.Content != "bundle quick content" {
		t.Fatalf("Get(bundle/quick) = %+v, %v", got, ok)
	}
	if got, ok := c.Lookup("bundle/quick"); !ok || got.Package != "bundle" {
		t.Fatalf("Lookup(bundle/quick) = %+v, %v; want the exact key match", got, ok)
	}
	if _, ok := c.Lookup("quick"); ok {
		t.Fatal("Lookup(quick) resolved an ambiguous bare name")
	}
	if got, ok := c.Lookup("deep/nested"); !ok || got.Content != "nested content" {
		t.Fatalf("Lookup(deep/nested) = %+v, %v", got, ok)
	}
	if _, ok := c.Lookup("missing"); ok {
		t.Fatal("Lookup(missing) found a skill")
	}

	names := c.Names()
	want := []string{"bundle/quick", "pkg-a/deep/nested", "pkg-a/quick"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("Names() = %v, want %v", names, want)
	}
}

func TestMergeOverridesSameKey(t *testing.T) {
	base := New()
	base.Add("bundle", "quick", "bundle content")
	other := New()
	other.Add("pkg-a", "quick", "package content")
	if err := base.Merge(other); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got, ok := base.Get("pkg-a/quick")
	if !ok || got.Content != "package content" {
		t.Fatalf("merged = %+v, %v", got, ok)
	}
	if err := base.Merge(nil); err != nil {
		t.Fatalf("Merge(nil): %v", err)
	}
}

func TestFromBundleLoadsSkillsOnly(t *testing.T) {
	fsys := fstest.MapFS{
		"content/skills/quick.md":        {Data: []byte("# quick\nusage")},
		"content/skills/orchestrator.md": {Data: []byte("# orchestrator")},
		"content/skills/not-a-skill.txt": {Data: []byte("skip me")},
		"content/agents/agent-a.md":      {Data: []byte("# agent")},
	}
	b := sdk.Bundle{ID: "digitalygo", FS: fsys}
	c, err := FromBundle(b)
	if err != nil {
		t.Fatalf("FromBundle: %v", err)
	}
	names := c.Names()
	if len(names) != 2 {
		t.Fatalf("Names() = %v, want the two .md skills only", names)
	}
	got, ok := c.Get("digitalygo/quick")
	if !ok || got.Content != "# quick\nusage" {
		t.Fatalf("Get(quick) = %+v, %v", got, ok)
	}
	if _, ok := c.Get("digitalygo/agent-a"); ok {
		t.Fatal("agents must not be loaded as skills")
	}
}

func TestFromBundleNilFSEmpty(t *testing.T) {
	c, err := FromBundle(sdk.Bundle{ID: "digitalygo"})
	if err != nil {
		t.Fatalf("FromBundle: %v", err)
	}
	if len(c.Names()) != 0 {
		t.Fatalf("Names() = %v, want empty for a bundle without FS", c.Names())
	}
}

func TestFromBundleMissingSkillsDirEmpty(t *testing.T) {
	fsys := fstest.MapFS{"content/other/readme.md": {Data: []byte("x")}}
	c, err := FromBundle(sdk.Bundle{ID: "digitalygo", FS: fsys})
	if err != nil {
		t.Fatalf("FromBundle: %v", err)
	}
	if len(c.Names()) != 0 {
		t.Fatalf("Names() = %v, want empty", c.Names())
	}
}

func TestFromBundleRejectsOversizeSkill(t *testing.T) {
	fsys := fstest.MapFS{
		"content/skills/huge.md": {Data: []byte(strings.Repeat("x", MaxSkillBytes+1))},
	}
	_, err := FromBundle(sdk.Bundle{ID: "digitalygo", FS: fsys})
	if err == nil {
		t.Fatal("FromBundle accepted an oversize skill")
	}
}

func TestFromPackagesLoadsActiveSkills(t *testing.T) {
	store, err := packages.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"skills/quick.md":        "# quick\npackage usage",
		"skills/unit/cfg.md":     "# cfg",
		"agents/orchestrator.md": "# agent",
	}
	m := skillsTestManifest("skills-pkg", "v1.0.0", files)
	buildPackageFixture(t, store.Root(), m, files, true)

	c, err := FromPackages(store)
	if err != nil {
		t.Fatalf("FromPackages: %v", err)
	}
	names := c.Names()
	if len(names) != 2 {
		t.Fatalf("Names() = %v, want the two .md skills of the active package", names)
	}
	got, ok := c.Get("skills-pkg/quick")
	if !ok || got.Content != "# quick\npackage usage" {
		t.Fatalf("Get(quick) = %+v, %v", got, ok)
	}
	if _, ok := c.Get("skills-pkg/orchestrator"); ok {
		t.Fatal("agents must not be loaded as skills")
	}
	if _, ok := c.Get("skills-pkg/unit/cfg"); !ok {
		t.Fatal("nested skill missing")
	}
}

func TestFromPackagesIgnoresInactive(t *testing.T) {
	store, err := packages.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"skills/quick.md": "# quick"}
	m := skillsTestManifest("skills-pkg", "v1.0.0", files)
	buildPackageFixture(t, store.Root(), m, files, false)

	c, err := FromPackages(store)
	if err != nil {
		t.Fatalf("FromPackages: %v", err)
	}
	if len(c.Names()) != 0 {
		t.Fatalf("Names() = %v, want empty for an installed-but-inactive package", c.Names())
	}
}

func TestFromPackagesNilStore(t *testing.T) {
	c, err := FromPackages(nil)
	if err != nil {
		t.Fatalf("FromPackages(nil): %v", err)
	}
	if len(c.Names()) != 0 {
		t.Fatalf("Names() = %v, want empty", c.Names())
	}
}

func TestFromPackagesRejectsOversize(t *testing.T) {
	store, err := packages.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"skills/huge.md": strings.Repeat("x", MaxSkillBytes+1)}
	m := skillsTestManifest("skills-pkg", "v1.0.0", files)
	buildPackageFixture(t, store.Root(), m, files, true)
	if _, err := FromPackages(store); err == nil {
		t.Fatal("FromPackages accepted an oversize skill")
	}
}

func TestFromPackagesMissingSkillsDirSkipped(t *testing.T) {
	store, err := packages.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := skillsTestManifest("empty-pkg", "v1.0.0", map[string]string{})
	buildPackageFixture(t, store.Root(), m, map[string]string{}, true)
	c, err := FromPackages(store)
	if err != nil {
		t.Fatalf("FromPackages: %v", err)
	}
	if len(c.Names()) != 0 {
		t.Fatalf("Names() = %v, want empty", c.Names())
	}
}

func skillsTestManifest(id, version string, files map[string]string) packages.Manifest {
	entries := make([]packages.FileEntry, 0, len(files))
	for path, content := range files {
		sum := sha256.Sum256([]byte(content))
		entries = append(entries, packages.FileEntry{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return packages.Manifest{
		SchemaVersion:  0,
		ID:             id,
		Version:        version,
		Owner:          "digitalygo",
		Repo:           id,
		Description:    "skills test package",
		Contents:       map[string]string{"skills": "skills", "agents": "agents"},
		MinimumHarness: "v0.1.0",
		Files:          entries,
	}
}

func buildPackageFixture(t *testing.T, root string, m packages.Manifest, files map[string]string, active bool) {
	t.Helper()
	dir := filepath.Join(root, m.ID, m.Version)
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, packages.ManifestFilename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	idx := packages.Index{
		SchemaVersion: 0,
		Installed: []packages.InstalledRecord{{
			ID: m.ID, Version: m.Version, Owner: m.Owner, Repo: m.Repo,
			Commit: "abc123", ManifestSHA256: "00", Integrity: packages.IntegrityOK,
			Authenticity: packages.AuthenticityUnverified,
		}},
	}
	if active {
		idx.Active = []packages.ActiveEntry{{ID: m.ID, Version: m.Version}}
	}
	idxData, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), idxData, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFromPackagesCorruptIndexErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".staging")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := packages.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromPackages(store); err == nil {
		t.Fatal("FromPackages accepted a corrupt index")
	}
}

func TestFromPackagesCorruptActiveManifestErrors(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "bad-pkg", "v1.0.0")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, packages.ManifestFilename), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := packages.Index{
		SchemaVersion: 0,
		Active:        []packages.ActiveEntry{{ID: "bad-pkg", Version: "v1.0.0"}},
		Installed: []packages.InstalledRecord{{
			ID: "bad-pkg", Version: "v1.0.0", Owner: "o", Repo: "r",
			Integrity: packages.IntegrityOK, Authenticity: packages.AuthenticityUnverified,
		}},
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := packages.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromPackages(store); err == nil {
		t.Fatal("FromPackages accepted a corrupt active manifest")
	}
}
