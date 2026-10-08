package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/digitalygo/smidja/internal/session"
)

func lockedBranchCandidate(t *testing.T) (*session.Store, *deleteCandidate, string) {
	t.Helper()
	store, cwd, path := newDeleteTarget(t)
	if err := os.WriteFile(path+".lock", []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	return store, validatedDeleteCandidate(t, store, cwd, path), path
}

func TestRevalidateDeleteParentBranches(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := revalidateDeleteParent(candidate); err != nil {
			t.Fatalf("revalidateDeleteParent = %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.RemoveAll(candidate.parent); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteParent(candidate); err == nil {
			t.Fatal("a missing parent must fail")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.RemoveAll(candidate.parent); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), candidate.parent); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteParent(candidate); err != errDeleteSymlink {
			t.Fatalf("symlinked parent = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("not-directory", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.RemoveAll(candidate.parent); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(candidate.parent, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteParent(candidate); err != errDeleteForeign {
			t.Fatalf("non-directory parent = %v, want errDeleteForeign", err)
		}
	})
	t.Run("identity", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Rename(candidate.parent, candidate.parent+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(candidate.parent, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteParent(candidate); err == nil {
			t.Fatal("a swapped parent must fail")
		}
	})
	t.Run("held-handle-mismatch", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		other, err := os.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_ = candidate.parentFile.Close()
		candidate.parentFile = other
		if err := revalidateDeleteParent(candidate); err == nil {
			t.Fatal("a mismatched held parent must fail")
		}
	})
	t.Run("parent-file-closed", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		_ = candidate.parentFile.Close()
		if err := revalidateDeleteParent(candidate); err == nil {
			t.Fatal("a closed parent handle must fail")
		}
	})
	t.Run("root-closed", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		_ = candidate.parentRoot.Close()
		if err := revalidateDeleteParent(candidate); err == nil {
			t.Fatal("a closed parent root must fail")
		}
	})
	t.Run("root-mismatch", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		other, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_ = candidate.parentRoot.Close()
		candidate.parentRoot = other
		if err := revalidateDeleteParent(candidate); err == nil {
			t.Fatal("a mismatched parent root must fail")
		}
	})
	t.Run("canonical-mismatch", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		candidate.canonicalDir = filepath.Join(t.TempDir(), "elsewhere")
		if err := revalidateDeleteParent(candidate); err != errDeleteForeign {
			t.Fatalf("canonical mismatch = %v, want errDeleteForeign", err)
		}
	})
}

func TestRevalidateDeleteTargetBranches(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := revalidateDeleteTarget(candidate); err != nil {
			t.Fatalf("revalidateDeleteTarget = %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Remove(candidate.path); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteTarget(candidate); err == nil {
			t.Fatal("a missing target must fail")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Remove(candidate.path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(candidate.parent, "elsewhere"), candidate.path); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteTarget(candidate); err != errDeleteSymlink {
			t.Fatalf("symlinked target = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("not-regular", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Remove(candidate.path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(candidate.path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteTarget(candidate); err != errDeleteSymlink {
			t.Fatalf("directory target = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("identity", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Remove(candidate.path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(candidate.path, []byte("swapped"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteTarget(candidate); err == nil {
			t.Fatal("a swapped target must fail")
		}
	})
	t.Run("root-closed", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		_ = candidate.parentRoot.Close()
		if err := revalidateDeleteTarget(candidate); err == nil {
			t.Fatal("a closed parent root must fail")
		}
	})
	t.Run("root-missing-name", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		other, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_ = candidate.parentRoot.Close()
		candidate.parentRoot = other
		if err := revalidateDeleteTarget(candidate); err == nil {
			t.Fatal("a root without the target must fail")
		}
	})
	t.Run("rel-symlink", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Symlink(candidate.path, filepath.Join(candidate.parent, "link.jsonl")); err != nil {
			t.Fatal(err)
		}
		candidate.name = "link.jsonl"
		if err := revalidateDeleteTarget(candidate); err != errDeleteSymlink {
			t.Fatalf("relative symlink = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("rel-identity", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.WriteFile(filepath.Join(candidate.parent, "other.jsonl"), []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
		candidate.name = "other.jsonl"
		if err := revalidateDeleteTarget(candidate); err == nil {
			t.Fatal("a different relative target must fail")
		}
	})
	t.Run("held-closed", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		_ = candidate.targetHeld.Close()
		if err := revalidateDeleteTarget(candidate); err == nil {
			t.Fatal("a closed target handle must fail")
		}
	})
	t.Run("held-mismatch", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		other, err := os.Open(candidate.path + ".lock")
		if err != nil {
			if err := os.WriteFile(candidate.path+".lock", []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			other, err = os.Open(candidate.path + ".lock")
			if err != nil {
				t.Fatal(err)
			}
		}
		_ = candidate.targetHeld.Close()
		candidate.targetHeld = other
		if err := revalidateDeleteTarget(candidate); err == nil {
			t.Fatal("a mismatched target handle must fail")
		}
	})
}

func TestRevalidateDeleteLockBeforeAcquireBranches(t *testing.T) {
	t.Run("absent-ok", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := revalidateDeleteLockBeforeAcquire(candidate); err != nil {
			t.Fatalf("absent lock = %v", err)
		}
	})
	t.Run("appeared-regular", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.WriteFile(candidate.lockPath, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("an appeared lock must fail")
		}
	})
	t.Run("appeared-symlink", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Symlink(candidate.path, candidate.lockPath); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockBeforeAcquire(candidate); err != errDeleteSymlink {
			t.Fatalf("symlinked lock = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("appeared-non-regular", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := os.Mkdir(candidate.lockPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockBeforeAcquire(candidate); err != errDeleteSymlink {
			t.Fatalf("directory lock = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("lstat-error", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		candidate.lockPath = filepath.Join(candidate.path, "nested.lock")
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("an uninspectable lock path must fail")
		}
	})
	t.Run("root-appeared", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		candidate.lockPath = filepath.Join(candidate.parent, "absent.lock")
		if err := os.WriteFile(filepath.Join(candidate.parent, candidate.name+".lock"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("a lock visible through the parent root must fail")
		}
	})
	t.Run("root-error", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		candidate.lockPath = filepath.Join(candidate.parent, "absent.lock")
		_ = candidate.parentRoot.Close()
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("a closed parent root must fail")
		}
	})
	t.Run("existing-ok", func(t *testing.T) {
		_, candidate, _ := lockedBranchCandidate(t)
		if err := revalidateDeleteLockBeforeAcquire(candidate); err != nil {
			t.Fatalf("existing lock = %v", err)
		}
	})
	t.Run("existing-missing", func(t *testing.T) {
		_, candidate, path := lockedBranchCandidate(t)
		if err := os.Remove(path + ".lock"); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("a removed lock must fail")
		}
	})
	t.Run("existing-symlink", func(t *testing.T) {
		_, candidate, path := lockedBranchCandidate(t)
		if err := os.Remove(path + ".lock"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(candidate.path, path+".lock"); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockBeforeAcquire(candidate); err != errDeleteSymlink {
			t.Fatalf("symlinked existing lock = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("existing-identity", func(t *testing.T) {
		_, candidate, path := lockedBranchCandidate(t)
		if err := os.Remove(path + ".lock"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".lock", []byte("swapped"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("a swapped lock must fail")
		}
	})
	t.Run("existing-root-closed", func(t *testing.T) {
		_, candidate, _ := lockedBranchCandidate(t)
		_ = candidate.parentRoot.Close()
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("a closed parent root must fail")
		}
	})
	t.Run("existing-rel-symlink", func(t *testing.T) {
		_, candidate, _ := lockedBranchCandidate(t)
		if err := os.Symlink(candidate.path+".lock", filepath.Join(candidate.parent, "link.jsonl.lock")); err != nil {
			t.Fatal(err)
		}
		candidate.name = "link.jsonl"
		if err := revalidateDeleteLockBeforeAcquire(candidate); err != errDeleteSymlink {
			t.Fatalf("relative symlinked lock = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("existing-rel-identity", func(t *testing.T) {
		_, candidate, _ := lockedBranchCandidate(t)
		if err := os.WriteFile(filepath.Join(candidate.parent, "other.jsonl.lock"), []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
		candidate.name = "other.jsonl"
		if err := revalidateDeleteLockBeforeAcquire(candidate); err == nil {
			t.Fatal("a different relative lock must fail")
		}
	})
}

func TestRevalidateDeleteLockHeldBranches(t *testing.T) {
	t.Run("nil-lock", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		if err := revalidateDeleteLockHeld(candidate, nil); err == nil {
			t.Fatal("a nil lock must fail")
		}
	})
	t.Run("ok", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		if err := revalidateDeleteLockHeld(candidate, lock); err != nil {
			t.Fatalf("held lock = %v", err)
		}
	})
	t.Run("identity-error", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		file := lock.file
		lock.file = nil
		_ = file.Close()
		if err := revalidateDeleteLockHeld(candidate, lock); err == nil {
			t.Fatal("a released lock handle must fail")
		}
	})
	t.Run("lock-missing", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		if err := os.Remove(candidate.lockPath); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockHeld(candidate, lock); err == nil {
			t.Fatal("a removed held lock must fail")
		}
	})
	t.Run("lock-symlink", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		if err := os.Remove(candidate.lockPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(candidate.path, candidate.lockPath); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockHeld(candidate, lock); err != errDeleteSymlink {
			t.Fatalf("symlinked held lock = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("lock-identity", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		if err := os.Remove(candidate.lockPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(candidate.lockPath, []byte("swapped"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateDeleteLockHeld(candidate, lock); err == nil {
			t.Fatal("a swapped held lock must fail")
		}
	})
	t.Run("lockinfo-mismatch", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		other := filepath.Join(t.TempDir(), "other.lock")
		if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(other)
		if err != nil {
			t.Fatal(err)
		}
		candidate.lockInfo = info
		if err := revalidateDeleteLockHeld(candidate, lock); err == nil {
			t.Fatal("a mismatched recorded lock must fail")
		}
	})
	t.Run("root-closed", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		_ = candidate.parentRoot.Close()
		if err := revalidateDeleteLockHeld(candidate, lock); err == nil {
			t.Fatal("a closed parent root must fail")
		}
	})
	t.Run("rel-symlink", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		if err := os.Symlink(candidate.lockPath, filepath.Join(candidate.parent, "link.jsonl.lock")); err != nil {
			t.Fatal(err)
		}
		candidate.name = "link.jsonl"
		if err := revalidateDeleteLockHeld(candidate, lock); err != errDeleteSymlink {
			t.Fatalf("relative symlinked held lock = %v, want errDeleteSymlink", err)
		}
	})
	t.Run("rel-identity", func(t *testing.T) {
		store, cwd, path := newDeleteTarget(t)
		candidate := validatedDeleteCandidate(t, store, cwd, path)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		if err := os.WriteFile(filepath.Join(candidate.parent, "other.jsonl.lock"), []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
		candidate.name = "other.jsonl"
		if err := revalidateDeleteLockHeld(candidate, lock); err == nil {
			t.Fatal("a different relative held lock must fail")
		}
	})
	t.Run("lockinfo-match", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("darwin fails closed on a pre-existing sidecar")
		}
		_, candidate, _ := lockedBranchCandidate(t)
		lock, err := acquireDeleteSidecarLock(candidate)
		if err != nil {
			t.Fatalf("acquire existing sidecar: %v", err)
		}
		defer lock.release()
		if err := revalidateDeleteLockHeld(candidate, lock); err != nil {
			t.Fatalf("held existing lock = %v", err)
		}
	})
}

func TestAnchoredUnlinkRequiresHandles(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	broken := *candidate
	broken.parentFile = nil
	if err := anchoredUnlink(&broken); err == nil {
		t.Fatal("anchoredUnlink without a parent handle must fail")
	}
}

func TestDeleteSidecarLockGuardBranches(t *testing.T) {
	if _, err := acquireDeleteSidecarLock(nil); err == nil {
		t.Fatal("acquireDeleteSidecarLock(nil) must fail")
	}
	var lock *deleteSidecarLock
	if err := lock.release(); err != nil {
		t.Fatalf("nil release = %v", err)
	}
	if _, err := lock.identity(); err == nil {
		t.Fatal("nil lock identity must fail")
	}
	empty := &deleteSidecarLock{}
	if err := empty.release(); err != nil {
		t.Fatalf("empty release = %v", err)
	}
	if _, err := empty.identity(); err == nil {
		t.Fatal("empty lock identity must fail")
	}
	store, cwd, path := newDeleteTarget(t)
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	nameless := *candidate
	nameless.name = ""
	if _, err := acquireDeleteSidecarLock(&nameless); err == nil {
		t.Fatal("acquire without a lock name must fail")
	}
}

func TestValidateDeleteCandidateBoundaryBranches(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	sessionDir, err := store.DirForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, "", path, ""); err == nil {
		t.Error("an empty cwd must be rejected")
	}
	linkDir := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(sessionDir, linkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, cwd, filepath.Join(linkDir, filepath.Base(path)), ""); err != errDeleteSymlink {
		t.Fatalf("symlinked parent = %v, want errDeleteSymlink", err)
	}
	plainParent := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plainParent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, cwd, filepath.Join(plainParent, "child.jsonl"), ""); err != errDeleteForeign {
		t.Fatalf("non-directory parent = %v, want errDeleteForeign", err)
	}
	if _, err := validateDeleteCandidate(store, cwd, filepath.Join(sessionDir, "missing.jsonl"), ""); err == nil {
		t.Error("a missing target must be rejected")
	}
}
