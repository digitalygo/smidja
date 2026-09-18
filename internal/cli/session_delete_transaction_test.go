package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/session"
)

func newDeleteTarget(t *testing.T) (*session.Store, string, string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sess, "delete target")
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	return store, cwd, path
}

func validatedDeleteCandidate(t *testing.T, store *session.Store, cwd, path string) *deleteCandidate {
	t.Helper()
	candidate, err := validateDeleteCandidate(store, cwd, path, "")
	if err != nil {
		t.Fatalf("validateDeleteCandidate: %v", err)
	}
	t.Cleanup(candidate.Close)
	return candidate
}

type deleteHookSlot struct {
	name  string
	apply func(func(*deleteCandidate))
	clear func()
}

func deleteHookSlots() []deleteHookSlot {
	return []deleteHookSlot{
		{"during-confirm", func(f func(*deleteCandidate)) { deleteHookDuringConfirm = f }, func() { deleteHookDuringConfirm = nil }},
		{"before-open", func(f func(*deleteCandidate)) { deleteHookBeforeOpen = f }, func() { deleteHookBeforeOpen = nil }},
		{"after-lock", func(f func(*deleteCandidate)) { deleteHookAfterLock = f }, func() { deleteHookAfterLock = nil }},
		{"before-unlink", func(f func(*deleteCandidate)) { deleteHookBeforeUnlink = f }, func() { deleteHookBeforeUnlink = nil }},
	}
}

func TestDeleteRejectsTargetReplacementAtEveryHook(t *testing.T) {
	for _, slot := range deleteHookSlots() {
		t.Run(slot.name, func(t *testing.T) {
			store, cwd, path := newDeleteTarget(t)
			slot.apply(func(c *deleteCandidate) {
				replacement := filepath.Join(c.parent, "replacement-target")
				if err := os.WriteFile(replacement, []byte("attacker"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, c.path); err != nil {
					t.Fatal(err)
				}
			})
			defer slot.clear()
			candidate := validatedDeleteCandidate(t, store, cwd, path)
			if err := deleteSessionFile(store, candidate); err == nil {
				t.Fatal("a replaced target must abort the delete transaction")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("replacement target must stay untouched: %v", err)
			}
			if string(data) != "attacker" {
				t.Fatalf("attacker target was mutated: %q", data)
			}
		})
	}
}

func TestDeleteRejectsParentReplacementAtEveryHook(t *testing.T) {
	for _, slot := range deleteHookSlots() {
		t.Run(slot.name, func(t *testing.T) {
			store, cwd, path := newDeleteTarget(t)
			moved := ""
			slot.apply(func(c *deleteCandidate) {
				moved = c.parent + ".moved"
				if err := os.Rename(c.parent, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(c.parent, 0o700); err != nil {
					t.Fatal(err)
				}
			})
			defer slot.clear()
			candidate := validatedDeleteCandidate(t, store, cwd, path)
			if err := deleteSessionFile(store, candidate); err == nil {
				t.Fatal("a replaced parent directory must abort the delete transaction")
			}
			if _, err := os.Stat(filepath.Join(moved, filepath.Base(path))); err != nil {
				t.Fatalf("the moved target must stay untouched: %v", err)
			}
		})
	}
}

func TestDeleteRejectsLockReplacementAtEveryHook(t *testing.T) {
	for _, slot := range deleteHookSlots() {
		t.Run(slot.name, func(t *testing.T) {
			store, cwd, path := newDeleteTarget(t)
			lockPath := path + ".lock"
			slot.apply(func(c *deleteCandidate) {
				if err := os.Remove(c.lockPath); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if err := os.WriteFile(c.lockPath, []byte("attacker-lock"), 0o600); err != nil {
					t.Fatal(err)
				}
			})
			defer slot.clear()
			candidate := validatedDeleteCandidate(t, store, cwd, path)
			if err := deleteSessionFile(store, candidate); err == nil {
				t.Fatal("a replaced sidecar lock must abort the delete transaction")
			}
			data, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("the replacement lock must stay untouched: %v", err)
			}
			if string(data) != "attacker-lock" {
				t.Fatalf("the replacement lock was mutated: %q", data)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the target must survive a lock replacement: %v", err)
			}
		})
	}
}

func TestDeleteSidecarLockExcludesCompetingWriter(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	lock, err := acquireDeleteSidecarLock(candidate)
	if err != nil {
		t.Fatalf("acquireDeleteSidecarLock: %v", err)
	}
	if _, err := store.Open(path, session.OpenOptions{Strict: true}); err == nil {
		t.Fatal("a competing writer acquired the session while the delete lock was held")
	}
	if err := revalidateDeleteLockHeld(candidate, lock); err != nil {
		t.Fatalf("revalidate held lock: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	reopened, err := store.Open(path, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("a released delete lock must not keep the session locked: %v", err)
	}
	reopened.Close()
	_, statErr := os.Stat(path + ".lock")
	switch runtime.GOOS {
	case "linux":
		if statErr != nil {
			t.Fatalf("linux keeps the sidecar lock file after release: %v", statErr)
		}
	case "darwin":
		if statErr == nil {
			t.Fatal("darwin removes the sidecar lock it created")
		}
	}
}

func TestDeleteSidecarLockRespectsExistingSidecar(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	if err := os.WriteFile(path+".lock", []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	lock, err := acquireDeleteSidecarLock(candidate)
	if runtime.GOOS == "darwin" {
		if err == nil {
			_ = lock.release()
			t.Fatal("darwin must fail closed when a sidecar already exists")
		}
		if _, statErr := os.Stat(path + ".lock"); statErr != nil {
			t.Fatalf("the existing sidecar must stay untouched: %v", statErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("linux acquires an unlocked existing sidecar: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("linux must keep the pre-existing sidecar: %v", err)
	}
}

func TestDeleteSidecarLockRejectsSymlinkAndNonRegular(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.lock"), candidate.lockPath); err != nil {
		t.Fatal(err)
	}
	_, err := acquireDeleteSidecarLock(candidate)
	if runtime.GOOS == "linux" {
		if err != errDeleteSymlink {
			t.Fatalf("symlinked sidecar = %v, want errDeleteSymlink", err)
		}
	} else if err == nil {
		t.Fatal("a symlinked sidecar must fail closed")
	}
	if err := os.Remove(candidate.lockPath); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(candidate.lockPath, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	_, err = acquireDeleteSidecarLock(candidate)
	if runtime.GOOS == "linux" {
		if err != errDeleteSymlink {
			t.Fatalf("fifo sidecar = %v, want errDeleteSymlink", err)
		}
	} else if err == nil {
		t.Fatal("a fifo sidecar must fail closed")
	}
	if err := os.Remove(candidate.lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(candidate.lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireDeleteSidecarLock(candidate); err == nil {
		t.Fatal("a directory as sidecar must fail the delete lock")
	}
}

func TestDeleteSessionFileRejectsIncompleteCandidates(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	if err := deleteSessionFile(nil, nil); err == nil {
		t.Error("deleteSessionFile without a candidate must fail")
	}
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	if err := deleteSessionFile(nil, candidate); err == nil {
		t.Error("deleteSessionFile without a store must fail")
	}
	handles := validatedDeleteCandidate(t, store, cwd, path)
	handles.parentFile = nil
	if err := deleteSessionFile(store, handles); err == nil {
		t.Error("deleteSessionFile without the parent handle must fail")
	}
	incomplete := validatedDeleteCandidate(t, store, cwd, path)
	incomplete.path = ""
	if err := deleteSessionFile(store, incomplete); err == nil {
		t.Error("deleteSessionFile with an empty path must fail")
	}
	lockSuffix := validatedDeleteCandidate(t, store, cwd, path)
	lockSuffix.path = path + ".lock"
	if err := deleteSessionFile(store, lockSuffix); err != errDeleteSymlink {
		t.Errorf("deleteSessionFile with a lock path = %v, want errDeleteSymlink", err)
	}
}

func TestDeleteSessionFileRejectsHeldLock(t *testing.T) {
	store, cwd, path := newDeleteTarget(t)
	holder, err := store.Open(path, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	if err := deleteSessionFile(store, candidate); err == nil {
		t.Fatal("deleting a session whose lock is held must fail")
	}
}

func TestDeleteSessionFileSurfacesUnlinkFailure(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("unlink permission failures need a non-root linux host")
	}
	store, cwd, path := newDeleteTarget(t)
	if err := os.WriteFile(path+".lock", []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := validatedDeleteCandidate(t, store, cwd, path)
	deleteHookBeforeUnlink = func(c *deleteCandidate) {
		if err := os.Chmod(c.parent, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		deleteHookBeforeUnlink = nil
		_ = os.Chmod(candidate.parent, 0o700)
	}()
	if err := deleteSessionFile(store, candidate); err == nil {
		t.Fatal("an unlink failure must surface")
	}
}

const deleteLeakProbeIterations = 8

func TestValidateDeleteCandidateStageFailuresReleaseDescriptors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor accounting uses /proc/self/fd")
	}
	store, cwd, path := newDeleteTarget(t)
	sessionDir, err := store.DirForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	baseline := countOpenDescriptorsUnder(t, sessionDir)
	previousGCPercent := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGCPercent)
	stages := []string{
		deleteValidationParentHeld,
		deleteValidationRootOpen,
		deleteValidationTargetOpen,
		deleteValidationTargetHeld,
		deleteValidationLock,
		deleteValidationLoad,
		deleteValidationCwd,
		deleteValidationPostLoad,
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			deleteValidationStageHook = func(string, *deleteCandidate) error {
				return fmt.Errorf("session: injected validation failure at %s", stage)
			}
			defer func() { deleteValidationStageHook = nil }()
			for i := 0; i < deleteLeakProbeIterations; i++ {
				if _, err := validateDeleteCandidate(store, cwd, path, ""); err == nil {
					t.Fatalf("injected failure at %s must abort validation", stage)
				}
			}
			if got := countOpenDescriptorsUnder(t, sessionDir); got != baseline {
				t.Fatalf("%d failures at %s leaked transaction descriptors: %d open, want %d", deleteLeakProbeIterations, stage, got, baseline)
			}
		})
	}
}

func TestValidateDeleteCandidateBranchFailuresReleaseDescriptors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor accounting uses /proc/self/fd")
	}
	scenarios := []struct {
		name    string
		attempt func(t *testing.T, store *session.Store, cwd, path string) error
	}{
		{"active-session", func(t *testing.T, store *session.Store, cwd, path string) error {
			_, err := validateDeleteCandidate(store, cwd, path, path)
			return err
		}},
		{"missing-target", func(t *testing.T, store *session.Store, cwd, path string) error {
			_, err := validateDeleteCandidate(store, cwd, path+".missing", "")
			return err
		}},
		{"symlinked-target", func(t *testing.T, store *session.Store, cwd, path string) error {
			sessionDir, err := store.DirForCwd(cwd)
			if err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(sessionDir, "leak-probe-link.jsonl")
			if _, statErr := os.Lstat(link); statErr != nil {
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
			}
			_, err = validateDeleteCandidate(store, cwd, link, "")
			return err
		}},
		{"unreadable-target", func(t *testing.T, store *session.Store, cwd, path string) error {
			if os.Geteuid() == 0 {
				t.Skip("root opens unreadable files")
			}
			if err := os.Chmod(path, 0o000); err != nil {
				t.Fatal(err)
			}
			_, err := validateDeleteCandidate(store, cwd, path, "")
			return err
		}},
		{"garbage-content", func(t *testing.T, store *session.Store, cwd, path string) error {
			if err := os.WriteFile(path, []byte("this is not a session\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := validateDeleteCandidate(store, cwd, path, "")
			return err
		}},
		{"foreign-header", func(t *testing.T, store *session.Store, cwd, path string) error {
			line := `{"type":"session","version":3,"id":"01a0acc5-0487-793d-8a82-f3a26b3f089f","timestamp":"2026-01-01T00:00:00.000Z","cwd":"` + t.TempDir() + `"}` + "\n"
			if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := validateDeleteCandidate(store, cwd, path, "")
			return err
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			store, cwd, path := newDeleteTarget(t)
			sessionDir, err := store.DirForCwd(cwd)
			if err != nil {
				t.Fatal(err)
			}
			baseline := countOpenDescriptorsUnder(t, sessionDir)
			previousGCPercent := debug.SetGCPercent(-1)
			defer debug.SetGCPercent(previousGCPercent)
			for i := 0; i < deleteLeakProbeIterations; i++ {
				if err := scenario.attempt(t, store, cwd, path); err == nil {
					t.Fatalf("%s must fail validation", scenario.name)
				}
			}
			if got := countOpenDescriptorsUnder(t, sessionDir); got != baseline {
				t.Fatalf("%d repeated %s failures leaked transaction descriptors: %d open, want %d", deleteLeakProbeIterations, scenario.name, got, baseline)
			}
		})
	}
}

func TestValidateDeleteCandidateSuccessTransfersOwnership(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor accounting uses /proc/self/fd")
	}
	store, cwd, path := newDeleteTarget(t)
	sessionDir, err := store.DirForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	baseline := countOpenDescriptorsUnder(t, sessionDir)
	candidate, err := validateDeleteCandidate(store, cwd, path, "")
	if err != nil {
		t.Fatal(err)
	}
	if held := countOpenDescriptorsUnder(t, sessionDir); held != baseline+3 {
		t.Fatalf("a validated candidate must hold the parent, root, and target descriptors: %d open, want %d", held, baseline+3)
	}
	if err := candidate.parentFile.Close(); err != nil {
		t.Fatalf("the caller must own the validated candidate handles: %v", err)
	}
	candidate.Close()
	if got := countOpenDescriptorsUnder(t, sessionDir); got != baseline {
		t.Fatalf("closing the candidate must release every transaction descriptor: %d open, want %d", got, baseline)
	}
}

func TestDeleteHookFailuresReleaseTransactionDescriptors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor accounting uses /proc/self/fd")
	}
	for _, slot := range deleteHookSlots() {
		t.Run(slot.name, func(t *testing.T) {
			store, cwd, path := newDeleteTarget(t)
			sessionDir, err := store.DirForCwd(cwd)
			if err != nil {
				t.Fatal(err)
			}
			baseline := countOpenDescriptorsUnder(t, sessionDir)
			previousGCPercent := debug.SetGCPercent(-1)
			defer debug.SetGCPercent(previousGCPercent)
			sessionContent, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			slot.apply(func(c *deleteCandidate) {
				replacement := filepath.Join(c.parent, "replacement-target")
				if err := os.WriteFile(replacement, sessionContent, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, c.path); err != nil {
					t.Fatal(err)
				}
			})
			defer slot.clear()
			for i := 0; i < deleteLeakProbeIterations; i++ {
				candidate := validatedDeleteCandidate(t, store, cwd, path)
				if err := deleteSessionFile(store, candidate); err == nil {
					t.Fatal("a replaced target must abort the delete transaction")
				}
			}
			if got := countOpenDescriptorsUnder(t, sessionDir); got != baseline {
				t.Fatalf("%d hook failures at %s leaked transaction descriptors: %d open, want %d", deleteLeakProbeIterations, slot.name, got, baseline)
			}
		})
	}
}

func countOpenDescriptorsUnder(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("/proc/self/fd unavailable: %v", err)
	}
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			continue
		}
		if target == dir || strings.HasPrefix(target, dir+string(os.PathSeparator)) {
			count++
		}
	}
	return count
}

func TestBridgeDeleteReleasesHandlesOnCancelAndDialogFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor accounting uses /proc/self/fd")
	}
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "handle leak")
	sessionDir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	baseline := countOpenDescriptorsUnder(t, sessionDir)

	done := make(chan error, 1)
	go func() { done <- fixture.bridge.deleteSessionInteractive(target) }()
	waitForOutputSettled(t, fixture.terminal, "Delete session", 3*time.Second)
	fixture.terminal.SendInput("n")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled delete = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete dialog did not close")
	}
	if got := countOpenDescriptorsUnder(t, sessionDir); got != baseline {
		t.Fatalf("cancelled delete leaked transaction handles: %d, want %d", got, baseline)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("cancelled delete must keep the session: %v", err)
	}

	fixture.runner.Stop()
	if err := fixture.bridge.deleteSessionInteractive(target); err == nil {
		t.Fatal("a dialog failure must surface")
	}
	if got := countOpenDescriptorsUnder(t, sessionDir); got != baseline {
		t.Fatalf("failed delete dialog leaked transaction handles: %d, want %d", got, baseline)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("failed delete dialog must keep the session: %v", err)
	}
}
