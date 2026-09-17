package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func newSessionBridgeFixture(t *testing.T) *bridgeFixture {
	t.Helper()
	fixture := newBridgeFixture(t, nil, nil)
	controller := testController(t, fixture.store, fixture.cwd)
	fixture.bridge.sessions = controller
	active, err := controller.Adopt(fixture.sess, sessionModeNew)
	if err != nil {
		t.Fatal(err)
	}
	fixture.bridge.rd.recorder = active.recorder
	fixture.bridge.history = active.history
	fixture.bridge.entryIDs = active.entryIDs
	return fixture
}

func TestBridgeNewSessionSwitchesRecorderAndFile(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.rd.client = &capturingClient{script: []*agent.AssistantMessage{textStop("first answer"), textStop("second answer"), textStop("third answer")}}
	firstPath := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("first question")
	if fixture.bridge.rd.sessionPath != firstPath {
		t.Fatalf("session path changed without a switch: %s", fixture.bridge.rd.sessionPath)
	}
	fixture.bridge.handle("/new")
	secondPath := fixture.bridge.rd.sessionPath
	if secondPath == firstPath {
		t.Fatal("/new must activate a different session file")
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("new session file was not created: %v", err)
	}
	fixture.bridge.handle("second question")
	assertFileContains(t, firstPath, "first answer", true)
	assertFileContains(t, firstPath, "second answer", false)
	assertFileContains(t, secondPath, "second answer", true)
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("history after /new = %d messages, want only the new turn", len(fixture.bridge.history))
	}
}

func TestBridgeResumeReplaysHistoryAndDropsQueuedFollowUps(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	other, err := fixture.store.Create(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AppendUser(&agent.UserMessage{Role: "user", Content: []byte(`"prior question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := other.AppendAssistant(&agent.AssistantMessage{Role: "assistant", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "prior answer"}}, StopReason: "stop", Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	otherPath := other.Path()
	other.Close()

	fixture.bridge.runner.Surface().Editor().SetText("queued draft")
	fixture.bridge.handle("/resume " + otherPath)
	if fixture.bridge.rd.sessionPath != otherPath {
		t.Fatalf("session path = %s, want %s", fixture.bridge.rd.sessionPath, otherPath)
	}
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("resumed history = %d messages, want 2", len(fixture.bridge.history))
	}
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "prior question") || !strings.Contains(text, "prior answer") {
		t.Errorf("resumed transcript was not replayed:\n%s", text)
	}
}

func TestBridgeRenameAppendsSessionInfoWithoutRenamingFile(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	path := fixture.bridge.rd.sessionPath
	if err := fixture.bridge.renameActiveSession("release notes"); err != nil {
		t.Fatal(err)
	}
	if fixture.bridge.rd.sessionPath != path {
		t.Fatalf("rename changed the file path to %s", fixture.bridge.rd.sessionPath)
	}
	assertFileContains(t, path, "release notes", true)
	assertFileContains(t, path, `"session_info"`, true)
}

func TestBridgeDeleteCancelsWithoutRemovingFile(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "delete cancel")
	done := make(chan error, 1)
	go func() {
		done <- fixture.bridge.deleteSessionInteractive(target)
	}()
	waitForOutputSettled(t, fixture.terminal, "Delete session", 3*time.Second)
	fixture.terminal.SendInput("n")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("deleteSessionInteractive = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete dialog did not close")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("cancel must keep the session file: %v", err)
	}
}

func TestBridgeDeleteConfirmRemovesOnlySessionFile(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "delete confirm")
	done := make(chan error, 1)
	go func() {
		done <- fixture.bridge.deleteSessionInteractive(target)
	}()
	waitForOutputSettled(t, fixture.terminal, "Delete session", 3*time.Second)
	fixture.terminal.SendInput("y")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("deleteSessionInteractive = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete dialog did not close")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("session file still present after confirmed delete: %v", err)
	}
	_, lockErr := os.Stat(target + ".lock")
	if runtime.GOOS == "linux" && lockErr != nil {
		t.Fatalf("linux keeps the store sidecar lock: %v", lockErr)
	}
	if runtime.GOOS == "darwin" && lockErr == nil {
		t.Fatal("darwin removes the transaction lock it created")
	}
}

func seedStoredSession(t *testing.T, fixture *bridgeFixture, text string) string {
	t.Helper()
	sess, err := fixture.store.Create(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sess, text)
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBridgeDeleteActiveSessionRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	err := fixture.bridge.deleteSessionInteractive(fixture.bridge.rd.sessionPath)
	if err != errDeleteActive {
		t.Fatalf("deleteSessionInteractive active = %v, want errDeleteActive", err)
	}
}

func TestBridgeCommandInventoryAgreement(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if _, err := fixture.commands.Register("tree", sdk.Command{Description: "extension tree", Handler: func(sdk.CommandContext, string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	names := fixture.bridge.commandNames()
	seen := map[string]int{}
	for _, name := range names {
		seen[name]++
	}
	for _, want := range []string{"new", "tree", "fork", "resume", "sessions", "help", "model", "theme", "settings", "quit", "exit"} {
		if seen[want] != 1 {
			t.Errorf("command %q appears %d times in the inventory, want 1", want, seen[want])
		}
	}
	for _, unsupported := range []string{"clear", "thinking"} {
		if seen[unsupported] != 0 {
			t.Errorf("unsupported default command %q leaked into the inventory", unsupported)
		}
	}
	helpNames := map[string]bool{}
	for _, entry := range fixture.bridge.effectiveHelpEntries() {
		helpNames[entry.Name] = true
	}
	autocompleteNames := map[string]bool{}
	for _, item := range fixture.bridge.autocompleteInventory() {
		autocompleteNames[item.Value] = true
	}
	for _, name := range names {
		if !helpNames[name] {
			t.Errorf("command %q is missing from help", name)
		}
		if !autocompleteNames[name] {
			t.Errorf("command %q is missing from autocomplete", name)
		}
	}
	if len(helpNames) != len(names) {
		t.Errorf("help has %d commands, inventory has %d", len(helpNames), len(names))
	}
	if !fixture.bridge.hasCommand("tree") || fixture.bridge.hasCommand("nope") {
		t.Error("hasCommand does not match the inventory")
	}
}

func TestDeleteCandidateDefenses(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	valid, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, valid, "valid")
	validPath := valid.Path()
	valid.Close()

	if _, err := validateDeleteCandidate(store, cwd, validPath, ""); err != nil {
		t.Fatalf("valid candidate rejected: %v", err)
	}
	if _, err := validateDeleteCandidate(store, cwd, validPath, validPath); err != errDeleteActive {
		t.Fatalf("active candidate = %v, want errDeleteActive", err)
	}
	if _, err := validateDeleteCandidate(store, t.TempDir(), validPath, ""); err != errDeleteForeign {
		t.Fatalf("foreign candidate = %v, want errDeleteForeign", err)
	}

	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	data, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, cwd, outside, ""); err == nil {
		t.Error("candidate outside the project session directory must be rejected")
	}

	sessionDir, err := store.DirForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(sessionDir, "link.jsonl")
	if err := os.Symlink(validPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, cwd, symlinkPath, ""); err != errDeleteSymlink {
		t.Fatalf("symlinked candidate = %v, want errDeleteSymlink", err)
	}

	locked, err := store.Open(validPath, session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Close()
	candidate, err := validateDeleteCandidate(store, cwd, validPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteSessionFile(store, candidate); err == nil {
		t.Error("deleting a locked session must fail")
	}
}

func TestBridgeRenameStoredSessionKeepsFile(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "stored rename")
	if err := fixture.bridge.renameStoredSession(target, "archived"); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, target, "archived", true)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("rename must not change the file name: %v", err)
	}
}

func TestBridgeRenameStoredSessionUsesActiveSession(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if err := fixture.bridge.renameStoredSession(fixture.bridge.rd.sessionPath, "live rename"); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, fixture.bridge.rd.sessionPath, "live rename", true)
}

func TestBridgeShowTreeOpensBrowser(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "tree entry")
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.showTree() }()
	waitForOutputSettled(t, fixture.terminal, "Session tree", 3*time.Second)
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("showTree = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tree browser did not close")
	}
}

func focusSessionInBrowser(t *testing.T, fixture *bridgeFixture, target string) {
	t.Helper()
	base := strings.TrimSuffix(filepath.Base(target), ".jsonl")
	unique := base
	if len(unique) > 16 {
		unique = unique[len(unique)-16:]
	}
	fixture.terminal.SendInput("/")
	time.Sleep(50 * time.Millisecond)
	fixture.terminal.SendInput(unique)
	fixture.terminal.SendInput("\r")
	time.Sleep(80 * time.Millisecond)
	fixture.terminal.SendInput("\x1b[B")
	time.Sleep(50 * time.Millisecond)
}

func TestBridgeSessionsBrowserResumesSelection(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "browser resume")
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.sessionsBrowser() }()
	waitForOutputSettled(t, fixture.terminal, "Sessions", 3*time.Second)
	focusSessionInBrowser(t, fixture, target)
	fixture.terminal.SendInput("\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sessionsBrowser = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sessions browser did not close after resume")
	}
	if fixture.bridge.rd.sessionPath != target {
		t.Fatalf("resumed path = %s, want %s", fixture.bridge.rd.sessionPath, target)
	}
}

func TestBridgeSessionsBrowserRenamesSelection(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "browser rename")
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.sessionsBrowser() }()
	waitForOutputSettled(t, fixture.terminal, "Sessions", 3*time.Second)
	focusSessionInBrowser(t, fixture, target)
	fixture.terminal.SendInput("r")
	waitForOutputSettled(t, fixture.terminal, "Rename session", 3*time.Second)
	fixture.terminal.SendInput("renamed through browser")
	fixture.terminal.SendInput("\r")
	waitForOutputSettled(t, fixture.terminal, "session renamed to renamed through browser", 3*time.Second)
	time.Sleep(200 * time.Millisecond)
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sessionsBrowser = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sessions browser did not close after rename")
	}
	assertFileContains(t, target, "renamed through browser", true)
}

func TestBridgeSessionsBrowserDeletesSelection(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "browser delete")
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.sessionsBrowser() }()
	waitForOutputSettled(t, fixture.terminal, "Sessions", 3*time.Second)
	focusSessionInBrowser(t, fixture, target)
	fixture.terminal.SendInput("d")
	waitForOutputSettled(t, fixture.terminal, "Delete session", 3*time.Second)
	fixture.terminal.SendInput("y")
	waitForOutputSettled(t, fixture.terminal, "deleted session", 3*time.Second)
	time.Sleep(200 * time.Millisecond)
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sessionsBrowser = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sessions browser did not close after delete")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target session still exists after browser delete: %v", err)
	}
}

func TestBridgeSessionsBrowserDeleteUsesEmittedTargetUnderBurst(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	target := seedStoredSession(t, fixture, "emitted burst target")
	other := seedStoredSession(t, fixture, "emitted burst other")
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.sessionsBrowser() }()
	waitForOutputSettled(t, fixture.terminal, "Sessions", 3*time.Second)
	focusSessionInBrowser(t, fixture, target)
	fixture.terminal.SendInput("d")
	fixture.terminal.SendInput("\x1b[B")
	waitForOutputSettled(t, fixture.terminal, "Delete session", 3*time.Second)
	fixture.terminal.SendInput("y")
	waitForOutputSettled(t, fixture.terminal, "deleted session", 3*time.Second)
	time.Sleep(200 * time.Millisecond)
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sessionsBrowser = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sessions browser did not close after delete")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("searched target still exists after the delete burst: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated session was removed: %v", err)
	}
}

func TestBridgeModelChangePersistsToTheActiveSession(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		textStop("seed answer"),
		textStop("new answer"),
		textStop("resumed answer"),
	}, nil)
	fixture.sess.Close()
	initial, err := createLockedSession(fixture.store, fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	fixture.sess = initial
	fixture.bridge.rd.sess = initial
	fixture.bridge.rd.recorder = &sessionRecorder{initial}
	fixture.bridge.rd.sessionPath = initial.Path()
	controller := testController(t, fixture.store, fixture.cwd)
	t.Cleanup(func() { controller.Close() })
	fixture.bridge.sessions = controller
	if _, err := controller.Adopt(initial, sessionModeNew); err != nil {
		t.Fatal(err)
	}

	registry := models.NewRegistry()
	registry.Register("openai/alpha", models.ModelInfo{ID: "openai/alpha", Provider: "openai", ContextWindow: 4096})
	registry.Register("openai/beta", models.ModelInfo{ID: "openai/beta", Provider: "openai", ContextWindow: 4096})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "openrouter"
	fixture.bridge.rd.preparer = testPreparer(t)
	cfg := config.Config{Model: "openai/alpha", WorkspaceRoot: fixture.cwd}
	fixture.bridge.rd.persistModel = newModelPersister(controller, &cfg, "openrouter", fixture.bridge.rd.system, fixture.bridge.rd.catalog, fixture.bridge.rd.tools, fixture.cwd, func() string { return "fp" })

	fixture.bridge.handle("seed question")
	initialAfterSeed := readFileString(t, initial.Path())

	fixture.bridge.handle("/new")
	newPath := fixture.bridge.rd.sessionPath
	if newPath == initial.Path() {
		t.Fatal("/new must activate a distinct session file")
	}
	fixture.bridge.applyModel("openai/alpha")
	if got := readFileString(t, initial.Path()); got != initialAfterSeed {
		t.Fatal("the new-session model change wrote to the initial session")
	}
	fixture.bridge.handle("new question")
	if got := lastRequestModel(t, fixture); got != "openai/alpha" {
		t.Fatalf("request model after the new-session change = %q, want openai/alpha", got)
	}
	if models := sessionProfileModels(t, newPath); len(models) == 0 || models[len(models)-1] != "openai/alpha" {
		t.Fatalf("new session profiles = %v, want the alpha profile", models)
	}

	fixture.bridge.handle("/resume " + initial.Path())
	if fixture.bridge.rd.sessionPath != initial.Path() {
		t.Fatalf("resumed path = %s, want %s", fixture.bridge.rd.sessionPath, initial.Path())
	}
	newAfterSwitch := readFileString(t, newPath)
	fixture.bridge.applyModel("openai/beta")
	if got := readFileString(t, newPath); got != newAfterSwitch {
		t.Fatal("the resumed model change wrote to the previously active session")
	}
	if models := sessionProfileModels(t, initial.Path()); len(models) == 0 || models[len(models)-1] != "openai/beta" {
		t.Fatalf("initial session profiles = %v, want the beta profile", models)
	}
	fixture.bridge.handle("resumed question")
	if got := lastRequestModel(t, fixture); got != "openai/beta" {
		t.Fatalf("request model after the resumed change = %q, want openai/beta", got)
	}
}

func TestNewModelPersisterRequiresAnActiveSession(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := newSessionController(store, t.TempDir())
	cfg := config.Config{Model: "openai/alpha"}
	persist := newModelPersister(controller, &cfg, "openrouter", "be terse", nil, nil, t.TempDir(), func() string { return "fp" })
	if err := persist("openai/beta"); err == nil {
		t.Error("persisting a model change without an active session must fail")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return string(data)
}

func lastRequestModel(t *testing.T, fixture *bridgeFixture) string {
	t.Helper()
	if len(fixture.client.reqs) == 0 {
		t.Fatal("no turn request was captured")
	}
	return fixture.client.reqs[len(fixture.client.reqs)-1].Model
}

func TestBridgeNewSessionRejectsArguments(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if err := fixture.bridge.newSession("unexpected"); err == nil {
		t.Fatal("newSession must reject unexpected arguments")
	}
}

func TestBridgeForkSessionActivatesFork(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "fork origin")
	original := fixture.bridge.rd.sessionPath
	if err := fixture.bridge.forkSession(""); err != nil {
		t.Fatal(err)
	}
	if fixture.bridge.rd.sessionPath == original {
		t.Fatal("forkSession must activate a new session file")
	}
	assertFileContains(t, fixture.bridge.rd.sessionPath, "fork origin", true)
}

func TestDeleteCandidateRejectsExtraDefenses(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sessionDir, err := store.DirForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}

	garbage := filepath.Join(sessionDir, "garbage.jsonl")
	if err := os.WriteFile(garbage, []byte("this is not a session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, cwd, garbage, ""); err == nil {
		t.Error("a non-session file must be rejected")
	}

	foreign := filepath.Join(sessionDir, "2026-01-01T00-00-00-000Z_01a0acc5-0487-793d-8a82-f3a26b3f089f.jsonl")
	otherCwd := t.TempDir()
	line := `{"type":"session","version":3,"id":"01a0acc5-0487-793d-8a82-f3a26b3f089f","timestamp":"2026-01-01T00:00:00.000Z","cwd":"` + otherCwd + `"}` + "\n"
	if err := os.WriteFile(foreign, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, cwd, foreign, ""); err != errDeleteForeign {
		t.Fatalf("foreign header cwd = %v, want errDeleteForeign", err)
	}

	valid, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, valid, "lock symlink")
	validPath := valid.Path()
	valid.Close()
	if err := os.Symlink(filepath.Join(sessionDir, "elsewhere.lock"), validPath+".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := validateDeleteCandidate(store, cwd, validPath, ""); err != errDeleteSymlink {
		t.Fatalf("symlinked lock = %v, want errDeleteSymlink", err)
	}
	if err := os.Remove(validPath + ".lock"); err != nil {
		t.Fatal(err)
	}

	if _, err := validateDeleteCandidate(store, cwd, filepath.Join(sessionDir, "nested", "missing.jsonl"), ""); err == nil {
		t.Error("a candidate with a missing parent must be rejected")
	}
	if _, err := validateDeleteCandidate(nil, cwd, validPath, ""); err == nil {
		t.Error("a nil store must be rejected")
	}
	if _, err := validateDeleteCandidate(store, cwd, "", ""); err == nil {
		t.Error("an empty path must be rejected")
	}
	if err := deleteSessionFile(nil, nil); err == nil {
		t.Error("deleteSessionFile without a candidate must fail")
	}
}

func TestBridgeResumeWithoutArgumentAndEmptyBrowser(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.rd.cwd = t.TempDir()
	if err := fixture.bridge.resumeSession(""); err != nil {
		t.Fatalf("resumeSession with no sessions = %v", err)
	}
	waitForOutputSettled(t, fixture.terminal, "no sessions for this project", 3*time.Second)
}

func TestBridgeRenameSessionInteractiveActive(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	path := fixture.bridge.rd.sessionPath
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.renameSessionInteractive(path) }()
	waitForOutputSettled(t, fixture.terminal, "Rename session", 3*time.Second)
	fixture.terminal.SendInput("active rename")
	fixture.terminal.SendInput("\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("renameSessionInteractive = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rename dialog did not close")
	}
	assertFileContains(t, path, "active rename", true)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active rename must keep the file: %v", err)
	}
}

func TestBridgeShowTreeWithoutEntries(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.sessions.Current().path = filepath.Join(t.TempDir(), "missing.jsonl")
	if err := fixture.bridge.showTree(); err != nil {
		t.Fatalf("showTree = %v", err)
	}
	waitForOutputSettled(t, fixture.terminal, "no entries yet", 3*time.Second)
}

func TestBridgeSessionCommandsUnavailableWithoutController(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	if err := fixture.bridge.newSession(""); err == nil {
		t.Error("newSession must fail without a controller")
	}
	if err := fixture.bridge.resumeSession("some-id"); err == nil {
		t.Error("resumeSession must fail without a controller")
	}
	if err := fixture.bridge.forkSession(""); err == nil {
		t.Error("forkSession must fail without a controller")
	}
	if err := fixture.bridge.showTree(); err == nil {
		t.Error("showTree must fail without a controller")
	}
	if err := fixture.bridge.sessionsBrowser(); err == nil {
		t.Error("sessionsBrowser must fail without a controller")
	}
}

func TestBridgeSessionCommandGuardBranches(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if err := fixture.bridge.activateSession(nil, ""); err == nil {
		t.Error("activateSession nil must fail")
	}
	fixture.bridge.replaySession(nil)
	fixture.bridge.sessions.mu.Lock()
	fixture.bridge.sessions.current = nil
	fixture.bridge.sessions.mu.Unlock()
	if err := fixture.bridge.showTree(); err == nil {
		t.Error("showTree without a current session must fail")
	}
	if err := fixture.bridge.renameSessionInteractive(""); err == nil {
		t.Error("renameSessionInteractive with an empty path must fail")
	}
	if err := fixture.bridge.renameActiveSession(""); err != nil {
		t.Errorf("renameActiveSession with an empty name = %v", err)
	}
	if err := fixture.bridge.renameStoredSession("", ""); err != nil {
		t.Errorf("renameStoredSession with an empty name = %v", err)
	}
	if err := fixture.bridge.deleteSessionInteractive(""); err == nil {
		t.Error("deleteSessionInteractive with an empty path must fail")
	}

	bare := newBridgeFixture(t, nil, nil)
	next := &activeSession{path: "next"}
	if err := bare.bridge.activateSession(next, ""); err == nil {
		t.Error("activateSession without a controller must fail")
	}
	next.close()
}
