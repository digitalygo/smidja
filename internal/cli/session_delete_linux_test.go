//go:build linux

package cli

import (
	"os"
	"testing"

	"github.com/digitalygo/smidja/internal/session"
)

func TestLinuxDeleteLockPersistsAcrossRelease(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	lock, err := acquireDeleteSidecarLock(candidate)
	if err != nil {
		t.Fatalf("acquireDeleteSidecarLock: %v", err)
	}
	if _, err := os.Stat(candidate.lockPath); err != nil {
		t.Fatalf("linux materializes the sidecar: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(candidate.lockPath); err != nil {
		t.Fatalf("linux keeps the sidecar lock file after release: %v", err)
	}
	reopened, err := store.Open(path, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("the released flock must allow a writer: %v", err)
	}
	reopened.Close()
}
