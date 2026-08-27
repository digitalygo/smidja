package summary

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

func header(id, ts, cwd string) *session.Header {
	return &session.Header{
		Type:      session.EntryTypeSession,
		Version:   3,
		ID:        id,
		Timestamp: ts,
		Cwd:       cwd,
	}
}

func ptr(s string) *string { return &s }

func msgEntry(id, parentID, ts string, payload map[string]any) *session.MessageEntry {
	b, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return &session.MessageEntry{
		EntryBase: session.EntryBase{
			Type:      session.EntryTypeMessage,
			ID:        id,
			ParentID:  ptr(parentID),
			Timestamp: ts,
		},
		Message: b,
	}
}

func user(id, parentID, ts, content string) *session.MessageEntry {
	return msgEntry(id, parentID, ts, map[string]any{
		"role": "user", "content": content, "timestamp": 1,
	})
}

func userBlocks(id, parentID, ts string, content []agent.ContentBlock) *session.MessageEntry {
	return msgEntry(id, parentID, ts, map[string]any{
		"role": "user", "content": content, "timestamp": 1,
	})
}

func asst(id, parentID, ts string, blocks []agent.ContentBlock) *session.MessageEntry {
	return msgEntry(id, parentID, ts, map[string]any{
		"role": "assistant", "content": blocks, "timestamp": 2,
	})
}

func toolResultEntry(id, parentID, ts, body string) *session.MessageEntry {
	return msgEntry(id, parentID, ts, map[string]any{
		"role": "toolResult", "toolCallId": "t1", "toolName": "bash",
		"content": []agent.ContentBlock{{Type: agent.BlockTypeText, Text: body}},
		"isError": false, "timestamp": 3,
	})
}

func compactEntry(id, parentID, ts string) *session.CompactionEntry {
	return &session.CompactionEntry{
		EntryBase: session.EntryBase{
			Type: session.EntryTypeCompaction, ID: id, ParentID: ptr(parentID), Timestamp: ts,
		},
		Summary: "compacted prefix",
	}
}

func textBlock(s string) agent.ContentBlock {
	return agent.ContentBlock{Type: agent.BlockTypeText, Text: s}
}
func thinkingBlock(s string) agent.ContentBlock {
	return agent.ContentBlock{Type: agent.BlockTypeThinking, Thinking: s}
}
func callBlock(id, name, args string) agent.ContentBlock {
	return agent.ContentBlock{Type: agent.BlockTypeToolCall, ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func TestBuildGoldenDigest(t *testing.T) {
	entries := []Entryish{
		header("0196b87c-7a2b-7000-8000-0000000000a1", "2026-08-25T10:00:00.000Z", "/tmp/proj"),
		user("e01", "", "2026-08-25T10:00:01.000Z", "inspect the build"),
		asst("e02", "e01", "2026-08-25T10:00:02.000Z", []agent.ContentBlock{
			thinkingBlock("deep reasoning"),
			textBlock("I will look at the repo."),
			callBlock("c1", "bash", `{"command":"ls"}`),
		}),
		toolResultEntry("e03", "e02", "2026-08-25T10:00:03.000Z", "file list output"),
		user("e04", "e03", "2026-08-25T10:00:04.000Z", "fix the lint errors"),
		asst("e05", "e04", "2026-08-25T10:00:05.000Z", []agent.ContentBlock{
			textBlock("Done."),
			callBlock("c2", "read", `{"path":"go.mod"}`),
		}),
		toolResultEntry("e06", "e05", "2026-08-25T10:00:06.000Z", "module content"),
		user("e07", "e06", "2026-08-25T10:00:07.000Z", "thanks"),
	}
	d := Build(entries, Options{})

	if d.ShortID != "0196b87c" {
		t.Errorf("ShortID = %q, want 0196b87c", d.ShortID)
	}
	if d.Workspace != "/tmp/proj" {
		t.Errorf("Workspace = %q, want /tmp/proj", d.Workspace)
	}
	wantStart := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	if !d.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v", d.StartedAt, wantStart)
	}
	wantLast := time.Date(2026, 8, 25, 10, 0, 7, 0, time.UTC)
	if !d.LastActivity.Equal(wantLast) {
		t.Errorf("LastActivity = %v, want %v", d.LastActivity, wantLast)
	}
	if !reflect.DeepEqual(d.LastIntents, []string{"inspect the build", "fix the lint errors", "thanks"}) {
		t.Errorf("LastIntents = %v", d.LastIntents)
	}
	if d.AssistantTurns != 2 {
		t.Errorf("AssistantTurns = %d, want 2", d.AssistantTurns)
	}
	if !reflect.DeepEqual(d.ToolCallsByTool, map[string]int{"bash": 1, "read": 1}) {
		t.Errorf("ToolCallsByTool = %v", d.ToolCallsByTool)
	}
	if d.LastResponseExcerpt != "Done." {
		t.Errorf("LastResponseExcerpt = %q, want Done.", d.LastResponseExcerpt)
	}
	if d.Compacted {
		t.Errorf("Compacted = true, want false")
	}

	again := Build(entries, Options{})
	if !reflect.DeepEqual(d, again) {
		t.Errorf("digest is not deterministic:\n%+v\n%+v", d, again)
	}
}

func TestBuildExcludesToolBodiesArgumentsAndThinking(t *testing.T) {
	entries := []Entryish{
		header("h", "2026-08-25T10:00:00.000Z", "/w"),
		userBlocks("u1", "", "2026-08-25T10:00:01.000Z", []agent.ContentBlock{
			textBlock("visible prompt"),
			thinkingBlock("secret thinking"),
		}),
		asst("a1", "u1", "2026-08-25T10:00:02.000Z", []agent.ContentBlock{
			callBlock("c1", "exec", `{"command":"rm -rf / && echo pwned"}`),
			textBlock("visible response"),
		}),
		toolResultEntry("t1", "a1", "2026-08-25T10:00:03.000Z", "secret tool body content"),
	}
	d := Build(entries, Options{})
	if len(d.LastIntents) != 1 || d.LastIntents[0] != "visible prompt" {
		t.Errorf("LastIntents = %v, want only the text block", d.LastIntents)
	}
	if d.LastResponseExcerpt != "visible response" {
		t.Errorf("LastResponseExcerpt = %q, want visible response", d.LastResponseExcerpt)
	}
	if !reflect.DeepEqual(d.ToolCallsByTool, map[string]int{"exec": 1}) {
		t.Errorf("ToolCallsByTool = %v", d.ToolCallsByTool)
	}
	for _, s := range []string{"secret thinking", "rm -rf", "pwned", "secret tool body"} {
		if strings.Contains(strings.Join(d.LastIntents, " ")+" "+d.LastResponseExcerpt, s) {
			t.Errorf("digest leaked %q", s)
		}
	}
}

func TestBuildRedactsSecrets(t *testing.T) {
	intent := "my key is sk-proj-abcdef123456 and gh token ghp_1234ABCD5678 and aws AKIA12345678ABCDEFGH and jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	entries := []Entryish{
		header("h", "2026-08-25T10:00:00.000Z", "/w"),
		user("u1", "", "2026-08-25T10:00:01.000Z", intent),
		asst("a1", "u1", "2026-08-25T10:00:02.000Z", []agent.ContentBlock{
			textBlock("token sk-ant-apidemo123 and ghp_ZZZZ9999 in the excerpt"),
		}),
	}
	d := Build(entries, Options{})
	got := strings.Join(d.LastIntents, " ") + " " + d.LastResponseExcerpt
	for _, want := range []string{
		"sk-REDACTED",
		"ghp_REDACTED",
		"AKIA-REDACTED",
		"JWT-REDACTED",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest %q must contain %q", got, want)
		}
	}
	for _, leak := range []string{
		"sk-proj-abcdef123456",
		"ghp_1234ABCD5678",
		"AKIA12345678ABCDEFGH",
		"eyJhbGciOiJIUzI1NiJ9",
		"dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
	} {
		if strings.Contains(got, leak) {
			t.Errorf("digest leaked secret %q in %q", leak, got)
		}
	}
}

func TestBuildDoesNotRedactVersionLikeStrings(t *testing.T) {
	entries := []Entryish{
		header("h", "2026-08-25T10:00:00.000Z", "/w"),
		user("u1", "", "2026-08-25T10:00:01.000Z", "upgrade to go1.27.0 and v1.2.3 please"),
	}
	d := Build(entries, Options{})
	if len(d.LastIntents) != 1 || d.LastIntents[0] != "upgrade to go1.27.0 and v1.2.3 please" {
		t.Errorf("version-like strings must survive redaction: %v", d.LastIntents)
	}
}

func TestBuildLimitsIntentsExcerptAndRunes(t *testing.T) {
	longIntent := strings.Repeat("意", 260)
	longExcerpt := strings.Repeat("文", 500)
	entries := []Entryish{
		header("h", "2026-08-25T10:00:00.000Z", "/w"),
		user("u1", "", "2026-08-25T10:00:01.000Z", longIntent),
		asst("a1", "u1", "2026-08-25T10:00:02.000Z", []agent.ContentBlock{textBlock(longExcerpt)}),
	}
	for i := 0; i < 7; i++ {
		entries = append(entries, user("u"+string(rune('2'+i)), "a1", "2026-08-25T10:00:03.000Z", "intent "+string(rune('a'+i))))
	}
	d := Build(entries, Options{})
	if len(d.LastIntents) != 5 {
		t.Fatalf("LastIntents = %d, want 5", len(d.LastIntents))
	}
	if d.LastIntents[0] != "intent c" {
		t.Errorf("LastIntents[0] = %q, want intent c (oldest dropped)", d.LastIntents[0])
	}
	if got := []rune(d.LastResponseExcerpt); len(got) != 400 {
		t.Errorf("excerpt runes = %d, want 400", len(got))
	}

	few := Build(entries[:3], Options{})
	if len(few.LastIntents) != 1 {
		t.Errorf("few intents = %d, want 1", len(few.LastIntents))
	}
	if got := []rune(few.LastIntents[0]); len(got) != 200 {
		t.Errorf("long intent runes = %d, want 200", len(got))
	}
}

func TestBuildCustomLimits(t *testing.T) {
	entries := []Entryish{
		header("h", "2026-08-25T10:00:00.000Z", "/w"),
		user("u1", "", "2026-08-25T10:00:01.000Z", "abcdef"),
		user("u2", "u1", "2026-08-25T10:00:02.000Z", "ghijkl"),
		user("u3", "u2", "2026-08-25T10:00:03.000Z", "mnopqr"),
	}
	d := Build(entries, Options{MaxLastIntents: 2, MaxIntentRunes: 3, MaxExcerptRunes: 5})
	if !reflect.DeepEqual(d.LastIntents, []string{"ghi", "mno"}) {
		t.Errorf("LastIntents = %v, want [ghi mno]", d.LastIntents)
	}
}

func TestBuildHeaderOnlyAndEmpty(t *testing.T) {
	only := Build([]Entryish{header("0196b87c", "2026-08-25T10:00:00.000Z", "/w")}, Options{})
	if only.ShortID != "0196b87c" || only.Workspace != "/w" || only.LastActivity.IsZero() {
		t.Errorf("header-only digest = %+v", only)
	}
	if len(only.LastIntents) != 0 || only.AssistantTurns != 0 || only.LastResponseExcerpt != "" {
		t.Errorf("header-only digest has content: %+v", only)
	}
	if only.ToolCallsByTool == nil || len(only.ToolCallsByTool) != 0 {
		t.Errorf("ToolCallsByTool = %v, want empty non-nil map", only.ToolCallsByTool)
	}
	zero := Build(nil, Options{})
	if zero.ShortID != "" || zero.AssistantTurns != 0 || zero.ToolCallsByTool == nil {
		t.Errorf("empty digest = %+v", zero)
	}
}

func TestBuildCompactedFlag(t *testing.T) {
	entries := []Entryish{
		header("h", "2026-08-25T10:00:00.000Z", "/w"),
		user("u1", "", "2026-08-25T10:00:01.000Z", "a"),
		compactEntry("c1", "u1", "2026-08-25T10:00:02.000Z"),
		user("u2", "c1", "2026-08-25T10:00:03.000Z", "b"),
	}
	d := Build(entries, Options{})
	if !d.Compacted {
		t.Errorf("Compacted = false, want true")
	}
	if len(d.LastIntents) != 2 || d.LastIntents[1] != "b" {
		t.Errorf("LastIntents = %v", d.LastIntents)
	}
}

func TestBuildShortIDBoundary(t *testing.T) {
	d := Build([]Entryish{header("ab", "2026-08-25T10:00:00.000Z", "/w")}, Options{})
	if d.ShortID != "ab" {
		t.Errorf("ShortID = %q, want ab", d.ShortID)
	}
}
