package managerpackages

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedIncludesVerifiedHistoricalRevisions(t *testing.T) {
	s := packageStore(t)
	if got, err := s.Retained(); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	first := installFixture(t, s, "1.0.0")
	installFixture(t, s, "1.1.0")
	got, err := s.Retained()
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if got[0].Version != "1.0.0" || got[1].Version != "1.1.0" {
		t.Fatal(got)
	}
	path := filepath.Join(s.revisionPath(first.ID, first.Version, first.Digest), "payload", "bin", "tool.sh")
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = s.Retained()
	if err == nil || len(got) != 1 || got[0].Version != "1.1.0" {
		t.Fatal("corrupted old revision was accepted or healthy revision lost", got, err)
	}
}
func TestRetainedRejectsUnsafeOrInvalidStorage(t *testing.T) {
	for _, mode := range []string{"root-file", "root-symlink", "package-file", "invalid-version", "missing-receipt", "bad-receipt", "wrong-identity", "symlink-payload"} {
		t.Run(mode, func(t *testing.T) {
			s := packageStore(t)
			root := filepath.Join(s.cfg.Dir, "revisions")
			if mode == "root-file" {
				if err := os.WriteFile(root, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if mode == "root-symlink" {
				if err := os.Symlink(t.TempDir(), root); err != nil {
					t.Fatal(err)
				}
			} else {
				revision := installFixture(t, s, "1.0.0")
				path := s.revisionPath(revision.ID, revision.Version, revision.Digest)
				if err := os.Chmod(path, 0700); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "package-file":
					if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("x"), 0600); err != nil {
						t.Fatal(err)
					}
				case "invalid-version":
					if err := os.Mkdir(filepath.Join(root, revision.ID, "invalid"), 0700); err != nil {
						t.Fatal(err)
					}
				case "missing-receipt":
					if err := os.Remove(filepath.Join(path, "receipt.json")); err != nil {
						t.Fatal(err)
					}
				case "bad-receipt", "wrong-identity":
					receipt := filepath.Join(path, "receipt.json")
					if err := os.Chmod(receipt, 0600); err != nil {
						t.Fatal(err)
					}
					raw := []byte("{")
					if mode == "wrong-identity" {
						revision.Version = "9.0.0"
						raw, _ = json.Marshal(revision)
					}
					if err := os.WriteFile(receipt, raw, 0600); err != nil {
						t.Fatal(err)
					}
				case "symlink-payload":
					if err := os.Symlink(t.TempDir(), filepath.Join(path, "linked")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.Retained(); err == nil {
				t.Fatal("unsafe retained storage accepted")
			}
		})
	}
}
