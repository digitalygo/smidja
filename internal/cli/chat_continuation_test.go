package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/internal/session"
)

type turnStep struct {
	err error
	msg *agent.AssistantMessage
}

type stepClient struct {
	steps []turnStep
	calls int
	reqs  []*agent.TurnRequest
}

func (c *stepClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.reqs = append(c.reqs, req)
	if c.calls >= len(c.steps) {
		return nil, errors.New("stepClient: script exhausted")
	}
	step := c.steps[c.calls]
	c.calls++
	if step.err != nil {
		return nil, step.err
	}
	if step.msg != nil {
		for _, block := range step.msg.Content {
			switch block.Type {
			case agent.BlockTypeText:
				if onText != nil {
					onText(block.Text)
				}
			case agent.BlockTypeThinking:
				if onThinking != nil {
					onThinking(block.Thinking)
				}
			}
		}
	}
	return step.msg, nil
}

func continuationProviderError(message string) *agent.AssistantMessage {
	return &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		StopReason:   "error",
		ErrorMessage: message,
		Timestamp:    1,
	}
}

func transportOverflowError() error {
	return errors.New("openrouter: 400: prompt is too long")
}

func seedContinuationHistory(t *testing.T, sess *session.Session, turns int) {
	t.Helper()
	for i := 0; i < turns; i++ {
		userContent := json.RawMessage(strconv.Quote(strings.Repeat("x", 2500)))
		if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: userContent, Timestamp: int64(2*i + 1)}); err != nil {
			t.Fatal(err)
		}
		assistant := &agent.AssistantMessage{
			Role:       string(agent.RoleAssistant),
			Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: strings.Repeat("y", 2500)}},
			StopReason: "stop",
			Timestamp:  int64(2*i + 2),
		}
		if err := sess.AppendAssistant(assistant); err != nil {
			t.Fatal(err)
		}
	}
}

func continuationPreparer(t *testing.T) *contextPreparerAdapter {
	t.Helper()
	cfg := contextmanager.Config{
		Enabled:                true,
		ContextWindowTokens:    10_000,
		CacheMissAfter:         contextmanager.DefaultCacheMissAfter,
		PruneThreshold:         contextmanager.DefaultPruneThreshold,
		CompactThreshold:       contextmanager.DefaultCompactThreshold,
		SafetyCompactThreshold: contextmanager.DefaultSafetyCompactThreshold,
		CompactTarget:          contextmanager.DefaultCompactTarget,
		KeepRecentMessages:     2,
		SelectorChunkTokens:    contextmanager.DefaultSelectorChunkTokens,
		SelectorModel:          "test/model",
	}
	live, err := contextmanager.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return newContextPreparerAdapter(live, cfg)
}

func captureHookRequests(t *testing.T) (*capturingContextHooks, agent.HookDispatcher) {
	t.Helper()
	runtime := extensions.NewRuntime(extensions.NewRegistry())
	base := runtime.Dispatcher()
	return &capturingContextHooks{HookDispatcher: base}, base
}

func countPersistedUserMessages(t *testing.T, path, text string) int {
	t.Helper()
	loader := p4Load(t, path)
	count := 0
	for _, entry := range loader.Entries() {
		messageEntry, ok := entry.(*session.MessageEntry)
		if !ok {
			continue
		}
		message, err := messageEntry.DecodeMessage()
		if err != nil {
			t.Fatal(err)
		}
		if message.User == nil {
			continue
		}
		var decoded string
		if err := json.Unmarshal(message.User.Content, &decoded); err == nil && decoded == text {
			count++
		}
	}
	return count
}

func assertRequestsUseRealAlignedIDs(t *testing.T, path string, requests []contextRequest) {
	t.Helper()
	loader := p4Load(t, path)
	branch, err := loader.ActiveBranch()
	if err != nil {
		t.Fatal(err)
	}
	onBranch := make(map[string]bool, len(branch))
	for _, entry := range branch {
		onBranch[session.EntryID(entry)] = true
	}
	if len(requests) != 2 {
		t.Fatalf("context requests = %d, want one per provider call (2)", len(requests))
	}
	for requestIndex, request := range requests {
		if len(request.entryIDs) == 0 {
			t.Fatalf("request %d carried no entry ids", requestIndex)
		}
		if len(request.entryIDs) < len(request.messages) {
			t.Fatalf("request %d entry ids %d fewer than messages %d", requestIndex, len(request.entryIDs), len(request.messages))
		}
		seen := make(map[string]bool, len(request.entryIDs))
		for i, id := range request.entryIDs {
			if id == "" {
				t.Fatalf("request %d position %d has an empty entry id", requestIndex, i)
			}
			if seen[id] {
				t.Fatalf("request %d repeats entry id %q", requestIndex, id)
			}
			seen[id] = true
			entry, ok := loader.Get(id)
			if !ok {
				t.Fatalf("request %d entry id %q does not resolve", requestIndex, id)
			}
			if !onBranch[id] {
				t.Fatalf("request %d entry id %q is not on the active branch", requestIndex, id)
			}
			if len(request.entryIDs) != len(request.messages) {
				continue
			}
			messageEntry, ok := entry.(*session.MessageEntry)
			if !ok {
				continue
			}
			decoded, err := messageEntry.DecodeMessage()
			if err != nil {
				t.Fatalf("request %d entry %s decode: %v", requestIndex, id, err)
			}
			if !messagesEqual(decoded, request.messages[i]) {
				t.Fatalf("request %d message %d does not match the recorded entry %s", requestIndex, i, id)
			}
		}
	}
}

func assertContinuationResumeContext(t *testing.T, path, prompt, answer string) {
	t.Helper()
	loader := p4Load(t, path)
	history, entryIDs, warnings, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 || len(history) != len(entryIDs) {
		t.Fatalf("resume history %d entry ids %d must align and be non-empty", len(history), len(entryIDs))
	}
	branch, err := loader.ActiveBranch()
	if err != nil {
		t.Fatal(err)
	}
	if anchor := unresolvedLatestCompactionAnchor(branch); anchor != "" {
		t.Fatalf("persisted compaction anchor %q is unresolved", anchor)
	}
	latest, _ := latestBranchCompaction(branch)
	if latest == nil {
		t.Fatal("the overflow recovery must persist a compaction entry")
	}
	if _, ok := loader.Get(latest.FirstKeptEntryID); !ok {
		t.Fatalf("compaction anchor %q does not resolve", latest.FirstKeptEntryID)
	}
	text := p4HistoryText(history)
	if got := strings.Count(text, prompt); got != 1 {
		t.Fatalf("resume context has the prompt %d times, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, answer) {
		t.Fatalf("resume context is missing the recovered answer:\n%s", text)
	}
	if strings.Contains(text, "prompt is too long") {
		t.Fatalf("resume context kept the provider error text:\n%s", text)
	}
	for _, message := range history {
		if message != nil && message.Assistant != nil && message.Assistant.StopReason == "error" {
			t.Fatal("resume context kept a provider error assistant")
		}
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "unresolved") {
			t.Fatalf("resume reported an unresolved anchor: %q", warning)
		}
	}
}

func assertTranscriptPreservesError(t *testing.T, path, message string) {
	t.Helper()
	loader := p4Load(t, path)
	transcript, _ := projectTranscript(loader)
	for _, item := range transcript {
		if item.Kind == "assistant" && item.ErrorMessage == message {
			return
		}
	}
	t.Fatal("the provider error assistant must stay visible in the transcript")
}

func TestOverflowContinuationLineModeTransport(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	seedContinuationHistory(t, sess, 7)
	path := sess.Path()

	controller := testController(t, store, cwd)
	controller.Hold(sess)
	t.Cleanup(func() { _ = controller.Close() })

	hooks, _ := captureHookRequests(t)
	client := &stepClient{steps: []turnStep{
		{err: transportOverflowError()},
		{msg: textStop("recovered answer")},
	}}
	var stdout, stderr bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: path,
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		preparer:    continuationPreparer(t),
		hooks:       hooks,
		isOverflow:  retry.IsContextOverflow,
		controller:  controller,
		sess:        sess,
	}

	if err := runOnceContinued(context.Background(), d, sess, "new question"); err != nil {
		t.Fatalf("runOnceContinued: %v", err)
	}
	if client.calls != 2 {
		t.Fatalf("provider calls = %d, want exactly 2", client.calls)
	}
	if got := countPersistedUserMessages(t, path, "new question"); got != 1 {
		t.Fatalf("persisted user entries = %d, want exactly 1", got)
	}
	if !strings.Contains(stdout.String(), "recovered answer") {
		t.Fatalf("stdout = %q, want the recovered answer", stdout.String())
	}
	if !strings.Contains(stderr.String(), "compacting and retrying once") {
		t.Fatalf("stderr = %q, want the recovery notice", stderr.String())
	}
	assertRequestsUseRealAlignedIDs(t, path, hooks.captured())
	assertContinuationResumeContext(t, path, "new question", "recovered answer")
}

func TestOverflowContinuationLineModePersistedErrorAssistant(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	seedContinuationHistory(t, sess, 7)
	path := sess.Path()

	controller := testController(t, store, cwd)
	controller.Hold(sess)
	t.Cleanup(func() { _ = controller.Close() })

	hooks, _ := captureHookRequests(t)
	client := &stepClient{steps: []turnStep{
		{msg: continuationProviderError("prompt is too long")},
		{msg: textStop("recovered answer")},
	}}
	var stdout, stderr bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: path,
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		preparer:    continuationPreparer(t),
		hooks:       hooks,
		isOverflow:  retry.IsContextOverflow,
		controller:  controller,
		sess:        sess,
	}

	if err := runOnceContinued(context.Background(), d, sess, "new question"); err != nil {
		t.Fatalf("runOnceContinued: %v", err)
	}
	if client.calls != 2 {
		t.Fatalf("provider calls = %d, want exactly 2", client.calls)
	}
	if got := countPersistedUserMessages(t, path, "new question"); got != 1 {
		t.Fatalf("persisted user entries = %d, want exactly 1", got)
	}
	if !strings.Contains(stdout.String(), "recovered answer") {
		t.Fatalf("stdout = %q, want the recovered answer", stdout.String())
	}
	assertRequestsUseRealAlignedIDs(t, path, hooks.captured())
	assertContinuationResumeContext(t, path, "new question", "recovered answer")
	assertTranscriptPreservesError(t, path, "prompt is too long")
}

func newOverflowBridgeFixture(t *testing.T, client agent.Client) (*bridgeFixture, *capturingContextHooks) {
	t.Helper()
	fixture := newBridgeFixture(t, nil, nil)
	seedContinuationHistory(t, fixture.sess, 7)
	controller := testController(t, fixture.store, fixture.cwd)
	t.Cleanup(func() { _ = controller.Close() })
	active, err := controller.Adopt(fixture.sess, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	fixture.bridge.sessions = controller
	fixture.bridge.rd.controller = controller
	fixture.bridge.rd.sess = fixture.sess
	fixture.bridge.rd.client = client
	fixture.bridge.rd.recorder = active.recorder
	fixture.bridge.rd.preparer = continuationPreparer(t)
	fixture.bridge.rd.isOverflow = retry.IsContextOverflow
	fixture.bridge.history = active.history
	fixture.bridge.entryIDs = active.entryIDs
	hooks, _ := captureHookRequests(t)
	fixture.bridge.rd.hooks = hooks
	return fixture, hooks
}

func TestOverflowContinuationTUIPersistedErrorAssistant(t *testing.T) {
	client := &stepClient{steps: []turnStep{
		{msg: continuationProviderError("prompt is too long")},
		{msg: textStop("recovered answer")},
	}}
	fixture, hooks := newOverflowBridgeFixture(t, client)
	path := fixture.bridge.rd.sessionPath

	fixture.bridge.handle("new question")

	if client.calls != 2 {
		t.Fatalf("provider calls = %d, want exactly 2", client.calls)
	}
	if got := countPersistedUserMessages(t, path, "new question"); got != 1 {
		t.Fatalf("persisted user entries = %d, want exactly 1", got)
	}
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "new question") || !strings.Contains(text, "recovered answer") {
		t.Fatalf("frame missing the recovered turn:\n%s", text)
	}
	if len(fixture.bridge.history) != len(fixture.bridge.entryIDs) {
		t.Fatalf("bridge history %d entry ids %d must align", len(fixture.bridge.history), len(fixture.bridge.entryIDs))
	}
	assertRequestsUseRealAlignedIDs(t, path, hooks.captured())
	assertContinuationResumeContext(t, path, "new question", "recovered answer")
	assertTranscriptPreservesError(t, path, "prompt is too long")
}

func TestOverflowContinuationTUITransport(t *testing.T) {
	client := &stepClient{steps: []turnStep{
		{err: transportOverflowError()},
		{msg: textStop("recovered answer")},
	}}
	fixture, hooks := newOverflowBridgeFixture(t, client)
	path := fixture.bridge.rd.sessionPath

	fixture.bridge.handle("new question")

	if client.calls != 2 {
		t.Fatalf("provider calls = %d, want exactly 2", client.calls)
	}
	if got := countPersistedUserMessages(t, path, "new question"); got != 1 {
		t.Fatalf("persisted user entries = %d, want exactly 1", got)
	}
	if len(fixture.bridge.history) != len(fixture.bridge.entryIDs) {
		t.Fatalf("bridge history %d entry ids %d must align", len(fixture.bridge.history), len(fixture.bridge.entryIDs))
	}
	assertRequestsUseRealAlignedIDs(t, path, hooks.captured())
	assertContinuationResumeContext(t, path, "new question", "recovered answer")
}

func TestOverflowContinuationSecondOverflowFails(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	seedContinuationHistory(t, sess, 7)

	controller := testController(t, store, cwd)
	controller.Hold(sess)
	t.Cleanup(func() { _ = controller.Close() })

	client := &stepClient{steps: []turnStep{
		{err: transportOverflowError()},
		{err: transportOverflowError()},
	}}
	var stdout, stderr bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		preparer:    continuationPreparer(t),
		isOverflow:  retry.IsContextOverflow,
		controller:  controller,
		sess:        sess,
	}
	err = runOnceContinued(context.Background(), d, sess, "new question")
	if err == nil {
		t.Fatal("a second overflow must fail")
	}
	if !strings.Contains(err.Error(), "context still overflows the model window after compaction") {
		t.Fatalf("error = %q, want the existing overflow message", err.Error())
	}
	if client.calls != 2 {
		t.Fatalf("provider calls = %d, want exactly 2", client.calls)
	}
	if got := countPersistedUserMessages(t, sess.Path(), "new question"); got != 1 {
		t.Fatalf("persisted user entries = %d, want exactly 1", got)
	}
}
