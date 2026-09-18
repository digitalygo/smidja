package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui"
)

const (
	blocker9DupID = "01a0acc5-0487-793d-8a82-f3a26b3f089f"
)

func blocker9JSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %q: %v", value, err)
	}
	return string(encoded)
}

func blocker9SessionBody(t *testing.T, id, cwd, text string) []byte {
	t.Helper()
	header := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + blocker9JSON(t, cwd) + `}`
	message := `{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":` + blocker9JSON(t, text) + `}}`
	return []byte(header + "\n" + message + "\n")
}

func blocker9WriteFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func blocker9ReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func blocker9SessionID(t *testing.T, path string) string {
	t.Helper()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return loader.Header().ID
}

func blocker9CandidatePaths(t *testing.T, store *session.Store, cwd string) []string {
	t.Helper()
	candidates, err := validatedSessionCandidates(store, cwd)
	if err != nil {
		t.Fatalf("validatedSessionCandidates: %v", err)
	}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}
	return paths
}

func blocker9AssertFileUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	if after := blocker9ReadFile(t, path); string(after) != string(before) {
		t.Fatalf("attacker target %s was modified", path)
	}
}

func blocker9AssertListing(t *testing.T, store *session.Store, cwd string, want, notWant []string) {
	t.Helper()
	paths := blocker9CandidatePaths(t, store, cwd)
	for _, wanted := range want {
		if !slices.Contains(paths, wanted) {
			t.Fatalf("listing %v is missing %s", paths, wanted)
		}
	}
	for _, unwanted := range notWant {
		if slices.Contains(paths, unwanted) {
			t.Fatalf("listing %v must not contain %s", paths, unwanted)
		}
	}
}

func blocker9NamedSession(t *testing.T, fixture *bridgeFixture, name, text string) string {
	t.Helper()
	sess, err := fixture.store.Create(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sess, text)
	if err := sess.AppendEntry(&session.SessionInfoEntry{Name: &name}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBlocker9EncodedCwdCollisionStaysProjectLocal(t *testing.T) {
	base := t.TempDir()
	dashProject := filepath.Join(base, "a-b")
	slashProject := filepath.Join(base, "a", "b")
	for _, project := range []string{dashProject, slashProject} {
		if err := os.MkdirAll(project, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dashDir, err := store.DirForCwd(dashProject)
	if err != nil {
		t.Fatal(err)
	}
	slashDir, err := store.DirForCwd(slashProject)
	if err != nil {
		t.Fatal(err)
	}
	if dashDir != slashDir {
		t.Fatalf("encoded directories %q and %q must collide for this regression", dashDir, slashDir)
	}

	dashSess, err := store.Create(dashProject)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, dashSess, "dash side")
	dashPath := dashSess.Path()
	dashID := blocker9SessionID(t, dashPath)
	if err := dashSess.Close(); err != nil {
		t.Fatal(err)
	}
	slashSess, err := store.Create(slashProject)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, slashSess, "slash side")
	slashPath := slashSess.Path()
	slashID := blocker9SessionID(t, slashPath)
	if err := slashSess.Close(); err != nil {
		t.Fatal(err)
	}

	dashPaths := blocker9CandidatePaths(t, store, dashProject)
	if len(dashPaths) != 1 || dashPaths[0] != dashPath {
		t.Fatalf("dash listing = %v, want only %s", dashPaths, dashPath)
	}
	slashPaths := blocker9CandidatePaths(t, store, slashProject)
	if len(slashPaths) != 1 || slashPaths[0] != slashPath {
		t.Fatalf("slash listing = %v, want only %s", slashPaths, slashPath)
	}
	for _, target := range []string{slashPath, slashID} {
		if _, err := resolveValidatedSessionTarget(store, dashProject, target); err == nil {
			t.Fatalf("dash project resolved %q", target)
		}
	}
	for _, target := range []string{dashPath, dashID} {
		if _, err := resolveValidatedSessionTarget(store, slashProject, target); err == nil {
			t.Fatalf("slash project resolved %q", target)
		}
	}
	for project, want := range map[string]string{dashProject: dashPath, slashProject: slashPath} {
		resolved, err := resolveValidatedSessionTarget(store, project, want)
		if err != nil || resolved != want {
			t.Fatalf("project %q resolved %q (%v), want %q", project, resolved, err, want)
		}
	}
	blocker9AssertFileUnchanged(t, dashPath, blocker9ReadFile(t, dashPath))
	blocker9AssertFileUnchanged(t, slashPath, blocker9ReadFile(t, slashPath))
}

func TestBlocker9ForeignHeaderHiddenAndUnactionable(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	legit := seedStoredSession(t, fixture, "foreign header neighbor")
	origin := fixture.bridge.rd.sessionPath
	sessionDir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(sessionDir, "blocker9-evil-foreign.jsonl")
	blocker9WriteFile(t, foreignPath, blocker9SessionBody(t, "03a0acc5-0487-793d-8a82-f3a26b3f089f", t.TempDir(), "foreign payload"))
	foreignBefore := blocker9ReadFile(t, foreignPath)

	blocker9AssertListing(t, fixture.store, fixture.cwd, []string{legit}, []string{foreignPath})
	if err := fixture.bridge.resumeSessionWithSignal(context.Background(), foreignPath); err == nil {
		t.Fatal("resume of a foreign header session must fail")
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("resume changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	if err := fixture.bridge.deleteSessionInteractive(foreignPath); err == nil {
		t.Fatal("delete of a foreign header session must fail")
	}
	if err := fixture.bridge.renameSessionInteractive(foreignPath); err == nil {
		t.Fatal("rename of a foreign header session must fail")
	}
	blocker9AssertFileUnchanged(t, foreignPath, foreignBefore)
	blocker9AssertFileUnchanged(t, legit, blocker9ReadFile(t, legit))
}

func TestBlocker9SessionSymlinkHiddenAndUnactionable(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	victim := seedStoredSession(t, fixture, "symlink victim")
	origin := fixture.bridge.rd.sessionPath
	sessionDir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(sessionDir, "blocker9-evil-link.jsonl")
	if err := os.Symlink(victim, linkPath); err != nil {
		t.Fatal(err)
	}
	victimBefore := blocker9ReadFile(t, victim)

	blocker9AssertListing(t, fixture.store, fixture.cwd, []string{victim}, []string{linkPath})
	if err := fixture.bridge.resumeSessionWithSignal(context.Background(), linkPath); err == nil {
		t.Fatal("resume through a session symlink must fail")
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("resume changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	if err := fixture.bridge.deleteSessionInteractive(linkPath); err == nil {
		t.Fatal("delete through a session symlink must fail")
	}
	if err := fixture.bridge.renameSessionInteractive(linkPath); err == nil {
		t.Fatal("rename through a session symlink must fail")
	}
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink itself must stay untouched")
	}
	blocker9AssertFileUnchanged(t, victim, victimBefore)
}

func TestBlocker9ParentSymlinkAndReplacementRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	legit := seedStoredSession(t, fixture, "inside listing")
	sessionDir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	legitBefore := blocker9ReadFile(t, legit)

	attackerDir := t.TempDir()
	planted := filepath.Join(attackerDir, "blocker9-evil-planted.jsonl")
	blocker9WriteFile(t, planted, legitBefore)
	if err := os.RemoveAll(sessionDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(attackerDir, sessionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := validatedSessionCandidates(fixture.store, fixture.cwd); err == nil {
		t.Fatal("a symlinked session directory must not produce candidates")
	}
	if _, err := resolveValidatedSessionTarget(fixture.store, fixture.cwd, planted); err == nil {
		t.Fatal("a planted session behind a symlinked parent must not resolve")
	}
	blocker9AssertFileUnchanged(t, planted, legitBefore)

	if err := os.Remove(sessionDir); err != nil {
		t.Fatal(err)
	}
	blocker9WriteFile(t, sessionDir, []byte("not a directory\n"))
	if _, err := validatedSessionCandidates(fixture.store, fixture.cwd); err == nil {
		t.Fatal("a session directory replaced by a file must not produce candidates")
	}

	if err := os.Remove(sessionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := validatedSessionCandidates(fixture.store, fixture.cwd); err != nil {
		t.Fatalf("a missing session directory must list empty, got %v", err)
	}
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker9WriteFile(t, legit, legitBefore)
	paths := blocker9CandidatePaths(t, fixture.store, fixture.cwd)
	if len(paths) != 1 || paths[0] != legit {
		t.Fatalf("restored listing = %v, want only %s", paths, legit)
	}
}

func TestBlocker9LockSymlinkHiddenAndUnactionable(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	victim := seedStoredSession(t, fixture, "lock symlink victim")
	origin := fixture.bridge.rd.sessionPath
	victimBefore := blocker9ReadFile(t, victim)
	if err := os.Symlink(filepath.Join(fixture.store.Root(), "elsewhere.lock"), victim+".lock"); err != nil {
		t.Fatal(err)
	}
	blocker9AssertListing(t, fixture.store, fixture.cwd, nil, []string{victim})
	if err := fixture.bridge.resumeSessionWithSignal(context.Background(), victim); err == nil {
		t.Fatal("resume of a session with a symlinked lock must fail")
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("resume changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	if err := fixture.bridge.deleteSessionInteractive(victim); err == nil {
		t.Fatal("delete of a session with a symlinked lock must fail")
	}
	if err := fixture.bridge.renameSessionInteractive(victim); err == nil {
		t.Fatal("rename of a session with a symlinked lock must fail")
	}
	blocker9AssertFileUnchanged(t, victim, victimBefore)
}

func TestBlocker9MalformedFilesHidden(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	legit := seedStoredSession(t, fixture, "malformed neighbor")
	sessionDir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-garbage.jsonl"), []byte("this is not a session\n"))
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-empty.jsonl"), nil)
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-headerless.jsonl"), []byte(`{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"no header"}}`+"\n"))
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-nocwd.jsonl"), []byte(`{"type":"session","version":3,"id":"04a0acc5-0487-793d-8a82-f3a26b3f089f","timestamp":"2026-01-01T00:00:00.000Z"}`+"\n"))
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-ghostcwd.jsonl"), []byte(`{"type":"session","version":3,"id":"05a0acc5-0487-793d-8a82-f3a26b3f089f","timestamp":"2026-01-01T00:00:00.000Z","cwd":"`+filepath.Join(t.TempDir(), "missing", "project")+`"}`+"\n"))
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-notes.txt"), []byte("ignored by extension"))
	fifoPath := filepath.Join(sessionDir, "blocker9-evil-fifo.jsonl")
	if err := exec.Command("mkfifo", fifoPath).Run(); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	t.Cleanup(func() { os.Remove(fifoPath) })

	blocker9AssertListing(t, fixture.store, fixture.cwd, []string{legit}, []string{
		filepath.Join(sessionDir, "blocker9-evil-garbage.jsonl"),
		filepath.Join(sessionDir, "blocker9-evil-empty.jsonl"),
		filepath.Join(sessionDir, "blocker9-evil-headerless.jsonl"),
		filepath.Join(sessionDir, "blocker9-evil-nocwd.jsonl"),
		filepath.Join(sessionDir, "blocker9-evil-ghostcwd.jsonl"),
		fifoPath,
	})
	if err := fixture.bridge.resumeSessionWithSignal(context.Background(), filepath.Join(sessionDir, "blocker9-evil-garbage.jsonl")); err == nil {
		t.Fatal("resume of a malformed file must fail")
	}
	blocker9AssertFileUnchanged(t, legit, blocker9ReadFile(t, legit))
}

func TestBlocker9DuplicateIDsRejectedTogether(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	legit := seedStoredSession(t, fixture, "duplicate neighbor")
	origin := fixture.bridge.rd.sessionPath
	sessionDir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	dupA := filepath.Join(sessionDir, "blocker9-evil-dup-a.jsonl")
	dupB := filepath.Join(sessionDir, "blocker9-evil-dup-b.jsonl")
	blocker9WriteFile(t, dupA, blocker9SessionBody(t, blocker9DupID, fixture.cwd, "duplicate a"))
	blocker9WriteFile(t, dupB, blocker9SessionBody(t, blocker9DupID, fixture.cwd, "duplicate b"))
	dupABefore := blocker9ReadFile(t, dupA)
	dupBBefore := blocker9ReadFile(t, dupB)

	blocker9AssertListing(t, fixture.store, fixture.cwd, []string{legit}, []string{dupA, dupB})
	for _, target := range []string{blocker9DupID, dupA, dupB} {
		if _, err := resolveValidatedSessionTarget(fixture.store, fixture.cwd, target); err == nil {
			t.Fatalf("duplicate target %q resolved against the validated set", target)
		}
	}
	if err := fixture.bridge.resumeSessionWithSignal(context.Background(), dupA); err == nil {
		t.Fatal("resume of a duplicate id must fail")
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("resume changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	if err := fixture.bridge.deleteSessionInteractive(dupA); err == nil {
		t.Fatal("delete of a duplicate id must fail")
	}
	if err := fixture.bridge.renameSessionInteractive(dupB); err == nil {
		t.Fatal("rename of a duplicate id must fail")
	}
	blocker9AssertFileUnchanged(t, dupA, dupABefore)
	blocker9AssertFileUnchanged(t, dupB, dupBBefore)
	blocker9AssertFileUnchanged(t, legit, blocker9ReadFile(t, legit))
}

func TestBlocker9ReplacedCandidateNotActionableAfterListing(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "replace victim")
	origin := fixture.bridge.rd.sessionPath
	foreignCwd := t.TempDir()

	nodes, err := fixture.bridge.sessionBrowserNodes()
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, node := range nodes {
		if node.ID == target {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("listing = %v, want %s", nodes, target)
	}

	foreignBody := blocker9SessionBody(t, "06a0acc5-0487-793d-8a82-f3a26b3f089f", foreignCwd, "replaced payload")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	blocker9WriteFile(t, target, foreignBody)

	if err := fixture.bridge.resumeSessionWithSignal(context.Background(), target); err == nil {
		t.Fatal("resume of a replaced candidate must fail")
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("resume changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	if err := fixture.bridge.deleteSessionInteractive(target); err == nil {
		t.Fatal("delete of a replaced candidate must fail")
	}
	if err := fixture.bridge.renameSessionInteractive(target); err == nil {
		t.Fatal("rename of a replaced candidate must fail")
	}
	blocker9AssertFileUnchanged(t, target, foreignBody)

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	other := seedStoredSession(t, fixture, "symlink swap target")
	if err := os.Symlink(other, target); err != nil {
		t.Fatal(err)
	}
	if err := fixture.bridge.deleteSessionInteractive(target); err == nil {
		t.Fatal("delete through a symlink replacement must fail")
	}
	linkInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the replacement symlink must stay untouched")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("the symlink destination was touched: %v", err)
	}
}

func TestBlocker9BrowserSelectOnReplacedCandidateIsRefused(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	target := seedStoredSession(t, fixture, "stale select victim")
	nodes, err := fixture.bridge.sessionBrowserNodes()
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, node := range nodes {
		if node.ID == target {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("listing = %v, want %s", nodes, target)
	}
	foreignBody := blocker9SessionBody(t, "07a0acc5-0487-793d-8a82-f3a26b3f089f", t.TempDir(), "stale replacement")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	blocker9WriteFile(t, target, foreignBody)
	outcome, err := fixture.bridge.resumeBrowserSelection(target, origin)
	if outcome != browserResumeRefused || err == nil {
		t.Fatalf("stale selection = (%v, %v), want refusal", outcome, err)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("a stale selection changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	blocker9AssertFileUnchanged(t, target, foreignBody)

	activeOutcome, err := fixture.bridge.resumeBrowserSelection(origin, origin)
	if activeOutcome != browserResumeAlreadyActive || err != nil {
		t.Fatalf("active selection = (%v, %v), want already active", activeOutcome, err)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("an active selection changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
}

func TestBlocker9BrowserDisplaysOnlyValidatedCandidates(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	legit := blocker9NamedSession(t, fixture, "blocker9-legit-marker", "browser legit")
	sessionDir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-foreign.jsonl"), blocker9SessionBody(t, "08a0acc5-0487-793d-8a82-f3a26b3f089f", t.TempDir(), "evil foreign text"))
	blocker9WriteFile(t, filepath.Join(sessionDir, "blocker9-evil-garbage.jsonl"), []byte("garbage\n"))
	if err := os.Symlink(legit, filepath.Join(sessionDir, "blocker9-evil-link.jsonl")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.sessionsBrowser() }()
	waitForOutputSettled(t, fixture.terminal, "Sessions", 3*time.Second)
	output := tui.StripTerminalSequences(fixture.terminal.Output())
	if !strings.Contains(output, "blocker9-legit-marker") {
		t.Fatalf("the validated session is missing from the browser output:\n%s", output)
	}
	if strings.Contains(output, "blocker9-evil") || strings.Contains(output, "evil foreign text") {
		t.Fatalf("an unvalidated candidate leaked into the browser output:\n%s", output)
	}
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sessionsBrowser = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sessions browser did not close")
	}
}

func TestBlocker9ValidatedTargetsResolveForCurrentProject(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "validated resolution")
	targetID := blocker9SessionID(t, target)
	blocker9AssertListing(t, fixture.store, fixture.cwd, []string{target}, nil)
	resolvedByID, err := resolveValidatedSessionTarget(fixture.store, fixture.cwd, targetID)
	if err != nil {
		t.Fatalf("resolve by id: %v", err)
	}
	if resolvedByID != target {
		t.Fatalf("resolve by id = %s, want %s", resolvedByID, target)
	}
	resolvedByPath, err := resolveValidatedSessionTarget(fixture.store, fixture.cwd, target)
	if err != nil {
		t.Fatalf("resolve by path: %v", err)
	}
	if resolvedByPath != target {
		t.Fatalf("resolve by path = %s, want %s", resolvedByPath, target)
	}
	if _, err := resolveValidatedSessionTarget(fixture.store, fixture.cwd, "09a0acc5-0487-793d-8a82-f3a26b3f089f"); err == nil {
		t.Fatal("an unknown id must not resolve")
	}
	fixture.bridge.handle("/resume " + targetID)
	if fixture.bridge.rd.sessionPath != target {
		t.Fatalf("resume by id = %s, want %s", fixture.bridge.rd.sessionPath, target)
	}
}

func TestBlocker9ValidationGuards(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validatedSessionCandidates(nil, t.TempDir()); err == nil {
		t.Fatal("a nil store must be rejected")
	}
	if _, err := validatedSessionCandidates(store, "   "); err == nil {
		t.Fatal("an empty cwd must be rejected")
	}
	bare := &tuiBridge{}
	if _, err := bare.resolveSessionTarget("anything"); err == nil {
		t.Fatal("a bridge without a store must fail resolution")
	}
	if _, err := resolveValidatedSessionTarget(store, t.TempDir(), "   "); err == nil {
		t.Fatal("an empty target must be rejected")
	}
	if isSessionTargetID(strings.Repeat("a", maxSessionTargetIDLen+1)) {
		t.Fatal("an over-long id must not look like an id")
	}
	if !isSessionTargetID(blocker9DupID) {
		t.Fatal("a uuid must look like an id")
	}
	if _, err := resolveValidatedSessionTarget(store, filepath.Join(t.TempDir(), "missing-project"), "x"); err == nil {
		t.Fatal("an unknown target must fail resolution")
	}
}

func TestBlocker9MissingStoreRootRecovers(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sessionDir, err := store.DirForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(sessionDir, "blocker9-evil-escape-probe.jsonl")
	body := blocker9SessionBody(t, blocker9DupID, cwd, "inside escape probe")
	blocker9WriteFile(t, victim, body)
	if err := os.RemoveAll(sessionDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker9WriteFile(t, victim, body)
	paths := blocker9CandidatePaths(t, store, cwd)
	if len(paths) != 1 || paths[0] != victim {
		t.Fatalf("restored listing = %v, want only %s", paths, victim)
	}
}
