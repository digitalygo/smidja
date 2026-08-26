package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

// marshalInput re-marshals a raw item for assertions.
func marshalInput(t *testing.T, items []json.RawMessage) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		var m map[string]any
		if err := json.Unmarshal(it, &m); err != nil {
			t.Fatalf("unmarshal item %s: %v", it, err)
		}
		out = append(out, m)
	}
	return out
}

// TestBuildInput verifies the wire conversion of every message variant:
// user items, assistant message ids, function call split ids, reasoning
// replay, and tool outputs.
func TestBuildInput(t *testing.T) {
	reasoningItem := `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"t"}],"encrypted_content":"enc_1"}`
	assistant := &agent.AssistantMessage{
		Role:  string(agent.RoleAssistant),
		API:   "openai-responses",
		Model: "gpt-5",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "t", ThinkingSignature: reasoningItem},
			{Type: agent.BlockTypeText, Text: "Sure"},
			{Type: agent.BlockTypeText, Text: " done"},
			{Type: agent.BlockTypeToolCall, ID: "call_1|fc_item_1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
			{Type: agent.BlockTypeToolCall, ID: "bare_call", Name: "exec", Arguments: json.RawMessage(`{"cmd":"ls"}`)},
		},
	}
	result := &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: "call_1|fc_item_1",
		ToolName:   "read",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "a"}, {Type: agent.BlockTypeText, Text: "b"}},
	}
	emptyResult := &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: "call_2",
		ToolName:   "exec",
		Content:    nil,
	}

	items := marshalInput(t, BuildInput([]*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		{Assistant: assistant},
		{ToolResult: result},
		{ToolResult: emptyResult},
	}))

	if len(items) != 8 {
		t.Fatalf("items = %d, want 8", len(items))
	}

	// [0] user message
	if items[0]["type"] != "message" || items[0]["role"] != "user" {
		t.Errorf("items[0] = %v, want user message", items[0])
	}
	if content := items[0]["content"].([]any); len(content) != 1 {
		t.Errorf("items[0] content = %v", items[0]["content"])
	} else if part := content[0].(map[string]any); part["type"] != "input_text" || part["text"] != "hi" {
		t.Errorf("items[0] part = %v", part)
	}

	// [1] reasoning replay verbatim
	if items[1]["type"] != "reasoning" || items[1]["id"] != "rs_1" || items[1]["encrypted_content"] != "enc_1" {
		t.Errorf("items[1] = %v, want verbatim reasoning item", items[1])
	}

	// [2] and [3] assistant messages with deterministic ids
	if items[2]["type"] != "message" || items[2]["id"] != "msg_pi_1" || items[2]["status"] != "completed" {
		t.Errorf("items[2] = %v, want msg_pi_1", items[2])
	}
	if items[3]["id"] != "msg_pi_1_1" {
		t.Errorf("items[3] = %v, want msg_pi_1_1", items[3])
	}
	if content := items[2]["content"].([]any); len(content) != 1 {
		t.Errorf("items[2] content = %v", items[2]["content"])
	} else if part := content[0].(map[string]any); part["type"] != "output_text" || part["text"] != "Sure" {
		t.Errorf("items[2] part = %v", part)
	}
	if annotations := items[2]["content"].([]any)[0].(map[string]any)["annotations"]; annotations == nil {
		t.Error("items[2] annotations = nil, want empty array")
	}

	// [4] function_call with split ids and stringified arguments
	if items[4]["type"] != "function_call" || items[4]["call_id"] != "call_1" || items[4]["id"] != "fc_item_1" || items[4]["name"] != "read" {
		t.Errorf("items[4] = %v, want function_call with split ids", items[4])
	}
	if items[4]["arguments"] != `{"path":"a.go"}` {
		t.Errorf("items[4] arguments = %v, want JSON string", items[4]["arguments"])
	}

	// [5] function_call with a bare id: the non-fc item id is dropped
	if items[5]["type"] != "function_call" || items[5]["call_id"] != "bare_call" {
		t.Errorf("items[5] = %v, want function_call with bare call id", items[5])
	}
	if _, ok := items[5]["id"]; ok {
		t.Errorf("items[5] id = %v, want omitted for non-fc id", items[5]["id"])
	}

	// [6] and [7] function_call_output items
	if items[6]["type"] != "function_call_output" || items[6]["call_id"] != "call_1" || items[6]["output"] != "a\nb" {
		t.Errorf("items[6] = %v, want newline-joined output", items[6])
	}
	if items[7]["type"] != "function_call_output" || items[7]["output"] != "(no tool output)" {
		t.Errorf("items[7] = %v, want no-tool-output placeholder", items[7])
	}
}

// TestBuildInputUserBlockArray verifies block-array user content
// flattens into input_text parts and empty arrays drop the message.
func TestBuildInputUserBlockArray(t *testing.T) {
	items := marshalInput(t, BuildInput([]*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(
			`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`)}},
	}))
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	content := items[0]["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %v, want two parts", content)
	}
	if content[0].(map[string]any)["text"] != "one" || content[1].(map[string]any)["text"] != "two" {
		t.Errorf("content = %v", content)
	}

	items = marshalInput(t, BuildInput([]*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`[{"type":"image","url":"x"}]`)}},
	}))
	if len(items) != 0 {
		t.Errorf("items = %v, want dropped non-text message", items)
	}
}

// TestBuildInputEmptyAssistant verifies an assistant message with no
// renderable blocks contributes nothing.
func TestBuildInputEmptyAssistant(t *testing.T) {
	items := marshalInput(t, BuildInput([]*agent.Message{
		{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "no signature"},
		}}},
	}))
	if len(items) != 0 {
		t.Errorf("items = %v, want dropped signature-less thinking", items)
	}
}

// TestBuildTools verifies the tool wire conversion, including the empty
// case and the strict field difference between plain and Codex modes.
func TestBuildTools(t *testing.T) {
	if empty := BuildTools(nil, false); empty != nil {
		t.Errorf("BuildTools(nil) = %v, want nil", empty)
	}
	tools := BuildTools([]agent.Tool{
		stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object"}`)},
	}, false)
	got, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"type":"function","name":"read","description":"Reads a file","parameters":{"type":"object"},"strict":false}]`
	if string(got) != want {
		t.Errorf("plain tools = %s\nwant         %s", got, want)
	}

	codexTools := BuildTools([]agent.Tool{
		stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object"}`)},
	}, true)
	got, err = json.Marshal(codexTools)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want = `[{"type":"function","name":"read","description":"Reads a file","parameters":{"type":"object"},"strict":null}]`
	if string(got) != want {
		t.Errorf("codex tools = %s\nwant          %s", got, want)
	}
}

// TestSplitToolCallID pins the id pair splitting.
func TestSplitToolCallID(t *testing.T) {
	callID, itemID := splitToolCallID("call_1|fc_item_1")
	if callID != "call_1" || itemID != "fc_item_1" {
		t.Errorf("split = %q, %q; want call_1, fc_item_1", callID, itemID)
	}
	callID, itemID = splitToolCallID("bare")
	if callID != "bare" || itemID != "" {
		t.Errorf("split = %q, %q; want bare, empty", callID, itemID)
	}
	callID, itemID = splitToolCallID("")
	if callID != "" || itemID != "" {
		t.Errorf("split = %q, %q; want both empty", callID, itemID)
	}
}

// TestCodexURL pins the Codex endpoint resolution.
func TestCodexURL(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"https://chatgpt.com/backend-api", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/codex", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/codex/responses", "https://chatgpt.com/backend-api/codex/responses"},
	}
	for _, tc := range cases {
		if got := codexURL(tc.base); got != tc.want {
			t.Errorf("codexURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// TestMapStatus pins the terminal status mapping.
func TestMapStatus(t *testing.T) {
	cases := []struct {
		status  string
		reason  string
		want    string
		wantErr string
	}{
		{"", "", "stop", ""},
		{"completed", "", "stop", ""},
		{"incomplete", "max_output_tokens", "length", ""},
		{"incomplete", "content_filter", "error", "Response incomplete: content_filter"},
		{"incomplete", "", "error", "Response incomplete: without a provider reason"},
		{"failed", "", "error", ""},
		{"cancelled", "", "error", ""},
		{"in_progress", "", "stop", ""},
		{"queued", "", "stop", ""},
		{"bogus", "", "error", "unknown response status: bogus"},
	}
	for _, tc := range cases {
		stop, errMsg := mapStatus(tc.status, tc.reason)
		if stop != tc.want || errMsg != tc.wantErr {
			t.Errorf("mapStatus(%q, %q) = (%q, %q), want (%q, %q)",
				tc.status, tc.reason, stop, errMsg, tc.want, tc.wantErr)
		}
	}
}

// TestAssistantItemsEmptyToolArgs verifies tool calls without arguments
// default to an empty object string.
func TestAssistantItemsEmptyToolArgs(t *testing.T) {
	items := marshalInput(t, BuildInput([]*agent.Message{
		{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{
			{Type: agent.BlockTypeToolCall, ID: "c|fc_1", Name: "exec"},
		}}},
	}))
	if len(items) != 1 || items[0]["arguments"] != "{}" {
		t.Errorf("items = %v, want arguments {}", items)
	}
}

// TestBuildInputJoin verifies the joined output of multi-block results
// uses a newline separator.
func TestBuildInputJoin(t *testing.T) {
	result := &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: "c",
		ToolName:   "read",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "line one"},
			{Type: agent.BlockTypeText, Text: "line two"},
		},
	}
	items := marshalInput(t, BuildInput([]*agent.Message{{ToolResult: result}}))
	out := items[0]["output"].(string)
	if !strings.Contains(out, "\n") {
		t.Errorf("output = %q, want newline separator", out)
	}
}
