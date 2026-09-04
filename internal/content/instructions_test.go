package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDiscoverProjectInstructionsNearestWins(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "cmd", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, repo, map[string]string{
		"AGENTS.md":         "# repo rules",
		".git/config":       "dummy",
		"cmd/app/AGENTS.md": "# app rules",
	})
	instr, err := DiscoverInstructions(sub, InstructionsOptions{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Project != "# app rules" {
		t.Errorf("Project = %q, want the nearest file", instr.Project)
	}
}

func TestDiscoverProjectInstructionsStopsAtGitRoot(t *testing.T) {
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	sub := filepath.Join(repo, "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, repo, map[string]string{
		"AGENTS.md":   "# repo rules",
		".git/config": "dummy",
	})
	writeTree(t, outer, map[string]string{
		"AGENTS.md": "# outer rules",
	})
	instr, err := DiscoverInstructions(sub, InstructionsOptions{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Project != "# repo rules" {
		t.Errorf("Project = %q, want the repo file inside the git root", instr.Project)
	}
}

func TestDiscoverProjectInstructionsStopsAtWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()
	sub := filepath.Join(workspace, "pkg", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, filepath.Join(workspace, "pkg", "x"), map[string]string{
		"AGENTS.md": "# sub rules",
	})
	instr, err := DiscoverInstructions(sub, InstructionsOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Project != "# sub rules" {
		t.Errorf("Project = %q, want the nearest file inside the workspace", instr.Project)
	}
	outer := filepath.Dir(workspace)
	writeTree(t, outer, map[string]string{"AGENTS.md": "# outer"})
	instr, err = DiscoverInstructions(sub, InstructionsOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if strings.Contains(instr.Project, "outer") {
		t.Error("search escaped the workspace root")
	}
}

func TestDiscoverProjectInstructionsNone(t *testing.T) {
	instr, err := DiscoverInstructions(t.TempDir(), InstructionsOptions{})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Project != "" {
		t.Errorf("Project = %q, want empty", instr.Project)
	}
}

func TestDiscoverGlobalInstructionsAppended(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	writeTree(t, repo, map[string]string{
		"AGENTS.md":   "# repo rules",
		".git/config": "dummy",
	})
	writeTree(t, filepath.Join(home, ".smidja"), map[string]string{
		"AGENTS.md": "# global rules",
	})
	instr, err := DiscoverInstructions(repo, InstructionsOptions{WorkspaceRoot: repo, UserHome: home})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Project != "# repo rules" || instr.Global != "# global rules" {
		t.Errorf("instr = %+v", instr)
	}
}

func TestDiscoverGlobalMissingIgnored(t *testing.T) {
	instr, err := DiscoverInstructions(t.TempDir(), InstructionsOptions{UserHome: t.TempDir()})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Global != "" {
		t.Errorf("Global = %q, want empty", instr.Global)
	}
}

func TestDiscoverInstructionsTruncatedSafely(t *testing.T) {
	repo := t.TempDir()
	content := strings.Repeat("é", 100)
	writeTree(t, repo, map[string]string{
		"AGENTS.md":   content,
		".git/config": "dummy",
	})
	instr, err := DiscoverInstructions(repo, InstructionsOptions{WorkspaceRoot: repo, MaxBytes: 50})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if !strings.HasPrefix(instr.Project, "é") || len(instr.Project) > 50 {
		t.Errorf("Project = %q (len %d), want a utf-8 safe prefix capped at 50", instr.Project, len(instr.Project))
	}
	if !strings.HasSuffix(instr.Project, "é") {
		t.Errorf("Project = %q, truncated mid-rune", instr.Project)
	}
}

func TestDiscoverInstructionsDefaultCap(t *testing.T) {
	repo := t.TempDir()
	big := strings.Repeat("a", MaxInstructionsBytes+4096)
	writeTree(t, repo, map[string]string{
		"AGENTS.md":   big,
		".git/config": "dummy",
	})
	instr, err := DiscoverInstructions(repo, InstructionsOptions{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if len(instr.Project) != MaxInstructionsBytes {
		t.Errorf("Project len = %d, want %d", len(instr.Project), MaxInstructionsBytes)
	}
}

func TestInstructionsSuffixRender(t *testing.T) {
	i := Instructions{Bundle: "bund", Project: "proj", Global: "glob"}
	suffix := i.Suffix()
	if !strings.Contains(suffix, "[bundle instructions]") || !strings.Contains(suffix, "bund") {
		t.Errorf("suffix = %q, want the bundle marker and content", suffix)
	}
	if !strings.Contains(suffix, "[project instructions]") || !strings.Contains(suffix, "proj") {
		t.Errorf("suffix = %q, want the project marker and content", suffix)
	}
	if !strings.Contains(suffix, "[user instructions]") || !strings.Contains(suffix, "glob") {
		t.Errorf("suffix = %q, want the user marker and content", suffix)
	}
	if strings.Index(suffix, "[bundle instructions]") > strings.Index(suffix, "[project instructions]") {
		t.Error("bundle section must precede the project section")
	}
	if strings.Index(suffix, "[project instructions]") > strings.Index(suffix, "[user instructions]") {
		t.Error("project section must precede the user section")
	}
	if only := (Instructions{Project: "proj"}).Suffix(); strings.Contains(only, "user instructions") {
		t.Errorf("suffix = %q, must omit the empty global section", only)
	}
	if empty := (Instructions{}).Suffix(); empty != "" {
		t.Errorf("empty instructions suffix = %q, want empty", empty)
	}
}

func TestDiscoverBundleInstructionsRootedPreferred(t *testing.T) {
	bundleFS := fstest.MapFS{
		"AGENTS.md":           {Data: []byte("# bundle rules")},
		"content/AGENTS.md":   {Data: []byte("# legacy bundle rules")},
		"content/skills/x.md": {Data: []byte("x")},
	}
	instr, err := DiscoverInstructions(t.TempDir(), InstructionsOptions{BundleFS: bundleFS})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Bundle != "# bundle rules" {
		t.Errorf("Bundle = %q, want the rooted AGENTS.md", instr.Bundle)
	}
}

func TestDiscoverBundleInstructionsLegacyLocation(t *testing.T) {
	bundleFS := fstest.MapFS{"content/AGENTS.md": {Data: []byte("# legacy bundle rules")}}
	instr, err := DiscoverInstructions(t.TempDir(), InstructionsOptions{BundleFS: bundleFS})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Bundle != "# legacy bundle rules" {
		t.Errorf("Bundle = %q, want the legacy content/AGENTS.md", instr.Bundle)
	}
}

func TestDiscoverBundleInstructionsMissingIgnored(t *testing.T) {
	bundleFS := fstest.MapFS{"content/skills/x.md": {Data: []byte("x")}}
	instr, err := DiscoverInstructions(t.TempDir(), InstructionsOptions{BundleFS: bundleFS})
	if err != nil {
		t.Fatalf("DiscoverInstructions: %v", err)
	}
	if instr.Bundle != "" {
		t.Errorf("Bundle = %q, want empty without a bundle AGENTS.md", instr.Bundle)
	}
}

func TestReadBoundedMissingFile(t *testing.T) {
	if _, err := readBounded(filepath.Join(t.TempDir(), "nope"), 64); err == nil {
		t.Fatal("readBounded on a missing file must error")
	}
}
