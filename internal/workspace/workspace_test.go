package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestWorkspace creates a workspace rooted at a fresh temp dir and
// returns it together with the canonical root (as resolved by New).
func newTestWorkspace(t *testing.T) (*Workspace, string) {
	t.Helper()
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w, w.Root()
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(%q -> %q): %v", link, target, err)
	}
}

func TestNewCanonicalizesRoot(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatalf("New(%q): %v", dir, err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	if got := w.Root(); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestNewNonexistentLeaf(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "missing", "deeper")
	w, err := New(leaf)
	if err != nil {
		t.Fatalf("New(%q) with nonexistent leaf: %v", leaf, err)
	}
	want := filepath.Join(dir, "missing", "deeper")
	if got := w.Root(); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestNewEmptyRoot(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("New(\"\"): expected error, got nil")
	}
}

func TestContainNestedPaths(t *testing.T) {
	w, root := newTestWorkspace(t)
	mustWrite(t, filepath.Join(root, "src", "main.go"), "package main\n")

	cases := map[string]string{
		"src/main.go":                         filepath.Join(root, "src", "main.go"),
		"./src/main.go":                       filepath.Join(root, "src", "main.go"),
		"src/../src/main.go":                  filepath.Join(root, "src", "main.go"),
		"src/deep/../main.go":                 filepath.Join(root, "src", "main.go"),
		".":                                   root,
		filepath.Join(root, "src", "main.go"): filepath.Join(root, "src", "main.go"),
	}
	for in, want := range cases {
		got, err := w.Contain(in)
		if err != nil {
			t.Errorf("Contain(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Contain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContainNonexistentNestedPath(t *testing.T) {
	w, root := newTestWorkspace(t)
	got, err := w.Contain("a/b/c.txt")
	if err != nil {
		t.Fatalf("Contain(nonexistent nested): %v", err)
	}
	if want := filepath.Join(root, "a", "b", "c.txt"); got != want {
		t.Errorf("Contain = %q, want %q", got, want)
	}
}

func TestContainRejectsTraversal(t *testing.T) {
	w, _ := newTestWorkspace(t)
	bad := []string{
		"..",
		"../outside",
		"../../etc/passwd",
		"a/../../outside",
		"../smidja",
	}
	for _, p := range bad {
		if got, err := w.Contain(p); err == nil {
			t.Errorf("Contain(%q) = %q, want error", p, got)
		}
	}
}

func TestContainRejectsAbsoluteOutsideRoot(t *testing.T) {
	w, _ := newTestWorkspace(t)
	bad := []string{
		"/etc/passwd",
		"/tmp",
		filepath.Join(filepath.Dir(w.Root()), "sibling"),
	}
	for _, p := range bad {
		if got, err := w.Contain(p); err == nil {
			t.Errorf("Contain(%q) = %q, want error", p, got)
		}
	}
}

func TestContainRejectsSymlinkEscape(t *testing.T) {
	w, root := newTestWorkspace(t)

	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.txt"), "top secret")
	mustSymlink(t, outside, filepath.Join(root, "evil"))

	// The symlink itself and anything through it must be rejected.
	if got, err := w.Contain("evil"); err == nil {
		t.Errorf("Contain(evil) = %q, want error", got)
	}
	if got, err := w.Contain("evil/secret.txt"); err == nil {
		t.Errorf("Contain(evil/secret.txt) = %q, want error", got)
	}
	if got, err := w.Contain("evil/nonexistent/deep"); err == nil {
		t.Errorf("Contain(evil/nonexistent/deep) = %q, want error", got)
	}
}

func TestContainAllowsSymlinkInsideRoot(t *testing.T) {
	w, root := newTestWorkspace(t)

	sub := filepath.Join(root, "sub")
	mustWrite(t, filepath.Join(sub, "file.txt"), "hello")
	mustSymlink(t, sub, filepath.Join(root, "alias"))

	got, err := w.Contain("alias/file.txt")
	if err != nil {
		t.Fatalf("Contain(alias/file.txt): %v", err)
	}
	if want := filepath.Join(root, "alias", "file.txt"); got != want {
		t.Errorf("Contain = %q, want %q", got, want)
	}
}

func TestContainRejectsEmptyPath(t *testing.T) {
	w, _ := newTestWorkspace(t)
	if _, err := w.Contain(""); err == nil {
		t.Error("Contain(\"\"): expected error, got nil")
	}
}

func TestIsForbidden(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/repo/.git/config", true},
		{"/repo/.git", true},
		{"/repo/.git/objects/ab/123", true},
		{"/repo/sub/.git/HEAD", true},
		{"/repo/sub/deep/.git/index", true},
		{"/.git/HEAD", true},
		{".git/HEAD", true},
		{"/repo/.github/workflows/ci.yml", false},
		{"/repo/.gitignore", false},
		{"/repo/.gitconfig", false},
		{"/repo/src/main.go", false},
		{"/repo", false},
		{"/", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsForbidden(tc.path); got != tc.want {
			t.Errorf("IsForbidden(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestContainForbiddenGitPath(t *testing.T) {
	// Contain must accept the path (it is inside the root), and
	// IsForbidden must flag it: the two checks compose.
	w, root := newTestWorkspace(t)
	mustWrite(t, filepath.Join(root, ".git", "config"), "[core]\n")

	got, err := w.Contain(".git/config")
	if err != nil {
		t.Fatalf("Contain(.git/config): %v", err)
	}
	if !strings.HasPrefix(got, root) {
		t.Errorf("Contain(.git/config) = %q, want under root %q", got, root)
	}
	if !IsForbidden(got) {
		t.Errorf("IsForbidden(%q) = false, want true", got)
	}
}
