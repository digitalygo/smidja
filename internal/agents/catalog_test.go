package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/digitalygo/smidja/internal/content"
)

func writeAgentFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogConsumesResolvedTiersAndTrust(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	packageDir := t.TempDir()
	bundle := fstest.MapFS{
		"content/agents/shared.md":      {Data: []byte("bundle shared")},
		"content/agents/bundle-only.md": {Data: []byte("bundle only")},
	}
	writeAgentFile(t, filepath.Join(workspace, ".smidja", "agents"), "shared", "workspace shared")
	writeAgentFile(t, filepath.Join(workspace, ".smidja", "agents"), "workspace-only", "workspace only")
	writeAgentFile(t, filepath.Join(home, ".smidja", "agents"), "user-only", "user only")
	writeAgentFile(t, filepath.Join(packageDir, "agents"), "package-only", "package only")
	if err := os.WriteFile(filepath.Join(packageDir, "smidja.json"), []byte(`{"id":"demo","contents":{"agents":"agents"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	load := func(trust bool) content.Snapshot {
		t.Helper()
		snapshot, err := content.Load(content.Options{
			BundleID:       "bundle",
			BundleFS:       bundle,
			WorkspaceDir:   workspace,
			UserHome:       home,
			PackagesDirs:   []string{packageDir},
			TrustWorkspace: trust,
		})
		if err != nil {
			t.Fatalf("content.Load: %v", err)
		}
		return snapshot
	}

	untrusted := NewCatalog(load(false))
	if untrusted.Len() != 4 {
		t.Fatalf("untrusted agents = %v", untrusted.Names())
	}
	if _, ok := untrusted.Lookup("workspace-only"); ok {
		t.Fatal("untrusted workspace agent must not be executable")
	}
	if info, ok := untrusted.Info("shared"); !ok || info.Tier != content.TierBundle {
		t.Fatalf("shared tier = %+v ok=%v", info, ok)
	}

	trusted := NewCatalog(load(true))
	shared, ok := trusted.Lookup("shared")
	if !ok || shared.Body != "bundle shared" || shared.Tier != content.TierBundle {
		t.Fatalf("shared definition = %+v ok=%v", shared, ok)
	}
	for name, tier := range map[string]content.Tier{
		"bundle-only":    content.TierBundle,
		"workspace-only": content.TierWorkspace,
		"user-only":      content.TierUser,
		"package-only":   content.TierPackages,
	} {
		info, ok := trusted.Info(name)
		if !ok || info.Tier != tier {
			t.Fatalf("%s tier = %+v ok=%v, want %s", name, info, ok, tier)
		}
	}
}

func TestCatalogKeepsParseErrorsBounded(t *testing.T) {
	snapshot := content.Snapshot{Agents: map[string]content.AgentRef{
		"good":    {Name: "good", Content: "body"},
		"bad":     {Name: "bad", Content: "---\nname: x\n---\n   "},
		" broken": {Name: " broken", Content: "body"},
	}}
	catalog := NewCatalog(snapshot)
	if _, ok := catalog.Lookup("bad"); ok {
		t.Fatal("malformed definition must not be executable")
	}
	if _, ok := catalog.Lookup(" broken"); ok {
		t.Fatal("unsafe name must not be executable")
	}
	if err := catalog.ParseError("bad"); err == nil {
		t.Fatal("malformed definition must report a parse error")
	}
	good, ok := catalog.Lookup("good")
	if !ok || good.Body != "body" {
		t.Fatalf("good definition = %+v ok=%v", good, ok)
	}
	entries := catalog.Entries()
	if len(entries) != 1 || entries[0].Info.Name != "good" {
		t.Fatalf("entries = %+v", entries)
	}
	if _, ok := catalog.Info("bad"); ok {
		t.Fatal("malformed definition must not be listed")
	}
}

func TestCatalogDescriptionAndDisplayName(t *testing.T) {
	long := strings.Repeat("d", 120)
	snapshot := content.Snapshot{Agents: map[string]content.AgentRef{
		"worker": {Name: "worker", Content: "---\nname: The Worker\ndescription: " + long + "\n---\nbody"},
		"plain":  {Name: "plain", Content: "plain\x00text\nsecond line"},
	}}
	catalog := NewCatalog(snapshot)
	worker, _ := catalog.Lookup("worker")
	if len([]rune(worker.Description)) != descriptionRunes {
		t.Fatalf("description length = %d", len([]rune(worker.Description)))
	}
	if worker.DisplayName != "The Worker" {
		t.Fatalf("display name = %q", worker.DisplayName)
	}
	plain, _ := catalog.Lookup("plain")
	if plain.Description != "plaintext" {
		t.Fatalf("plain description = %q", plain.Description)
	}
}
