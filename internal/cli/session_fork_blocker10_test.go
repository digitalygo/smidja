package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func blocker10Quote(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %q: %v", value, err)
	}
	return string(encoded)
}

func blocker10CraftSession(t *testing.T, store *session.Store, cwd string, entries ...string) string {
	t.Helper()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"seed"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	header := loader.Header()
	body := `{"type":"session","version":3,"id":"` + header.ID + `","timestamp":"` + header.Timestamp + `","cwd":` + blocker10Quote(t, header.Cwd) + `}`
	for _, entry := range entries {
		body += "\n" + entry
	}
	body += "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func blocker10Load(t *testing.T, path string) *session.Loader {
	t.Helper()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return loader
}

func blocker10Controller(t *testing.T, store *session.Store, cwd, sourcePath string) *sessionController {
	t.Helper()
	controller := testController(t, store, cwd)
	sess, err := store.Open(sourcePath, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("open source %s: %v", sourcePath, err)
	}
	controller.Hold(sess)
	t.Cleanup(func() { controller.Close() })
	return controller
}

func blocker10UserEntry(id, parent, content string) string {
	parentRef := "null"
	if parent != "" {
		parentRef = `"` + parent + `"`
	}
	return `{"type":"message","id":"` + id + `","parentId":` + parentRef + `,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"` + content + `"}}`
}

func blocker10ForkFiles(t *testing.T, store *session.Store) int {
	t.Helper()
	return countJSONL(t, store.Root())
}

func TestBlocker10ForkRejectsMissingParentBeforeCreate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := blocker10CraftSession(t, store, cwd,
		blocker10UserEntry("a1", "", "one"),
		blocker10UserEntry("a2", "ghost", "two"),
	)
	controller := blocker10Controller(t, store, cwd, path)
	before := blocker10ForkFiles(t, store)
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("fork with a missing parent must fail")
	} else if !strings.Contains(err.Error(), "missing its parent") {
		t.Fatalf("missing parent error = %v", err)
	}
	if after := blocker10ForkFiles(t, store); after != before {
		t.Fatalf("rejected fork created %d session files", after-before)
	}
}

func TestBlocker10ForkRejectsAncestorCycleBeforeCreate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := blocker10CraftSession(t, store, cwd,
		blocker10UserEntry("a1", "a2", "one"),
		blocker10UserEntry("a2", "a1", "two"),
	)
	controller := blocker10Controller(t, store, cwd, path)
	before := blocker10ForkFiles(t, store)
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("fork with an ancestor cycle must fail")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
	if after := blocker10ForkFiles(t, store); after != before {
		t.Fatalf("rejected fork created %d session files", after-before)
	}
}

func TestBlocker10ForkRejectsSelfParentBeforeCreate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := blocker10CraftSession(t, store, cwd, blocker10UserEntry("a1", "a1", "one"))
	controller := blocker10Controller(t, store, cwd, path)
	before := blocker10ForkFiles(t, store)
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("fork with a self parent must fail")
	}
	if after := blocker10ForkFiles(t, store); after != before {
		t.Fatalf("rejected fork created %d session files", after-before)
	}
}

func TestBlocker10ForkRejectsForwardLabelBeforeCreate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	label := `{"type":"label","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","targetId":"a3","label":"early"}`
	path := blocker10CraftSession(t, store, cwd,
		blocker10UserEntry("a1", "", "one"),
		label,
		blocker10UserEntry("a3", "a2", "three"),
	)
	controller := blocker10Controller(t, store, cwd, path)
	before := blocker10ForkFiles(t, store)
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("fork with a forward label must fail")
	} else if !strings.Contains(err.Error(), "forward reference") {
		t.Fatalf("forward label error = %v", err)
	}
	if after := blocker10ForkFiles(t, store); after != before {
		t.Fatalf("rejected fork created %d session files", after-before)
	}
}

func TestBlocker10ForkRemapsBackwardLabelAndKeepsOrder(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	label := `{"type":"label","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","targetId":"a1","label":"keep"}`
	path := blocker10CraftSession(t, store, cwd,
		blocker10UserEntry("a1", "", "one"),
		label,
		blocker10UserEntry("a3", "a2", "three"),
	)
	controller := blocker10Controller(t, store, cwd, path)
	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(forked); err != nil {
		t.Fatal(err)
	}
	loader := blocker10Load(t, forked.path)
	newIDs := map[string]bool{}
	var order []string
	var labelEntry *session.LabelEntry
	for _, entry := range loader.Entries() {
		newIDs[session.EntryID(entry)] = true
		if custom, ok := entry.(*session.CustomEntry); ok {
			if custom.CustomType == sessionProvenanceCustomType || custom.CustomType == session.RuntimeProfileCustomType {
				continue
			}
		}
		order = append(order, entry.EntryType())
		if typed, ok := entry.(*session.LabelEntry); ok {
			labelEntry = typed
		}
	}
	if len(order) != 3 || order[0] != session.EntryTypeMessage || order[1] != session.EntryTypeLabel || order[2] != session.EntryTypeMessage {
		t.Fatalf("fork entry order = %v, want message,label,message", order)
	}
	if labelEntry == nil {
		t.Fatal("fork lost the label entry")
	}
	if labelEntry.TargetID == "" || labelEntry.TargetID == "a1" || !newIDs[labelEntry.TargetID] {
		t.Fatalf("label target %q was not remapped to a retained id", labelEntry.TargetID)
	}
	if labelEntry.Label == nil || *labelEntry.Label != "keep" {
		t.Fatalf("label payload = %v, want keep", labelEntry.Label)
	}
}

func TestBlocker10ForkValidatesCompactionAndBranchOrder(t *testing.T) {
	build := func(t *testing.T, entries ...string) string {
		t.Helper()
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return blocker10CraftSession(t, store, t.TempDir(), entries...)
	}
	cases := map[string][]string{
		"forward compaction anchor": {
			blocker10UserEntry("a1", "", "one"),
			`{"type":"compaction","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","summary":"s","firstKeptEntryId":"a3","tokensBefore":1}`,
			blocker10UserEntry("a3", "a2", "three"),
		},
		"outside compaction anchor": {
			blocker10UserEntry("a1", "", "one"),
			`{"type":"compaction","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","summary":"s","firstKeptEntryId":"ghost","tokensBefore":1}`,
		},
		"forward branch source": {
			`{"type":"branch_summary","id":"a1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","fromId":"a2","summary":"s"}`,
			blocker10UserEntry("a2", "a1", "two"),
		},
		"outside branch source": {
			blocker10UserEntry("a1", "", "one"),
			`{"type":"branch_summary","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","fromId":"ghost","summary":"s"}`,
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			path := build(t, entries...)
			loader := blocker10Load(t, path)
			if _, err := forkPrefixEntries(loader, ""); err == nil {
				t.Fatalf("%s must be rejected", name)
			}
		})
	}
}

func TestBlocker10ForkRemapsCompactionAndBranchSources(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := blocker10CraftSession(t, store, cwd,
		blocker10UserEntry("a1", "", "one"),
		blocker10UserEntry("a2", "a1", "two"),
		`{"type":"compaction","id":"a3","parentId":"a2","timestamp":"2026-01-01T00:00:03.000Z","summary":"compacted","firstKeptEntryId":"a1","tokensBefore":7}`,
		`{"type":"branch_summary","id":"a4","parentId":"a3","timestamp":"2026-01-01T00:00:04.000Z","fromId":"a2","summary":"branched"}`,
	)
	controller := blocker10Controller(t, store, cwd, path)
	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(forked); err != nil {
		t.Fatal(err)
	}
	loader := blocker10Load(t, forked.path)
	newIDs := map[string]bool{}
	for _, entry := range loader.Entries() {
		newIDs[session.EntryID(entry)] = true
	}
	var compaction *session.CompactionEntry
	var branch *session.BranchSummaryEntry
	for _, entry := range loader.Entries() {
		switch typed := entry.(type) {
		case *session.CompactionEntry:
			compaction = typed
		case *session.BranchSummaryEntry:
			branch = typed
		}
	}
	if compaction == nil || branch == nil {
		t.Fatal("fork lost compaction or branch summary")
	}
	if compaction.FirstKeptEntryID == "a1" || !newIDs[compaction.FirstKeptEntryID] {
		t.Fatalf("compaction anchor %q was not remapped", compaction.FirstKeptEntryID)
	}
	if branch.FromID == "a2" || !newIDs[branch.FromID] {
		t.Fatalf("branch source %q was not remapped", branch.FromID)
	}
}

func TestBlocker10ForkRejectsOpaqueBeforeCreate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := blocker10CraftSession(t, store, cwd,
		blocker10UserEntry("a1", "", "one"),
		`{"type":"mystery","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z"}`,
	)
	controller := blocker10Controller(t, store, cwd, path)
	before := blocker10ForkFiles(t, store)
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("fork with an opaque entry must fail")
	} else if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("opaque error = %v", err)
	}
	if after := blocker10ForkFiles(t, store); after != before {
		t.Fatalf("rejected fork created %d session files", after-before)
	}
}

func TestBlocker10ForkPreservesRawPayloadsAndOrder(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	messageOne := `{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"one"}}`
	label := `{"type":"label","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","targetId":"a1","label":"keep"}`
	custom := `{"type":"custom","id":"a3","parentId":"a2","timestamp":"2026-01-01T00:00:03.000Z","customType":"note","data":{"b":1,"a":2}}`
	path := blocker10CraftSession(t, store, cwd, messageOne, label, custom)
	controller := blocker10Controller(t, store, cwd, path)
	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(forked); err != nil {
		t.Fatal(err)
	}
	loader := blocker10Load(t, forked.path)
	var order []string
	for _, entry := range loader.Entries() {
		if entry.EntryType() == session.EntryTypeCustom {
			customEntry := entry.(*session.CustomEntry)
			if customEntry.CustomType == sessionProvenanceCustomType || customEntry.CustomType == session.RuntimeProfileCustomType {
				continue
			}
		}
		order = append(order, entry.EntryType())
	}
	if len(order) != 3 || order[0] != session.EntryTypeMessage || order[1] != session.EntryTypeLabel || order[2] != session.EntryTypeCustom {
		t.Fatalf("fork order = %v, want message,label,custom", order)
	}
	sourceLoader := blocker10Load(t, path)
	var wantMessage, wantCustom string
	for _, entry := range sourceLoader.Entries() {
		switch typed := entry.(type) {
		case *session.MessageEntry:
			wantMessage = string(typed.Message)
		case *session.CustomEntry:
			if typed.CustomType == "note" {
				wantCustom = string(typed.Data)
			}
		}
	}
	var gotMessage, gotCustom string
	for _, entry := range loader.Entries() {
		switch typed := entry.(type) {
		case *session.MessageEntry:
			gotMessage = string(typed.Message)
		case *session.CustomEntry:
			if typed.CustomType == "note" {
				gotCustom = string(typed.Data)
			}
		}
	}
	if gotMessage != wantMessage || wantMessage == "" {
		t.Fatalf("message payload = %s, want %s", gotMessage, wantMessage)
	}
	if gotCustom != wantCustom || wantCustom != `{"b":1,"a":2}` {
		t.Fatalf("custom payload = %s, want %s", gotCustom, wantCustom)
	}
}

func TestBlocker10ForkRemapsRuntimeProfileAnchor(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	profile := `{"type":"custom","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","customType":"smidja.runtime.profile","data":{"providerID":"test","modelID":"test/model","systemPromptSHA256":"","toolSchemasCanonicalJSONSHA256":"","orderingVersion":0,"contentFingerprint":"","affinityKey":"","estimatorAnchor":{"lastInputTokens":42,"leafID":"a1"}}}`
	path := blocker10CraftSession(t, store, cwd, blocker10UserEntry("a1", "", "one"), profile)
	controller := blocker10Controller(t, store, cwd, path)
	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(forked); err != nil {
		t.Fatal(err)
	}
	loader := blocker10Load(t, forked.path)
	newIDs := map[string]bool{}
	for _, entry := range loader.Entries() {
		newIDs[session.EntryID(entry)] = true
	}
	found := false
	for _, entry := range loader.Entries() {
		custom, ok := entry.(*session.CustomEntry)
		if !ok || custom.CustomType != session.RuntimeProfileCustomType {
			continue
		}
		var decoded session.RuntimeProfile
		if err := json.Unmarshal(custom.Data, &decoded); err != nil {
			t.Fatalf("decode cloned profile: %v", err)
		}
		if decoded.EstimatorAnchor.LastInputTokens != 42 {
			continue
		}
		found = true
		if decoded.EstimatorAnchor.LeafID == "a1" || !newIDs[decoded.EstimatorAnchor.LeafID] {
			t.Fatalf("profile anchor %q was not remapped", decoded.EstimatorAnchor.LeafID)
		}
	}
	if !found {
		t.Fatal("fork lost the anchored runtime profile")
	}
}

func TestBlocker10CandidateOriginAndIdentity(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	sourcePath := source.Path()
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, cwd)
	defer controller.Close()

	opened, err := controller.PrepareOpen(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if opened.origin != candidateOpened || opened.identity != nil {
		t.Fatalf("opened candidate origin=%d identity=%v", opened.origin, opened.identity)
	}
	if err := controller.Commit(opened); err != nil {
		t.Fatal(err)
	}

	created, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if created.origin != candidateCreated || created.identity == nil {
		t.Fatalf("created candidate origin=%d identity=%v", created.origin, created.identity)
	}
	if info, err := os.Lstat(created.path); err != nil || !os.SameFile(created.identity, info) {
		t.Fatalf("created candidate identity does not match its file: %v", err)
	}
	if err := controller.Commit(created); err != nil {
		t.Fatal(err)
	}

	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if forked.origin != candidateCreated || forked.identity == nil {
		t.Fatalf("forked candidate origin=%d identity=%v", forked.origin, forked.identity)
	}
	if err := controller.Abort(forked); err != nil {
		t.Fatalf("Abort = %v", err)
	}
	if _, err := os.Stat(forked.path); !os.IsNotExist(err) {
		t.Fatalf("aborted fork still exists: %v", err)
	}
}

func TestBlocker10AbortCleansCreatedAndClosesOpened(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	sourcePath := source.Path()
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, cwd)
	defer controller.Close()

	opened, err := controller.PrepareOpen(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Abort(opened); err != nil {
		t.Fatalf("Abort opened = %v", err)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("aborting an opened candidate must keep its file: %v", err)
	}

	created, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Abort(created); err != nil {
		t.Fatalf("Abort created = %v", err)
	}
	if _, err := os.Stat(created.path); !os.IsNotExist(err) {
		t.Fatalf("aborted created candidate still exists: %v", err)
	}
	if err := controller.Abort(created); err != nil {
		t.Fatalf("second Abort = %v", err)
	}
}

func TestBlocker10ApplyFailureCleansForkCandidate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	controller := testController(t, store, cwd)
	defer controller.Close()
	active, err := controller.Adopt(source, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	controller.SetApplier(func(previous, next *activeSession) error {
		return errors.New("apply boom")
	})
	forked, err := controller.PrepareFork("", sdk.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(forked); err == nil || err.Error() != "apply boom" {
		t.Fatalf("Commit = %v, want apply boom", err)
	}
	if controller.Current() != active {
		t.Fatal("failed apply must leave the current session intact")
	}
	if _, err := os.Stat(forked.path); !os.IsNotExist(err) {
		t.Fatalf("failed apply left the fork candidate behind: %v", err)
	}
	if files := blocker10ForkFiles(t, store); files != 1 {
		t.Fatalf("session files after failed fork apply = %d, want 1", files)
	}
}

func TestBlocker10CloseRaceCleansForkCandidate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	controller := testController(t, store, cwd)
	active, err := controller.Adopt(source, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	_ = active

	entered := make(chan struct{})
	release := make(chan struct{})
	controller.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		close(entered)
		<-release
		return testSessionPreparer(sess, mode)
	})

	forked := make(chan *activeSession, 1)
	forkErr := make(chan error, 1)
	go func() {
		next, err := controller.PrepareFork("", sdk.ForkOptions{})
		forked <- next
		forkErr <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fork prepare did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- controller.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned before the in-flight fork finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	next := <-forked
	if err := <-forkErr; err == nil {
		t.Fatal("fork that raced Close must fail")
	}
	if next != nil {
		t.Fatal("fork that raced Close must not return a candidate")
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if files := blocker10ForkFiles(t, store); files != 1 {
		t.Fatalf("fork that raced Close left %d files, want 1", files)
	}
}

func TestBlocker10ReplacementAttackSurvives(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(source, sessionModeResume); err != nil {
		t.Fatal(err)
	}

	t.Run("regular replacement", func(t *testing.T) {
		candidate, err := controller.PrepareNew()
		if err != nil {
			t.Fatal(err)
		}
		original := candidate.path
		if err := os.Rename(original, original+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(original, []byte("replacement\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := controller.Abort(candidate); err == nil {
			t.Fatal("aborting a replaced candidate must surface the cleanup failure")
		}
		if got, err := os.ReadFile(original); err != nil || string(got) != "replacement\n" {
			t.Fatalf("replacement file = %q err=%v", got, err)
		}
	})

	t.Run("symlink replacement", func(t *testing.T) {
		candidate, err := controller.PrepareNew()
		if err != nil {
			t.Fatal(err)
		}
		victim := candidate.path + ".victim"
		if err := os.WriteFile(victim, []byte("victim\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(candidate.path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, candidate.path); err != nil {
			t.Fatal(err)
		}
		if err := controller.Abort(candidate); err == nil {
			t.Fatal("aborting a symlinked replacement must surface the cleanup failure")
		}
		if got, err := os.ReadFile(victim); err != nil || string(got) != "victim\n" {
			t.Fatalf("symlink victim = %q err=%v", got, err)
		}
	})
}

func TestBlocker10CleanupFailureSurfaced(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(source, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	original := removeCreatedCandidateFile
	removeCreatedCandidateFile = func(string) error { return errors.New("remove boom") }
	t.Cleanup(func() { removeCreatedCandidateFile = original })

	err = controller.Abort(candidate)
	if err == nil || !strings.Contains(err.Error(), "remove boom") {
		t.Fatalf("Abort = %v, want the removal failure", err)
	}
	if _, statErr := os.Stat(candidate.path); statErr != nil {
		t.Fatalf("cleanup failure must leave the file for inspection: %v", statErr)
	}
	removeCreatedCandidateFile = original
	if err := os.Remove(candidate.path); err != nil {
		t.Fatal(err)
	}
}

func TestBlocker10ForkHelperGuards(t *testing.T) {
	if err := removeCreatedCandidate("", nil); err == nil {
		t.Error("removeCreatedCandidate without identity must fail")
	}
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	plainInfo, err := os.Lstat(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeCreatedCandidate(filepath.Join(plain, "child"), plainInfo); err == nil {
		t.Error("removeCreatedCandidate with an uninspectable path must fail")
	}
	if err := removeCreatedCandidate(filepath.Join(t.TempDir(), "absent"), plainInfo); err != nil {
		t.Errorf("removeCreatedCandidate for a missing file = %v", err)
	}

	var nilActive *activeSession
	if err := nilActive.dispose(); err != nil {
		t.Errorf("nil dispose = %v", err)
	}
	if err := disposeCandidate(nil, candidateCreated, nil); err != nil {
		t.Errorf("disposeCandidate nil = %v", err)
	}
	if err := disposeCandidate(nil, candidateOpened, plainInfo); err != nil {
		t.Errorf("disposeCandidate nil opened = %v", err)
	}

	if got, err := remapForkReference(nil, "", "label target"); err != nil || got != "" {
		t.Errorf("remapForkReference empty = %q, %v", got, err)
	}
	if _, err := remapForkReference(nil, "missing", "label target"); err == nil {
		t.Error("remapForkReference with a missing reference must fail")
	}

	if _, err := lastAppendedEntryID(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("lastAppendedEntryID for a missing file must fail")
	}
	emptyPath := filepath.Join(t.TempDir(), "empty.jsonl")
	header := `{"type":"session","version":3,"id":"01a0acc5-0487-793d-8a82-f3a26b3f089f","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}` + "\n"
	if err := os.WriteFile(emptyPath, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lastAppendedEntryID(emptyPath); err == nil {
		t.Error("lastAppendedEntryID without entries must fail")
	}
	emptyLoader, err := session.LoadWithOptions(emptyPath, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forkPrefixEntries(nil, ""); err == nil {
		t.Error("forkPrefixEntries without a loader must fail")
	}
	if _, err := forkPrefixEntries(emptyLoader, ""); err == nil {
		t.Error("forkPrefixEntries without entries must fail")
	}
	if _, err := forkAncestry(emptyLoader, "missing"); err == nil {
		t.Error("forkAncestry for a missing target must fail")
	}

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	closed, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cloneSessionPrefix(nil, nil); err == nil {
		t.Error("cloneSessionPrefix without a target must fail")
	}
	if err := cloneSessionPrefix(closed, []session.Entry{&session.MessageEntry{EntryBase: session.EntryBase{ID: "a1"}}}); err == nil {
		t.Error("cloneSessionPrefix on a closed target must fail")
	}
	if err := cloneSessionPrefix(closed, []session.Entry{&session.MessageEntry{}}); err == nil {
		t.Error("cloneSessionPrefix without ids must fail")
	}
	if _, err := cloneEntryForAppend(&session.OpaqueEntry{TypeName: "mystery"}, nil); err == nil {
		t.Error("cloneEntryForAppend for an opaque entry must fail")
	}
	if _, err := forkRuntimeProfileAnchor(json.RawMessage(`{`)); err == nil {
		t.Error("forkRuntimeProfileAnchor with malformed data must fail")
	}
	if _, err := cloneForkCustomData(session.RuntimeProfileCustomType, json.RawMessage(`{`), nil); err == nil {
		t.Error("cloneForkCustomData with malformed data must fail")
	}
	duplicate := []session.Entry{
		&session.MessageEntry{EntryBase: session.EntryBase{ID: "dup"}},
		&session.MessageEntry{EntryBase: session.EntryBase{ID: "dup"}},
	}
	if err := validateForkReferences(duplicate); err == nil {
		t.Error("validateForkReferences with duplicate ids must fail")
	}
}

func TestBlocker10ForkRejectsUnsupportedMessageRole(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := blocker10CraftSession(t, store, cwd,
		blocker10UserEntry("a1", "", "one"),
		`{"type":"message","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"custom","content":"x"}}`,
	)
	controller := blocker10Controller(t, store, cwd, path)
	before := blocker10ForkFiles(t, store)
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("fork with an unsupported message role must fail")
	}
	if after := blocker10ForkFiles(t, store); after != before {
		t.Fatalf("rejected fork created %d session files", after-before)
	}
}

func TestBlocker10ApplyFailureClosesOpenedCandidate(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	sourcePath := source.Path()
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, cwd)
	defer controller.Close()
	controller.SetApplier(func(previous, next *activeSession) error {
		return errors.New("apply boom")
	})
	opened, err := controller.PrepareOpen(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Commit(opened); err == nil || err.Error() != "apply boom" {
		t.Fatalf("Commit = %v, want apply boom", err)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("a failed apply on a resumed candidate must keep its file: %v", err)
	}
	held, err := store.Open(sourcePath, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("a failed apply on a resumed candidate must release its lock: %v", err)
	}
	held.Close()
}

func TestBlocker10AbortWithoutReplacement(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(source, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(candidate.path); err != nil {
		t.Fatal(err)
	}
	if err := controller.Abort(candidate); err != nil {
		t.Fatalf("Abort after the candidate was removed = %v", err)
	}
}

func TestBlocker10PrepareFailureNeverDeletesReplacement(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(source, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	var replaced string
	controller.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		replaced = sess.Path()
		if err := os.Rename(replaced, replaced+".moved"); err != nil {
			return nil, err
		}
		if err := os.WriteFile(replaced, []byte("replacement\n"), 0o600); err != nil {
			return nil, err
		}
		return nil, errors.New("prepare boom")
	})
	if _, err := controller.PrepareFork("", sdk.ForkOptions{}); err == nil {
		t.Fatal("prepare failure must surface")
	} else if !strings.Contains(err.Error(), "prepare boom") {
		t.Fatalf("PrepareFork = %v, want prepare boom", err)
	}
	if got, err := os.ReadFile(replaced); err != nil || string(got) != "replacement\n" {
		t.Fatalf("replacement file = %q err=%v", got, err)
	}
}

func TestBlocker10ConcurrentPrepareAndAbort(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "origin")
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(source, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			var candidate *activeSession
			var prepareErr error
			if index%2 == 0 {
				candidate, prepareErr = controller.PrepareNew()
			} else {
				candidate, prepareErr = controller.PrepareFork("", sdk.ForkOptions{})
			}
			if prepareErr != nil {
				return
			}
			if err := controller.Abort(candidate); err != nil {
				t.Errorf("Abort = %v", err)
			}
		}(i)
	}
	wg.Wait()
	if files := blocker10ForkFiles(t, store); files != 1 {
		t.Fatalf("concurrent prepare/abort left %d session files, want 1", files)
	}
}
