package machinetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitSelectedFiles(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-q")
	name := "literal[1]\nfile.txt"
	writeFixture(t, filepath.Join(dir, name), "old\n")
	writeFixture(t, filepath.Join(dir, "other"), "other\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-qm", "initial")
	writeFixture(t, filepath.Join(dir, name), "staged\n")
	gitCommand(t, dir, "add", "--", name)
	writeFixture(t, filepath.Join(dir, name), "working\n")
	writeFixture(t, filepath.Join(dir, "other"), "unrelated\n")
	s := New(Config{})
	for _, tc := range []struct {
		staged       bool
		want, absent string
	}{{false, "+working", "+staged"}, {true, "+staged", "+working"}} {
		r := s.git(context.Background(), Arguments{Path: dir, File: name, Staged: tc.staged}, true)
		requireOK(t, r)
		output := r.Data.(GitResult).Output
		if !strings.Contains(output, tc.want) || strings.Contains(output, tc.absent) || strings.Contains(output, "unrelated") {
			t.Fatal(output)
		}
	}
	writeFixture(t, filepath.Join(dir, "new[2]"), "new content\n")
	r := s.git(context.Background(), Arguments{Path: dir, File: "new[2]"}, true)
	requireOK(t, r)
	if !strings.Contains(r.Data.(GitResult).Output, "+new content") {
		t.Fatal(r)
	}
	r = s.git(context.Background(), Arguments{Path: dir, File: "new[2]", Staged: true}, true)
	requireOK(t, r)
	if r.Data.(GitResult).Output != "" {
		t.Fatal(r)
	}
	r = s.git(context.Background(), Arguments{Path: dir}, false)
	requireOK(t, r)
	g := r.Data.(GitResult)
	if g.Branch == "" || len(g.Files) != 3 {
		t.Fatal(g)
	}
	found := false
	for _, file := range g.Files {
		if file.Path == name {
			found = true
			if file.IndexStatus != "M" || file.WorktreeStatus != "M" {
				t.Fatal(file)
			}
		}
	}
	if !found {
		t.Fatal(g)
	}
	for _, file := range []string{"../outside", "/tmp/outside", ".", "missing"} {
		if r := s.git(context.Background(), Arguments{Path: dir, File: file}, true); r.OK {
			t.Fatal(file, r)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if r := s.git(context.Background(), Arguments{Path: dir, File: "directory"}, true); r.OK {
		t.Fatal(r)
	}
	if err := os.Symlink("not-read-target", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	r = s.git(context.Background(), Arguments{Path: dir, File: "link"}, true)
	requireOK(t, r)
	if !strings.Contains(r.Data.(GitResult).Output, "+not-read-target") {
		t.Fatal(r)
	}
	writeFixture(t, filepath.Join(dir, "large"), strings.Repeat("x", 4096))
	r = New(Config{MaxBytes: 512}).git(context.Background(), Arguments{Path: dir, File: "large"}, true)
	requireOK(t, r)
	if !r.Truncated {
		t.Fatal(r)
	}
	gitCommand(t, dir, "checkout", "--detach", "-q")
	r = s.git(context.Background(), Arguments{Path: dir}, false)
	requireOK(t, r)
	if !r.Data.(GitResult).Detached {
		t.Fatal(r)
	}
}

func TestParseGitFiles(t *testing.T) {
	files := parseGitFiles("R  new\x00old\x00?? newline\nfile\x00 M partial")
	if len(files) != 2 || files[0].OriginalPath != "old" || files[0].Path != "new" || files[1].Path != "newline\nfile" {
		t.Fatal(files)
	}
	if len(parseGitFiles("R  new\x00partial")) != 0 {
		t.Fatal("incomplete rename accepted")
	}
	if len(parseGitFiles("x\x00")) != 0 {
		t.Fatal("malformed record accepted")
	}
}
