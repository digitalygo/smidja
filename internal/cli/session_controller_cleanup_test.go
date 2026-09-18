package cli

import (
	"os"
	"testing"

	"github.com/digitalygo/smidja/internal/session"
)

func TestCreatedCandidateAbortRemovesWhileLockIsHeld(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	controller := testController(t, store, cwd)
	defer controller.Close()
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	path := candidate.path

	original := removeCreatedCandidateFile
	var competitorErr error
	var seamRan bool
	removeCreatedCandidateFile = func(p string) error {
		seamRan = true
		holder, err := store.Open(p, session.OpenOptions{Strict: true})
		competitorErr = err
		if holder != nil {
			holder.Close()
		}
		return os.Remove(p)
	}
	err = controller.Abort(candidate)
	removeCreatedCandidateFile = original
	if err != nil {
		t.Fatalf("Abort = %v", err)
	}
	if !seamRan {
		t.Fatal("the removal path did not run")
	}
	if competitorErr == nil {
		t.Fatal("a competing writer acquired the created candidate while it was being removed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("aborted candidate still exists: %v", err)
	}
}

func TestCreatedCandidateReopenFailureKeepsMaterializedFile(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	controller := testController(t, store, cwd)
	defer controller.Close()

	var held *session.Session
	createdPath := ""
	createHookBeforeReopen = func(path string) {
		createdPath = path
		holder, err := store.Open(path, session.OpenOptions{Strict: true})
		if err == nil {
			held = holder
		}
	}
	defer func() {
		createHookBeforeReopen = nil
		if held != nil {
			held.Close()
			held = nil
		}
	}()

	if _, err := controller.PrepareNew(); err == nil {
		t.Fatal("PrepareNew must fail when ownership is not acquired")
	}
	if createdPath == "" {
		t.Fatal("the reopen hook did not observe the materialized file")
	}
	if _, err := os.Stat(createdPath); err != nil {
		t.Fatalf("a reopen failure must not remove the materialized file: %v", err)
	}
	if _, err := session.LoadWithOptions(createdPath, session.LoadOptions{Strict: true}); err != nil {
		t.Fatalf("the materialized file must stay a valid session: %v", err)
	}
	if held != nil {
		held.Close()
		held = nil
	}
	reopened, err := store.Open(createdPath, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("the materialized file must stay openable: %v", err)
	}
	reopened.Close()
}

func TestDisposeCandidateOriginBranches(t *testing.T) {
	if err := disposeCandidate(nil, candidateCreated, nil); err != nil {
		t.Fatalf("disposeCandidate(nil) = %v", err)
	}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	opened, err := createLockedSession(store, cwd)
	if err != nil {
		t.Fatal(err)
	}
	openedPath := opened.Path()
	info, err := os.Lstat(openedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := disposeCandidate(opened, candidateOpened, info); err != nil {
		t.Fatalf("disposeCandidate(opened) = %v", err)
	}
	if _, err := os.Stat(openedPath); err != nil {
		t.Fatalf("an opened candidate must not be removed: %v", err)
	}

	caller, err := createLockedSession(store, cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { caller.Close() })
	if err := disposeCandidate(caller, candidateCallerOwned, info); err != nil {
		t.Fatalf("disposeCandidate(caller-owned) = %v", err)
	}
	seedTurn(t, caller, "caller-owned session stays usable")
}

func TestCreatedCandidateAbortSurfacesRemovalFailureAndLeavesFile(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	controller := testController(t, store, cwd)
	defer controller.Close()
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	original := removeCreatedCandidateFile
	removeCreatedCandidateFile = func(string) error { return os.ErrPermission }
	err = controller.Abort(candidate)
	removeCreatedCandidateFile = original
	if err == nil {
		t.Fatal("Abort must surface the removal failure")
	}
	if _, statErr := os.Stat(candidate.path); statErr != nil {
		t.Fatalf("a failed removal must leave the file for inspection: %v", statErr)
	}
	if err := os.Remove(candidate.path); err != nil {
		t.Fatal(err)
	}
}
