package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/subagent"
)

type keepToolResultsSelector struct{}

func (keepToolResultsSelector) Select(_ context.Context, req subagent.SelectionRequest) (subagent.Selection, error) {
	kept := make([]string, 0, len(req.Candidates))
	for _, candidate := range req.Candidates {
		if candidate.Message != nil && candidate.Message.ToolResult != nil {
			kept = append(kept, candidate.Ref)
		}
	}
	return subagent.Selection{KeptIDs: kept}, nil
}

func compactionPairConfig() contextmanager.Config {
	return contextmanager.Config{
		Enabled:                true,
		ContextWindowTokens:    100_000,
		CacheMissAfter:         contextmanager.DefaultCacheMissAfter,
		PruneThreshold:         contextmanager.DefaultPruneThreshold,
		CompactThreshold:       contextmanager.DefaultCompactThreshold,
		SafetyCompactThreshold: contextmanager.DefaultSafetyCompactThreshold,
		CompactTarget:          contextmanager.DefaultCompactTarget,
		KeepRecentMessages:     2,
		SelectorChunkTokens:    contextmanager.DefaultSelectorChunkTokens,
		SelectorModel:          "test/model",
	}
}

func seedCompactionPairSession(t *testing.T) *session.Session {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"old question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"second old question"`), Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "calling"},
			{Type: agent.BlockTypeToolCall, ID: "call_split", Name: "probe", Arguments: json.RawMessage(`{}`)},
		},
		StopReason: "toolUse",
		Timestamp:  3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "call_split", ToolName: "probe",
		Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "split output"}},
		Timestamp: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"recent one"`), Timestamp: 5}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"recent two"`), Timestamp: 6}); err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestForcedCompactionPreservesToolPairOnReopen(t *testing.T) {
	sess := seedCompactionPairSession(t)
	path := sess.Path()
	loader := p4Load(t, path)
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 6 || len(entryIDs) != 6 {
		t.Fatalf("history %d entry ids %d, want 6 each", len(history), len(entryIDs))
	}

	cfg := compactionPairConfig()
	manager, err := contextmanager.New(cfg, keepToolResultsSelector{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := manager.Prepare(context.Background(), agent.ContextRequest{
		Messages:       history,
		EntryIDs:       entryIDs,
		LastUsageInput: cfg.ContextWindowTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted || res.Compaction == nil {
		t.Fatalf("forced compaction did not fire: compacted=%v", res.Compacted)
	}
	if res.Compaction.FirstKeptEntryID == "" {
		t.Fatal("forced compaction must anchor on a real entry id")
	}
	if err := (&sessionRecorder{sess: sess}).appendCompaction(res.Compaction); err != nil {
		t.Fatal(err)
	}

	reopened := p4Load(t, path)
	recovered, recoveredIDs, warnings, err := projectModelHistoryWithIDs(reopened)
	if err != nil {
		t.Fatalf("reopen after compaction failed: %v", err)
	}
	if len(recovered) != len(recoveredIDs) {
		t.Fatalf("recovered history %d entry ids %d must align", len(recovered), len(recoveredIDs))
	}
	text := p4HistoryText(recovered)
	if !strings.Contains(text, "call_split") && !strings.Contains(text, "split output") {
		t.Fatalf("recovered history lost the tool pair:\n%s", text)
	}
	if !strings.Contains(text, "split output") {
		t.Fatalf("recovered history lost the kept tool result:\n%s", text)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "unresolved") {
			t.Fatalf("reopen reported a recovery warning: %q", warning)
		}
	}

	var stdout bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: path,
		client:      &fakeClient{script: []*agent.AssistantMessage{textStop("next answer")}},
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
	}
	if err := runOnceContinued(context.Background(), d, sess, "next prompt"); err != nil {
		t.Fatalf("next prompt after compaction failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "next answer") {
		t.Fatalf("stdout = %q, want the next answer", stdout.String())
	}
	if got := countPersistedUserMessages(t, path, "next prompt"); got != 1 {
		t.Fatalf("persisted next prompt entries = %d, want 1", got)
	}
}

func compactionPairFallbackSession(t *testing.T) *session.Session {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	for i, text := range []string{"filler zero", "filler one", "filler two"} {
		if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(strconv.Quote(text)), Timestamp: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "calling"},
			{Type: agent.BlockTypeToolCall, ID: "call_fallback", Name: "probe", Arguments: json.RawMessage(`{}`)},
		},
		StopReason: "toolUse",
		Timestamp:  4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "call_fallback", ToolName: "probe",
		Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "fallback output"}},
		Timestamp: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"recent one"`), Timestamp: 6}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"recent two"`), Timestamp: 7}); err != nil {
		t.Fatal(err)
	}
	return sess
}

func approxRawTokens(message *agent.Message) int64 {
	raw, err := json.Marshal([]*agent.Message{message})
	if err != nil {
		return 0
	}
	return int64((len(raw) + 3) / 4)
}

func TestCompactionSplitAcrossFallbackStillReopens(t *testing.T) {
	sess := compactionPairFallbackSession(t)
	path := sess.Path()
	loader := p4Load(t, path)
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 7 {
		t.Fatalf("history = %d, want 7", len(history))
	}
	resultMessage := history[4]
	if resultMessage.ToolResult == nil {
		t.Fatalf("history[4] = %+v, want the tool result", resultMessage)
	}
	cfg := compactionPairConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = 1000
	cfg.CompactTarget = float64(approxRawTokens(resultMessage)) / float64(cfg.ContextWindowTokens)
	manager, err := contextmanager.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := manager.Prepare(context.Background(), agent.ContextRequest{
		Messages:       history,
		EntryIDs:       entryIDs,
		LastUsageInput: cfg.ContextWindowTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compaction == nil {
		t.Fatal("fallback compaction did not produce an entry")
	}
	if !strings.Contains(string(res.Compaction.Summary), "smidja-fallback-v1") {
		t.Fatalf("summary = %s, want the fallback strategy", res.Compaction.Summary)
	}
	if res.Compaction.FirstKeptEntryID != entryIDs[3] {
		t.Fatalf("FirstKeptEntryID = %q, want the split call %q", res.Compaction.FirstKeptEntryID, entryIDs[3])
	}
	if err := (&sessionRecorder{sess: sess}).appendCompaction(res.Compaction); err != nil {
		t.Fatal(err)
	}
	reopened := p4Load(t, path)
	recovered, _, warnings, err := projectModelHistoryWithIDs(reopened)
	if err != nil {
		t.Fatalf("reopen after fallback compaction failed: %v", err)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "unresolved") {
			t.Fatalf("reopen reported a recovery warning: %q", warning)
		}
	}
	text := p4HistoryText(recovered)
	if !strings.Contains(text, "fallback output") {
		t.Fatalf("recovered history lost the tool result:\n%s", text)
	}
	foundCall := false
	for _, message := range recovered {
		if message.Assistant == nil {
			continue
		}
		for _, block := range message.Assistant.Content {
			if block.Type == agent.BlockTypeToolCall && block.ID == "call_fallback" {
				foundCall = true
			}
		}
	}
	if !foundCall {
		t.Fatalf("recovered history lost the tool call that the fallback split from its result:\n%s", text)
	}
}

func TestGenuineOrphanImportStillRejects(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"import head"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "ghost_call", ToolName: "probe",
		Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "orphan"}},
		Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if _, _, _, err := projectModelHistoryWithIDs(p4Load(t, path)); err == nil {
		t.Fatal("a genuine orphan import must still reject reconstruction")
	}
	if _, err := projectSession(p4Load(t, path)); err == nil {
		t.Fatal("a genuine orphan import must still reject projection")
	}
	controller := testController(t, store, cwd)
	defer controller.Close()
	if _, err := controller.Adopt(sess, sessionModeResume); err == nil {
		t.Fatal("a genuine orphan import must still reject resume")
	}

	pending, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	if err := pending.AppendAssistant(&agent.AssistantMessage{
		Role:       "assistant",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "stuck_call", Name: "probe", Arguments: json.RawMessage(`{}`)}},
		StopReason: "toolUse", Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := projectModelHistoryWithIDs(p4Load(t, pending.Path())); err == nil {
		t.Fatal("a genuine pending import must still reject reconstruction")
	}
}
