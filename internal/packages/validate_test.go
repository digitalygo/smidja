package packages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestValidateTreeValid(t *testing.T) {
	root := t.TempDir()
	m := testManifest("pkg", "v1.0.0", "acme", "pkg", map[string]string{"skills": "skills"}, map[string]string{
		"skills/read.md": "hello world",
	}, nil)
	buildPackageTree(t, root, m, map[string]string{"skills/read.md": "hello world"})
	if err := ValidateTree(root, m); err != nil {
		t.Fatalf("ValidateTree: %v", err)
	}
}

func TestValidateTreeExcludesManifestAndReceipt(t *testing.T) {
	root := t.TempDir()
	m := testManifest("pkg", "v1.0.0", "acme", "pkg", map[string]string{"skills": "skills"}, map[string]string{
		"skills/read.md": "hello world",
	}, nil)
	buildPackageTree(t, root, m, map[string]string{"skills/read.md": "hello world"})
	extra := []string{
		filepath.Join(root, ReceiptFilename),
	}
	for _, p := range extra {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateTree(root, m); err != nil {
		t.Fatalf("ValidateTree with receipt: %v", err)
	}
}

func TestValidateTreeCases(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(root string, m Manifest) (Manifest, error)
		want    string
		prepare func(root string) error
	}{
		{
			name: "extra file",
			prepare: func(root string) error {
				return os.WriteFile(filepath.Join(root, "skills", "extra.md"), []byte("x"), 0o644)
			},
			want: "unexpected file",
		},
		{
			name: "extra file outside contents",
			prepare: func(root string) error {
				return os.WriteFile(filepath.Join(root, "rogue.md"), []byte("x"), 0o644)
			},
			want: "unexpected file",
		},
		{
			name: "missing file",
			mutate: func(root string, m Manifest) (Manifest, error) {
				m.Files = append(m.Files, FileEntry{Path: "skills/zzz.md", SHA256: strings.Repeat("aa", 32), Size: 1})
				return m, nil
			},
			want: "missing file",
		},
		{
			name: "wrong size",
			mutate: func(root string, m Manifest) (Manifest, error) {
				m.Files[0].Size++
				return m, nil
			},
			want: "size mismatch",
		},
		{
			name: "wrong hash",
			mutate: func(root string, m Manifest) (Manifest, error) {
				m.Files[0].SHA256 = strings.Repeat("ff", 32)
				return m, nil
			},
			want: "hash mismatch",
		},
		{
			name: "symlink",
			prepare: func(root string) error {
				return os.Symlink(filepath.Join(root, "skills", "read.md"), filepath.Join(root, "skills", "link.md"))
			},
			want: "symlink",
		},
		{
			name: "symlink dir",
			prepare: func(root string) error {
				return os.Symlink(root, filepath.Join(root, "outside"))
			},
			want: "symlink",
		},
		{
			name: "fifo",
			prepare: func(root string) error {
				return syscall.Mkfifo(filepath.Join(root, "skills", "pipe"), 0o644)
			},
			want: "fifo",
		},
		{
			name: "hardlink",
			prepare: func(root string) error {
				return os.Link(filepath.Join(root, "skills", "read.md"), filepath.Join(root, "skills", "hard.md"))
			},
			want: "hardlink",
		},
		{
			name: "nested receipt not excluded",
			prepare: func(root string) error {
				return os.WriteFile(filepath.Join(root, "skills", ReceiptFilename), []byte("{}"), 0o644)
			},
			want: "unexpected file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"skills/read.md": "hello world", "skills/sub/a.md": "nested"}
			m := testManifest("pkg", "v1.0.0", "acme", "pkg", map[string]string{"skills": "skills"}, files, nil)
			buildPackageTree(t, root, m, files)
			if tc.mutate != nil {
				var err error
				m, err = tc.mutate(root, m)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.prepare != nil {
				if err := tc.prepare(root); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateTree(root, m)
			if err == nil {
				t.Fatalf("ValidateTree succeeded, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ValidateTree error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateTreeHardlinkOutsideTree(t *testing.T) {
	root := t.TempDir()
	m := testManifest("pkg", "v1.0.0", "acme", "pkg", map[string]string{"skills": "skills"}, map[string]string{
		"skills/read.md": "hello world",
	}, nil)
	buildPackageTree(t, root, m, map[string]string{"skills/read.md": "hello world"})
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.Link(filepath.Join(root, "skills", "read.md"), outside); err != nil {
		t.Fatal(err)
	}
	err := ValidateTree(root, m)
	if err == nil || !strings.Contains(err.Error(), "hardlink") {
		t.Errorf("ValidateTree error = %v, want hardlink rejection", err)
	}
}

func TestValidateTreeCaps(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		root := t.TempDir()
		m := testManifest("pkg", "v1.0.0", "acme", "pkg", map[string]string{"skills": "skills"}, map[string]string{
			"skills/read.md": "x",
		}, nil)
		extra := make([]FileEntry, MaxPackageFiles)
		for i := range extra {
			extra[i] = FileEntry{Path: fmt.Sprintf("skills/f%03d", i), SHA256: strings.Repeat("a", 64), Size: 1}
		}
		m.Files = append(m.Files, extra...)
		err := ValidateTree(root, m)
		if err == nil || !strings.Contains(err.Error(), "exceeds cap") {
			t.Errorf("ValidateTree error = %v, want cap error", err)
		}
	})
	t.Run("total size", func(t *testing.T) {
		root := t.TempDir()
		m := testManifest("pkg", "v1.0.0", "acme", "pkg", map[string]string{"skills": "skills"}, map[string]string{
			"skills/read.md": "x",
		}, nil)
		m.Files[0].Size = MaxPackageBytes + 1
		err := ValidateTree(root, m)
		if err == nil || !strings.Contains(err.Error(), "exceeds cap") {
			t.Errorf("ValidateTree error = %v, want cap error", err)
		}
	})
}

func TestValidateTreeMissingRoot(t *testing.T) {
	m := testManifest("pkg", "v1.0.0", "acme", "pkg", map[string]string{"skills": "skills"}, map[string]string{
		"skills/read.md": "x",
	}, nil)
	if err := ValidateTree(filepath.Join(t.TempDir(), "missing"), m); err == nil {
		t.Error("ValidateTree on missing root must fail")
	}
}

func TestVerifyFileHash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	content := "hello"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	want := hashBytes([]byte(content))
	if err := verifyFileHash(path, "f.txt", want); err != nil {
		t.Errorf("verifyFileHash: %v", err)
	}
	if err := verifyFileHash(path, "f.txt", strings.ToUpper(want)); err != nil {
		t.Errorf("verifyFileHash uppercase: %v", err)
	}
	if err := verifyFileHash(path, "f.txt", strings.Repeat("0", 64)); err == nil {
		t.Error("verifyFileHash accepted wrong digest")
	}
}
