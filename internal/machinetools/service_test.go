package machinetools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func invoke(t *testing.T, s *Service, name string, args Arguments) Result {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return s.Execute(context.Background(), name, b)
}
func requireOK(t *testing.T, r Result) {
	t.Helper()
	if !r.OK {
		t.Fatalf("unexpected error: %+v", r)
	}
}
func writeFixture(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}
func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestFileLifecycleAndOutsideProject(t *testing.T) {
	s := New(Config{})
	dir := t.TempDir()
	p := filepath.Join(dir, "file.py")
	r := invoke(t, s, "write", Arguments{Path: p, Content: "print('hi')\n"})
	requireOK(t, r)
	if r.Data.(File).SHA256 != sha("print('hi')\n") {
		t.Fatal(r)
	}
	read := invoke(t, s, "read", Arguments{Path: p})
	requireOK(t, read)
	if read.Data.(File).Content != "print('hi')\n" {
		t.Fatal(read)
	}
	bad := invoke(t, s, "write", Arguments{Path: p, Content: "overwrite"})
	if bad.Code != "conflict" {
		t.Fatal(bad)
	}
	bad = invoke(t, s, "write", Arguments{Path: p, Content: "overwrite", ExpectedSHA256: "wrong"})
	if bad.Code != "conflict" {
		t.Fatal(bad)
	}
	r = invoke(t, s, "edit", Arguments{Path: p, OldText: "hi", NewText: "there", ExpectedSHA256: read.Data.(File).SHA256})
	requireOK(t, r)
	if r.Data.(File).Content != "print('there')\n" {
		t.Fatal(r)
	}
	for _, old := range []string{"", "missing"} {
		r = invoke(t, s, "edit", Arguments{Path: p, OldText: old, ExpectedSHA256: sha("print('there')\n")})
		if r.Code != "ambiguous_edit" {
			t.Fatal(r)
		}
	}
	r = invoke(t, s, "edit", Arguments{Path: p, OldText: "there", ExpectedSHA256: "stale"})
	if r.Code != "conflict" {
		t.Fatal(r)
	}
	if err := os.Chmod(p, 0o750); err != nil {
		t.Fatal(err)
	}
	r = invoke(t, s, "write", Arguments{Path: p, Content: "new", ExpectedSHA256: sha("print('there')\n")})
	requireOK(t, r)
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o750 {
		t.Fatal(info.Mode())
	}
	r = invoke(t, s, "write", Arguments{Path: filepath.Join(dir, "gone"), Content: "new", ExpectedSHA256: "old"})
	if r.Code != "conflict" {
		t.Fatal(r)
	}
}

func TestTextLimitsAndInvalidPaths(t *testing.T) {
	s := New(Config{MaxBytes: 8})
	dir := t.TempDir()
	p := filepath.Join(dir, "bin")
	for _, name := range []string{"read", "write", "edit", "list", "git_diff", "grep"} {
		r := invoke(t, s, name, Arguments{})
		if r.OK {
			t.Fatalf("%s accepted empty", name)
		}
	}
	writeFixture(t, p, "123456789")
	if r := invoke(t, s, "read", Arguments{Path: p}); r.OK {
		t.Fatal(r)
	}
	for _, content := range []string{"123456789", "a\x00b"} {
		if r := invoke(t, s, "write", Arguments{Path: p, Content: content}); r.OK {
			t.Fatal(r)
		}
	}
	writeFixture(t, p, "a\x00b")
	if r := invoke(t, s, "read", Arguments{Path: p}); r.Code != "unsupported_file" {
		t.Fatal(r)
	}
	if r := invoke(t, s, "edit", Arguments{Path: p, OldText: "a", ExpectedSHA256: sha("a\x00b")}); r.Code != "unsupported_file" {
		t.Fatal(r)
	}
	if r := invoke(t, s, "write", Arguments{Path: p, Content: "abc", ExpectedSHA256: sha("a\x00b")}); r.Code != "unsupported_file" {
		t.Fatal(r)
	}
	if r := invoke(t, s, "read", Arguments{Path: dir}); r.OK {
		t.Fatal(r)
	}
	if r := invoke(t, s, "read", Arguments{Path: p + "none"}); r.OK {
		t.Fatal(r)
	}
	if r := invoke(t, s, "write", Arguments{Path: filepath.Join(dir, "missing", "file"), Content: "abc"}); r.OK {
		t.Fatal(r)
	}
	if r := invoke(t, s, "write", Arguments{Path: dir, Content: "abc", ExpectedSHA256: "hash"}); r.OK {
		t.Fatal(r)
	}
	if r := invoke(t, s, "edit", Arguments{Path: p + "none"}); r.OK {
		t.Fatal(r)
	}
	if r := s.Execute(context.Background(), "read", []byte("{")); r.Code != "invalid_arguments" {
		t.Fatal(r)
	}
	if r := invoke(t, s, "unknown", Arguments{}); r.Code != "unknown_tool" {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := s.Execute(ctx, "read", nil); r.Code != "cancelled" {
		t.Fatal(r)
	}
}

func TestSymlinkAndList(t *testing.T) {
	s := New(Config{})
	dir := t.TempDir()
	p := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	writeFixture(t, p, "abc")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	requireOK(t, invoke(t, s, "write", Arguments{Path: link, Content: "def", ExpectedSHA256: sha("abc")}))
	info, _ := os.Lstat(link)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink overwritten")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "def" {
		t.Fatal(string(b))
	}
	r := invoke(t, s, "list", Arguments{Path: dir})
	requireOK(t, r)
	if len(r.Data.(Listing).Entries) != 2 {
		t.Fatal(r)
	}
	r = invoke(t, New(Config{MaxEntries: 1}), "list", Arguments{Path: dir})
	requireOK(t, r)
	if !r.Truncated || len(r.Data.(Listing).Entries) != 1 {
		t.Fatal(r)
	}
	r = invoke(t, s, "list", Arguments{Path: t.TempDir()})
	requireOK(t, r)
	if len(r.Data.(Listing).Entries) != 0 {
		t.Fatal(r)
	}
	if r = invoke(t, s, "list", Arguments{Path: p}); r.OK {
		t.Fatal(r)
	}
	if r = invoke(t, s, "list", Arguments{Path: p + "none"}); r.OK {
		t.Fatal(r)
	}
	if err := os.Symlink(link, filepath.Join(dir, "cycle")); err != nil {
		t.Fatal(err)
	}
}

func TestGrep(t *testing.T) {
	s := New(Config{})
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "a.go"), "alpha\nbeta\nalpha2\n")
	writeFixture(t, filepath.Join(dir, "binary"), "alpha\x00")
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, ".git", "hidden"), "alpha")
	if err := os.Symlink(dir, filepath.Join(dir, "loop")); err != nil {
		t.Fatal(err)
	}
	r := invoke(t, s, "grep", Arguments{Path: dir, Pattern: "^alpha"})
	requireOK(t, r)
	if len(r.Data.([]Match)) != 2 {
		t.Fatal(r)
	}
	if r = invoke(t, s, "grep", Arguments{Path: dir, Pattern: "["}); r.OK {
		t.Fatal(r)
	}
	if r = invoke(t, s, "grep", Arguments{Path: dir + "missing", Pattern: "a"}); r.OK {
		t.Fatal(r)
	}
	r = invoke(t, New(Config{MaxEntries: 1}), "grep", Arguments{Path: dir, Pattern: "alpha"})
	requireOK(t, r)
	if !r.Truncated {
		t.Fatal(r)
	}
	r = invoke(t, New(Config{MaxBytes: 4}), "grep", Arguments{Path: dir, Pattern: "alpha"})
	requireOK(t, r)
	r = invoke(t, s, "grep", Arguments{Path: filepath.Join(dir, "a.go"), Pattern: "beta"})
	requireOK(t, r)
	if r.Data.([]Match)[0].Line != 2 {
		t.Fatal(r)
	}
}

func TestBash(t *testing.T) {
	s := New(Config{MaxBytes: 8, DefaultTimeout: 200 * time.Millisecond, MaxTimeout: time.Second, Environment: []string{"SMIDJA_TEST_MARKER=yes"}})
	r := invoke(t, s, "bash", Arguments{Command: "printf \"$SMIDJA_TEST_MARKER\""})
	requireOK(t, r)
	if r.Data.(ProcessResult).Output != "yes" {
		t.Fatal(r)
	}
	r = invoke(t, s, "bash", Arguments{Command: "printf 123456789012"})
	requireOK(t, r)
	if !r.Truncated || r.Data.(ProcessResult).Output != "12345678" {
		t.Fatal(r)
	}
	r = invoke(t, s, "bash", Arguments{Command: "printf err >&2; exit 7"})
	if r.OK || r.Data.(ProcessResult).ExitCode != 7 {
		t.Fatal(r)
	}
	r = invoke(t, s, "bash", Arguments{Command: "sleep 10"})
	if r.OK || !r.Data.(ProcessResult).TimedOut {
		t.Fatal(r)
	}
	for _, a := range []Arguments{{Command: ""}, {Command: "true", TimeoutSeconds: -1}, {Command: "true", TimeoutSeconds: 2}} {
		if r = invoke(t, s, "bash", a); r.Code != "invalid_arguments" {
			t.Fatal(r)
		}
	}
	r = invoke(t, s, "bash", Arguments{Command: "pwd", Path: t.TempDir(), TimeoutSeconds: 1})
	requireOK(t, r)
	r = invoke(t, s, "bash", Arguments{Command: "true", Path: "/nonexistent/smidja-test"})
	if r.OK {
		t.Fatal(r)
	}
	r = s.run(context.Background(), "", time.Second, "/nonexistent/smidja-executable")
	if r.OK {
		t.Fatal(r)
	}
	cfg := New(Config{DefaultTimeout: time.Minute, MaxTimeout: time.Second})
	if cfg.cfg.DefaultTimeout != time.Second {
		t.Fatal(cfg.cfg)
	}
}

func gitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
}
func TestGitDetectionDiffAndWorktree(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-q")
	p := filepath.Join(dir, "a.txt")
	writeFixture(t, p, "old\n")
	gitCommand(t, dir, "add", "a.txt")
	gitCommand(t, dir, "commit", "-qm", "initial")
	writeFixture(t, p, "new\n")
	writeFixture(t, filepath.Join(dir, "untracked"), "other")
	s := New(Config{})
	r := invoke(t, s, "git_status", Arguments{Path: dir})
	requireOK(t, r)
	g := r.Data.(GitResult)
	if g.Root != dir || !strings.Contains(g.Output, "a.txt") || !strings.Contains(g.Output, "untracked") {
		t.Fatal(r)
	}
	r = invoke(t, s, "git_diff", Arguments{Path: dir})
	requireOK(t, r)
	if !strings.Contains(r.Data.(GitResult).Output, "+new") {
		t.Fatal(r)
	}
	gitCommand(t, dir, "add", "a.txt")
	r = invoke(t, s, "git_diff", Arguments{Path: dir, Staged: true})
	requireOK(t, r)
	if !strings.Contains(r.Data.(GitResult).Output, "+new") {
		t.Fatal(r)
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	gitCommand(t, dir, "worktree", "add", "--detach", worktree, "HEAD")
	writeFixture(t, filepath.Join(worktree, "a.txt"), "worktree\n")
	r = invoke(t, s, "git_diff", Arguments{Path: worktree})
	requireOK(t, r)
	if !strings.Contains(r.Data.(GitResult).Output, "+worktree") {
		t.Fatal(r)
	}
	r = invoke(t, s, "git_status", Arguments{Path: t.TempDir()})
	if r.OK {
		t.Fatal(r)
	}
	r = invoke(t, New(Config{MaxBytes: 1}), "git_status", Arguments{Path: dir})
	if r.Code != "too_large" {
		t.Fatal(r)
	}
}
func TestDefinitions(t *testing.T) {
	d := Definitions()
	if len(d) != 8 {
		t.Fatal(d)
	}
	for _, v := range d {
		if v.Name == "" || v.Description == "" || !json.Valid(v.Parameters) {
			t.Fatal(v)
		}
	}
}

func TestGitDoesNotExecuteRepositoryFilters(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-q")
	p := filepath.Join(dir, "a.txt")
	writeFixture(t, p, "before\n")
	gitCommand(t, dir, "add", "a.txt")
	gitCommand(t, dir, "commit", "-qm", "initial")
	marker := filepath.Join(dir, "executed")
	gitCommand(t, dir, "config", "filter.hostile.clean", "touch "+marker+"; cat")
	gitCommand(t, dir, "config", "filter.hostile.smudge", "touch "+marker+"; cat")
	gitCommand(t, dir, "config", "filter.hostile.required", "true")
	gitCommand(t, dir, "config", "diff.hostile.textconv", "touch "+marker)
	gitCommand(t, dir, "config", "diff.external", "touch "+marker)
	gitCommand(t, dir, "config", "core.fsmonitor", "touch "+marker)
	writeFixture(t, filepath.Join(dir, ".gitattributes"), "*.txt filter=hostile diff=hostile\n")
	writeFixture(t, p, "after\n")
	s := New(Config{})
	requireOK(t, invoke(t, s, "git_status", Arguments{Path: dir}))
	r := invoke(t, s, "git_diff", Arguments{Path: dir})
	requireOK(t, r)
	if !strings.Contains(r.Data.(GitResult).Output, "after") {
		t.Fatal(r)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository commands ran: %v", err)
	}
}

func TestListDirectorySymlinkAndGrepIncomplete(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(dir, "linked-directory")); err != nil {
		t.Fatal(err)
	}
	r := invoke(t, New(Config{}), "list", Arguments{Path: dir})
	requireOK(t, r)
	entry := r.Data.(Listing).Entries[0]
	if !entry.Directory || !entry.Symlink {
		t.Fatal(entry)
	}
	writeFixture(t, filepath.Join(dir, "large"), "123456789")
	r = invoke(t, New(Config{MaxBytes: 2}), "grep", Arguments{Path: dir, Pattern: "x"})
	requireOK(t, r)
	if !r.Truncated {
		t.Fatal("skipped oversized text must mark search incomplete")
	}
}
