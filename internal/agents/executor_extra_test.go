package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/session"
)

func TestNewExecutorValidation(t *testing.T) {
	base := Dependencies{
		Catalog:      func() Catalog { return Catalog{} },
		ParentTools:  func() agent.ToolCatalog { return nil },
		Client:       func(Definition, Parent) (Client, error) { return Client{}, nil },
		Preparer:     func(string, string, agent.Client) (Preparer, error) { return nil, nil },
		SessionsRoot: t.TempDir(),
		Cwd:          t.TempDir(),
	}
	cases := []struct {
		name   string
		mutate func(*Dependencies)
		match  string
	}{
		{"no catalog", func(d *Dependencies) { d.Catalog = nil }, "catalog provider"},
		{"no parent tools", func(d *Dependencies) { d.ParentTools = nil }, "parent tool catalog"},
		{"no client", func(d *Dependencies) { d.Client = nil }, "client factory"},
		{"no preparer", func(d *Dependencies) { d.Preparer = nil }, "preparer factory"},
		{"no root", func(d *Dependencies) { d.SessionsRoot = "  " }, "session root"},
		{"no cwd", func(d *Dependencies) { d.Cwd = "" }, "working directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := base
			tc.mutate(&deps)
			_, err := NewExecutor(deps)
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("error = %v, want %q", err, tc.match)
			}
		})
	}
	exec, err := NewExecutor(base)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	if exec.deps.DepthLimit != DefaultDepthLimit {
		t.Fatalf("default depth = %d", exec.deps.DepthLimit)
	}
	base.DepthLimit = 2
	exec, err = NewExecutor(base)
	if err != nil {
		t.Fatalf("NewExecutor custom depth: %v", err)
	}
	if exec.deps.DepthLimit != 2 {
		t.Fatalf("custom depth = %d", exec.deps.DepthLimit)
	}
}

func TestRunNilContextAndFactoryFailures(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	res, err := fixture.exec.Run(nil, Request{Name: "reader", Task: "t", Parent: defaultParents()})
	if err != nil {
		t.Fatalf("nil context run: %v", err)
	}
	if res.Answer != "child answer" {
		t.Fatalf("answer = %q", res.Answer)
	}
	factoryErr := errors.New("factory exploded")
	fixture.exec.deps.Client = func(Definition, Parent) (Client, error) { return Client{}, factoryErr }
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()}); !errors.Is(err, factoryErr) {
		t.Fatalf("factory error = %v", err)
	}
	fixture.exec.deps.Client = func(Definition, Parent) (Client, error) { return Client{}, nil }
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()}); err == nil || !strings.Contains(err.Error(), "no client") {
		t.Fatalf("nil client error = %v", err)
	}
	fixture.exec.deps.Client = func(def Definition, parent Parent) (Client, error) {
		return Client{Client: fixture.client, Model: "test/model", Wire: "test/wire"}, nil
	}
	preparerErr := errors.New("preparer exploded")
	fixture.exec.deps.Preparer = func(string, string, agent.Client) (Preparer, error) { return nil, preparerErr }
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()}); !errors.Is(err, preparerErr) {
		t.Fatalf("preparer error = %v", err)
	}
}

func TestRunSessionCreationFailure(t *testing.T) {
	rootFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(rootFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := newToggleCatalog()
	exec, err := NewExecutor(Dependencies{
		Catalog:     func() Catalog { return executorCatalog(t, map[string]string{"reader": "child body"}) },
		ParentTools: func() agent.ToolCatalog { return tools },
		Client: func(Definition, Parent) (Client, error) {
			return Client{Client: &scriptClient{reps: []string{"answer"}}, Model: "m", Wire: "m"}, nil
		},
		Preparer: func(string, string, agent.Client) (Preparer, error) {
			return &fakePreparer{}, nil
		},
		SessionsRoot: rootFile,
		Cwd:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	if _, err := exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()}); err == nil {
		t.Fatal("session creation failure was not reported")
	}
}

func TestRunEmptyParentSystemUsesBodyOnly(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	parent := defaultParents()
	parent.System = ""
	_, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: parent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fixture.client.lastRequest().System; got != "child body" {
		t.Fatalf("child system = %q", got)
	}
}

func TestFinalAnswerBranches(t *testing.T) {
	history := []*agent.Message{
		nil,
		{Assistant: &agent.AssistantMessage{StopReason: "stop"}},
		{Assistant: &agent.AssistantMessage{StopReason: "error", ErrorMessage: "provider failed"}},
	}
	if got := finalAnswer(history, nil); got != "provider failed" {
		t.Fatalf("provider error answer = %q", got)
	}
	if got := finalAnswer(nil, errors.New("stream failed")); got != "stream failed" {
		t.Fatalf("run error answer = %q", got)
	}
	if got := finalAnswer(nil, nil); got != "" {
		t.Fatalf("empty answer = %q", got)
	}
	withText := []*agent.Message{{Assistant: &agent.AssistantMessage{Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: " real answer "}}}}}
	if got := finalAnswer(withText, nil); got != "real answer" {
		t.Fatalf("text answer = %q", got)
	}
}

func TestResultIdentityFallsBackToWire(t *testing.T) {
	got := resultIdentity(Result{Name: "reader", Tier: "user", Origin: "user:agents", Depth: 3, Wire: "wire/model"})
	if !strings.Contains(got, "model=wire/model") {
		t.Fatalf("identity = %q", got)
	}
}

func TestBoundAnswerTempFileFailure(t *testing.T) {
	exec := &Executor{deps: Dependencies{TempDir: filepath.Join(t.TempDir(), "missing", "dir")}}
	text := strings.Repeat("line\n", childMaxLines+10)
	display, path, truncated := exec.boundAnswer(text)
	if !truncated || path != "" {
		t.Fatalf("truncation = %v path=%q", truncated, path)
	}
	if !strings.Contains(display, "could not be saved") {
		t.Fatalf("display = %q", display)
	}
}

func TestHeadBoundAndClampUTF8(t *testing.T) {
	if got := headBound(strings.Repeat("a", childMaxBytes) + "\nb"); len(got) != childMaxBytes {
		t.Fatalf("head bound length = %d", len(got))
	}
	if got := headBound(strings.Repeat("a", childMaxBytes+10)); len(got) != childMaxBytes {
		t.Fatalf("clamped head length = %d", len(got))
	}
	if got := clampUTF8("héllo", 2); got != "h" {
		t.Fatalf("clampUTF8 = %q", got)
	}
	if got := clampUTF8("hello", 9); got != "hello" {
		t.Fatalf("clampUTF8 passthrough = %q", got)
	}
	if got := clampUTF8("é", 0); got != "" {
		t.Fatalf("clampUTF8 zero = %q", got)
	}
}

func TestChildSessionHelperBranches(t *testing.T) {
	var missing *childSession
	missing.close()
	(&childSession{}).close()
	if err := (&childSession{}).appendCompaction(nil); err != nil {
		t.Fatalf("nil compaction: %v", err)
	}
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	child, err := fixture.exec.newChildSession(Definition{Name: "reader", Tier: content.TierBundle}, defaultParents())
	if err != nil {
		t.Fatalf("newChildSession: %v", err)
	}
	defer child.close()
	history := []*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"a"`)}},
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"b"`)}},
	}
	if _, err := child.refreshEntryIDs(history); err == nil || !strings.Contains(err.Error(), "do not align") {
		t.Fatalf("alignment error = %v", err)
	}
}

func TestChildSessionDirValidation(t *testing.T) {
	if _, err := childSessionDir("", "parent"); err == nil {
		t.Fatal("empty root accepted")
	}
	root := t.TempDir()
	dir, err := childSessionDir(root, "parent")
	if err != nil {
		t.Fatalf("childSessionDir: %v", err)
	}
	if !strings.Contains(dir, subagentSessionsDir) {
		t.Fatalf("dir = %q", dir)
	}
	linkRoot := t.TempDir()
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(linkRoot, subagentSessionsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := childSessionDir(linkRoot, "parent"); err == nil || !strings.Contains(err.Error(), "escapes the session root") {
		t.Fatalf("symlinked parent error = %v", err)
	}
	finalRoot := t.TempDir()
	finalDir := filepath.Join(finalRoot, subagentSessionsDir, "parent")
	if err := os.MkdirAll(finalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(finalDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, finalDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := childSessionDir(finalRoot, "parent"); err == nil || !strings.Contains(err.Error(), "plain directory") {
		t.Fatalf("symlinked final error = %v", err)
	}
}

func TestChildSessionDirRejectsSymlinksBeforeWrite(t *testing.T) {
	root := t.TempDir()
	victim := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(root, subagentSessionsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := childSessionDir(root, "parent"); err == nil || !strings.Contains(err.Error(), "escapes the session root") {
		t.Fatalf("symlinked parent error = %v", err)
	}
	if entries, err := os.ReadDir(victim); err != nil || len(entries) != 0 {
		t.Fatalf("victim directory changed through symlink: %v err=%v", entries, err)
	}
	middleRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(middleRoot, subagentSessionsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(middleRoot, subagentSessionsDir, "parent")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := childSessionDir(middleRoot, "parent"); err == nil || !strings.Contains(err.Error(), "escapes the session root") {
		t.Fatalf("symlinked component error = %v", err)
	}
	if entries, err := os.ReadDir(victim); err != nil || len(entries) != 0 {
		t.Fatalf("victim directory changed through component symlink: %v err=%v", entries, err)
	}
}

func TestChildSessionLockLifetimeAndModes(t *testing.T) {
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	child, err := fixture.exec.newChildSession(Definition{Name: "reader", Tier: content.TierBundle}, defaultParents())
	if err != nil {
		t.Fatalf("newChildSession: %v", err)
	}
	childRoot := filepath.Join(fixture.root, subagentSessionsDir, "parent123")
	if !strings.HasPrefix(child.path, childRoot+string(os.PathSeparator)) {
		t.Fatalf("child path = %q, want under %q", child.path, childRoot)
	}
	info, err := os.Stat(child.path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("child file mode = %v", info.Mode())
	}
	for _, dir := range []string{filepath.Join(fixture.root, subagentSessionsDir), childRoot, filepath.Dir(child.path)} {
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("dir %s mode = %v", dir, dirInfo.Mode().Perm())
		}
	}
	store, err := session.NewStore(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(child.path, session.OpenOptions{Strict: true}); err == nil || !strings.Contains(err.Error(), "held by another writer") {
		t.Fatalf("second open while active = %v", err)
	}
	child.close()
	reopened, err := store.Open(child.path, session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	if reopened.Path() != child.path {
		t.Fatalf("reopened path = %q, want %q", reopened.Path(), child.path)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestChildSessionStaysOutOfParentBrowser(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	parent, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := parent.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}); err != nil {
		t.Fatal(err)
	}
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	fixture.exec.deps.SessionsRoot = store.Root()
	fixture.exec.deps.Cwd = cwd
	res, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(res.SessionPath, filepath.Join(store.Root(), subagentSessionsDir)) {
		t.Fatalf("child path = %q", res.SessionPath)
	}
	listed, err := store.List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0] != parent.Path() {
		t.Fatalf("parent browser list = %v, want only %q", listed, parent.Path())
	}
}

func TestDiscardChildCandidateRefusesReplacements(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "child.jsonl")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := discardChildCandidate(nil, path, nil); err != nil {
		t.Fatalf("discard existing candidate: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("candidate was not removed")
	}
	if err := os.Symlink(filepath.Join(dir, "target"), path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := discardChildCandidate(nil, path, nil); err == nil || !strings.Contains(err.Error(), "refusing to remove") {
		t.Fatalf("symlink discard error = %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("symlink candidate was removed: %v", err)
	}
}

func TestChildSessionPermissionFailureLeavesNoArtifacts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	if _, err := session.NewStore(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, subagentSessionsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, subagentSessionsDir)
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o700)
	fixture := newExecutorFixture(t, map[string]string{"reader": "child body"}, newToggleCatalog())
	fixture.exec.deps.SessionsRoot = root
	if _, err := fixture.exec.Run(context.Background(), Request{Name: "reader", Task: "t", Parent: defaultParents()}); err == nil {
		t.Fatal("permission failure was not reported")
	}
	var artifacts []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			artifacts = append(artifacts, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("failed child creation left artifacts: %v", artifacts)
	}
}

func TestSafeSessionDir(t *testing.T) {
	cases := map[string]string{
		"":        "unknown",
		"   ":     "unknown",
		".":       "unknown",
		"..":      "unknown",
		"abc-123": "abc-123",
		"a/b":     "a_b",
		"~!@":     "___",
	}
	for input, want := range cases {
		if got := safeSessionDir(input); got != want {
			t.Errorf("safeSessionDir(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestChildSessionStorageErrorBranches(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	parentDir := filepath.Join(root, subagentSessionsDir, "parent123")
	if err := os.MkdirAll(parentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parentDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parentDir, 0o700)
	exec := &Executor{deps: Dependencies{SessionsRoot: root, Cwd: t.TempDir(), DepthLimit: DefaultDepthLimit}}
	if _, err := exec.newChildSession(Definition{Name: "reader", Tier: content.TierBundle}, defaultParents()); err == nil {
		t.Fatal("an unwritable child directory did not fail session creation")
	}
}

func TestRefreshEntryIDsProjectFailure(t *testing.T) {
	child := &childSession{path: filepath.Join(t.TempDir(), "missing.jsonl")}
	if _, err := child.refreshEntryIDs(nil); err == nil {
		t.Fatal("a missing child session did not fail entry refresh")
	}
	child = &childSession{path: filepath.Join(t.TempDir(), "garbage.jsonl")}
	if err := os.WriteFile(child.path, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := child.refreshEntryIDs(nil); err == nil {
		t.Fatal("a malformed child session did not fail entry refresh")
	}
}

func TestDiscardChildCandidateBranches(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := discardChildCandidate(nil, "", nil); err != nil {
		t.Fatalf("empty path: %v", err)
	}
	if err := discardChildCandidate(nil, filepath.Join(dir, "missing.jsonl"), nil); err != nil {
		t.Fatalf("missing path: %v", err)
	}
	regular := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := discardChildCandidate(nil, filepath.Join(regular, "child.jsonl"), nil); err == nil {
		t.Fatal("an invalid parent path did not fail discard")
	}
	first := filepath.Join(dir, "first.jsonl")
	second := filepath.Join(dir, "second.jsonl")
	if err := os.WriteFile(first, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := os.Lstat(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := discardChildCandidate(nil, second, identity); err == nil {
		t.Fatal("a replaced candidate was discarded")
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("the replacement was removed: %v", err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	removable := filepath.Join(locked, "child.jsonl")
	if err := os.WriteFile(removable, []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o700)
	if err := discardChildCandidate(nil, removable, nil); err == nil {
		t.Fatal("an unremovable candidate did not fail discard")
	}
}

func TestChildSessionDirMissingRoot(t *testing.T) {
	if _, err := childSessionDir(filepath.Join(t.TempDir(), "missing"), "parent"); err == nil {
		t.Fatal("a missing session root was accepted")
	}
}

func TestProjectChildLoadFailures(t *testing.T) {
	if _, _, err := projectChild(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Fatal("a missing child session was projected")
	}
	garbage := filepath.Join(t.TempDir(), "garbage.jsonl")
	if err := os.WriteFile(garbage, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := projectChild(garbage); err == nil {
		t.Fatal("a malformed child session was projected")
	}
}

func TestIsProviderErrorAssistantBranches(t *testing.T) {
	if isProviderErrorAssistant(nil) {
		t.Fatal("a nil message was treated as a provider error")
	}
	if isProviderErrorAssistant(&agent.Message{}) {
		t.Fatal("a message without an assistant was treated as a provider error")
	}
	if isProviderErrorAssistant(&agent.Message{Assistant: &agent.AssistantMessage{StopReason: "stop"}}) {
		t.Fatal("a completed assistant was treated as a provider error")
	}
	if !isProviderErrorAssistant(&agent.Message{Assistant: &agent.AssistantMessage{StopReason: "error", ErrorMessage: "boom"}}) {
		t.Fatal("a provider error assistant was not detected")
	}
	withCall := &agent.Message{Assistant: &agent.AssistantMessage{StopReason: "error", Content: []agent.ContentBlock{{Type: agent.BlockTypeToolCall}}}}
	if isProviderErrorAssistant(withCall) {
		t.Fatal("an assistant error with a tool call was treated as a provider error")
	}
}

func TestProjectChildCompactionAndProviderErrors(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	assistant := &agent.AssistantMessage{Role: string(agent.RoleAssistant), StopReason: "error", ErrorMessage: "boom"}
	if err := sess.AppendAssistant(assistant); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: `{"s":1}`, FirstKeptEntryID: "", TokensBefore: 5}); err != nil {
		t.Fatal(err)
	}
	user := &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"task"`)}
	if err := sess.AppendUser(user); err != nil {
		t.Fatal(err)
	}
	history, ids, err := projectChild(sess.Path())
	if err != nil {
		t.Fatalf("projectChild: %v", err)
	}
	if len(history) != 2 || len(ids) != 2 {
		t.Fatalf("history = %d ids = %d", len(history), len(ids))
	}
	if history[0].User == nil || !strings.Contains(string(history[0].User.Content), "[compaction") {
		t.Fatalf("compaction message = %+v", history[0])
	}
	if history[1].User == nil || string(history[1].User.Content) != `"task"` {
		t.Fatalf("user message = %+v", history[1])
	}

	broken, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	if err := broken.AppendEntry(&session.MessageEntry{Message: json.RawMessage(`{"role":"bogus"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := projectChild(broken.Path()); err == nil {
		t.Fatal("malformed entry was projected")
	}
}

type recordingHooks struct {
	calls []string
}

func (h *recordingHooks) Context(context.Context, agent.ContextRequest) (agent.ContextResult, error) {
	h.calls = append(h.calls, "context")
	return agent.ContextResult{}, nil
}

func (h *recordingHooks) MessageEnd(context.Context, *agent.Message) (*agent.Message, error) {
	h.calls = append(h.calls, "message_end")
	return nil, nil
}

func (h *recordingHooks) AutoRetryStart(context.Context, int, int, int64, string) error {
	h.calls = append(h.calls, "retry_start")
	return nil
}

func (h *recordingHooks) AutoRetryEnd(context.Context, bool, int, string) error {
	h.calls = append(h.calls, "retry_end")
	return nil
}

func (h *recordingHooks) ToolCall(context.Context, string, string, json.RawMessage) (agent.ToolCallDecision, error) {
	h.calls = append(h.calls, "tool_call")
	return agent.ToolCallDecision{}, nil
}

func (h *recordingHooks) ToolResult(context.Context, string, string, json.RawMessage, agent.Result) (agent.Result, error) {
	h.calls = append(h.calls, "tool_result")
	return agent.Result{}, nil
}

func (h *recordingHooks) SessionStart(context.Context, string) error {
	h.calls = append(h.calls, "session_start")
	return nil
}

func (h *recordingHooks) SessionShutdown(context.Context, string) error {
	h.calls = append(h.calls, "session_shutdown")
	return nil
}

func TestProgressHooksForwardAndEmit(t *testing.T) {
	inner := &recordingHooks{}
	var events []Event
	hooks := &progressHooks{inner: inner, onEvent: func(event Event) { events = append(events, event) }}
	ctx := context.Background()
	if _, err := hooks.Context(ctx, agent.ContextRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.MessageEnd(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := hooks.AutoRetryStart(ctx, 1, 3, 10, "e"); err != nil {
		t.Fatal(err)
	}
	if err := hooks.AutoRetryEnd(ctx, true, 1, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.ToolCall(ctx, "read", "id", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.ToolResult(ctx, "read", "id", nil, agent.Result{}); err != nil {
		t.Fatal(err)
	}
	if err := hooks.SessionStart(ctx, "start"); err != nil {
		t.Fatal(err)
	}
	if err := hooks.SessionShutdown(ctx, "stop"); err != nil {
		t.Fatal(err)
	}
	want := "context,message_end,retry_start,retry_end,tool_call,tool_result,session_start,session_shutdown"
	if strings.Join(inner.calls, ",") != want {
		t.Fatalf("forwarded calls = %v", inner.calls)
	}
	if len(events) != 2 || events[0].Kind != EventToolCall || events[1].Kind != EventToolResult {
		t.Fatalf("events = %+v", events)
	}
	bare := &progressHooks{}
	bare.onEvent = func(Event) {}
	if _, err := bare.Context(ctx, agent.ContextRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := bare.MessageEnd(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := bare.AutoRetryStart(ctx, 1, 1, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := bare.AutoRetryEnd(ctx, false, 1, ""); err != nil {
		t.Fatal(err)
	}
	if decision, err := bare.ToolCall(ctx, "x", "id", nil); err != nil || decision.Block {
		t.Fatal("bare ToolCall")
	}
	if _, err := bare.ToolResult(ctx, "x", "id", nil, agent.Result{}); err != nil {
		t.Fatal(err)
	}
	if err := bare.SessionStart(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if err := bare.SessionShutdown(ctx, "s"); err != nil {
		t.Fatal(err)
	}
}

type plainCatalog struct {
	tools []agent.Tool
}

func (c *plainCatalog) Tools() []agent.Tool { return c.tools }

func (c *plainCatalog) Get(name string) (agent.Tool, bool) {
	for _, tool := range c.tools {
		if tool.Name() == name {
			return tool, true
		}
	}
	return nil, false
}

func TestChildToolsGetAndFallbackCatalog(t *testing.T) {
	read := &probeTool{name: "read"}
	write := &probeTool{name: "write"}
	parent := &plainCatalog{tools: []agent.Tool{read, write}}
	tools := &childTools{parent: parent, allowed: map[string]struct{}{"read": {}}}
	if _, ok := tools.Get("read"); !ok {
		t.Fatal("allowed tool missing")
	}
	if _, ok := tools.Get("write"); ok {
		t.Fatal("disallowed tool returned")
	}
	if names := toolNames(tools.Tools()); strings.Join(names, ",") != "read" {
		t.Fatalf("tools = %v", names)
	}
	if _, ok := tools.GetActive("read"); !ok {
		t.Fatal("active fallback missing")
	}
	if _, ok := tools.GetActive("missing"); ok {
		t.Fatal("unknown tool returned")
	}
	openTools := &childTools{parent: parent}
	if !openTools.allows("anything") {
		t.Fatal("nil allowed map must allow")
	}
	var nilTools *childTools
	if nilTools.allows("read") {
		t.Fatal("nil receiver must not allow")
	}
	nested := NewTool(nil)
	tools.nested = nested
	if got, ok := tools.Get("subagent"); ok || got != nil {
		t.Fatalf("subagent without a parent tool = %v %v", got, ok)
	}
	parentWithSubagent := &plainCatalog{tools: []agent.Tool{read, nested}}
	withNested := &childTools{parent: parentWithSubagent, nested: nested}
	if got, ok := withNested.Get("subagent"); !ok || got != nested {
		t.Fatal("nested subagent not substituted")
	}
	if got, ok := withNested.GetActive("subagent"); !ok || got != nested {
		t.Fatal("nested subagent not active")
	}
	names := toolNames(withNested.Tools())
	if strings.Join(names, ",") != "read,subagent" {
		t.Fatalf("nested tools = %v", names)
	}
	var nilTool *Tool
	nilTool.SetInvoke(func(context.Context, string, string) agent.Result { return agent.Result{} })
	if nilTool != nil {
		t.Fatal("nil tool pointer")
	}
}

func TestChildToolsReactivationKeepsDepthScoping(t *testing.T) {
	read := &probeTool{name: "read"}
	root := NewTool(nil)
	tools := newToggleCatalog(read, root)
	tools.disable(SubagentToolName)
	fixture := newExecutorFixture(t, map[string]string{"mid": "mid body"}, tools)
	definition, ok := executorCatalog(t, map[string]string{"mid": "mid body"}).Lookup("mid")
	if !ok {
		t.Fatal("mid definition missing")
	}
	client := Client{Client: &scriptClient{}, Model: "test/model", Wire: "test/wire"}
	view, err := fixture.exec.buildTools(definition, defaultParents(), client)
	if err != nil {
		t.Fatalf("buildTools: %v", err)
	}
	if view.nested != nil {
		t.Fatal("inactive parent built a nested wrapper eagerly")
	}
	if _, ok := view.GetActive(SubagentToolName); ok {
		t.Fatal("inactive parent exposed the builtin delegation")
	}
	scoped, ok := view.Get(SubagentToolName)
	if !ok || scoped == nil || scoped == agent.Tool(root) {
		t.Fatalf("non-active lookup = %v ok=%v, want a depth-scoped wrapper", scoped, ok)
	}
	if strings.Contains(strings.Join(toolNames(view.Tools()), ","), SubagentToolName) {
		t.Fatal("inactive parent advertised the builtin delegation")
	}
	for i := 0; i < 10; i++ {
		tools.enable(SubagentToolName)
		active, ok := view.GetActive(SubagentToolName)
		if !ok || active == nil || active == agent.Tool(root) {
			t.Fatalf("toggle %d active = %v ok=%v, want a depth-scoped wrapper", i, active, ok)
		}
		names := toolNames(view.Tools())
		if strings.Join(names, ",") != "read,subagent" {
			t.Fatalf("toggle %d tools = %v", i, names)
		}
		tools.disable(SubagentToolName)
		if _, ok := view.GetActive(SubagentToolName); ok {
			t.Fatalf("toggle %d disabled parent exposed the builtin delegation", i)
		}
	}
	tools.enable(SubagentToolName)
	bare := &childTools{parent: tools}
	if got, ok := bare.Get(SubagentToolName); ok || got != nil {
		t.Fatalf("builder-less view Get = %v ok=%v, want denial", got, ok)
	}
	if got, ok := bare.GetActive(SubagentToolName); ok || got != nil {
		t.Fatalf("builder-less view GetActive = %v ok=%v, want denial", got, ok)
	}
	if strings.Contains(strings.Join(toolNames(bare.Tools()), ","), SubagentToolName) {
		t.Fatal("builder-less view advertised the root builtin")
	}
	override := &probeTool{name: SubagentToolName}
	overrideFixture := newExecutorFixture(t, map[string]string{"mid": "mid body"}, newToggleCatalog(read, override))
	overrideView, err := overrideFixture.exec.buildTools(definition, defaultParents(), client)
	if err != nil {
		t.Fatalf("buildTools override: %v", err)
	}
	if got, ok := overrideView.GetActive(SubagentToolName); !ok || got != agent.Tool(override) {
		t.Fatalf("extension override GetActive = %v ok=%v, want the identical override", got, ok)
	}
	if got, ok := overrideView.Get(SubagentToolName); !ok || got != agent.Tool(override) {
		t.Fatalf("extension override Get = %v ok=%v, want the identical override", got, ok)
	}
	advertised := false
	for _, tool := range overrideView.Tools() {
		if tool == agent.Tool(override) {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("extension override was not advertised unchanged")
	}
	if _, err := fixture.exec.buildTools(definition, Parent{Depth: 2}, client); err == nil || !strings.Contains(err.Error(), "immediate parent tool view") {
		t.Fatalf("nested build without an immediate view = %v", err)
	}
	limited, ok := executorCatalog(t, map[string]string{"limited": "---\ntools: [read]\n---\nlimited body"}).Lookup("limited")
	if !ok {
		t.Fatal("limited definition missing")
	}
	tools.disable("read")
	if _, err := fixture.exec.buildTools(limited, defaultParents(), client); err == nil || !strings.Contains(err.Error(), `tool "read" is not active`) {
		t.Fatalf("inactive allowlist tool error = %v", err)
	}
}
