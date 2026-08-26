package gemini

import (
	"encoding/json"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

// TestBuildContents verifies the wire conversion of every message
// variant: roles, thinking preservation rules, tool call ids, and
// functionResponse merging.
func TestBuildContents(t *testing.T) {
	sameAssistant := &agent.AssistantMessage{
		Role:     string(agent.RoleAssistant),
		Provider: "gemini",
		Model:    "gemini-2.5-pro",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "Sure"},
			{Type: agent.BlockTypeThinking, Thinking: "thinking", ThinkingSignature: "c2lnbmF0dXJl"},
			{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		},
	}
	crossAssistant := &agent.AssistantMessage{
		Role:     string(agent.RoleAssistant),
		Provider: "openrouter",
		Model:    "some/model",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "cross thinking"},
		},
	}
	emptyThinking := &agent.AssistantMessage{
		Role:     string(agent.RoleAssistant),
		Provider: "gemini",
		Model:    "gemini-2.5-pro",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "   "},
		},
	}
	toolResult := &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: "call_1",
		ToolName:   "read",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "a"}, {Type: agent.BlockTypeText, Text: "b"}},
	}
	errorResult := &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: "call_2",
		ToolName:   "exec",
		IsError:    true,
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "boom"}},
	}

	contents, system := BuildContents("Be helpful", []*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		{Assistant: sameAssistant},
		{ToolResult: toolResult},
		{ToolResult: errorResult},
		{Assistant: crossAssistant},
		{Assistant: emptyThinking},
	}, "gemini", "gemini-2.5-pro")

	if system == nil || len(system.Parts) != 1 || *system.Parts[0].Text != "Be helpful" {
		t.Fatalf("system = %+v, want one text part", system)
	}

	// user, assistant, merged tool results, cross assistant: the empty
	// thinking message is dropped entirely.
	if len(contents) != 4 {
		t.Fatalf("contents = %d, want 4 (empty thinking message dropped)", len(contents))
	}

	// [0] user text
	if contents[0].Role != "user" || len(contents[0].Parts) != 1 || *contents[0].Parts[0].Text != "hi" {
		t.Errorf("contents[0] = %+v, want user text", contents[0])
	}

	// [1] same-provider model turn: text, thought+signature, functionCall
	c1 := contents[1]
	if c1.Role != "model" || len(c1.Parts) != 3 {
		t.Fatalf("contents[1] = %+v, want model with 3 parts", c1)
	}
	if *c1.Parts[0].Text != "Sure" || c1.Parts[0].Thought {
		t.Errorf("parts[0] = %+v, want plain text", c1.Parts[0])
	}
	if !c1.Parts[1].Thought || *c1.Parts[1].Text != "thinking" || c1.Parts[1].ThoughtSignature != "c2lnbmF0dXJl" {
		t.Errorf("parts[1] = %+v, want thought part with signature", c1.Parts[1])
	}
	if c1.Parts[2].FunctionCall == nil || c1.Parts[2].FunctionCall.Name != "read" ||
		string(c1.Parts[2].FunctionCall.Args) != `{"path":"a.go"}` {
		t.Errorf("parts[2] = %+v, want functionCall read", c1.Parts[2])
	}

	// [2] and [3] merge into one user turn with two functionResponses
	c2 := contents[2]
	if c2.Role != "user" || len(c2.Parts) != 2 {
		t.Fatalf("contents[2] = %+v, want merged user turn with 2 functionResponses", c2)
	}
	if c2.Parts[0].FunctionResponse == nil || c2.Parts[0].FunctionResponse.Name != "read" ||
		string(c2.Parts[0].FunctionResponse.Response) != `{"output":"ab"}` {
		t.Errorf("parts[0] = %+v, want read output ab", c2.Parts[0])
	}
	if c2.Parts[1].FunctionResponse == nil || c2.Parts[1].FunctionResponse.Name != "exec" ||
		string(c2.Parts[1].FunctionResponse.Response) != `{"error":"boom"}` {
		t.Errorf("parts[1] = %+v, want exec error boom", c2.Parts[1])
	}

	// [3] cross-provider thinking degrades to plain text
	c4 := contents[3]
	if c4.Role != "model" || len(c4.Parts) != 1 || *c4.Parts[0].Text != "cross thinking" || c4.Parts[0].Thought {
		t.Errorf("contents[3] = %+v, want plain text degradation", c4)
	}
}

// TestBuildContentsGemini3ToolCallIDs verifies that Gemini 3+ models
// carry ids on functionCall and functionResponse parts, and that
// signature-bearing thinking is kept.
func TestBuildContentsGemini3ToolCallIDs(t *testing.T) {
	assistant := &agent.AssistantMessage{
		Role:     string(agent.RoleAssistant),
		Provider: "gemini",
		Model:    "gemini-3-pro",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeToolCall, ID: "call_9", Name: "read", Arguments: json.RawMessage(`{}`)},
		},
	}
	result := &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: "call_9",
		ToolName:   "read",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "body"}},
	}
	contents, _ := BuildContents("", []*agent.Message{
		{Assistant: assistant},
		{ToolResult: result},
	}, "gemini", "gemini-3-pro")

	if contents[0].Parts[0].FunctionCall == nil || contents[0].Parts[0].FunctionCall.ID != "call_9" {
		t.Errorf("functionCall = %+v, want id call_9 on gemini-3", contents[0].Parts[0].FunctionCall)
	}
	if contents[1].Parts[0].FunctionResponse == nil || contents[1].Parts[0].FunctionResponse.ID != "call_9" {
		t.Errorf("functionResponse = %+v, want id call_9 on gemini-3", contents[1].Parts[0].FunctionResponse)
	}
}

// TestBuildContentsInvalidSignature verifies that an invalid base64
// signature is dropped even for same-provider messages.
func TestBuildContentsInvalidSignature(t *testing.T) {
	assistant := &agent.AssistantMessage{
		Role:     string(agent.RoleAssistant),
		Provider: "gemini",
		Model:    "gemini-2.5-pro",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "t", ThinkingSignature: "not valid!?"},
		},
	}
	contents, _ := BuildContents("", []*agent.Message{{Assistant: assistant}}, "gemini", "gemini-2.5-pro")
	if len(contents) != 1 || contents[0].Parts[0].ThoughtSignature != "" {
		t.Errorf("contents = %+v, want signature dropped", contents)
	}
}

// TestBuildContentsEmptyToolArgs verifies tool calls without arguments
// default to an empty object.
func TestBuildContentsEmptyToolArgs(t *testing.T) {
	assistant := &agent.AssistantMessage{
		Role:     string(agent.RoleAssistant),
		Provider: "gemini",
		Model:    "gemini-2.5-pro",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeToolCall, ID: "c", Name: "exec"},
		},
	}
	contents, _ := BuildContents("", []*agent.Message{{Assistant: assistant}}, "gemini", "gemini-2.5-pro")
	if got := string(contents[0].Parts[0].FunctionCall.Args); got != "{}" {
		t.Errorf("args = %s, want {}", got)
	}
}

// TestBuildContentsUserBlockArray verifies block-array user content
// flattens into text parts and empty arrays drop the message.
func TestBuildContentsUserBlockArray(t *testing.T) {
	contents, _ := BuildContents("", []*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(
			`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`)}},
	}, "gemini", "gemini-2.5-pro")
	if len(contents) != 1 || len(contents[0].Parts) != 2 {
		t.Fatalf("contents = %+v, want one user turn with 2 parts", contents)
	}
	if *contents[0].Parts[0].Text != "one" || *contents[0].Parts[1].Text != "two" {
		t.Errorf("parts = %+v", contents[0].Parts)
	}

	contents, _ = BuildContents("", []*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`[{"type":"image","url":"x"}]`)}},
	}, "gemini", "gemini-2.5-pro")
	if len(contents) != 0 {
		t.Errorf("contents = %+v, want dropped non-text message", contents)
	}
}

// TestBuildTools verifies the tool wire conversion, including the empty
// case which must omit the field entirely.
func TestBuildTools(t *testing.T) {
	if empty := BuildTools(nil); empty != nil {
		t.Errorf("BuildTools(nil) = %v, want nil", empty)
	}
	tools := BuildTools([]agent.Tool{
		stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object"}`)},
	})
	got, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"functionDeclarations":[{"name":"read","description":"Reads a file","parametersJsonSchema":{"type":"object"}}]}]`
	if string(got) != want {
		t.Errorf("tools = %s\nwant    %s", got, want)
	}
}

// TestRequiresToolCallID pins the model families that need explicit tool
// call ids, mirroring pi-ai's google-shared helper.
func TestRequiresToolCallID(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gemini-2.5-pro", false},
		{"gemini-2.5-flash", false},
		{"gemini-3-pro", true},
		{"gemini-3.1-pro", true},
		{"gemini-3-flash", true},
		{"gemini-live-3", true},
		{"claude-sonnet-4.5", true},
		{"gpt-oss-120b", true},
		{"some-other-model", false},
	}
	for _, tc := range cases {
		if got := requiresToolCallID(tc.model); got != tc.want {
			t.Errorf("requiresToolCallID(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

// TestMapStopReason pins the finish reason mapping.
func TestMapStopReason(t *testing.T) {
	cases := []struct {
		reason string
		want   string
	}{
		{"STOP", "stop"},
		{"MAX_TOKENS", "length"},
		{"SAFETY", "error"},
		{"BLOCKLIST", "error"},
		{"RECITATION", "error"},
		{"MALFORMED_FUNCTION_CALL", "error"},
		{"UNKNOWN_REASON", "error"},
	}
	for _, tc := range cases {
		if got := mapStopReason(tc.reason); got != tc.want {
			t.Errorf("mapStopReason(%q) = %q, want %q", tc.reason, got, tc.want)
		}
	}
}

// TestRetainThoughtSignature verifies last-non-empty semantics.
func TestRetainThoughtSignature(t *testing.T) {
	if got := retainThoughtSignature("", "new"); got != "new" {
		t.Errorf("empty then new = %q, want new", got)
	}
	if got := retainThoughtSignature("old", ""); got != "old" {
		t.Errorf("old then empty = %q, want old", got)
	}
	if got := retainThoughtSignature("old", "new"); got != "new" {
		t.Errorf("old then new = %q, want new", got)
	}
}

// TestIsValidThoughtSignature pins the base64 signature check.
func TestIsValidThoughtSignature(t *testing.T) {
	if !isValidThoughtSignature("c2lnbmF0dXJl") {
		t.Error("valid base64 signature rejected")
	}
	if isValidThoughtSignature("") {
		t.Error("empty signature accepted")
	}
	if isValidThoughtSignature("not base64!!") {
		t.Error("invalid signature accepted")
	}
	if isValidThoughtSignature("c2ln") == false {
		t.Error("padded-length signature rejected")
	}
}
