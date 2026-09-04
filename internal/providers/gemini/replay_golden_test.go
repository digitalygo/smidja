package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

const replayGoldenUpdateEnv = "SMIDJA_UPDATE_REPLAY_GOLDENS"

const replaySystemPrompt = "You are smidja replay fixture. Answer with exactly one sentence."

const replayModel = "gemini-3-pro"

const replayThoughtSignature = "c2lnX3JlcGxheTAxMjM0NQ=="

type replayFixture struct{}

func replayTools() []agent.Tool {
	return []agent.Tool{
		stubTool{
			name:   "zprobe",
			desc:   "Probe tool ordered first on the wire despite sorting last alphabetically.",
			schema: json.RawMessage(`{"type":"object","properties":{"serial":{"type":"string"}},"required":["serial"]}`),
		},
		stubTool{
			name:   "aprobe",
			desc:   "Probe tool ordered second on the wire despite sorting first alphabetically.",
			schema: json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}},"required":["label"]}`),
		},
		stubTool{
			name:   "midprobe",
			desc:   "Probe tool with a boolean flag argument.",
			schema: json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}},"required":["enabled"]}`),
		},
	}
}

func replayHistory() []*agent.Message {
	identity := func(m *agent.AssistantMessage) *agent.AssistantMessage {
		m.API = "google-generative-ai"
		m.Provider = "gemini"
		m.Model = replayModel
		return m
	}
	return []*agent.Message{
		{User: &agent.UserMessage{
			Role:      string(agent.RoleUser),
			Content:   json.RawMessage(`"Summarize the replay fixture plan."`),
			Timestamp: 1750000000001,
		}},
		{Assistant: identity(&agent.AssistantMessage{
			Role: string(agent.RoleAssistant),
			Content: []agent.ContentBlock{
				{Type: agent.BlockTypeThinking, Thinking: "Aligning fixture blocks before answering.", ThinkingSignature: replayThoughtSignature},
				{Type: agent.BlockTypeText, Text: "Plan summary ready."},
				{Type: agent.BlockTypeToolCall, ID: "call_a1", Name: "zprobe", Arguments: json.RawMessage(`{"serial":"rz-001"}`)},
			},
			StopReason: "toolUse", Timestamp: 1750000000002,
		})},
		{ToolResult: &agent.ToolResultMessage{
			Role: string(agent.RoleToolResult), ToolCallID: "call_a1", ToolName: "zprobe",
			Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "probe rz-001 ok"}},
			Timestamp: 1750000000003,
		}},
		{Assistant: identity(&agent.AssistantMessage{
			Role: string(agent.RoleAssistant),
			Content: []agent.ContentBlock{
				{Type: agent.BlockTypeThinking, Redacted: true, ThinkingSignature: "opaque-redacted-payload-01"},
				{Type: agent.BlockTypeText, Text: "Comparison "},
				{Type: agent.BlockTypeText, Text: "queued."},
				{Type: agent.BlockTypeToolCall, ID: "call_b2", Name: "aprobe", Arguments: json.RawMessage(`{"label":"compare"}`)},
				{Type: agent.BlockTypeToolCall, ID: "call_c3", Name: "midprobe", Arguments: json.RawMessage(`{"enabled":true}`)},
			},
			StopReason: "toolUse", Timestamp: 1750000000004,
		})},
		{ToolResult: &agent.ToolResultMessage{
			Role: string(agent.RoleToolResult), ToolCallID: "call_b2", ToolName: "aprobe",
			Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "label matched"}},
			Timestamp: 1750000000005,
		}},
		{ToolResult: &agent.ToolResultMessage{
			Role: string(agent.RoleToolResult), ToolCallID: "call_c3", ToolName: "midprobe", IsError: true,
			Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "flag set"}},
			Timestamp: 1750000000006,
		}},
	}
}

func replayAppendHistory(t *testing.T, sess *session.Session, history []*agent.Message) {
	t.Helper()
	for _, m := range history {
		var err error
		switch {
		case m.User != nil:
			err = sess.AppendUser(m.User)
		case m.Assistant != nil:
			err = sess.AppendAssistant(m.Assistant)
		case m.ToolResult != nil:
			err = sess.AppendToolResult(m.ToolResult)
		default:
			t.Fatal("replay history contains an empty message")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func replayLoadedHistory(t *testing.T, path string) []*agent.Message {
	t.Helper()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := loader.BuildContextEntries()
	if err != nil {
		t.Fatal(err)
	}
	var history []*agent.Message
	for _, e := range entries {
		me, ok := e.(*session.MessageEntry)
		if !ok {
			continue
		}
		msg, err := me.DecodeMessage()
		if err != nil {
			t.Fatal(err)
		}
		history = append(history, msg)
	}
	if len(history) == 0 {
		t.Fatal("strict session loading produced no messages")
	}
	return history
}

func replayCapture(t *testing.T, history []*agent.Message) []byte {
	t.Helper()
	srv, captured := captureServer(t,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],"responseId":"replay_gemini_turn"}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`)
	defer srv.Close()
	req := &agent.TurnRequest{Model: replayModel, System: replaySystemPrompt, Messages: history, Tools: replayTools()}
	if _, err := testDriver(t, srv.URL).StreamTurn(context.Background(), req, nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if captured.method != http.MethodPost {
		t.Errorf("request method = %q, want POST", captured.method)
	}
	if captured.path != "/models/"+replayModel+":streamGenerateContent" {
		t.Errorf("request path = %q, want /models/%s:streamGenerateContent", captured.path, replayModel)
	}
	return captured.body
}

func replayNormalized(t *testing.T, payload []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("normalize payload: %v", err)
	}
	return append(out, '\n')
}

func replayGolden(t *testing.T, name string, payload []byte) {
	t.Helper()
	normalized := replayNormalized(t, payload)
	path := filepath.Join("testdata", "replay", name)
	if os.Getenv(replayGoldenUpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, normalized, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (set %s=1 to regenerate)", path, err, replayGoldenUpdateEnv)
	}
	if !bytes.Equal(want, normalized) {
		t.Errorf("golden %s mismatch:\n--- committed ---\n%s\n--- observed ---\n%s", path, want, normalized)
	}
}

func replayPayloadFields(t *testing.T, payload []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode payload fields: %v", err)
	}
	return fields
}

func replayListField(t *testing.T, fields map[string]json.RawMessage, key string) []json.RawMessage {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(fields[key], &list); err != nil {
		t.Fatalf("decode %s list: %v", key, err)
	}
	return list
}

func replayAssertPrefixGrowth(t *testing.T, before, after map[string]json.RawMessage, listKey string, stableKeys []string) {
	t.Helper()
	beforeList := replayListField(t, before, listKey)
	afterList := replayListField(t, after, listKey)
	if len(afterList) != len(beforeList)+1 {
		t.Fatalf("%s length after = %d, want %d (one appended element only)", listKey, len(afterList), len(beforeList)+1)
	}
	for i := range beforeList {
		if !bytes.Equal(beforeList[i], afterList[i]) {
			t.Errorf("%s[%d] mutated between turns:\nbefore %s\nafter  %s", listKey, i, beforeList[i], afterList[i])
		}
	}
	for _, key := range stableKeys {
		beforeRaw, beforeOK := before[key]
		afterRaw, afterOK := after[key]
		if beforeOK != afterOK || (beforeOK && !bytes.Equal(beforeRaw, afterRaw)) {
			t.Errorf("request field %q mutated between turns:\nbefore %s\nafter  %s", key, beforeRaw, afterRaw)
		}
	}
}

func TestReplayGoldenGemini(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	replayAppendHistory(t, sess, replayHistory())
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	first := replayCapture(t, replayLoadedHistory(t, sess.Path()))

	reopened, err := store.Open(sess.Path(), session.OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.AppendUser(&agent.UserMessage{
		Role:      string(agent.RoleUser),
		Content:   json.RawMessage(`"Follow up: ship the fixture report."`),
		Timestamp: 1750000000007,
	}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	second := replayCapture(t, replayLoadedHistory(t, sess.Path()))

	replayGolden(t, "gemini_turn1.json", first)
	replayGolden(t, "gemini_turn2.json", second)

	before := replayPayloadFields(t, first)
	after := replayPayloadFields(t, second)
	replayAssertPrefixGrowth(t, before, after, "contents", []string{
		"systemInstruction", "tools", "thinkingConfig",
	})
}
