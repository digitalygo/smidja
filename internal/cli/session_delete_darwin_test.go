//go:build darwin

package cli

import (
	"os"
	"testing"
)

func TestDarwinDeleteLockIsCreatedAndAnchoredOnRelease(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	lock, err := acquireDeleteSidecarLock(candidate)
	if err != nil {
		t.Fatalf("acquireDeleteSidecarLock: %v", err)
	}
	if _, err := os.Lstat(candidate.lockPath); err != nil {
		t.Fatalf("darwin must materialize an exclusive sidecar: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Lstat(candidate.lockPath); !os.IsNotExist(err) {
		t.Fatalf("darwin must remove the sidecar this transaction created: %v", err)
	}
}

func TestDarwinDeleteLockLeavesReplacementUntouched(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	lock, err := acquireDeleteSidecarLock(candidate)
	if err != nil {
		t.Fatalf("acquireDeleteSidecarLock: %v", err)
	}
	if err := os.Remove(candidate.lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate.lockPath, []byte("attacker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.release(); err == nil {
		t.Fatal("release must surface a replaced sidecar")
	}
	data, err := os.ReadFile(candidate.lockPath)
	if err != nil {
		t.Fatalf("the replacement sidecar must stay untouched: %v", err)
	}
	if string(data) != "attacker" {
		t.Fatalf("the replacement sidecar was mutated: %q", data)
	}
}
