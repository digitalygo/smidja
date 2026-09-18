package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSlashCommands(t *testing.T) {
	commands := DefaultSlashCommands()
	if len(commands) < 5 {
		t.Fatalf("commands = %d", len(commands))
	}
	seen := map[string]bool{}
	for _, cmd := range commands {
		if cmd.Value == "" {
			t.Fatalf("empty command value")
		}
		if seen[cmd.Value] {
			t.Fatalf("duplicate %q", cmd.Value)
		}
		seen[cmd.Value] = true
	}
	for _, want := range []string{"new", "tree", "help"} {
		if !seen[want] {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestSlashSuggestions(t *testing.T) {
	provider := NewAutocompleteProvider("")
	items := provider.SlashSuggestions("/")
	if len(items) == 0 {
		t.Fatalf("slash root should list all")
	}
	filtered := provider.SlashSuggestions("/ne")
	found := false
	for _, item := range filtered {
		if item.Value == "new" {
			found = true
		}
	}
	if !found {
		t.Fatalf("/ne should match new: %v", filtered)
	}
	if got := provider.SlashSuggestions("/zzz-no-match"); len(got) != 0 {
		t.Fatalf("no match = %v", got)
	}
	provider.SetExtraCommands([]AutocompleteItem{{Value: "deploy", Label: "deploy", Description: "custom"}})
	filtered = provider.SlashSuggestions("/dep")
	found = false
	for _, item := range filtered {
		if item.Value == "deploy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("extra command not suggested: %v", filtered)
	}
	provider.SetExtraCommands([]AutocompleteItem{{Value: "new", Label: "new"}, {Value: "", Label: ""}})
	if got := provider.SlashSuggestions("/new"); len(got) == 0 {
		t.Fatalf("dedup should keep new")
	}
}

func TestBestSlashMatchIndex(t *testing.T) {
	provider := NewAutocompleteProvider("")
	items := []AutocompleteItem{{Value: "new"}, {Value: "news"}, {Value: "tree"}}
	if idx := provider.BestSlashMatchIndex(items, "/new"); idx != 0 {
		t.Fatalf("exact = %d", idx)
	}
	if idx := provider.BestSlashMatchIndex(items, "/ne"); idx != 0 {
		t.Fatalf("prefix = %d", idx)
	}
	if idx := provider.BestSlashMatchIndex(items, "/zzz"); idx != -1 {
		t.Fatalf("no match = %d", idx)
	}
	if idx := provider.BestSlashMatchIndex(items, "/"); idx != -1 {
		t.Fatalf("empty = %d", idx)
	}
}

func TestExtractAtToken(t *testing.T) {
	if token, ok := extractAtToken("hello @src/ma"); !ok || token != "@src/ma" {
		t.Fatalf("token = %q %v", token, ok)
	}
	if _, ok := extractAtToken("hello world"); ok {
		t.Fatalf("no @ should fail")
	}
	if _, ok := extractAtToken("email@example.com"); ok {
		t.Fatalf("mid-word @ should fail: email case")
	}
	if token, ok := extractAtToken("@README"); !ok || token != "@README" {
		t.Fatalf("start token = %q %v", token, ok)
	}
	if _, ok := extractAtToken("@a b"); ok {
		t.Fatalf("space in token should fail")
	}
}

func TestConstrainedJoin(t *testing.T) {
	root := "/tmp/root"
	if _, ok := constrainedJoin(root, "/tmp/root/sub"); !ok {
		t.Fatalf("sub should be allowed")
	}
	if _, ok := constrainedJoin(root, "/tmp/other"); ok {
		t.Fatalf("escape should be rejected")
	}
	if _, ok := constrainedJoin(root, "/tmp/root/../other"); ok {
		t.Fatalf("dotdot escape should be rejected")
	}
}

func writeAutocompleteFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir = %v", err)
		}
		if strings.HasSuffix(name, "/") {
			continue
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write = %v", err)
		}
	}
	return root
}

func TestPathSuggestionsConstrained(t *testing.T) {
	root := writeAutocompleteFixture(t, map[string]string{
		"alpha.txt":       "a",
		"beta.txt":        "b",
		"subdir/gamma.go": "c",
		".git/ignored":    "x",
	})
	provider := NewAutocompleteProvider(root)
	items := provider.PathSuggestions("@")
	if len(items) == 0 {
		t.Fatalf("root @ should list files")
	}
	for _, item := range items {
		if strings.Contains(item.Value, ".git") {
			t.Fatalf("should skip .git: %v", item)
		}
	}
	items = provider.PathSuggestions("@alpha")
	if len(items) != 1 || !strings.Contains(items[0].Value, "alpha") {
		t.Fatalf("@alpha = %v", items)
	}
	items = provider.PathSuggestions("@subdir/")
	if len(items) != 1 || !strings.Contains(items[0].Value, "gamma") {
		t.Fatalf("@subdir/ = %v", items)
	}
	if got := provider.PathSuggestions("no-at-prefix"); len(got) != 0 {
		t.Fatalf("no @ = %v", got)
	}
	if got := provider.PathSuggestions("@../escape"); len(got) != 0 {
		t.Fatalf("escape should be empty: %v", got)
	}
	provider.SetWorkspaceRoot("")
	provider.SetLister(nil)
	if provider.workspaceBase() != "." {
		t.Fatalf("empty root base = %q", provider.workspaceBase())
	}
	provider.SetWorkspaceRoot(root)
	provider.SetLister(func(dir string) ([]autocompleteFileEntry, error) {
		return nil, os.ErrNotExist
	})
	if got := provider.PathSuggestions("@"); len(got) != 0 {
		t.Fatalf("lister error should be empty: %v", got)
	}
}

func TestPathSuggestionsAbsoluteRejected(t *testing.T) {
	root := writeAutocompleteFixture(t, map[string]string{"a.txt": "a"})
	provider := NewAutocompleteProvider(root)
	if got := provider.PathSuggestions("@/etc/pass"); len(got) != 0 {
		t.Fatalf("absolute dir should be rejected: %v", got)
	}
}

func TestApplyCompletions(t *testing.T) {
	provider := NewAutocompleteProvider("")
	completed, col := provider.ApplySlashCompletion("/ne", 3, AutocompleteItem{Value: "new"}, "/ne")
	if completed != "/new " || col != 5 {
		t.Fatalf("slash = %q %d", completed, col)
	}
	completed, col = provider.ApplySlashCompletion("", 0, AutocompleteItem{Value: "new"}, "")
	if !strings.HasPrefix(completed, "/new ") {
		t.Fatalf("empty slash = %q", completed)
	}
	completed, col = provider.ApplyPathCompletion("see @alp", 8, AutocompleteItem{Value: "@alpha.txt"}, "@alp")
	if completed != "see @alpha.txt " {
		t.Fatalf("path = %q", completed)
	}
	if col != len(completed) {
		t.Fatalf("path col = %d want %d", col, len(completed))
	}
	completed, col = provider.ApplyPathCompletion("see @sub", 8, AutocompleteItem{Value: "@subdir/"}, "@sub")
	if completed != "see @subdir/" || col != len(completed) {
		t.Fatalf("dir path = %q %d", completed, col)
	}
	completed, _ = provider.ApplyPathCompletion("x", -5, AutocompleteItem{Value: "@a"}, "@")
	if !strings.Contains(completed, "@a") {
		t.Fatalf("negative cursor = %q", completed)
	}
	completed, _ = provider.ApplySlashCompletion("abc", 99, AutocompleteItem{Value: "new"}, "/a")
	if !strings.Contains(completed, "/new") {
		t.Fatalf("overflow cursor = %q", completed)
	}
}

func TestInlineHint(t *testing.T) {
	if got := InlineHint("/ne", AutocompleteItem{Value: "new"}); got != "w" {
		t.Fatalf("hint = %q", got)
	}
	if got := InlineHint("/new", AutocompleteItem{Value: "new"}); got != "" {
		t.Fatalf("exact hint = %q", got)
	}
	if got := InlineHint("/zzz", AutocompleteItem{Value: "new"}); got != "" {
		t.Fatalf("no prefix hint = %q", got)
	}
	if got := InlineHint("ne", AutocompleteItem{Value: "new"}); got != "w" {
		t.Fatalf("bare hint = %q", got)
	}
}

func TestSlashContextHelpers(t *testing.T) {
	if !isSlashContext("/ne", 0) {
		t.Fatalf("/ne should be slash context")
	}
	if isSlashContext("/ne", 1) {
		t.Fatalf("line 1 should not be slash context")
	}
	if isSlashContext("/new arg", 0) {
		t.Fatalf("with space should not be slash context")
	}
	if isSlashContext("hello", 0) {
		t.Fatalf("no slash should fail")
	}
	if prefix, ok := slashPrefixOf("  /ne"); !ok || prefix != "/ne" {
		t.Fatalf("prefix = %q %v", prefix, ok)
	}
	if _, ok := slashPrefixOf("  /new arg"); ok {
		t.Fatalf("with space should fail")
	}
	if _, ok := slashPrefixOf("hello"); ok {
		t.Fatalf("no slash should fail")
	}
}

func TestAutocompleteProviderSetters(t *testing.T) {
	provider := NewAutocompleteProvider("/tmp")
	provider.SetWorkspaceRoot("/other")
	if provider.workspaceBase() != "/other" {
		t.Fatalf("root not set")
	}
	provider.SetExtraCommands(nil)
	if len(provider.allCommands()) == 0 {
		t.Fatalf("all commands empty")
	}
	provider.SetLister(nil)
	if provider.listFiles == nil {
		t.Fatalf("lister nil after reset")
	}
}
func TestPathSuggestionsSymlinkOutsideBlocked(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write outside = %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inner.txt"), []byte("y"), 0o600); err != nil {
		t.Fatalf("write inner = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	provider := NewAutocompleteProvider(root)
	items := provider.PathSuggestions("@link/")
	for _, item := range items {
		if strings.Contains(item.Value, "secret") {
			t.Fatalf("outside file exposed: %v", items)
		}
	}
	if len(items) != 0 {
		t.Fatalf("@link/ should not list outside files: %v", items)
	}
	top := provider.PathSuggestions("@")
	for _, item := range top {
		if strings.TrimSuffix(item.Label, "/") == "link" && strings.HasSuffix(item.Value, "/") {
			t.Fatalf("outside symlink suggested as traversable: %v", top)
		}
	}
}

func TestPathSuggestionsSymlinkInsideUsable(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "realdir")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir = %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "kept.txt"), []byte("z"), 0o600); err != nil {
		t.Fatalf("write = %v", err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "insidelink")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	provider := NewAutocompleteProvider(root)
	top := provider.PathSuggestions("@")
	found := false
	for _, item := range top {
		if item.Label == "insidelink/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inside symlink should remain traversable: %v", top)
	}
	items := provider.PathSuggestions("@insidelink/")
	found = false
	for _, item := range items {
		if strings.Contains(item.Value, "kept.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("@insidelink/ should list inside files: %v", items)
	}
}
