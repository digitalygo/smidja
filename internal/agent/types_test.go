package agent

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// jsonKeys marshals v and returns the set of top-level JSON keys. It guards
// the exact JSON tag contract that the session and provider packages depend
// on: any accidental tag rename or omission fails the tests.
func jsonKeys(t *testing.T, v any) map[string]bool {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	keys := make(map[string]bool, len(m))
	for k := range m {
		keys[k] = true
	}
	return keys
}

func wantJSONKeys(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want exactly %v", got, want)
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("missing key %q in %v", k, got)
		}
	}
}

func TestContentBlockJSON(t *testing.T) {
	full := ContentBlock{
		Type:              BlockTypeToolCall,
		Text:              "t",
		Thinking:          "th",
		ThinkingSignature: "sig",
		Redacted:          true,
		ID:                "call_1",
		Name:              "read",
		Arguments:         json.RawMessage(`{"path":"main.go"}`),
	}
	wantJSONKeys(t, jsonKeys(t, full),
		"type", "text", "thinking", "thinkingSignature", "redacted",
		"id", "name", "arguments")

	// Round-trip must be lossless.
	b, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back ContentBlock
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, full) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, full)
	}

	// Empty optional fields must be omitted.
	minimal := ContentBlock{Type: BlockTypeText, Text: "hi"}
	wantJSONKeys(t, jsonKeys(t, minimal), "type", "text")
}

func TestUsageCostJSON(t *testing.T) {
	usage := Usage{
		Input:       10,
		Output:      5,
		CacheRead:   2,
		CacheWrite:  3,
		Reasoning:   4,
		TotalTokens: 24,
		Cost: Cost{
			Input:      0.1,
			Output:     0.2,
			CacheRead:  0.01,
			CacheWrite: 0.02,
			Total:      0.33,
		},
	}
	wantJSONKeys(t, jsonKeys(t, usage),
		"input", "output", "cacheRead", "cacheWrite", "reasoning",
		"totalTokens", "cost")

	b, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Usage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, usage) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, usage)
	}

	// Reasoning is omitted when zero.
	wantJSONKeys(t, jsonKeys(t, Usage{}),
		"input", "output", "cacheRead", "cacheWrite", "totalTokens", "cost")
}

func TestUserMessageJSON(t *testing.T) {
	m := UserMessage{
		Role:      string(RoleUser),
		Content:   json.RawMessage(`"hello"`),
		Timestamp: 1234,
	}
	wantJSONKeys(t, jsonKeys(t, m), "role", "content", "timestamp")

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back UserMessage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, m) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, m)
	}
}

func TestAssistantMessageJSON(t *testing.T) {
	m := AssistantMessage{
		Role:       string(RoleAssistant),
		Content:    []ContentBlock{{Type: BlockTypeText, Text: "hi"}},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "anthropic/claude-sonnet-4.5",
		ResponseID: "resp_1",
		Usage:      Usage{Input: 1, Output: 1, TotalTokens: 2},
		StopReason: "toolUse",
		Timestamp:  1234,
	}
	wantJSONKeys(t, jsonKeys(t, m),
		"role", "content", "api", "provider", "model", "responseId",
		"usage", "stopReason", "timestamp")

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back AssistantMessage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, m) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, m)
	}

	// Optional fields are omitted when empty.
	noOpts := AssistantMessage{
		Role:       string(RoleAssistant),
		Content:    nil,
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "m",
		Usage:      Usage{},
		StopReason: "stop",
		Timestamp:  1,
	}
	wantJSONKeys(t, jsonKeys(t, noOpts),
		"role", "content", "api", "provider", "model", "usage", "stopReason", "timestamp")
}

func TestToolResultMessageJSON(t *testing.T) {
	m := ToolResultMessage{
		Role:       string(RoleToolResult),
		ToolCallID: "call_1",
		ToolName:   "read",
		Content:    []ContentBlock{{Type: BlockTypeText, Text: "file body"}},
		IsError:    false,
		Timestamp:  1234,
	}
	wantJSONKeys(t, jsonKeys(t, m),
		"role", "toolCallId", "toolName", "content", "isError", "timestamp")

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back ToolResultMessage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, m) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, m)
	}
}

func TestMessageRole(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want string
	}{
		{"user", Message{User: &UserMessage{Role: string(RoleUser)}}, "user"},
		{"assistant", Message{Assistant: &AssistantMessage{Role: string(RoleAssistant)}}, "assistant"},
		{"toolResult", Message{ToolResult: &ToolResultMessage{Role: string(RoleToolResult)}}, "toolResult"},
		{"empty", Message{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.Role(); got != tc.want {
				t.Errorf("Role() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNowMillis(t *testing.T) {
	before := time.Now().UnixMilli()
	got := NowMillis()
	after := time.Now().UnixMilli()
	if got < before || got > after {
		t.Errorf("NowMillis() = %d, not within [%d, %d]", got, before, after)
	}
	if got <= 0 {
		t.Errorf("NowMillis() = %d, want positive", got)
	}
}

func TestTextAndErrorResult(t *testing.T) {
	r := TextResult("ok")
	if r.IsError || len(r.Content) != 1 || r.Content[0].Type != BlockTypeText || r.Content[0].Text != "ok" {
		t.Errorf("TextResult = %+v, want single text block", r)
	}
	e := ErrorResult("boom")
	if !e.IsError || len(e.Content) != 1 || e.Content[0].Text != "boom" {
		t.Errorf("ErrorResult = %+v, want failing text block", e)
	}
}
