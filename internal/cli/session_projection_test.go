package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func seedSession(t *testing.T, cwd string) *session.Session {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func userJSON(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(agent.UserMessage{Role: "user", Content: json.RawMessage(`"` + text + `"`), Timestamp: 1})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProjectTranscriptRoundTripsSupportedEntries(t *testing.T) {
	cwd := t.TempDir()
	sess := seedSession(t, cwd)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "plan"},
			{Type: agent.BlockTypeText, Text: "answer"},
			{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "probe", Arguments: json.RawMessage(`{"x":1}`)},
		},
		StopReason: "toolUse",
		Timestamp:  2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: "call_1",
		ToolName:   "probe",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "probe result"}},
		Timestamp:  3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: "compacted", FirstKeptEntryID: "missing", TokensBefore: 10}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: "skill", Data: json.RawMessage(`{"name":"quick"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: session.RuntimeProfileCustomType, Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "notice", Content: json.RawMessage(`"visible custom"`), Display: true}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "hidden", Content: json.RawMessage(`"hidden"`), Display: false}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.ModelChangeEntry{Provider: "openrouter", ModelID: "other/model"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: "high"}); err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	if err := sess.AppendEntry(&session.SessionInfoEntry{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"second"`), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectSession(loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.history) != 7 {
		t.Fatalf("model history length = %d, want 7 with uncompacted recovery including hidden custom", len(projection.history))
	}
	if projection.history[0].User == nil {
		t.Fatalf("model history entry = %+v, want the recovered user message", projection.history[0])
	}
	if len(projection.entryIDs) != len(projection.history) {
		t.Fatalf("entry ids = %d, want %d aligned with history", len(projection.entryIDs), len(projection.history))
	}
	foundCompaction := false
	foundCustom := false
	foundHiddenCustom := false
	for _, message := range projection.history {
		if message.User == nil {
			continue
		}
		text := string(message.User.Content)
		if strings.Contains(text, "compacted") {
			foundCompaction = true
		}
		if strings.Contains(text, "visible custom") {
			foundCustom = true
		}
		if strings.Contains(text, "[custom hidden") {
			foundHiddenCustom = true
		}
	}
	if !foundCompaction {
		t.Error("model history is missing the explicit compaction summary")
	}
	if !foundCustom {
		t.Error("model history is missing the explicit custom message")
	}
	if !foundHiddenCustom {
		t.Error("model history is missing the hidden custom message content")
	}
	if projection.name != "renamed" {
		t.Errorf("session name = %q, want renamed", projection.name)
	}
	var kinds []string
	for _, item := range projection.transcript {
		kinds = append(kinds, item.Kind)
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"user", "assistant", "tool", "compaction", "custom", "notice"} {
		if !strings.Contains(joined, want) {
			t.Errorf("transcript kinds %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "assistant,custom,notice") {
		t.Logf("kinds: %s", joined)
	}
	for _, item := range projection.transcript {
		if item.Kind == interactive.ReplayCustom && item.CustomType == session.RuntimeProfileCustomType {
			t.Error("runtime profile entry leaked into the transcript")
		}
		if item.Kind == interactive.ReplayCustom && item.CustomType == "hidden" {
			t.Error("hidden custom message leaked into the transcript")
		}
		if item.Kind == interactive.ReplayNotice && !strings.Contains(item.Text, "historical model change") {
			if !strings.Contains(item.Text, "thinking level") && !strings.Contains(item.Text, "session renamed") {
				t.Errorf("unexpected notice %q", item.Text)
			}
		}
	}
	foundUnresolved := false
	for _, warning := range projection.warnings {
		if strings.Contains(warning, "compaction anchor") {
			foundUnresolved = true
		}
	}
	if !foundUnresolved {
		t.Errorf("warnings = %v, want an unresolved compaction anchor", projection.warnings)
	}
}

func TestProjectTranscriptMarksOrphanAndPendingTools(t *testing.T) {
	cwd := t.TempDir()
	sess := seedSession(t, cwd)
	defer sess.Close()
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeToolCall, ID: "call_pending", Name: "probe", Arguments: json.RawMessage(`{}`)},
		},
		StopReason: "toolUse",
		Timestamp:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: "orphan",
		ToolName:   "missing",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "stray"}},
		Timestamp:  2,
	}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectSession(loader)
	if err == nil {
		t.Fatal("projectSession with orphan and pending tools must fail reconstruction")
	}
	if !strings.Contains(err.Error(), "orphan") && !strings.Contains(err.Error(), "pending") {
		t.Fatalf("reconstruction error = %v, want orphan or pending", err)
	}
	transcript, warnings := projectTranscript(loader)
	var pending, orphan bool
	for _, item := range transcript {
		if item.Kind != interactive.ReplayTool {
			continue
		}
		if item.ToolName == "probe" && item.Pending {
			pending = true
		}
		if item.ToolName == "missing" && item.Pending {
			orphan = true
		}
	}
	if !pending {
		t.Error("missing pending tool call item")
	}
	if !orphan {
		t.Error("missing orphan tool result item")
	}
	if !containsWarning(warnings, "no recorded result") {
		t.Errorf("warnings = %v, want a pending tool warning", warnings)
	}
	if !containsWarning(warnings, "orphan tool result") {
		t.Errorf("warnings = %v, want an orphan tool warning", warnings)
	}
}

func containsWarning(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}

func TestBuildTreeNodesHandlesCyclesAndMissingParents(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	dirForCwd, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir, err := dirForCwd.DirForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "2026-01-01T00-00-00-000Z_01a0acc5-0487-793d-8a82-f3a26b3f089e.jsonl")
	lines := []string{
		`{"type":"session","version":3,"id":"01a0acc5-0487-793d-8a82-f3a26b3f089e","timestamp":"2026-01-01T00:00:00.000Z","cwd":"` + cwd + `"}`,
		`{"type":"message","id":"aaa","parentId":"bbb","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"cycle a"}}`,
		`{"type":"message","id":"bbb","parentId":"aaa","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"user","content":"cycle b"}}`,
		`{"type":"message","id":"ccc","parentId":"ghost","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"user","content":"missing parent"}}`,
		`{"type":"message","id":"ddd","parentId":"ccc","timestamp":"2026-01-01T00:00:04.000Z","message":{"role":"user","content":"child"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, warnings := buildTreeNodes(loader, false, false)
	if len(nodes) != len(lines)-1 {
		t.Fatalf("tree nodes = %d, want %d", len(nodes), len(lines)-1)
	}
	var corrupt int
	for _, node := range nodes {
		if node.Corrupt {
			corrupt++
		}
	}
	if corrupt < 3 {
		t.Errorf("corrupt nodes = %d, want at least 3 (cycle pair plus missing parent)", corrupt)
	}
	if !containsWarning(warnings, "cycle") && !containsWarning(warnings, "unreachable") {
		t.Errorf("warnings = %v, want a cycle or unreachable warning", warnings)
	}
	named := map[string]bool{}
	for _, node := range nodes {
		named[node.ID] = true
	}
	if !named["aaa"] || !named["bbb"] || !named["ccc"] || !named["ddd"] {
		t.Errorf("tree is missing entries: %v", named)
	}
}

func TestBuildTreeNodesAppliesLatestLabelAndTimestamps(t *testing.T) {
	cwd := t.TempDir()
	sess := seedSession(t, cwd)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"head"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	target := ""
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target = session.EntryID(loader.Leaf())
	first := "first"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &first}); err != nil {
		t.Fatal(err)
	}
	second := "second"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &second}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"tail"`), Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader, err = session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := buildTreeNodes(loader, false, false)
	var label string
	var timestamp string
	for _, node := range nodes {
		if node.ID == target {
			label = node.Label
			timestamp = node.Timestamp
		}
	}
	if label != "second" {
		t.Errorf("label = %q, want the latest label second", label)
	}
	if timestamp != "2026" && !strings.HasPrefix(timestamp, "20") {
		t.Errorf("timestamp = %q, want the entry timestamp", timestamp)
	}
}

func TestForkPrefixRejectsUnsupportedEntries(t *testing.T) {
	cwd := t.TempDir()
	sess := seedSession(t, cwd)
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"ok"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	raw := `{"type":"mystery","id":"zzz","parentId":null,"timestamp":"2026-01-01T00:00:02.000Z"}` + "\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(raw); err != nil {
		t.Fatal(err)
	}
	file.Close()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forkPrefixEntries(loader, "zzz"); err == nil {
		t.Error("fork prefix with an opaque entry must be rejected")
	}
	if _, err := forkPrefixEntries(loader, ""); err == nil {
		t.Error("fork prefix ending at the opaque leaf must be rejected")
	}
}
