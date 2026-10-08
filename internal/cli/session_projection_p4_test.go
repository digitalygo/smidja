package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

func p4SeedStore(t *testing.T) (*session.Store, string, *session.Session) {
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
	return store, cwd, sess
}

func p4Load(t *testing.T, path string) *session.Loader {
	t.Helper()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	return loader
}

func p4HistoryText(history []*agent.Message) string {
	var b strings.Builder
	for _, m := range history {
		if m == nil {
			continue
		}
		switch {
		case m.User != nil:
			b.WriteString(string(m.User.Content))
			b.WriteString("\n")
		case m.Assistant != nil:
			for _, block := range m.Assistant.Content {
				b.WriteString(block.Text)
				b.WriteString(block.Thinking)
			}
			b.WriteString("\n")
		case m.ToolResult != nil:
			b.WriteString(blocksText(m.ToolResult.Content))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestP4ValidCompactionProjection(t *testing.T) {
	_, cwd, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"first"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	loader := p4Load(t, sess.Path())
	firstID := session.EntryID(loader.Leaf())
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"second"`), Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: "keep this summary", FirstKeptEntryID: firstID, TokensBefore: 42}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"third"`), Timestamp: 3}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader = p4Load(t, path)
	history, entryIDs, warnings, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(entryIDs) {
		t.Fatalf("history %d entryIDs %d must align", len(history), len(entryIDs))
	}
	text := p4HistoryText(history)
	if !strings.Contains(text, "keep this summary") {
		t.Fatalf("history missing compaction summary:\n%s", text)
	}
	if !strings.Contains(text, "42") || !strings.Contains(text, firstID) {
		t.Fatalf("history missing compaction metadata:\n%s", text)
	}
	if !strings.Contains(text, "first") || !strings.Contains(text, "third") {
		t.Fatalf("history missing kept messages:\n%s", text)
	}
	for _, id := range entryIDs {
		if strings.Contains(id, "#") || strings.Contains(id, ":") {
			t.Fatalf("entry id %q looks invented", id)
		}
		if id == "" {
			t.Fatal("empty entry id")
		}
	}
	_ = warnings
	_ = cwd
}

func TestP4ValidBranchSummaryProjection(t *testing.T) {
	_, _, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"root"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	loader := p4Load(t, sess.Path())
	rootID := session.EntryID(loader.Leaf())
	if err := sess.AppendEntry(&session.BranchSummaryEntry{FromID: rootID, Summary: "abandoned branch truth"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"live"`), Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader = p4Load(t, path)
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(entryIDs) {
		t.Fatalf("history %d entryIDs %d must align", len(history), len(entryIDs))
	}
	text := p4HistoryText(history)
	if !strings.Contains(text, "abandoned branch truth") {
		t.Fatalf("history missing branch summary:\n%s", text)
	}
	if !strings.Contains(text, rootID) {
		t.Fatalf("history missing branch source metadata:\n%s", text)
	}
}

func TestP4ValidCustomProjection(t *testing.T) {
	_, _, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"hello"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "notice", Content: json.RawMessage(`"custom payload here"`), Display: true}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "hidden", Content: json.RawMessage(`"should stay out"`), Display: false}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: "bookkeeping", Data: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	name := "some name"
	if err := sess.AppendEntry(&session.SessionInfoEntry{Name: &name}); err != nil {
		t.Fatal(err)
	}
	label := "mark"
	loader := p4Load(t, sess.Path())
	target := session.EntryID(loader.Leaf())
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &label}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.ModelChangeEntry{Provider: "openrouter", ModelID: "m"}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader = p4Load(t, path)
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(entryIDs) {
		t.Fatalf("history %d entryIDs %d must align", len(history), len(entryIDs))
	}
	text := p4HistoryText(history)
	if !strings.Contains(text, "custom payload here") {
		t.Fatalf("history missing custom payload:\n%s", text)
	}
	if !strings.Contains(text, "notice") {
		t.Fatalf("history missing custom type:\n%s", text)
	}
	if !strings.Contains(text, "should stay out") {
		t.Fatalf("history missing hidden custom payload:\n%s", text)
	}
	for _, bad := range []string{"bookkeeping", "some name", "mark"} {
		if strings.Contains(text, bad) {
			t.Fatalf("history leaked bookkeeping %q:\n%s", bad, text)
		}
	}
	if len(history) != 3 {
		t.Fatalf("history = %d, want 3 (user plus visible and hidden custom)", len(history))
	}
}

func TestP4HiddenCustomInvalidStillFails(t *testing.T) {
	loader := blocker5RawLoader(t, []string{
		blocker5Header,
		blocker5UserLine("hc000001", "", "hello", 1),
		`{"type":"custom_message","id":"hc000002","parentId":"hc000001","timestamp":"2026-01-01T00:00:02.000Z","customType":"hidden-bad","display":false}`,
	})
	if _, _, _, err := projectModelHistoryWithIDs(loader); err == nil {
		t.Fatal("a hidden custom message with empty content must still fail reconstruction")
	}
	if _, err := projectSession(loader); err == nil {
		t.Fatal("a hidden custom message with empty content must still fail projection")
	}
}

func TestP4UnresolvedAnchorRecovery(t *testing.T) {
	_, _, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"before"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: "lost summary", FirstKeptEntryID: "missing-anchor", TokensBefore: 9}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"after"`), Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader := p4Load(t, path)
	history, entryIDs, warnings, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %d, want 3 with full recovery", len(history))
	}
	text := p4HistoryText(history)
	if !strings.Contains(text, "before") || !strings.Contains(text, "after") || !strings.Contains(text, "lost summary") {
		t.Fatalf("recovery dropped history:\n%s", text)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "missing-anchor") && strings.Contains(w, "unresolved") && strings.Contains(w, "uncompacted") && strings.Contains(w, "fresh") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want unresolved uncompacted fresh warning", warnings)
	}
	if len(entryIDs) != len(history) {
		t.Fatalf("entry ids %d history %d must align", len(entryIDs), len(history))
	}
}

func TestP4UnsupportedOpaqueFailure(t *testing.T) {
	_, _, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"ok"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	raw := `{"type":"mystery","id":"zzz","parentId":null,"timestamp":"2026-01-01T00:00:02.000Z"}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(raw); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loader := p4Load(t, path)
	if _, _, _, err := projectModelHistoryWithIDs(loader); err == nil {
		t.Fatal("opaque context entry must fail reconstruction")
	} else if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("opaque error = %v, want unsupported", err)
	}
	if _, err := projectSession(loader); err == nil {
		t.Fatal("projectSession with opaque must fail")
	}
	if _, err := forkPrefixEntries(loader, "zzz"); err == nil {
		t.Fatal("fork prefix with opaque must fail")
	}
}

func TestP4UnsupportedMessageRoleFailure(t *testing.T) {
	_, _, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"ok"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader := p4Load(t, path)
	leaf := loader.Leaf()
	parent := session.EntryID(leaf)
	line := `{"type":"message","id":"badrole1","parentId":"` + parent + `","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"custom","content":"x"}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loader = p4Load(t, path)
	if _, _, _, err := projectModelHistoryWithIDs(loader); err == nil {
		t.Fatal("unsupported message role must fail reconstruction")
	}
}

func TestP4ValidToolRoundtrip(t *testing.T) {
	_, _, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"run tool"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "doing"},
			{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "probe", Arguments: json.RawMessage(`{}`)},
		},
		StopReason: "toolUse",
		Timestamp:  2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "call_1", ToolName: "probe",
		Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "ok"}},
		Timestamp: 3,
	}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader := p4Load(t, path)
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %d, want 3 with valid tool roundtrip", len(history))
	}
	if len(entryIDs) != 3 {
		t.Fatalf("entry ids = %d, want 3", len(entryIDs))
	}
	if history[1].Assistant == nil || history[2].ToolResult == nil {
		t.Fatal("valid tool history was not preserved")
	}
}

func TestP4ToolRejections(t *testing.T) {
	build := func(t *testing.T, setup func(sess *session.Session)) string {
		t.Helper()
		_, _, sess := p4SeedStore(t)
		defer sess.Close()
		setup(sess)
		path := sess.Path()
		sess.Close()
		return path
	}
	cases := map[string]func(sess *session.Session){
		"orphan": func(sess *session.Session) {
			_ = sess.AppendToolResult(&agent.ToolResultMessage{
				Role: "toolResult", ToolCallID: "ghost", ToolName: "probe",
				Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "x"}}, Timestamp: 1,
			})
		},
		"duplicate result": func(sess *session.Session) {
			_ = sess.AppendAssistant(&agent.AssistantMessage{
				Role:       "assistant",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}},
				StopReason: "toolUse", Timestamp: 1,
			})
			_ = sess.AppendToolResult(&agent.ToolResultMessage{
				Role: "toolResult", ToolCallID: "c1", ToolName: "probe",
				Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "a"}}, Timestamp: 2,
			})
			_ = sess.AppendToolResult(&agent.ToolResultMessage{
				Role: "toolResult", ToolCallID: "c1", ToolName: "probe",
				Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "b"}}, Timestamp: 3,
			})
		},
		"duplicate call": func(sess *session.Session) {
			_ = sess.AppendAssistant(&agent.AssistantMessage{
				Role:       "assistant",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "dup", Name: "probe", Arguments: json.RawMessage(`{}`)}},
				StopReason: "toolUse", Timestamp: 1,
			})
			_ = sess.AppendAssistant(&agent.AssistantMessage{
				Role:       "assistant",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "dup", Name: "probe", Arguments: json.RawMessage(`{}`)}},
				StopReason: "toolUse", Timestamp: 2,
			})
		},
		"unmatched": func(sess *session.Session) {
			_ = sess.AppendAssistant(&agent.AssistantMessage{
				Role:       "assistant",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "c2", Name: "alpha", Arguments: json.RawMessage(`{}`)}},
				StopReason: "toolUse", Timestamp: 1,
			})
			_ = sess.AppendToolResult(&agent.ToolResultMessage{
				Role: "toolResult", ToolCallID: "c2", ToolName: "beta",
				Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "x"}}, Timestamp: 2,
			})
		},
		"pending": func(sess *session.Session) {
			_ = sess.AppendAssistant(&agent.AssistantMessage{
				Role:       "assistant",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "pend", Name: "probe", Arguments: json.RawMessage(`{}`)}},
				StopReason: "toolUse", Timestamp: 1,
			})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			path := build(t, setup)
			loader := p4Load(t, path)
			if _, _, _, err := projectModelHistoryWithIDs(loader); err == nil {
				t.Fatalf("%s must reject reconstruction", name)
			}
			if _, err := forkPrefixEntries(loader, ""); err == nil {
				t.Fatalf("%s must reject fork boundary", name)
			}
		})
	}
}

func TestP4InitialContinueFailure(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	store, err := session.NewStore(sessDir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role:       "assistant",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "bad", Name: "probe", Arguments: json.RawMessage(`{}`)}},
		StopReason: "toolUse", Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	deps, _ := continueDeps(t, cwd, sessDir, &fakeClient{script: []*agent.AssistantMessage{textStop("x")}})
	if err := RunWithDeps([]string{"-p", "hello", "-continue", path, "-model", "test/model"}, deps); err == nil {
		t.Fatal("initial continue with pending tool must fail")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed continue must leave the session file intact")
	}
}

func TestP4ResumeRollback(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	good, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, good, "good")
	controller := testController(t, store, cwd)
	defer controller.Close()
	active, err := controller.Adopt(good, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.AppendAssistant(&agent.AssistantMessage{
		Role:       "assistant",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "stuck", Name: "probe", Arguments: json.RawMessage(`{}`)}},
		StopReason: "toolUse", Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	badPath := bad.Path()
	bad.Close()
	if _, err := controller.PrepareOpen(badPath); err == nil {
		t.Fatal("resume with pending tool must fail")
	}
	if controller.Current() != active {
		t.Fatal("failed resume must leave the current session intact")
	}
	seedTurn(t, controller.Current().sess, "still live")
	assertFileContains(t, active.path, "still live", true)
}

func TestP4ForkBoundaryValidation(t *testing.T) {
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
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role:       "assistant",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "midcall", Name: "probe", Arguments: json.RawMessage(`{}`)}},
		StopReason: "toolUse", Timestamp: 9,
	}); err != nil {
		t.Fatal(err)
	}
	midLoader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	midID := session.EntryID(midLoader.Leaf())
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "midcall", ToolName: "probe",
		Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "done"}}, Timestamp: 10,
	}); err != nil {
		t.Fatal(err)
	}
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(sess, sessionModeResume); err != nil {
		t.Fatal(err)
	}
	before := countJSONL(t, store.Root())
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forkPrefixEntries(loader, midID); err == nil {
		t.Fatal("fork at pending tool boundary must fail validation")
	}
	if _, err := forkPrefixEntries(loader, ""); err != nil {
		t.Fatalf("fork at valid leaf must succeed: %v", err)
	}
	if after := countJSONL(t, store.Root()); after != before {
		t.Fatalf("fork validation created %d files", after-before)
	}
}

func TestP4ProjectionEdgeCoverage(t *testing.T) {
	_, _, sess := p4SeedStore(t)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"edge"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader := p4Load(t, path)
	if _, _, err := projectModelHistory(loader); err != nil {
		t.Fatal(err)
	}
	badStore, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	badSess, err := badStore.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := badSess.AppendToolResult(&agent.ToolResultMessage{Role: "toolResult", ToolCallID: "ghost", ToolName: "p", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "x"}}, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	badPath := badSess.Path()
	badSess.Close()
	badLoader := p4Load(t, badPath)
	if _, _, err := projectModelHistory(badLoader); err == nil {
		t.Fatal("wrapper with orphan must fail")
	}
	if _, _, _, err := projectModelHistoryWithIDs(nil); err != nil {
		t.Fatal(err)
	}
	emptyCustom := &session.CustomMessageEntry{CustomType: "x"}
	if _, err := customHistoryMessage(emptyCustom); err == nil {
		t.Fatal("empty custom must fail")
	}
	invalidCustom := &session.CustomMessageEntry{CustomType: "x", Content: json.RawMessage(`{invalid}`)}
	if _, err := customHistoryMessage(invalidCustom); err == nil {
		t.Fatal("invalid custom must fail")
	}
	if err := validateModelHistoryTools([]*agent.Message{{User: &agent.UserMessage{Role: "user"}}}); err != nil {
		t.Fatal(err)
	}
	if err := validateModelHistoryTools([]*agent.Message{{Assistant: &agent.AssistantMessage{Content: []agent.ContentBlock{{Type: agent.BlockTypeToolCall}}}}}); err == nil {
		t.Fatal("tool call without id must fail")
	}
	if err := validateModelHistoryTools([]*agent.Message{{ToolResult: &agent.ToolResultMessage{}}}); err == nil {
		t.Fatal("tool result without id must fail")
	}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	anchored, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := anchored.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"a"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	aloader := p4Load(t, anchored.Path())
	rootID := session.EntryID(aloader.Leaf())
	if err := anchored.AppendEntry(&session.CompactionEntry{Summary: "s", FirstKeptEntryID: "outside", TokensBefore: 1}); err != nil {
		t.Fatal(err)
	}
	if err := anchored.AppendEntry(&session.BranchSummaryEntry{FromID: "elsewhere", Summary: "b"}); err != nil {
		t.Fatal(err)
	}
	_ = rootID
	anchored.Close()
	bloater, err := session.LoadWithOptions(anchored.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := bloater.ActiveBranch()
	if err != nil {
		t.Fatal(err)
	}
	leafID := session.EntryID(bloater.Leaf())
	if _, err := forkPrefixEntries(bloater, leafID); err == nil {
		t.Fatal("fork with outside anchors must fail")
	}
	_ = branch
}

func TestP4RunOnceContinuedCoverage(t *testing.T) {
	ctx := t.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"hi"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	rd := &runDeps{
		model: "test/model", system: "s", client: &fakeClient{script: []*agent.AssistantMessage{textStop("ok")}}, recorder: &sessionRecorder{sess}, stdout: &out,
	}
	if err := runOnceContinued(ctx, rd, sess, "hello"); err != nil {
		t.Fatalf("runOnceContinued valid = %v", err)
	}
	bad, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.AppendToolResult(&agent.ToolResultMessage{Role: "toolResult", ToolCallID: "ghost", ToolName: "p", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "x"}}, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	rdBad := &runDeps{
		model: "test/model", system: "s", client: &fakeClient{script: []*agent.AssistantMessage{textStop("x")}}, recorder: &sessionRecorder{bad}, stdout: &out,
	}
	if err := runOnceContinued(ctx, rdBad, bad, "hi"); err == nil {
		t.Fatal("runOnceContinued with orphan must fail")
	}
	missing, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	missingPath := missing.Path()
	missing.Close()
	os.Remove(missingPath)
	rdMissing := &runDeps{
		model: "test/model", system: "s", client: &fakeClient{}, recorder: &sessionRecorder{missing}, stdout: &out,
	}
	if err := runOnceContinued(ctx, rdMissing, missing, "hi"); err == nil {
		t.Fatal("runOnceContinued with missing file must fail")
	}
	sess.Close()
	bad.Close()
}
