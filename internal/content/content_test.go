package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadBundleOnly(t *testing.T) {
	opts := Options{
		BundleID: "digitalygo",
		BundleFS: fstest.MapFS{
			"content/skills/quick.md":        {Data: []byte("# quick")},
			"content/skills/role/orch.md":    {Data: []byte("# orch")},
			"content/skills/not-a-skill.txt": {Data: []byte("skip")},
			"content/agents/planner.md":      {Data: []byte("# planner")},
			"content/prompts/system.md":      {Data: []byte("be concise")},
			"content/other/readme.md":        {Data: []byte("x")},
		},
	}
	s, err := Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Skills) != 2 {
		t.Errorf("Skills = %v, want 2", keys(s.Skills))
	}
	quick, ok := s.Skills["digitalygo/quick"]
	if !ok {
		t.Fatal("missing digitalygo/quick")
	}
	if quick.Content != "# quick" || quick.Tier != TierBundle || quick.Origin != "bundle:skills" {
		t.Errorf("quick = %+v", quick)
	}
	if _, ok := s.Skills["digitalygo/role/orch"]; !ok {
		t.Error("nested skill missing")
	}
	if len(s.Agents) != 1 || s.Agents["digitalygo/planner"].Tier != TierBundle {
		t.Errorf("Agents = %v", keys(s.Agents))
	}
	if len(s.Prompts) != 1 || s.Prompts["digitalygo/system"].Content != "be concise" {
		t.Errorf("Prompts = %v", keys(s.Prompts))
	}
}

func TestLoadEmptyOptions(t *testing.T) {
	s, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Skills)+len(s.Agents)+len(s.Prompts) != 0 {
		t.Error("expected an empty snapshot")
	}
	if s.Fingerprint() == "" {
		t.Error("empty snapshot still hashes to a fingerprint")
	}
}

func TestLoadDirectorySources(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	writeTree(t, filepath.Join(ws, ".smidja"), map[string]string{
		"skills/local.md":    "# ws skill",
		"agents/wsa.md":      "# ws agent",
		"prompts/wsp.md":     "ws prompt",
		"skills/ignored.txt": "skip",
	})
	writeTree(t, filepath.Join(home, ".smidja"), map[string]string{
		"skills/global.md": "# user skill",
		"agents/ua.md":     "# user agent",
	})
	s, err := Load(Options{WorkspaceDir: ws, UserHome: home, TrustWorkspace: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wsSkill, ok := s.Skills["workspace/local"]
	if !ok {
		t.Fatalf("workspace skill missing: %v", keys(s.Skills))
	}
	if wsSkill.Tier != TierWorkspace || !strings.HasSuffix(wsSkill.Origin, ".smidja/skills") {
		t.Errorf("workspace skill = %+v", wsSkill)
	}
	userSkill, ok := s.Skills["user/global"]
	if !ok || userSkill.Tier != TierUser {
		t.Errorf("user skill = %+v ok=%v", userSkill, ok)
	}
	if _, ok := s.Agents["workspace/wsa"]; !ok {
		t.Error("workspace agent missing")
	}
	if _, ok := s.Agents["user/ua"]; !ok {
		t.Error("user agent missing")
	}
	if _, ok := s.Prompts["workspace/wsp"]; !ok {
		t.Error("workspace prompt missing")
	}
	if _, ok := s.Skills["workspace/ignored"]; ok {
		t.Error("non-md file loaded as a skill")
	}
}

func TestLoadUntrustedWorkspaceSkipped(t *testing.T) {
	ws := t.TempDir()
	writeTree(t, filepath.Join(ws, ".smidja"), map[string]string{
		"skills/local.md": "# ws skill",
	})
	s, err := Load(Options{WorkspaceDir: ws, TrustWorkspace: false})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Skills) != 0 {
		t.Errorf("Skills = %v, want none without trust", keys(s.Skills))
	}
}

func TestLoadPrecedenceAcrossTiers(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	pkg := t.TempDir()
	writeTree(t, filepath.Join(ws, ".smidja"), map[string]string{
		"skills/shared.md": "workspace wins unless bundle",
	})
	writeTree(t, filepath.Join(home, ".smidja"), map[string]string{
		"skills/shared.md":    "user content",
		"skills/user-only.md": "user only",
	})
	writeTree(t, pkg, map[string]string{
		"smidja.json":         `{"id":"demo-pkg"}`,
		"skills/shared.md":    "package content",
		"skills/pkg-only.md":  "package only",
		"agents/pkg-agent.md": "# agent",
	})
	opts := Options{
		BundleID:       "b",
		BundleFS:       fstest.MapFS{"content/skills/shared.md": {Data: []byte("bundle content")}},
		WorkspaceDir:   ws,
		UserHome:       home,
		PackagesDirs:   []string{pkg},
		TrustWorkspace: true,
	}
	s, err := Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Skills["b/shared"]; got.Content != "bundle content" || got.Tier != TierBundle {
		t.Errorf("bundle should beat every other tier, got %+v", got)
	}
	opts.BundleFS = nil
	s, err = Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Skills["workspace/shared"]; got.Content != "workspace wins unless bundle" || got.Tier != TierWorkspace {
		t.Errorf("workspace should beat user and packages, got %+v", got)
	}
	opts.TrustWorkspace = false
	s, err = Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Skills["user/shared"]; got.Content != "user content" || got.Tier != TierUser {
		t.Errorf("user should beat packages, got %+v", got)
	}
	opts.UserHome = ""
	s, err = Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Skills["demo-pkg/shared"]; got.Content != "package content" || got.Tier != TierPackages {
		t.Errorf("packages are the lowest wired tier, got %+v", got)
	}
	if _, ok := s.Skills["demo-pkg/pkg-only"]; !ok {
		t.Error("package-only skill missing")
	}
	if _, ok := s.Agents["demo-pkg/pkg-agent"]; !ok {
		t.Error("package agent missing")
	}
	if _, ok := s.Skills["user/user-only"]; ok {
		t.Error("user tier must be skipped when UserHome is empty")
	}
}

func TestLoadPackageTierLaterDirWins(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeTree(t, first, map[string]string{
		"smidja.json":   `{"id":"pkg-a"}`,
		"skills/dup.md": "from first",
	})
	writeTree(t, second, map[string]string{
		"smidja.json":   `{"id":"pkg-b"}`,
		"skills/dup.md": "from second",
	})
	s, err := Load(Options{PackagesDirs: []string{first, second}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := s.Skills["pkg-b/dup"]
	if !ok || got.Content != "from second" || got.Tier != TierPackages {
		t.Errorf("later package dir should win within the tier, got %+v ok=%v", got, ok)
	}
}

func TestLoadPackageCustomContentsPath(t *testing.T) {
	pkg := t.TempDir()
	writeTree(t, pkg, map[string]string{
		"smidja.json":       `{"id":"custom-pkg","contents":{"skills":"my-skills","agents":"my-agents"}}`,
		"my-skills/s.md":    "# skill",
		"my-agents/a.md":    "# agent",
		"skills/ignored.md": "# not used",
	})
	s, err := Load(Options{PackagesDirs: []string{pkg}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := s.Skills["custom-pkg/s"]; !ok {
		t.Error("skill from custom contents path missing")
	}
	if _, ok := s.Agents["custom-pkg/a"]; !ok {
		t.Error("agent from custom contents path missing")
	}
	if _, ok := s.Skills["custom-pkg/ignored"]; ok {
		t.Error("skill from the default path must not load when contents says otherwise")
	}
}

func TestLoadPackageManifestFallbackToBaseName(t *testing.T) {
	pkg := t.TempDir()
	writeTree(t, pkg, map[string]string{"skills/s.md": "# skill"})
	s, err := Load(Options{PackagesDirs: []string{pkg}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	base := filepath.Base(pkg)
	if _, ok := s.Skills[base+"/s"]; !ok {
		t.Errorf("skill key %q missing, keys = %v", base+"/s", keys(s.Skills))
	}
}

func TestLoadRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".smidja", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "real.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(skillsDir, "real.md"), filepath.Join(skillsDir, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Load(Options{WorkspaceDir: dir, TrustWorkspace: true})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Load error = %v, want symlink rejection", err)
	}
}

func TestLoadRejectsNonUTF8(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".smidja", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "bad.md"), []byte{0xff, 0xfe, 0x00, 0x41}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(Options{WorkspaceDir: dir, TrustWorkspace: true})
	if err == nil || !strings.Contains(err.Error(), "utf-8") {
		t.Fatalf("Load error = %v, want utf-8 rejection", err)
	}
}

func TestLoadRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".smidja", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "huge.md"), []byte(strings.Repeat("x", MaxArtifactBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(Options{WorkspaceDir: dir, TrustWorkspace: true})
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("Load error = %v, want size-cap rejection", err)
	}
	atCap := filepath.Join(skillsDir, "at-cap.md")
	if err := os.WriteFile(atCap, []byte(strings.Repeat("x", MaxArtifactBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(skillsDir, "huge.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Options{WorkspaceDir: dir, TrustWorkspace: true}); err != nil {
		t.Fatalf("Load at the cap: %v", err)
	}
}

func TestLoadRejectsHiddenNames(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".smidja", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, ".hidden.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Options{WorkspaceDir: dir, TrustWorkspace: true}); err == nil {
		t.Fatal("Load accepted a dot-prefixed skill name")
	}
}

func TestLoadRejectsBadNamesInBundle(t *testing.T) {
	opts := Options{BundleFS: fstest.MapFS{
		"content/skills/../escape.md": {Data: []byte("x")},
	}}
	if _, err := Load(opts); err == nil {
		t.Fatal("Load accepted a traversal path in the bundle")
	}
	opts = Options{BundleFS: fstest.MapFS{
		"content/skills/.hidden.md": {Data: []byte("x")},
	}}
	if _, err := Load(opts); err == nil {
		t.Fatal("Load accepted a hidden name in the bundle")
	}
}

func TestFingerprintStability(t *testing.T) {
	opts := Options{
		BundleID: "digitalygo",
		BundleFS: fstest.MapFS{
			"content/skills/quick.md":   {Data: []byte("# quick\nusage")},
			"content/agents/planner.md": {Data: []byte("# planner")},
		},
		WorkspaceDir:   t.TempDir(),
		UserHome:       t.TempDir(),
		TrustWorkspace: true,
	}
	first, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Error("identical loads produced different fingerprints")
	}
	changed := opts
	changed.BundleFS = fstest.MapFS{
		"content/skills/quick.md":   {Data: []byte("# quick\nchanged usage")},
		"content/agents/planner.md": {Data: []byte("# planner")},
	}
	third, err := Load(changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.Fingerprint() == first.Fingerprint() {
		t.Error("content change did not change the fingerprint")
	}
	moved := Options{
		BundleID:       "digitalygo",
		WorkspaceDir:   t.TempDir(),
		UserHome:       t.TempDir(),
		TrustWorkspace: true,
	}
	writeTree(t, filepath.Join(moved.WorkspaceDir, ".smidja"), map[string]string{
		"skills/quick.md":   "# quick\nusage",
		"agents/planner.md": "# planner",
	})
	fourth, err := Load(moved)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Fingerprint() == first.Fingerprint() {
		t.Error("origin change did not change the fingerprint")
	}
}

func TestFingerprintMethodComputesWhenEmpty(t *testing.T) {
	var s Snapshot
	if s.Fingerprint() == "" {
		t.Error("hand-built snapshot must still produce a fingerprint")
	}
}

func TestValidNameRejectsBadSegments(t *testing.T) {
	for _, name := range []string{"a\\b", "a//b", "a/./b", "/abs", "a/../b", "..", "a/.hidden", "a/b/"} {
		if err := validName(name); err == nil {
			t.Errorf("validName(%q) accepted a bad name", name)
		}
	}
	for _, name := range []string{"quick", "role/orchestrator", "a/b/c"} {
		if err := validName(name); err != nil {
			t.Errorf("validName(%q) rejected a good name: %v", name, err)
		}
	}
}

func TestTierRankOrdering(t *testing.T) {
	if tierRank(TierBundle) <= tierRank(TierWorkspace) {
		t.Error("bundle must rank above workspace")
	}
	if tierRank(TierWorkspace) <= tierRank(TierUser) {
		t.Error("workspace must rank above user")
	}
	if tierRank(TierUser) <= tierRank(TierPackages) {
		t.Error("user must rank above packages")
	}
	if tierRank(TierPackages) <= tierRank(TierCore) {
		t.Error("packages must rank above core")
	}
	if got := tierRank(Tier("unknown")); got != 0 {
		t.Errorf("unknown tier rank = %d, want 0", got)
	}
}

func TestLoadBundleMissingKindDirs(t *testing.T) {
	opts := Options{BundleID: "b", BundleFS: fstest.MapFS{
		"content/agents/a.md": {Data: []byte("# a")},
	}}
	s, err := Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Skills) != 0 {
		t.Errorf("Skills = %v, want none without a skills dir", keys(s.Skills))
	}
	if _, ok := s.Agents["b/a"]; !ok {
		t.Error("agents dir must still load")
	}
	opts.BundleFS = fstest.MapFS{"content/skills": {Data: []byte("not a dir")}}
	if _, err := Load(opts); err != nil {
		t.Fatalf("Load with a file at the kind path: %v", err)
	}
}

func TestLoadWorkspaceKindPathIsFile(t *testing.T) {
	dir := t.TempDir()
	skillsPath := filepath.Join(dir, ".smidja", "skills")
	if err := os.MkdirAll(filepath.Dir(skillsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillsPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(Options{WorkspaceDir: dir, TrustWorkspace: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Skills) != 0 {
		t.Errorf("Skills = %v, want none when the kind path is a file", keys(s.Skills))
	}
}

func TestDiscoverProjectInstructionsStopsAtGitRootBoundless(t *testing.T) {
	repo := t.TempDir()
	outer := filepath.Dir(repo)
	sub := filepath.Join(repo, "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, repo, map[string]string{".git/config": "dummy"})
	writeTree(t, outer, map[string]string{"AGENTS.md": "# outside the repo"})
	instr, err := DiscoverInstructions(sub, InstructionsOptions{})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Project != "" {
		t.Errorf("Project = %q, must not escape the git root", instr.Project)
	}
}

func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
