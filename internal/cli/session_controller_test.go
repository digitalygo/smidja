package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func testSessionPreparer(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
	var loader *session.Loader
	if mode != sessionModeNew {
		loaded, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
		if err != nil {
			return nil, err
		}
		loader = loaded
	}
	projection, err := projectSession(loader)
	if err != nil {
		return nil, err
	}
	cur := session.CurrentProfile{ProviderID: "test", ModelID: "test/model", OrderingVersion: 1, AffinityKey: "workspace:test"}
	fingerprint := func() string { return "fp" }
	if mode == sessionModeFork {
		if _, err := sess.ResetRuntimeProfile(cur, fingerprint); err != nil {
			return nil, err
		}
	} else {
		if _, err := syncRuntimeProfile(sess, cur, fingerprint); err != nil {
			return nil, err
		}
	}
	profile, _ := sess.RuntimeProfile()
	return &activeSession{
		sess:       sess,
		recorder:   &sessionRecorder{sess},
		path:       sess.Path(),
		history:    projection.history,
		entryIDs:   projection.entryIDs,
		transcript: projection.transcript,
		tree:       projection.tree,
		warnings:   projection.warnings,
		profile:    profile,
		name:       projection.name,
		mode:       mode,
		loader:     loader,
	}, nil
}

func testController(t *testing.T, store *session.Store, cwd string) *sessionController {
	t.Helper()
	controller := newSessionController(store, cwd)
	controller.SetPreparer(testSessionPreparer)
	return controller
}

func seedTurn(t *testing.T, sess *session.Session, text string) {
	t.Helper()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"` + text + `"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{Role: "assistant", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: text + " reply"}}, StopReason: "stop", Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionControllerSwitchesRecorderProfileAndLocks(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sessA, "alpha")
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sessB, "beta")
	controller := testController(t, store, cwd)
	defer controller.Close()

	activeA, err := controller.PrepareOpen(sessA.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(activeA); err != nil {
		t.Fatal(err)
	}
	if len(activeA.history) != 2 {
		t.Fatalf("session A history = %d messages, want 2", len(activeA.history))
	}
	if activeA.profile == nil {
		t.Fatal("session A has no runtime profile")
	}
	if _, err := store.Open(sessA.Path(), session.OpenOptions{}); err == nil {
		t.Error("the active session lock must block a second writer")
	}
	seedTurn(t, activeA.sess, "alpha turn")

	activeB, err := controller.PrepareOpen(sessB.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(activeB); err != nil {
		t.Fatal(err)
	}
	if controller.Current() != activeB {
		t.Fatal("controller did not switch to session B")
	}
	if activeB.recorder == activeA.recorder {
		t.Error("session switch must install a distinct recorder")
	}
	if activeB.profile == nil {
		t.Error("session B has no runtime profile")
	}
	reopenedA, err := store.Open(sessA.Path(), session.OpenOptions{})
	if err != nil {
		t.Fatalf("session A lock was not released after the switch: %v", err)
	}
	reopenedA.Close()
	seedTurn(t, activeB.sess, "beta turn")

	activeNew, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(activeNew); err != nil {
		t.Fatal(err)
	}
	if activeNew.path == activeA.path || activeNew.path == activeB.path {
		t.Error("new session must get a new file path")
	}
	if activeNew.profile == nil {
		t.Error("new session has no runtime profile")
	}
	seedTurn(t, activeNew.sess, "new turn")

	assertFileContains(t, sessA.Path(), "alpha turn", true)
	assertFileContains(t, sessA.Path(), "beta turn", false)
	assertFileContains(t, sessB.Path(), "beta turn", true)
	assertFileContains(t, activeNew.path, "new turn", true)
	assertFileContains(t, sessA.Path(), session.RuntimeProfileCustomType, true)
	assertFileContains(t, activeNew.path, session.RuntimeProfileCustomType, true)
}

func assertFileContains(t *testing.T, path, fragment string, want bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	got := strings.Contains(string(data), fragment)
	if got != want {
		t.Fatalf("%s contains %q = %v, want %v", filepath.Base(path), fragment, got, want)
	}
}

func TestSessionControllerFailedSwitchLeavesCurrentIntact(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sessA, "alpha")
	controller := testController(t, store, cwd)
	defer controller.Close()
	activeA, err := controller.Adopt(sessA, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.PrepareOpen(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Fatal("PrepareOpen must fail for a missing session")
	}
	foreign := filepath.Join(t.TempDir(), "foreign.txt")
	if err := os.WriteFile(foreign, []byte("not a session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.PrepareOpen(foreign); err == nil {
		t.Fatal("PrepareOpen must fail for a non-session file")
	}
	if controller.Current() != activeA {
		t.Fatal("a failed switch must leave the current session intact")
	}
	seedTurn(t, controller.Current().sess, "after failure")
	assertFileContains(t, activeA.path, "after failure", true)
}

func TestSessionControllerAbortReleasesCandidate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sessA, "alpha")
	controller := testController(t, store, cwd)
	defer controller.Close()
	activeA, err := controller.Adopt(sessA, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Abort(candidate); err != nil {
		t.Fatalf("Abort = %v", err)
	}
	if controller.Current() != activeA {
		t.Fatal("abort must not change the current session")
	}
	if _, err := os.Stat(candidate.path); !os.IsNotExist(err) {
		t.Fatalf("aborted candidate file still exists: %v", err)
	}
	if files := countJSONL(t, store.Root()); files != 1 {
		t.Fatalf("session files after abort = %d, want 1", files)
	}
}

func TestSessionControllerForkRemapsReferencesAndPreservesPayloads(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sess, "origin")
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	firstID := session.EntryID(loader.Roots()[0])
	label := "keep me"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: firstID, Label: &label}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: "compacted", FirstKeptEntryID: firstID, TokensBefore: 3}); err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(sess, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	before := countJSONL(t, store.Root())
	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(forked); err != nil {
		t.Fatal(err)
	}
	if forked.profile == nil {
		t.Fatal("fork must persist a fresh profile")
	}
	after := countJSONL(t, store.Root())
	if after != before+1 {
		t.Fatalf("session files after fork = %d, want %d", after, before+1)
	}
	forkLoader, err := session.LoadWithOptions(forked.path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	newIDs := map[string]bool{}
	for _, entry := range forkLoader.Entries() {
		newIDs[session.EntryID(entry)] = true
	}
	if newIDs[firstID] {
		t.Error("cloned entries must get new ids")
	}
	var labelEntry *session.LabelEntry
	var compactionEntry *session.CompactionEntry
	for _, entry := range forkLoader.Entries() {
		switch typed := entry.(type) {
		case *session.LabelEntry:
			labelEntry = typed
		case *session.CompactionEntry:
			compactionEntry = typed
		}
	}
	if labelEntry == nil {
		t.Fatal("fork lost the label entry")
	}
	if !newIDs[labelEntry.TargetID] || labelEntry.TargetID == firstID {
		t.Errorf("label target %q was not remapped to a new id", labelEntry.TargetID)
	}
	if labelEntry.Label == nil || *labelEntry.Label != "keep me" {
		t.Errorf("label payload = %v, want keep me", labelEntry.Label)
	}
	if compactionEntry == nil {
		t.Fatal("fork lost the compaction entry")
	}
	if !newIDs[compactionEntry.FirstKeptEntryID] || compactionEntry.FirstKeptEntryID == firstID {
		t.Errorf("compaction anchor %q was not remapped to a new id", compactionEntry.FirstKeptEntryID)
	}
	rawBefore, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	rawAfter, err := os.ReadFile(forked.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawAfter), `"origin"`) || !strings.Contains(string(rawBefore), `"origin"`) {
		t.Error("fork must preserve the raw message payload")
	}
	if strings.Contains(string(rawAfter), firstID) {
		t.Error("fork output still references the original entry id")
	}
}

func countJSONL(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestSessionControllerForkRejectsUnsupportedPrefixWithoutSideEffects(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sess, "origin")
	path := sess.Path()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"mystery","id":"zzz","parentId":null,"timestamp":"2026-01-01T00:00:03.000Z"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"after"`), Timestamp: 9}); err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(sess, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	before := countJSONL(t, store.Root())
	if _, err := controller.PrepareFork("zzz", sdk.ForkOptions{}); err == nil {
		t.Fatal("fork of an unsupported prefix must fail")
	}
	if after := countJSONL(t, store.Root()); after != before {
		t.Fatalf("failed fork created %d new session files", after-before)
	}
	if _, err := controller.PrepareFork("", sdk.ForkOptions{Position: "middle"}); err == nil {
		t.Fatal("fork with an unsupported position must fail")
	}
	if after := countJSONL(t, store.Root()); after != before {
		t.Fatalf("rejected fork created %d new session files", after-before)
	}
}

func TestSessionControllerForkClonesEverySupportedEntryType(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: []byte(`"root"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rootID := session.EntryID(loader.Roots()[0])
	if err := sess.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.ModelChangeEntry{Provider: "openrouter", ModelID: "vendor/model"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{Role: "assistant", Content: []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "probe", Arguments: json.RawMessage(`{}`)}}, StopReason: "toolUse", Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call_1", ToolName: "probe", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "ok"}}, Timestamp: 3}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: "skill", Data: json.RawMessage(`{"name":"quick"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "notice", Content: json.RawMessage(`"hello"`), Display: true, Details: json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	usage := &agent.Usage{Input: 5, Output: 7}
	if err := sess.AppendEntry(&session.BranchSummaryEntry{FromID: rootID, Summary: "branch summary", Usage: usage}); err != nil {
		t.Fatal(err)
	}
	label := "important"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: rootID, Label: &label}); err != nil {
		t.Fatal(err)
	}
	name := "session name"
	if err := sess.AppendEntry(&session.SessionInfoEntry{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: "compaction", FirstKeptEntryID: rootID, TokensBefore: 9, Usage: usage}); err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(sess, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(forked); err != nil {
		t.Fatal(err)
	}
	forkLoader, err := session.LoadWithOptions(forked.path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	ids := map[string]bool{}
	for _, entry := range forkLoader.Entries() {
		kinds[entry.EntryType()] = true
		ids[session.EntryID(entry)] = true
	}
	for _, want := range []string{
		session.EntryTypeMessage,
		session.EntryTypeThinkingLevelChange,
		session.EntryTypeModelChange,
		session.EntryTypeCustom,
		session.EntryTypeCustomMessage,
		session.EntryTypeBranchSummary,
		session.EntryTypeLabel,
		session.EntryTypeSessionInfo,
		session.EntryTypeCompaction,
	} {
		if !kinds[want] {
			t.Errorf("fork is missing entry type %s", want)
		}
	}
	for _, entry := range forkLoader.Entries() {
		switch typed := entry.(type) {
		case *session.BranchSummaryEntry:
			if typed.FromID == rootID || !ids[typed.FromID] {
				t.Errorf("branch summary source %q not remapped", typed.FromID)
			}
			if typed.Usage == nil || typed.Usage.Input != 5 {
				t.Errorf("branch summary usage not preserved: %+v", typed.Usage)
			}
		case *session.CompactionEntry:
			if typed.FirstKeptEntryID == rootID || !ids[typed.FirstKeptEntryID] {
				t.Errorf("compaction anchor %q not remapped", typed.FirstKeptEntryID)
			}
			if typed.Usage == nil || typed.Usage.Output != 7 {
				t.Errorf("compaction usage not preserved: %+v", typed.Usage)
			}
		case *session.CustomMessageEntry:
			if !typed.Display {
				t.Error("custom message display flag was not preserved")
			}
			if string(typed.Details) != `{"a":1}` {
				t.Errorf("custom message details = %s", typed.Details)
			}
		}
	}
}

func TestSessionControllerHoldAndCloseWithoutPreparer(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := newSessionController(store, t.TempDir())
	controller.Hold(sess)
	if controller.Current() == nil {
		t.Fatal("Hold must install the session as current")
	}
	if err := controller.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := controller.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	next, err := controller.PrepareNew()
	if err == nil || next != nil {
		t.Fatal("PrepareNew on a closed controller must fail")
	}
}

func TestSessionControllerPrepareRequiresPreparer(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := newSessionController(store, t.TempDir())
	if _, err := controller.PrepareOpen("missing"); err == nil {
		t.Error("PrepareOpen without a preparer must fail")
	}
	if _, err := controller.PrepareNew(); err == nil {
		t.Error("PrepareNew without a preparer must fail")
	}
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Error("PrepareFork without a preparer must fail")
	}
	if _, err := controller.Adopt(nil, sessionModeNew); err == nil {
		t.Error("Adopt nil must fail")
	}
	if err := controller.Commit(nil); err == nil {
		t.Error("Commit nil must fail")
	}
	controller.Abort(nil)
}

func sessionProfileModels(t *testing.T, path string) []string {
	t.Helper()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatalf("load %s: %v", filepath.Base(path), err)
	}
	var models []string
	for _, entry := range loader.Entries() {
		custom, ok := entry.(*session.CustomEntry)
		if !ok || custom.CustomType != session.RuntimeProfileCustomType {
			continue
		}
		var profile session.RuntimeProfile
		if err := json.Unmarshal(custom.Data, &profile); err != nil {
			t.Fatalf("decode runtime profile: %v", err)
		}
		models = append(models, profile.ModelID)
	}
	return models
}

func assertActiveSessionLocked(t *testing.T, store *session.Store, cwd, path string) {
	t.Helper()
	if _, err := store.Open(path, session.OpenOptions{Strict: true}); err == nil {
		t.Error("an active session must block a second writer")
	}
	candidate, err := validateDeleteCandidate(store, cwd, path, "")
	if err != nil {
		t.Fatalf("validateDeleteCandidate(%s) = %v", filepath.Base(path), err)
	}
	if err := deleteSessionFile(store, candidate); err == nil {
		t.Error("deleting an active session must fail while it is locked")
	}
}

func TestCreateLockedSessionMaterializesAndLocks(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if _, err := createLockedSession(store, ""); err == nil {
		t.Error("createLockedSession with an empty cwd must fail")
	}
	sess, err := createLockedSession(store, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sess.Path()); err != nil {
		t.Fatalf("created session file missing: %v", err)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatalf("created session is not strictly loadable: %v", err)
	}
	if loader.Header() == nil || loader.Leaf() == nil {
		t.Fatal("created session must have a header and a materialized first entry")
	}
	assertActiveSessionLocked(t, store, cwd, sess.Path())
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(sess.Path(), session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("released created session could not be reopened: %v", err)
	}
	reopened.Close()
}

func TestSessionControllerCreatedPathsHoldExclusiveLocks(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	controller := testController(t, store, cwd)
	defer controller.Close()

	fresh, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.profile == nil {
		t.Fatal("prepared new session lost its runtime profile")
	}
	assertActiveSessionLocked(t, store, cwd, fresh.path)
	if err := controller.Commit(fresh); err != nil {
		t.Fatal(err)
	}
	assertActiveSessionLocked(t, store, cwd, fresh.path)
	seedTurn(t, fresh.sess, "fork source")

	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if forked.path == fresh.path {
		t.Fatal("fork must materialize a distinct session file")
	}
	assertActiveSessionLocked(t, store, cwd, forked.path)
	if err := controller.Commit(forked); err != nil {
		t.Fatal(err)
	}
	assertActiveSessionLocked(t, store, cwd, forked.path)

	held, err := store.Open(fresh.path, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("the previous session must be released after a fork switch: %v", err)
	}
	held.Close()
}

func TestSessionControllerDiscardsFailedCandidates(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	controller := newSessionController(store, cwd)
	controller.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		if mode == sessionModeNew {
			if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"partial"`), Timestamp: 1}); err != nil {
				return nil, err
			}
		}
		return nil, errors.New("prepare boom")
	})
	if _, err := controller.PrepareNew(); err == nil {
		t.Fatal("PrepareNew must surface the prepare failure")
	}
	if files := countJSONL(t, store.Root()); files != 0 {
		t.Fatalf("failed PrepareNew left %d partial session files", files)
	}

	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "fork source")
	controller.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		return nil, errors.New("fork prepare boom")
	})
	if _, err := controller.Adopt(source, sessionModeNew); err != nil {
		if err.Error() != "fork prepare boom" {
			t.Fatalf("Adopt = %v", err)
		}
	}
	controller.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		projection, err := projectSession(nil)
		if err != nil {
			return nil, err
		}
		if mode == sessionModeFork {
			return nil, errors.New("fork prepare boom")
		}
		return &activeSession{sess: sess, recorder: &sessionRecorder{sess}, path: sess.Path(), history: projection.history}, nil
	})
	if _, err := controller.Adopt(source, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	before := countJSONL(t, store.Root())
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("PrepareFork must surface the prepare failure")
	}
	if after := countJSONL(t, store.Root()); after != before {
		t.Fatalf("failed PrepareFork left %d partial session files", after-before)
	}
}

func TestSessionControllerCloseWaitsForGatedPrepare(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := newSessionController(store, t.TempDir())
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		close(entered)
		<-release
		return testSessionPreparer(sess, mode)
	})

	prepared := make(chan *activeSession, 1)
	prepareErr := make(chan error, 1)
	go func() {
		active, err := controller.PrepareNew()
		prepared <- active
		prepareErr <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- controller.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned before the in-flight prepare finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	active := <-prepared
	if err := <-prepareErr; err == nil {
		t.Fatal("prepare that raced Close must fail")
	}
	if active != nil {
		t.Fatal("prepare that raced Close must not return a session")
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if files := countJSONL(t, store.Root()); files != 0 {
		t.Fatalf("a prepare that raced Close leaked %d session files", files)
	}
	if controller.Current() != nil {
		t.Fatal("a closed controller must not keep a current session")
	}
}

func TestSessionControllerRejectsAfterCloseWithoutFiles(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "source")
	controller := testController(t, store, cwd)
	if _, err := controller.Adopt(source, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	before := countJSONL(t, store.Root())

	if _, err := controller.PrepareNew(); err == nil {
		t.Error("PrepareNew after Close must fail")
	}
	if _, err := controller.PrepareOpen(source.Path()); err == nil {
		t.Error("PrepareOpen after Close must fail")
	}
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Error("PrepareFork after Close must fail")
	}
	if _, err := controller.Adopt(source, sessionModeResume); err == nil {
		t.Error("Adopt after Close must fail")
	}
	if err := controller.Commit(candidate); err == nil {
		t.Error("Commit after Close must fail")
	}
	controller.Hold(source)
	if controller.Current() != nil {
		t.Error("Hold after Close must not install a session")
	}
	if after := countJSONL(t, store.Root()); after != before-1 {
		t.Fatalf("rejected operations after Close left %d session files, want %d", after, before-1)
	}
	if _, err := os.Stat(candidate.path); !os.IsNotExist(err) {
		t.Fatalf("rejected commit did not remove the created candidate: %v", err)
	}
	if err := controller.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

func TestSessionControllerApplyFailureLeavesCurrentAndAbortsCandidate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "source")
	controller := testController(t, store, cwd)
	defer controller.Close()
	activeSource, err := controller.Adopt(source, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}

	var appliedPrevious, appliedNext *activeSession
	controller.SetApplier(func(previous, next *activeSession) error {
		appliedPrevious = previous
		appliedNext = next
		return errors.New("apply boom")
	})
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(candidate); err == nil || err.Error() != "apply boom" {
		t.Fatalf("Commit = %v, want apply boom", err)
	}
	if controller.Current() != activeSource {
		t.Error("a failed apply must leave the current session intact")
	}
	if appliedPrevious != activeSource || appliedNext != candidate {
		t.Errorf("applier saw previous=%p next=%p, want previous=%p next=%p", appliedPrevious, appliedNext, activeSource, candidate)
	}
	if _, err := os.Stat(candidate.path); !os.IsNotExist(err) {
		t.Fatalf("a failed apply did not remove the created candidate: %v", err)
	}
	if files := countJSONL(t, store.Root()); files != 1 {
		t.Fatalf("session files after failed apply = %d, want 1", files)
	}
}

func TestSessionControllerCommitAppliesBeforePublishing(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "source")
	controller := testController(t, store, cwd)
	defer controller.Close()
	activeSource, err := controller.Adopt(source, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}

	var appliedPrevious, appliedNext *activeSession
	controller.SetApplier(func(previous, next *activeSession) error {
		appliedPrevious = previous
		appliedNext = next
		return nil
	})
	if err := controller.Commit(candidate); err != nil {
		t.Fatalf("Commit = %v", err)
	}
	if appliedPrevious != activeSource || appliedNext != candidate {
		t.Errorf("applier saw previous=%p next=%p", appliedPrevious, appliedNext)
	}
	if controller.Current() != candidate {
		t.Error("Commit must publish the candidate as the owner after the apply")
	}
	held, err := store.Open(source.Path(), session.OpenOptions{})
	if err != nil {
		t.Fatalf("the previous session must be released after the commit: %v", err)
	}
	held.Close()
}
