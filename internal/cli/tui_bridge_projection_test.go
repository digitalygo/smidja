package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/session"
)

type contextRequest struct {
	entryIDs []string
	messages []*agent.Message
}

type capturingContextHooks struct {
	agent.HookDispatcher
	mu       sync.Mutex
	requests []contextRequest
}

func (h *capturingContextHooks) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	h.mu.Lock()
	h.requests = append(h.requests, contextRequest{
		entryIDs: append([]string(nil), req.EntryIDs...),
		messages: append([]*agent.Message(nil), req.Messages...),
	})
	h.mu.Unlock()
	return h.HookDispatcher.Context(ctx, req)
}

func (h *capturingContextHooks) captured() []contextRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]contextRequest, len(h.requests))
	copy(out, h.requests)
	return out
}

func (h *capturingContextHooks) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func newProjectionFixture(t *testing.T, script []*agent.AssistantMessage, tools []agent.Tool) (*bridgeFixture, *capturingContextHooks) {
	t.Helper()
	fixture := newBridgeFixture(t, script, tools)
	controller := testController(t, fixture.store, fixture.cwd)
	t.Cleanup(func() { _ = controller.Close() })
	active, err := controller.Adopt(fixture.sess, sessionModeNew)
	if err != nil {
		t.Fatal(err)
	}
	fixture.bridge.sessions = controller
	fixture.bridge.rd.controller = controller
	fixture.bridge.rd.recorder = active.recorder
	fixture.bridge.history = active.history
	fixture.bridge.entryIDs = active.entryIDs
	hooks := &capturingContextHooks{HookDispatcher: fixture.bridge.rd.hooks}
	fixture.bridge.rd.hooks = hooks
	return fixture, hooks
}

func installProjectionPreparer(t *testing.T, fixture *bridgeFixture) {
	t.Helper()
	window := int64(100000)
	preparer, err := newContextPreparer(config.Config{
		Model:                      "test/model",
		ContextEnabled:             true,
		ContextWindowTokens:        window,
		ContextCacheMissAfter:      time.Minute,
		ContextPruneThreshold:      0.002,
		ContextCompactThreshold:    0.003,
		ContextSafetyThreshold:     0.95,
		ContextCompactTarget:       0.001,
		ContextKeepRecentMessages:  1,
		ContextSelectorChunkTokens: 1000,
	}, window, "test/model", nil)
	if err != nil {
		t.Fatalf("newContextPreparer: %v", err)
	}
	fixture.bridge.rd.preparer = preparer
}

func messagesEqual(a, b *agent.Message) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	if errLeft != nil || errRight != nil {
		return false
	}
	return bytes.Equal(left, right)
}

func contextEntryIDsInOrder(loader *session.Loader) []string {
	var ids []string
	for _, entry := range loader.Entries() {
		switch entry.(type) {
		case *session.MessageEntry, *session.CompactionEntry, *session.BranchSummaryEntry, *session.CustomMessageEntry:
			ids = append(ids, session.EntryID(entry))
		}
	}
	return ids
}

func assertCapturedEntryIDsResolve(t *testing.T, path string, requests []contextRequest) {
	t.Helper()
	loader := p4Load(t, path)
	ordered := contextEntryIDsInOrder(loader)
	if len(ordered) == 0 {
		t.Fatal("the session recorded no context entries")
	}
	for requestIndex, request := range requests {
		if len(request.entryIDs) == 0 {
			t.Fatalf("request %d captured no entry ids", requestIndex)
		}
		if len(request.entryIDs) > len(ordered) {
			t.Fatalf("request %d entry ids %d exceed the recorded %d", requestIndex, len(request.entryIDs), len(ordered))
		}
		for i, id := range request.entryIDs {
			if id != ordered[i] {
				t.Fatalf("request %d position %d entry id = %q, want the recorded %q", requestIndex, i, id, ordered[i])
			}
		}
		if len(request.entryIDs) < len(request.messages) {
			t.Fatalf("request %d entry ids %d fewer than messages %d", requestIndex, len(request.entryIDs), len(request.messages))
		}
		if len(request.entryIDs) != len(request.messages) {
			continue
		}
		for i, id := range request.entryIDs {
			message := request.messages[i]
			if message == nil || message.ToolResult != nil {
				continue
			}
			messageEntry, ok := loader.Get(id)
			if !ok {
				continue
			}
			entry, ok := messageEntry.(*session.MessageEntry)
			if !ok {
				continue
			}
			decoded, err := entry.DecodeMessage()
			if err != nil {
				t.Fatalf("request %d entry %s decode: %v", requestIndex, id, err)
			}
			if !messagesEqual(decoded, message) {
				t.Fatalf("request %d message %d does not match entry %s", requestIndex, i, id)
			}
		}
	}
}

func historyContainsCompaction(history []*agent.Message) bool {
	for _, message := range history {
		if message == nil || message.User == nil {
			continue
		}
		var text string
		if err := json.Unmarshal(message.User.Content, &text); err != nil {
			continue
		}
		if strings.Contains(text, "[compaction ") {
			return true
		}
	}
	return false
}

func TestProjectionEntryIDsAlignAcrossToolTurnsAndCompaction(t *testing.T) {
	fixture, hooks := newProjectionFixture(t, []*agent.AssistantMessage{
		toolUseWithText("call_1", "probe", `{}`, "turn one"),
		textStop("turn one done"),
		toolUseWithText("call_2", "probe", `{}`, "turn two"),
		textStop("turn two done"),
		toolUseWithText("call_3", "probe", `{}`, "turn three"),
		textStop("turn three done"),
	}, []agent.Tool{&probeTool{calls: new(int)}})
	installProjectionPreparer(t, fixture)
	path := fixture.bridge.rd.sessionPath

	fixture.bridge.handle("q1")
	fixture.bridge.handle("q2")
	fixture.bridge.rd.preparer.forceSafety()
	fixture.bridge.handle("q3")

	if len(fixture.bridge.history) != len(fixture.bridge.entryIDs) {
		t.Fatalf("bridge history %d entry ids %d must align", len(fixture.bridge.history), len(fixture.bridge.entryIDs))
	}
	requests := hooks.captured()
	if len(requests) != 6 {
		t.Fatalf("context requests = %d, want 6", len(requests))
	}
	assertCapturedEntryIDsResolve(t, path, requests)

	loader := p4Load(t, path)
	branch, err := loader.ActiveBranch()
	if err != nil {
		t.Fatal(err)
	}
	if anchor := unresolvedLatestCompactionAnchor(branch); anchor != "" {
		t.Fatalf("persisted compaction anchor %q is unresolved", anchor)
	}
	latest, _ := latestBranchCompaction(branch)
	if latest == nil {
		t.Fatal("expected a persisted compaction entry")
	}
	if _, ok := loader.Get(latest.FirstKeptEntryID); !ok {
		t.Fatalf("compaction anchor %q does not resolve", latest.FirstKeptEntryID)
	}
	resumedController := testController(t, fixture.store, fixture.cwd)
	t.Cleanup(func() { _ = resumedController.Close() })
	resumed, err := resumedController.PrepareOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range resumed.warnings {
		if strings.Contains(warning, "unresolved") {
			t.Fatalf("resume reported an unresolved anchor: %q", warning)
		}
	}
	if !historyContainsCompaction(resumed.history) {
		t.Fatal("resumed projection must include the compaction summary")
	}
}

func TestProjectionReloadFailureAbortsThenRepairs(t *testing.T) {
	fixture, hooks := newProjectionFixture(t, []*agent.AssistantMessage{textStop("first answer"), textStop("second answer")}, nil)
	installProjectionPreparer(t, fixture)
	path := fixture.bridge.rd.sessionPath

	fixture.bridge.handle("first")
	if len(fixture.bridge.entryIDs) != len(fixture.bridge.history) {
		t.Fatal("first turn must leave bridge history and entry ids aligned")
	}
	before := readFileString(t, path)
	beforeRequests := hooks.count()

	fixture.bridge.sessions.reload = func(string) (*session.Loader, error) { return nil, errors.New("reload boom") }
	fixture.bridge.handle("second")

	if !fixture.bridge.projectionStale {
		t.Fatal("a failed reload must leave the projection stale")
	}
	if got := readFileString(t, path); got != before {
		t.Fatal("a failed reload must preserve the persisted session file")
	}
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("in-memory history = %d messages, want the preserved two", len(fixture.bridge.history))
	}
	if hooks.count() != beforeRequests {
		t.Fatal("no context request may run with mismatched entry ids")
	}
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "cannot reload the active session") {
		t.Fatalf("reload failure was not surfaced:\n%s", text)
	}

	fixture.bridge.sessions.reload = nil
	fixture.bridge.handle("second")
	if fixture.bridge.projectionStale {
		t.Fatal("a repaired reload must clear the stale projection")
	}
	if len(fixture.bridge.history) != 4 {
		t.Fatalf("history after repair = %d messages, want 4", len(fixture.bridge.history))
	}
	if len(fixture.bridge.entryIDs) != len(fixture.bridge.history) {
		t.Fatal("repaired bridge history and entry ids must align")
	}
	assertCapturedEntryIDsResolve(t, path, hooks.captured())
}

func TestProjectionAfterTurnReloadFailureRecoversOnNextTurn(t *testing.T) {
	fixture, hooks := newProjectionFixture(t, []*agent.AssistantMessage{textStop("first answer"), textStop("second answer")}, nil)
	installProjectionPreparer(t, fixture)
	path := fixture.bridge.rd.sessionPath

	calls := 0
	fixture.bridge.sessions.reload = func(target string) (*session.Loader, error) {
		calls++
		if calls == 3 {
			return nil, errors.New("reload boom")
		}
		return session.LoadWithOptions(target, session.LoadOptions{Strict: true})
	}
	fixture.bridge.handle("first")
	if !fixture.bridge.projectionStale {
		t.Fatal("the failed after-turn reload must leave the projection stale")
	}
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("in-memory history = %d messages, want the completed two", len(fixture.bridge.history))
	}
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "cannot reload the active session") {
		t.Fatalf("after-turn reload failure was not surfaced:\n%s", text)
	}

	fixture.bridge.sessions.reload = nil
	fixture.bridge.handle("second")
	if fixture.bridge.projectionStale {
		t.Fatal("the recovery reload must clear the stale projection")
	}
	if len(fixture.bridge.history) != 4 || len(fixture.bridge.entryIDs) != 4 {
		t.Fatalf("recovered history %d entry ids %d, want four each", len(fixture.bridge.history), len(fixture.bridge.entryIDs))
	}
	assertCapturedEntryIDsResolve(t, path, hooks.captured())
}

func TestProjectionMidTurnReloadFailureSurfacesAndRecovers(t *testing.T) {
	fixture, hooks := newProjectionFixture(t, []*agent.AssistantMessage{textStop("first answer"), textStop("second answer")}, nil)
	installProjectionPreparer(t, fixture)
	path := fixture.bridge.rd.sessionPath

	calls := 0
	fixture.bridge.sessions.reload = func(target string) (*session.Loader, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("reload boom")
		}
		return session.LoadWithOptions(target, session.LoadOptions{Strict: true})
	}
	fixture.bridge.handle("first")
	if text := bridgeFrameText(t, fixture); !strings.Contains(text, "refresh session entry ids") {
		t.Fatalf("mid-turn reload failure was not surfaced:\n%s", text)
	}
	if len(fixture.bridge.history) != len(fixture.bridge.entryIDs) {
		t.Fatal("the recovered projection must keep bridge history and entry ids aligned")
	}

	fixture.bridge.sessions.reload = nil
	fixture.bridge.handle("second")
	if len(fixture.bridge.history) != 3 || len(fixture.bridge.entryIDs) != 3 {
		t.Fatalf("history %d entry ids %d, want three each", len(fixture.bridge.history), len(fixture.bridge.entryIDs))
	}
	assertCapturedEntryIDsResolve(t, path, hooks.captured())
}

func TestProjectionReloadFailurePreservesInMemoryHistory(t *testing.T) {
	fixture, _ := newProjectionFixture(t, []*agent.AssistantMessage{textStop("answer")}, nil)
	installProjectionPreparer(t, fixture)

	fixture.bridge.handle("question")
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("history = %d messages, want 2", len(fixture.bridge.history))
	}
	fixture.bridge.sessions.reload = func(string) (*session.Loader, error) { return nil, errors.New("reload boom") }
	if err := fixture.bridge.refreshProjection(); err == nil {
		t.Fatal("a failed reload must return an error")
	}
	if !fixture.bridge.projectionStale {
		t.Fatal("a failed reload must mark the projection stale")
	}
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("pending in-memory history = %d messages, want 2", len(fixture.bridge.history))
	}
}

func TestRefreshEntryIDsRejectsMisalignedHistory(t *testing.T) {
	fixture, _ := newProjectionFixture(t, nil, nil)
	installProjectionPreparer(t, fixture)

	if _, err := fixture.bridge.refreshEntryIDs([]*agent.Message{{User: &agent.UserMessage{Role: "user"}}}); err == nil {
		t.Fatal("misaligned history must be rejected")
	} else if !strings.Contains(err.Error(), "do not align") {
		t.Fatalf("misalignment error = %v", err)
	}
}

func TestSessionControllerRefreshGuards(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, t.TempDir())
	if _, err := controller.Refresh(); err == nil {
		t.Fatal("refreshing without an active session must fail")
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Refresh(); !errors.Is(err, errSessionControllerClosed) {
		t.Fatalf("refresh after close = %v, want closed", err)
	}
}

func TestSessionControllerRefreshReloadAndProjectionErrors(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, sess, "alpha")
	controller := testController(t, store, cwd)
	t.Cleanup(func() { _ = controller.Close() })
	active := controller.mustPrepareOpen(t, sess.Path())
	if err := controller.Commit(active); err != nil {
		t.Fatal(err)
	}

	controller.reload = func(string) (*session.Loader, error) { return nil, errors.New("reload boom") }
	if _, err := controller.Refresh(); err == nil {
		t.Fatal("a reload failure must surface")
	}
	controller.reload = nil

	if err := active.sess.AppendEntry(&session.MessageEntry{Message: json.RawMessage(`{"role":"weird"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Refresh(); err == nil {
		t.Fatal("a projection failure must surface")
	}
}

func TestSessionControllerRefreshRejectsChangedActive(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	first, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, first, "first")
	controller := testController(t, store, cwd)
	t.Cleanup(func() { _ = controller.Close() })
	active := controller.mustPrepareOpen(t, first.Path())
	if err := controller.Commit(active); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { active.close() })
	controller.reload = func(string) (*session.Loader, error) {
		controller.mu.Lock()
		controller.current = &activeSession{}
		controller.mu.Unlock()
		return session.LoadWithOptions(active.path, session.LoadOptions{Strict: true})
	}
	if _, err := controller.Refresh(); err == nil {
		t.Fatal("a changed active session during reload must surface")
	}
}

func (c *sessionController) mustPrepareOpen(t *testing.T, path string) *activeSession {
	t.Helper()
	active, err := c.PrepareOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	return active
}
